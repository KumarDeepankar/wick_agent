import os

# ========================================
# PERFORMANCE OPTIMIZATION CONFIGURATION
# ========================================
# Set these before imports to ensure they're available when modules initialize

# Tool Discovery Cache TTL (Priority 1 Optimization)
# - Caches tool definitions from MCP registry
# - Default: 300 seconds (5 minutes)
# - Reduces latency by ~200-500ms per query (after first query)
os.environ.setdefault("MCP_TOOLS_CACHE_TTL", "300")

# MCP Session  Pool TTL (Priority 2 Optimization)
# - Reuses MCP sessions across tool calls
# - Default: 600 seconds (10 minutes)
# - Reduces latency by ~300-800ms per tool call (after first call)
os.environ.setdefault("MCP_SESSION_TTL", "600")

# Combined effect: Saves ~1-3 seconds per query
# ========================================

from fastapi import FastAPI, Query, HTTPException, Request, Depends
from fastapi.responses import StreamingResponse, JSONResponse, HTMLResponse
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles

from typing import Optional, AsyncGenerator, Dict, Any, List
from contextlib import asynccontextmanager
import asyncio
import uuid
import inspect
import traceback
import aiohttp
import json
from pathlib import Path

from pydantic import BaseModel
from langgraph.types import Command, StateSnapshot

from ollama_query_agent.graph_definition import compiled_agent as search_compiled_agent
from ollama_query_agent.mcp_tool_client import mcp_tool_client, set_request_jwt_token, reset_request_jwt_token, GatewayAuthError
from ollama_query_agent.error_handler import format_error_for_display
from ollama_query_agent.model_config import (
    get_available_providers,
    get_models_for_provider,
    AVAILABLE_MODELS,
    DEFAULT_PROVIDER,
    DEFAULT_MODELS
)

# Import auth modules
from auth import (
    get_current_user,
    require_auth,
    get_jwt_token,
    fetch_jwks_from_gateway,
    invalidate_session_by_email,
    pending_logouts,
    check_and_clear_pending_logout
)
from auth_routes import router as auth_router
from debug_auth import router as debug_auth_router
from conversation_routes import router as conversation_router
from research_agent.routes import router as research_router
from conversation_store import get_preferences


async def with_jwt_context(jwt_token: Optional[str], async_gen: AsyncGenerator[str, None]) -> AsyncGenerator[str, None]:
    """Wrap an async generator to maintain JWT context throughout streaming.

    Since contextvars don't automatically propagate into async generators returned by
    StreamingResponse, this wrapper sets the JWT context before each yield.
    """
    reset_token = set_request_jwt_token(jwt_token)
    try:
        async for item in async_gen:
            yield item
    finally:
        reset_request_jwt_token(reset_token)


# Track active SSE connections for graceful shutdown
active_sse_connections: set = set()


# Modern FastAPI lifespan event handler (replaces deprecated on_event)
@asynccontextmanager
async def lifespan(app: FastAPI):
    """
    Lifespan event handler for startup and shutdown.
    Replaces deprecated @app.on_event("startup")
    """
    import logging
    logger = logging.getLogger(__name__)

    # Startup: Fetch JWKS from tools_gateway
    logger.info("Fetching JWKS (RS256 public keys) from tools_gateway...")
    jwks_success = fetch_jwks_from_gateway()

    if jwks_success:
        logger.info("✓ JWKS fetched successfully")
        logger.info("🔐 Authentication ready: RS256 only (industry standard)")
    else:
        logger.error("⚠ Failed to fetch JWKS from gateway - authentication will not work!")
        logger.error("   Please ensure tools_gateway is running and has generated RSA keys")

    # NOTE: We DO NOT pre-warm tool cache because:
    # - Tool lists are user-specific (based on JWT and roles)
    # - Pre-warming would cache tools for wrong user context
    # - First query per user will populate their specific tool cache
    logger.info("ℹ️  Tool cache will be populated on first authenticated request")

    yield  # Server runs here

    # Shutdown logic - close all resources
    logger.info("Shutting down Agentic Search Service...")

    # Cancel all active SSE connections
    if active_sse_connections:
        logger.info(f"Cancelling {len(active_sse_connections)} active SSE connections...")
        for task in active_sse_connections:
            task.cancel()
        # Wait briefly for cancellations
        await asyncio.sleep(0.1)

    # Close research agent's cached LLM clients (httpx connections)
    logger.info("Closing research agent LLM clients...")
    try:
        from research_agent.nodes import cleanup_llm_clients
        await cleanup_llm_clients()
        logger.info("✓ Research agent LLM clients closed")
    except Exception as e:
        logger.warning(f"Error closing research agent clients: {e}")

    # Close MCP tool client's HTTP connection pool
    logger.info("Closing MCP tool client connections...")
    try:
        await mcp_tool_client.close()
        logger.info("✓ MCP tool client closed")
    except Exception as e:
        logger.warning(f"Error closing MCP client: {e}")

    logger.info("✓ Shutdown complete")


app = FastAPI(
    title="Agentic Search Service",
    description="LangGraph-powered search agent using Ollama and MCP tools",
    version="1.0.0",
    lifespan=lifespan
)


# Include authentication routes
app.include_router(auth_router)
app.include_router(debug_auth_router)
app.include_router(conversation_router)
app.include_router(research_router)

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
STATIC_DIR = os.path.join(BASE_DIR, "static")

app.mount("/assets", StaticFiles(directory=os.path.join(STATIC_DIR, "assets")), name="assets")


@app.get("/health")
async def health_check():
    """Health check endpoint"""
    return {"status": "healthy", "service": "agentic-search"}


@app.get("/favicon.svg")
async def favicon():
    """Serve favicon"""
    from fastapi.responses import FileResponse
    return FileResponse(os.path.join(STATIC_DIR, "favicon.svg"))


@app.get("/")
async def root(request: Request):
    """Serve React app"""
    # Check if user is authenticated
    user = get_current_user(request)

    if not user:
        from fastapi.responses import RedirectResponse
        return RedirectResponse(url="/auth/login", status_code=302)

    from fastapi.responses import FileResponse
    return FileResponse(os.path.join(STATIC_DIR, "index.html"))


app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/tools")
async def get_available_tools(request: Request):
    """Get available tools from MCP registry (requires authentication)"""
    user = require_auth(request)
    jwt_token = get_jwt_token(request)
    reset_token = set_request_jwt_token(jwt_token)

    try:
        tools = await mcp_tool_client.get_available_tools()
        return JSONResponse(content={
            "tools": tools,
            "user": {"email": user.get("email"), "authenticated": True}
        })
    except GatewayAuthError as e:
        # User deleted/disabled from gateway - invalidate local session
        invalidate_session_by_email(e.user_email)
        raise HTTPException(status_code=401, detail="Session invalidated - user removed from gateway")
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error fetching tools: {str(e)}")
    finally:
        reset_request_jwt_token(reset_token)


@app.post("/tools/refresh")
async def refresh_tools_cache(request: Request):
    """Refresh tools - now a no-op since client is stateless"""
    user = require_auth(request)
    return JSONResponse(content={
        "success": True,
        "message": "Tools fetched fresh on each request. No cache to invalidate.",
        "user": {"email": user.get("email")}
    })


@app.get("/auth/validate")
async def validate_session(request: Request):
    """
    Validate session against gateway. Frontend should call this periodically.
    Returns 401 if user deleted/disabled from gateway.
    """
    user = get_current_user(request)
    if not user:
        raise HTTPException(status_code=401, detail="Not authenticated")

    jwt_token = get_jwt_token(request)
    if not jwt_token:
        raise HTTPException(status_code=401, detail="No token")

    reset_token = set_request_jwt_token(jwt_token)
    try:
        # Quick validation - try to list tools from gateway
        tools = await mcp_tool_client.get_available_tools()
        return JSONResponse(content={"valid": True, "email": user.get("email")})
    except GatewayAuthError as e:
        invalidate_session_by_email(e.user_email)
        raise HTTPException(status_code=401, detail="User removed from gateway")
    except Exception:
        # Gateway unreachable - don't invalidate, might be temporary
        return JSONResponse(content={"valid": True, "email": user.get("email"), "warning": "Gateway check failed"})
    finally:
        reset_request_jwt_token(reset_token)


@app.get("/auth/session-events")
async def session_events(request: Request):
    """
    SSE endpoint for real-time session events.
    Frontend subscribes to this to receive immediate logout notifications
    when user is deleted from tools_gateway.
    """
    import logging
    logger = logging.getLogger(__name__)

    user = get_current_user(request)
    if not user:
        raise HTTPException(status_code=401, detail="Not authenticated")

    user_email = user.get("email")

    async def event_stream():
        """Generate SSE events for session status."""
        # Track this connection for graceful shutdown
        current_task = asyncio.current_task()
        active_sse_connections.add(current_task)

        try:
            while True:
                # Check if client disconnected
                if await request.is_disconnected():
                    logger.info(f"SSE: Client disconnected for {user_email}")
                    break

                # Check if this user has a pending logout
                if user_email in pending_logouts:
                    check_and_clear_pending_logout(user_email)
                    logger.info(f"SSE: Sending logout event to {user_email}")
                    yield f"event: logout\ndata: {{\"reason\": \"User deleted from gateway\"}}\n\n"
                    break

                # Send heartbeat every 15 seconds to keep connection alive
                yield f"event: heartbeat\ndata: {{\"status\": \"ok\"}}\n\n"
                await asyncio.sleep(15)
        except asyncio.CancelledError:
            logger.info(f"SSE: Connection cancelled for {user_email}")
        except Exception as e:
            logger.warning(f"SSE: Error for {user_email}: {e}")
        finally:
            # Remove from active connections
            active_sse_connections.discard(current_task)

    return StreamingResponse(
        event_stream(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no"
        }
    )


class UserDeletedPayload(BaseModel):
    """Payload for user deletion webhook from tools_gateway"""
    email: str
    user_id: Optional[str] = None


@app.post("/internal/user-deleted")
async def handle_user_deleted(request: Request, payload: UserDeletedPayload):
    """
    Webhook endpoint called by tools_gateway when a user is deleted.
    Immediately invalidates the user's session in this service.

    Security: Only accepts requests from tools_gateway (validated via shared secret).
    """
    import logging
    logger = logging.getLogger(__name__)

    logger.info(f"🔔 Received user deletion webhook for: {payload.email}")

    # Validate the request is from tools_gateway
    # Check for internal webhook secret
    webhook_secret = os.getenv("INTERNAL_WEBHOOK_SECRET")
    if webhook_secret:
        provided_secret = request.headers.get("X-Webhook-Secret")
        if provided_secret != webhook_secret:
            logger.warning(f"❌ Invalid webhook secret for user deletion: {payload.email}")
            raise HTTPException(status_code=403, detail="Invalid webhook secret")

    # Invalidate user's session
    invalidated = invalidate_session_by_email(payload.email)

    if invalidated:
        logger.info(f"✅ User session invalidated via webhook: {payload.email}")
        logger.info(f"✅ User added to pending_logouts for SSE notification")
    else:
        logger.warning(f"⚠️ No active session found for deleted user: {payload.email}")

    return JSONResponse(content={
        "success": True,
        "email": payload.email,
        "session_invalidated": invalidated
    })


@app.get("/models")
async def get_available_models(request: Request):
    """Get available LLM providers and models (requires authentication)"""
    # Require authentication
    user = require_auth(request)

    try:
        return JSONResponse(content={
            "providers": get_available_providers(),
            "models": AVAILABLE_MODELS,
            "defaults": {
                "provider": DEFAULT_PROVIDER,
                "models": DEFAULT_MODELS
            },
            "user": {
                "email": user.get("email"),
                "authenticated": True
            }
        })
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error fetching models: {str(e)}")


class ConversationTurn(BaseModel):
    query: str
    response: str


class SearchRequest(BaseModel):
    query: str
    enabled_tools: Optional[List[str]] = None
    session_id: Optional[str] = None
    is_followup: Optional[bool] = False
    conversation_history: Optional[List[ConversationTurn]] = None  # Previous Q&A turns from frontend
    theme: Optional[str] = None  # User theme preference: professional, minimal, dark, vibrant, nature
    theme_strategy: Optional[str] = "auto"  # auto, intent, time, keywords, weighted, random
    llm_provider: Optional[str] = None  # LLM provider: "anthropic" or "ollama"
    llm_model: Optional[str] = None  # Model name specific to the provider


async def search_interaction_stream(
    session_id: str,
    query: str,
    enabled_tools: List[str],
    is_followup: bool = False,
    frontend_conversation_history: Optional[List[Dict[str, str]]] = None,
    theme: Optional[str] = None,
    theme_strategy: str = "auto",
    llm_provider: Optional[str] = None,
    llm_model: Optional[str] = None,
    user_preferences: Optional[str] = None
) -> AsyncGenerator[str, None]:
    """Stream search agent interaction"""

    try:
        # Always use session_id as thread_id to maintain conversation context
        # User will start a new conversation (new session_id) when they want a fresh thread
        thread_id = session_id
        config = {"configurable": {"thread_id": thread_id}}

        # Use frontend-provided conversation history if available (for loaded history sessions)
        # Otherwise, try to retrieve from checkpointer (for active sessions)
        conversation_history = []
        if frontend_conversation_history:
            # Frontend sent history (e.g., loaded from saved conversation)
            conversation_history = frontend_conversation_history
        else:
            # Try to retrieve from checkpointer (active session)
            try:
                state_snapshot = await search_compiled_agent.aget_state(config)
                if state_snapshot and state_snapshot.values:
                    conversation_history = state_snapshot.values.get("conversation_history", [])
            except Exception as e:
                # First query in this session - no history available yet
                pass

        inputs = {
            "input": query,
            "conversation_id": session_id,
            "enabled_tools": enabled_tools or [],
            "is_followup_query": bool(conversation_history),  # Auto-detect based on history
            "conversation_history": conversation_history,
            "theme_preference": theme,  # User's theme preference (optional)
            "theme_strategy": theme_strategy,  # Theme selection strategy
            "llm_provider": llm_provider,  # LLM provider selection
            "llm_model": llm_model,  # LLM model selection
            "user_preferences": user_preferences,  # User's agent instructions
        }

        relevant_node_names = [
            "parallel_initialization_node",
            "create_execution_plan_node",
            "execute_all_tasks_parallel_node",
            "gather_and_synthesize_node",
            "reduce_samples_node"  # Retry node for token limit errors
        ]

        final_response_started = False
        final_response_content = ""
        sent_thinking_steps = set()  # Track which thinking steps we've already sent (by content)
        completed_nodes = set()  # Track which nodes have been completed to avoid duplicate completion messages
        started_nodes = set()  # Track which nodes have started to avoid duplicate start messages

        try:
            async for event in search_compiled_agent.astream_events(inputs, config=config, version="v2"):
                event_type = event.get("event")
                event_name = event.get("name")
                data = event.get("data", {})

                # Send node start notification BEFORE node executes
                if event_type == "on_chain_start" and event_name in relevant_node_names:
                    if event_name not in started_nodes:
                        started_nodes.add(event_name)
                        # Send node name first, before any thinking steps
                        node_display_name = event_name.replace('_', ' ').title()
                        yield f"THINKING:▶ {node_display_name}\n"
                        await asyncio.sleep(0.01)

                if event_type == "on_chain_end" and event_name in relevant_node_names:
                    node_output = data.get("output")
                    if isinstance(node_output, dict):
                        # Get thinking steps and send only new ones (based on content)
                        thinking_steps_list = node_output.get("thinking_steps", [])

                        # Send only new thinking steps (ones we haven't sent before)
                        for thought in thinking_steps_list:
                            if thought and thought.strip() and thought not in sent_thinking_steps:
                                sent_thinking_steps.add(thought)
                                yield f"PROCESSING_STEP:{thought}\n"
                                await asyncio.sleep(0.01)

                        # Send node completion info only once per node
                        if event_name not in completed_nodes:
                            completed_nodes.add(event_name)
                            yield f"THINKING:✓ Completed: {event_name.replace('_', ' ').title()}\n"
                            await asyncio.sleep(0.01)

                        # Send conversation state to frontend (after initialization node)
                        if event_name == "parallel_initialization_node":
                            import json as json_lib  # Local import to avoid scope issues
                            is_reset = node_output.get("conversation_was_reset", False)
                            is_followup = node_output.get("is_followup_query", False)
                            history_len = len(node_output.get("conversation_history", []))

                            # Send turn info so frontend can update UI
                            turn_info = {
                                "is_reset": is_reset,
                                "is_followup": is_followup,
                                "turn_count": history_len,
                                "followup_allowed": True  # Continuous conversation enabled (sliding window)
                            }
                            yield f"TURN_INFO:{json_lib.dumps(turn_info)}\n"
                            await asyncio.sleep(0.01)

                        # Send extracted sources after task execution nodes complete
                        if event_name in ["execute_all_tasks_parallel_node", "execute_task_node"]:
                            extracted_sources = node_output.get("extracted_sources", [])
                            if extracted_sources:
                                import json
                                sources_json = json.dumps(extracted_sources)
                                yield f"SOURCES:{sources_json}\n"
                                await asyncio.sleep(0.01)

                            # Send chart configs (dynamic, no hardcoded fields!)
                            chart_configs = node_output.get("chart_configs", [])
                            if chart_configs:
                                import json
                                charts_json = json.dumps(chart_configs)
                                yield f"CHART_CONFIGS:{charts_json}\n"
                                await asyncio.sleep(0.01)

                        # Send RETRY_RESET when reduce_samples_node triggers a retry
                        if event_name == "reduce_samples_node":
                            if node_output.get("retry_ui_reset"):
                                yield f"RETRY_RESET:\n"
                                await asyncio.sleep(0.01)

                        if node_output.get("final_response_generated_flag") and not final_response_started:
                            final_response_started = True

                            # Signal that final response is starting
                            yield f"FINAL_RESPONSE_START:\n"
                            await asyncio.sleep(0.01)

                            # Handle new FinalResponse structure
                            final_response_obj = node_output.get("final_response")
                            final_response = ""

                            if final_response_obj:
                                # Extract response_content from FinalResponse object
                                if hasattr(final_response_obj, 'response_content'):
                                    final_response = final_response_obj.response_content
                                elif isinstance(final_response_obj, dict):
                                    final_response = final_response_obj.get('response_content', '')

                            # Fallback to old field name for compatibility
                            if not final_response and "final_response_content" in node_output:
                                final_response = node_output.get("final_response_content", "")

                            if final_response:
                                # Send markdown content with proper structure preservation
                                yield f"MARKDOWN_CONTENT_START:\n"
                                await asyncio.sleep(0.01)

                                # Stream markdown character-by-character for typing effect
                                for char in final_response:
                                    yield char
                                    await asyncio.sleep(0.002)  # 2ms delay per character for very fast typing effect

                                final_response_content = final_response
                                await asyncio.sleep(0.01)

                                yield f"\nMARKDOWN_CONTENT_END:\n"

                        if node_output.get("error_message") and not final_response_started:
                            error_msg = node_output['error_message']
                            yield f"ERROR:{error_msg}\n"

                elif event_type == "on_chain_error":
                    error_message = data if isinstance(data, str) else str(data)
                    user_friendly_error = format_error_for_display(error_message)
                    yield f"ERROR:{user_friendly_error}\n"

            if not final_response_started:
                yield "ERROR:Unable to generate a response. This may be due to a connection issue with the data sources. Please try again, or raise a support ticket if the problem continues.\n"

        except Exception as e_main_stream:
            traceback.print_exc()
            user_friendly_error = format_error_for_display(str(e_main_stream))
            yield f"ERROR:{user_friendly_error}\n"


    except Exception as e:
        traceback.print_exc()
        user_friendly_error = format_error_for_display(str(e))
        yield f"ERROR:{user_friendly_error}\n"


@app.post("/search")
async def search_endpoint(request_body: SearchRequest, http_request: Request):
    """Main search endpoint with streaming response (requires authentication)"""
    # Require authentication
    user = require_auth(http_request)

    # Get JWT token for per-user tool access (will be set in context by wrapper)
    jwt_token = get_jwt_token(http_request)

    effective_session_id = request_body.session_id if request_body.session_id else f"search-{str(uuid.uuid4())}"

    # Fetch user preferences
    user_email = user.get("email")
    user_preferences = get_preferences(user_email) if user_email else None

    # Convert conversation history to list of dicts if provided
    conv_history = None
    if request_body.conversation_history:
        conv_history = [{"query": turn.query, "response": turn.response} for turn in request_body.conversation_history]

    # Wrap the stream with JWT context to maintain per-user tool access
    return StreamingResponse(
        with_jwt_context(
            jwt_token,
            search_interaction_stream(
                effective_session_id,
                request_body.query,
                request_body.enabled_tools or [],
                request_body.is_followup or False,
                conv_history,
                request_body.theme,
                request_body.theme_strategy or "auto",
                request_body.llm_provider,
                request_body.llm_model,
                user_preferences
            )
        ),
        media_type="text/plain"
    )


@app.post("/chat")
async def chat_endpoint(
        request: Request,
        human_message: str = Query(..., description="Your search query"),
        enabled_tools: Optional[str] = Query(None, description="Comma-separated list of enabled tools"),
        session_id: Optional[str] = Query(None, description="Unique session ID")
):
    """Chat-style endpoint for compatibility (requires authentication)"""
    # Require authentication
    user = require_auth(request)

    # Get JWT token for per-user tool access (will be set in context by wrapper)
    jwt_token = get_jwt_token(request)

    effective_session_id = session_id if session_id else f"search-{str(uuid.uuid4())}"

    # Parse enabled tools
    enabled_tools_list = []
    if enabled_tools:
        enabled_tools_list = [tool.strip() for tool in enabled_tools.split(",")]

    # Wrap the stream with JWT context to maintain per-user tool access
    return StreamingResponse(
        with_jwt_context(
            jwt_token,
            search_interaction_stream(effective_session_id, human_message, enabled_tools_list)
        ),
        media_type="text/plain"
    )


if __name__ == "__main__":
    import uvicorn

    # Bind to 0.0.0.0 to accept connections from outside the container
    host = "0.0.0.0"
    port = 8023

    print("=" * 60)
    print(f"🔍 Starting Agentic Search Service on {host}:{port}")
    print("=" * 60)
    print("\n📊 Performance Optimizations:")
    print(f"   • Tool Cache TTL:    {os.environ.get('MCP_TOOLS_CACHE_TTL')}s")
    print(f"   • Session Pool TTL:  {os.environ.get('MCP_SESSION_TTL')}s")
    print(f"   • Expected savings:  ~1-3 seconds per query\n")
    print("🔗 Dependencies:")
    print("   • Ollama: http://localhost:11434 (llama3.2:latest)")
    print("   • MCP Gateway: http://localhost:8021")
    print("=" * 60)

    # Configure uvicorn with fast graceful shutdown (1 second timeout)
    # This prevents hanging when httpx keeps connections in pool
    config = uvicorn.Config(
        app,
        host=host,
        port=port,
        proxy_headers=True,
        forwarded_allow_ips="*",
        timeout_graceful_shutdown=1  # Wait max 1 second for connections to close
    )
    server = uvicorn.Server(config)
    server.run()

from __future__ import annotations

from enum import Enum
from typing import Any

from pydantic import BaseModel, Field


# ---------------------------------------------------------------------------
# Enums
# ---------------------------------------------------------------------------


class BackendType(str, Enum):
    """Supported deep-agent backend types."""
    state = "state"              # Default – ephemeral, thread-scoped
    filesystem = "filesystem"    # Local disk (virtual_mode by default)
    store = "store"              # Persistent cross-thread via LangGraph Store
    composite = "composite"      # Routes paths to different backends


# ---------------------------------------------------------------------------
# Nested configuration objects
# ---------------------------------------------------------------------------


class Message(BaseModel):
    role: str = Field(default="user", description="Message role: user | assistant | system")
    content: str = Field(..., description="Message content")


class SubAgentConfig(BaseModel):
    """Configuration for a subagent spawned via the built-in `task` tool."""
    name: str = Field(..., description="Unique subagent identifier")
    description: str = Field(..., description="What this subagent does (shown to the parent agent)")
    system_prompt: str = Field(..., description="System instructions for the subagent")
    tools: list[str] | None = Field(default=None, description="Names of registered tools")
    model: str | None = Field(default=None, description="Model override (e.g. openai:gpt-4.1)")
    middleware: list[str] | None = Field(default=None, description="Middleware names for this subagent")


class BackendConfig(BaseModel):
    """Backend storage configuration."""
    type: BackendType = Field(default=BackendType.state, description="Backend type")
    root_dir: str = Field(default=".", description="Root directory for filesystem backend")
    virtual_mode: bool = Field(default=True, description="Use virtual FS (filesystem backend)")
    routes: dict[str, BackendType] | None = Field(
        default=None,
        description="Path-prefix → backend routing (composite backend only). "
                    "E.g. {\"/memories/\": \"store\", \"/workspace/\": \"filesystem\"}",
    )


class InterruptRule(BaseModel):
    """Human-in-the-loop interrupt configuration for a single tool."""
    enabled: bool = Field(default=True, description="Whether to interrupt on this tool")
    allowed_decisions: list[str] | None = Field(
        default=None,
        description="Custom decision options (default: approve, edit, reject)",
    )


class SkillConfig(BaseModel):
    """Skill configuration — task-specific knowledge loaded contextually."""
    paths: list[str] = Field(
        ...,
        description="POSIX-format source paths to skill directories containing SKILL.md files. "
                    "E.g. [\"/skills/research/\", \"/skills/coding/\"]",
    )


class MemoryConfig(BaseModel):
    """Memory configuration — persistent context via AGENTS.md files."""
    paths: list[str] = Field(
        ...,
        description="Paths to memory files (AGENTS.md). E.g. [\"/AGENTS.md\"]",
    )
    initial_content: dict[str, str] | None = Field(
        default=None,
        description="Seed memory files: {path: content}. Loaded on first invoke.",
    )


class ResponseFormatField(BaseModel):
    """A single field in a structured response schema."""
    name: str = Field(..., description="Field name")
    type: str = Field(default="string", description="Field type: string | number | boolean | array | object")
    description: str = Field(default="", description="Field description for the LLM")
    required: bool = Field(default=True, description="Whether the field is required")


class ResponseFormatConfig(BaseModel):
    """Structured output schema — agent returns data matching this shape."""
    name: str = Field(..., description="Schema name (becomes the Pydantic model name)")
    fields: list[ResponseFormatField] = Field(
        ..., min_length=1,
        description="Fields the agent must populate in its response",
    )


class CacheConfig(BaseModel):
    """LLM response cache configuration."""
    enabled: bool = Field(default=False, description="Enable LLM response caching")
    type: str = Field(
        default="in_memory",
        description="Cache backend: in_memory | redis | sqlite",
    )
    ttl: int | None = Field(default=None, description="Cache TTL in seconds")


# ---------------------------------------------------------------------------
# Request schemas
# ---------------------------------------------------------------------------


class AgentCreateRequest(BaseModel):
    """Full agent creation request with all customization knobs."""

    # Identity
    agent_id: str = Field(..., description="Unique agent identifier")
    name: str | None = Field(default=None, description="Human-readable agent name")

    # Model — string or full config dict
    model: str | dict[str, Any] | None = Field(
        default=None,
        description="Model identifier. String format: 'ollama:llama3.1:8b', "
                    "'gateway:my-model', 'openai:gpt-4.1', 'claude-sonnet-4-5-20250929'. "
                    "Dict format for full control: "
                    "{'provider': 'gateway', 'model': 'llama3', 'base_url': 'http://...', "
                    "'api_key': '...', 'temperature': 0.7, 'max_tokens': 4096}",
    )

    # Prompt
    system_prompt: str | None = Field(
        default=None,
        description="Custom system instructions prepended before the base deep agent prompt",
    )

    # Tools
    tools: list[str] | None = Field(
        default=None,
        description="Names of registered tools to make available to the agent",
    )

    # Middleware
    middleware: list[str] | None = Field(
        default=None,
        description="Names of registered middleware functions. Applied after the default stack "
                    "(TodoList, Filesystem, SubAgent, Summarization, PromptCaching, PatchToolCalls)",
    )

    # Subagents
    subagents: list[SubAgentConfig] | None = Field(
        default=None,
        description="Subagent definitions spawnable via the built-in `task` tool",
    )

    # Backend & Store
    backend: BackendConfig | None = Field(
        default=None,
        description="Backend storage configuration (state, filesystem, store, composite)",
    )

    # Human-in-the-loop
    interrupt_on: dict[str, InterruptRule | bool] | None = Field(
        default=None,
        description="Map tool names to interrupt rules for human approval. "
                    "E.g. {\"delete_file\": true, \"send_email\": {\"allowed_decisions\": [\"approve\", \"reject\"]}}",
    )

    # Skills
    skills: SkillConfig | None = Field(
        default=None,
        description="Task-specific knowledge files loaded contextually via SKILL.md",
    )

    # Memory
    memory: MemoryConfig | None = Field(
        default=None,
        description="Persistent context via AGENTS.md files",
    )

    # Structured output
    response_format: ResponseFormatConfig | None = Field(
        default=None,
        description="Structured output schema — agent returns data matching this shape",
    )

    # Cache
    cache: CacheConfig | None = Field(
        default=None,
        description="LLM response cache configuration",
    )

    # Debug
    debug: bool = Field(
        default=False,
        description="Enable debug mode for verbose agent logging",
    )


class InvokeRequest(BaseModel):
    """Agent invocation request."""
    messages: list[Message] = Field(..., min_length=1, description="Conversation messages")
    thread_id: str | None = Field(
        default=None,
        description="Thread ID for conversation persistence. Auto-generated if omitted.",
    )
    model: str | None = Field(default=None, description="Model override for this invocation")
    system_prompt: str | None = Field(default=None, description="System prompt override")
    tools: list[str] | None = Field(default=None, description="Names of registered tools to enable")
    files: dict[str, str] | None = Field(
        default=None,
        description="Virtual files to seed into agent state. "
                    "Keys are POSIX paths (e.g. /AGENTS.md), values are file content.",
    )
    skills_files: dict[str, str] | None = Field(
        default=None,
        description="Skill files to load. Keys are paths (e.g. /skills/research/SKILL.md), "
                    "values are SKILL.md content.",
    )
    memory_files: dict[str, str] | None = Field(
        default=None,
        description="Memory files to seed. Keys are paths (e.g. /AGENTS.md), values are content.",
    )
    trace: bool = Field(
        default=True,
        description="Include full execution trace in response (input prompts, tool calls, timing)",
    )


class StreamRequest(InvokeRequest):
    """Same as InvokeRequest — response streamed via SSE."""
    pass


class ResumeRequest(BaseModel):
    """Resume an interrupted agent (human-in-the-loop)."""
    thread_id: str = Field(..., description="Thread ID of the interrupted conversation")
    decision: str = Field(
        default="approve",
        description="Decision for the interrupted tool call: approve | reject | edit",
    )
    edit_args: dict[str, Any] | None = Field(
        default=None,
        description="Modified tool arguments when decision is 'edit'",
    )


# ---------------------------------------------------------------------------
# Response schemas
# ---------------------------------------------------------------------------


class FileUploadRequest(BaseModel):
    """Upload/overwrite a file in the agent's workspace."""
    path: str = Field(..., description="Absolute path in the workspace (e.g. /workspace/slides.md)")
    content: str = Field(..., description="File content as a string")
    agent_id: str | None = Field(default=None, description="Agent ID (defaults to 'default')")


class InvokeResponse(BaseModel):
    thread_id: str = Field(..., description="Thread ID for follow-up messages")
    response: str = Field(..., description="Agent's final response text")
    tool_calls: list[dict[str, Any]] = Field(default_factory=list, description="Tool calls made")
    todos: list[dict[str, Any]] = Field(default_factory=list, description="Todo items created by write_todos")
    files: dict[str, Any] = Field(default_factory=dict, description="Files written by agent")
    structured_response: dict[str, Any] | None = Field(
        default=None,
        description="Structured output (present when agent has a response_format)",
    )
    interrupted: bool = Field(
        default=False,
        description="True if the agent paused for human-in-the-loop approval",
    )
    interrupted_tool: str | None = Field(
        default=None,
        description="Name of the tool that triggered the interrupt",
    )
    interrupted_args: dict[str, Any] | None = Field(
        default=None,
        description="Arguments of the interrupted tool call",
    )
    trace: dict[str, Any] | None = Field(
        default=None,
        description="Full execution trace with events, timing, and input prompts. "
                    "Includes: agent_start, input_prompt, files_seeded, agent_end.",
    )


class AgentInfo(BaseModel):
    agent_id: str
    name: str | None = None
    model: str
    system_prompt: str | None = None
    tools: list[str]
    subagents: list[str]
    middleware: list[str] = Field(default_factory=list)
    backend_type: str = "state"
    has_interrupt_on: bool = False
    skills: list[str] = Field(default_factory=list, description="Skill directory virtual paths")
    loaded_skills: list[str] = Field(default_factory=list, description="Skill names loaded from disk")
    memory: list[str] = Field(default_factory=list)
    has_response_format: bool = False
    cache_enabled: bool = False
    debug: bool = False


class HealthResponse(BaseModel):
    status: str = "ok"
    agents_loaded: int = 0

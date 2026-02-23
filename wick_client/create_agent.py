#!/usr/bin/env python3
"""Create and run a hello world agent."""

# WickClient talks to the server, WickServer manages its lifecycle.
from wick_deep_agent import WickClient, WickServer

# Message types live in the messages submodule.
from wick_deep_agent.messages import HumanMessage


def main() -> None:
    # Compile the Go server source into a binary (server/wick_go).
    # This is a no-op if the binary is already up to date.
    WickServer.build()

    # Create a server instance with one agent defined inline.
    # - port: which port the HTTP server listens on
    # - agents: dict of agent_id -> config (no YAML file needed)
    #   - name: display name for the agent
    #   - model: LLM to use (provider:model format)
    #   - system_prompt: instructions the LLM sees before user messages
    server = WickServer(
        port=8000,
        agents={
            "hello": {
                "name": "Hello World",
                "model": "ollama:llama3.1:8b",
                "system_prompt": "You are a friendly assistant. Keep responses short.",
            },
        },
    )

    # Start the server process in the background.
    # If a server is already running (detected via PID file), this returns
    # the existing PID and does NOT start a second process.
    pid = server.start()
    print(f"Server running (pid={pid})")

    # Block until the server responds to GET /health, up to 10 seconds.
    # Returns True if healthy, False if timed out.
    if not server.wait_ready():
        print("Server failed to start. Logs:")
        print(server.logs())
        server.stop()
        return

    print("Server ready.\n")

    try:
        # Create an HTTP client pointing at the server.
        client = WickClient("http://localhost:8000")

        # Send "Say hello world!" to the "hello" agent and wait for the response.
        # HumanMessage("...") creates {"role": "user", "content": "..."}.
        # agent_id selects which agent handles the request.
        result = client.invoke(HumanMessage("Say hello world!"), agent_id="hello")

        # result is the full JSON response from the server.
        print(result)
    finally:
        # Stop the server process (sends SIGTERM, waits up to 5s, then SIGKILL).
        # Safe to call even if the server was already running before this script —
        # it just cleans up the PID file.
        server.stop()
        print("\nServer stopped.")


if __name__ == "__main__":
    main()

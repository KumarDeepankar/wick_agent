#!/usr/bin/env python3
"""Getting started with wick_deep_agent — zero file dependencies."""

from __future__ import annotations

from wick_deep_agent import WickClient, WickServer
from wick_deep_agent.messages import HumanMessage, Messages, SystemMessage


def main() -> None:
    # 1. Build the server
    print("=== Building Server ===")
    WickServer.build()
    print("  build OK")

    # 2. Start with inline agent config — no YAML file needed
    print("\n=== Starting Server ===")
    server = WickServer(
        port=8000,
        agents={
            "default": {
                "name": "Ollama Local",
                "model": "ollama:llama3.1:8b",
                "system_prompt": "You are a helpful assistant. Be concise.",
            },
        },
    )
    pid = server.start()
    if not server.wait_ready():
        print("  FAIL: server did not become ready")
        print(server.logs(n=20))
        server.stop()
        return
    print(f"  server running (pid={pid})")

    try:
        client = WickClient("http://localhost:8000")

        # 3. Health check
        print("\n=== Health Check ===")
        print(f"  {client.health()}")

        # 4. List agents
        print("\n=== Available Agents ===")
        agents = client.list_agents()
        for a in agents:
            print(f"  - {a.get('agent_id', a.get('id', '?'))}: {a.get('name', '')}")

        if not agents:
            print("  (no agents configured)")
            return

        agent_id = agents[0].get("agent_id", agents[0].get("id"))
        print(f"\n  Using agent: {agent_id}")

        # 5. Simple invoke
        print("\n=== Invoke ===")
        result = client.invoke(HumanMessage("Hello! Who are you?"), agent_id=agent_id)
        print(f"  Response: {result}")

        # 6. Invoke with message chain
        print("\n=== Invoke with Message Chain ===")
        chain = (
            SystemMessage("You are a helpful assistant. Be concise.")
            + HumanMessage("What is 2 + 2?")
        )
        result = client.invoke(chain, agent_id=agent_id)
        print(f"  Response: {result}")

        # 7. Streaming
        print("\n=== Stream ===")
        for event in client.stream(HumanMessage("Count from 1 to 5."), agent_id=agent_id):
            etype = event.get("event", "")
            data = event.get("data", "")
            print(f"  [{etype}] {data}")

        # 8. Fluent message builder
        print("\n=== Fluent Message Builder ===")
        msgs = Messages().system("You are a pirate.").human("Greet me.")
        print(f"  Built chain: {msgs!r}")
        print(f"  As dicts: {msgs.to_list()}")

        print("\nDone.")
        client.shutdown()

    finally:
        print("\n=== Stopping Server ===")
        server.stop()
        print("  stopped")


if __name__ == "__main__":
    main()

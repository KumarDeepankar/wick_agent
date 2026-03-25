"""Simple agent with Python tools and Go-native LLM.

The Go server handles the LLM calls (OpenAI in this case).
Python only provides custom tool implementations.
No FastAPI sidecar is needed if you remove the tools.
"""

from wick import Agent, BackendConfig

agent = Agent(
    "calculator-agent",
    name="Calculator",
    model={
        "provider": "openai",
        "model": "gpt-4o",
        "api_key": "your-api-key-here",  # or set OPENAI_API_KEY
    },
    system_prompt="You are a helpful calculator assistant. Use the provided tools.",
    backend=BackendConfig(type="local", workdir="/tmp/wick-workspace"),
    debug=True,
)


@agent.tool(description="Add two numbers together")
def add(a: float, b: float) -> str:
    return str(a + b)


@agent.tool(description="Multiply two numbers together")
def multiply(a: float, b: float) -> str:
    return str(a * b)


@agent.tool(
    description="Search documents by keyword",
    parameters={
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "Search query"},
            "limit": {"type": "integer", "description": "Max results (default 5)"},
        },
        "required": ["query"],
    },
)
def search_docs(query: str, limit: int = 5) -> str:
    # Replace with your actual search logic (FAISS, Elasticsearch, etc.)
    return f"Found {limit} results for '{query}': [doc1, doc2, ...]"


if __name__ == "__main__":
    # Dev mode: starts Go binary + FastAPI sidecar automatically
    agent.run(
        go_port=8000,
        sidecar_port=9100,
    )

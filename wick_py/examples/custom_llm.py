"""Full agent setup for containerized deployment.

Registers two agents:
  - "default" — Ollama local (Go-native LLM)
  - "gateway-claude" — Custom gateway proxy (Anthropic-compatible)

Uses local backend — each user gets /workspace/{username}/.
Users can switch to Remote Docker in settings for full isolation.
"""

import asyncio
import json
import logging
import os
import threading
from datetime import datetime, timezone
from pathlib import Path

import httpx
from wick import Agent, LLMRequest, SkillsConfig, StreamChunk, ToolCallResult, tool
from gateway_auth import fetch_token

logger = logging.getLogger("wick.gateway")

# ── Paths ────────────────────────────────────────────────────────────────

repo_root = Path(__file__).resolve().parent.parent.parent
skills_dir = os.environ.get("WICK_SKILLS_DIR") or str(repo_root / "wick_py" / "skills")

# ── Tools (global pool — agents select via builtin_tools) ─────────────────

@tool(description="Get the current date and time in ISO format")
def current_datetime() -> str:
    return datetime.now(timezone.utc).isoformat()


@tool(description="Calculate a math expression (e.g. '2 + 3 * 4')")
def calculate(expression: str) -> str:
    allowed = set("0123456789+-*/.() ")
    if not all(c in allowed for c in expression):
        return "Error: only numeric expressions with +, -, *, /, (, ) are allowed"
    return str(eval(expression))


# ── Shared config ────────────────────────────────────────────────────────

system_prompt = """You are a helpful AI assistant. Use your available tools and skills to complete tasks. Write output files to the workspace directory."""

shared_config = {
    "backend": {"type": "local", "workdir": "/workspace"},
    "skills": SkillsConfig(paths=[skills_dir], exclude=["slides", "report-generator"]),
    "debug": True,
}

# ── Sub-agents ───────────────────────────────────────────────────────────

math_agent = Agent(
    "math",
    name="Math Assistant",
    system_prompt="You are a math assistant. Use the calculate tool to evaluate expressions. Show your work step by step.",
    builtin_tools=["calculate"],
)

REPORT_GENERATOR_PROMPT = """\
You generate visual slide-deck reports from research artifacts on disk.

## File Paths
All paths must be RELATIVE (no leading /). Example: research/my_index/report.md
NEVER use absolute paths like /workspace/..., /app/..., or /tmp/...

The task message you receive will contain these fields:
- Source directory: e.g. `research/<index_name>/`
- Focus: the user's original query / what to emphasize
- Primary data: the exact relative path to the consolidated report file
- Output: the exact relative path where you must write the slide deck

Use these exact paths verbatim — do not invent or rewrite them.

## Execution Steps (follow in order)

### Step 1: Read artifacts in this EXACT order
1. ls the source directory from the task message to see what's actually on disk.
2. read_file: the `Primary data` path from the task message
   (`<source>/final_report.md` for research runs, `<source>/comparison_report.md`
   for comparison runs). This is your PRIMARY data source — it has all
   consolidated findings.
3. ONLY if the primary report lacks numeric detail needed for a chart, read
   1-2 batch files from `<source>/batches/` (e.g. batch_001.md). There is no
   `summaries/` directory — do not try to read from it.

### Step 2: Write the report in a SINGLE write_file call
Write to the exact `Output` path from the task message
(typically `<source>/report.md`). You MUST issue this write_file call —
the run is not complete until report.md exists on disk.

The file MUST start with these two lines:
```
<!-- slides -->
<!-- theme: corporate -->
```
Aim for 8-15 slides. Separate slides with `---` on its own line.

### Themes
Pick the deck theme on line 2: `<!-- theme: corporate -->` (default and best
for research reports). Alternatives: `dark` (technical/demo aesthetic),
`editorial` (long-form qualitative), `vibrant` (pitches/marketing).

### Slide Layouts
Each slide can opt into a layout via `<!-- layout: name -->` placed BEFORE
the slide's `# Title`. Available layouts:
- `title` — cover slide, centered big title + subtitle. Use for slide 1.
- `section` — chapter divider with kicker text. Use between major parts.
- `content` — default. Title + bullets + optional charts.
- `content_chart` — chart-emphasized. Caption-sized body, big chart area.
  Use for any slide where the chart IS the point.
- `two_column` — side-by-side comparison. Wrap each column in `:::col1` /
  `:::col2` fenced divs.

Recommended slide sequence:
1. `title` — cover
2. `content` — Executive Summary (3-5 bullets)
3. `content` — Data Overview
4. `section` — "Findings" divider
5-7. `content_chart` — one finding per slide with chart
8. `section` — "Comparisons" or "Next Steps" divider (if needed)
9. `two_column` — comparisons (if applicable)
10. `content` — Recommendations

Slide format:
- First slide: `<!-- layout: title -->` then `# Title`
- All other slides: `## Heading` (becomes slide title), optionally preceded
  by a `<!-- layout: ... -->` directive
- Mix layouts so the deck has visual rhythm — don't use only `content`

### Chart DSL (embed in markdown code blocks)

```chart
type: bar
title: Chart Title
labels: [Label1, Label2, Label3]
data: [100, 200, 150]
showValues: true
```

Chart types: bar, hbar, line, area, pie, donut, stacked_bar

Multi-series (for comparisons):
```chart
type: bar
title: Comparison
labels: [Cat A, Cat B, Cat C]
series:
  - name: Group 1
    data: [10, 20, 30]
  - name: Group 2
    data: [15, 25, 20]
legend: true
```

Optional fields: legend, legendPosition, xLabel, yLabel.
**Do NOT set `colors:`** — the deck theme provides a curated palette and
auto-colors all chart series. Override only to encode meaning (red=error,
green=success).

### Rules
- NEVER fabricate numbers — every data point must come from the artifacts
- For fields with many values, show top 5-8 in chart, mention others in text
- One chart per insight — don't overload slides
- Extract real data: aggregation counts, batch statistics, report findings
- If artifacts lack numeric data, use tables and qualitative slides instead
- Use `content_chart` layout (not `content`) for any slide where the chart
  is the primary content — this gives the chart 70% of the slide height
"""

report_agent = Agent(
    "report-generator",
    name="Report Generator",
    system_prompt=REPORT_GENERATOR_PROMPT,
    builtin_tools=["read_file", "write_file", "ls", "glob"],
)

batch_processor_agent = Agent(
    "batch-processor",
    name="Batch Processor",
    system_prompt="You are a batch processing worker. You receive a task with:\n"
    "- A command to execute (to fetch data)\n"
    "- An analysis instruction (what to look for — themes, patterns, outliers, distributions)\n"
    "- An output path (where to write findings)\n\n"
    "Steps:\n"
    "1. Execute the command to fetch the data batch.\n"
    "2. Analyze the output according to the instruction.\n"
    "3. Write structured findings (key themes, notable data points, field distributions, "
    "data quality issues) to the output path using write_file.\n"
    "4. Return a 1-2 line summary of the batch findings.\n\n"
    "IMPORTANT: Always use the exact path provided in the task for write_file. "
    "Paths must be relative (no leading /). Example: research/my_index/batches/batch_001.md\n"
    "NEVER use absolute paths like /workspace/..., /app/..., or /tmp/... — they will fail.",
    builtin_tools=["execute", "read_file", "write_file"],
)

summarizer_agent = Agent(
    "summarizer",
    name="Summarizer",
    system_prompt="You are a summarization agent. You receive a task with:\n"
    "- A glob pattern or list of file paths to read\n"
    "- A summarization query (what to extract, how to aggregate, what to focus on)\n"
    "- An output path for the summary\n\n"
    "Steps:\n"
    "1. Use glob or ls to find the files matching the pattern.\n"
    "2. Read all specified files using read_file.\n"
    "3. Synthesize the content according to the query — merge common themes, "
    "rank by frequency/importance, preserve key statistics and evidence.\n"
    "4. Write the result to the output path using write_file.\n"
    "5. Return a brief summary of what was produced.\n\n"
    "IMPORTANT: All file paths must be relative (no leading /). "
    "Example: research/my_index/batches/batch_001.md\n"
    "NEVER use absolute paths like /workspace/..., /app/..., or /tmp/... — they will fail.\n"
    "Never fabricate data — every claim must trace to a source file. "
    "Focus on synthesis and insight, not concatenation.",
    builtin_tools=["read_file", "write_file", "glob", "ls"],
)

# ── Agent: gateway-claude (Anthropic via Python LLM proxy) ───────────────

claude_agent = Agent(
    "gateway-claude",
    name="Claude",
    system_prompt=system_prompt,
    builtin_tools=["calculate", "current_datetime"],
    subagents=[report_agent, batch_processor_agent, summarizer_agent],
    **shared_config,
)

# ── Agent: default (Ollama, Go-native LLM) ──────────────────────────────

ollama_host = os.environ.get("OLLAMA_HOST", "http://localhost:11434")

ollama_agent = Agent(
    "default",
    name="Ollama Local",
    model={"provider": "ollama", "model": "llama3.1:8b", "base_url": f"{ollama_host}/v1"},
    system_prompt=system_prompt,
    subagents=[math_agent, report_agent, batch_processor_agent, summarizer_agent],
    **shared_config,
)

# ── Gateway LLM provider for Claude ─────────────────────────────────────

GATEWAY_URL = os.environ.get("GATEWAY_URL", "https://xyz-abc")
GATEWAY_MODEL = os.environ.get("GATEWAY_MODEL", "anthropic.claude-4-5-sonnet-v1:0")
TOKEN_REFRESH_INTERVAL = 20 * 60  # 20 minutes

# Token state — refreshed by background thread
GATEWAY_TOKEN = ""
_token_lock = threading.Lock()


def _refresh_token():
    global GATEWAY_TOKEN
    try:
        new_token = fetch_token()
        with _token_lock:
            GATEWAY_TOKEN = new_token
        logger.info("Gateway token refreshed")
    except Exception as e:
        logger.error("Token refresh failed: %s", e)


def _token_refresh_loop():
    """Background thread that refreshes the token every 20 minutes."""
    while True:
        _refresh_token()
        threading.Event().wait(TOKEN_REFRESH_INTERVAL)


def _get_token() -> str:
    with _token_lock:
        return GATEWAY_TOKEN


# Fetch initial token and start refresh thread
_refresh_token()
_refresh_thread = threading.Thread(target=_token_refresh_loop, daemon=True)
_refresh_thread.start()


@claude_agent.llm_provider("claude-sonnet")
async def gateway_llm(request: LLMRequest):
    """Route LLM calls to gateway (OpenAI chat completions format)."""

    # Build messages in OpenAI chat completions format.
    # Must include all roles: system, user, assistant (with tool_calls), and tool.
    messages = []
    if request.system_prompt:
        messages.append({"role": "system", "content": request.system_prompt})
    for msg in request.messages:
        if msg.role == "user":
            messages.append({"role": "user", "content": msg.content})
        elif msg.role == "assistant":
            m = {"role": "assistant", "content": msg.content or ""}
            # Preserve tool_calls on assistant messages (OpenAI format)
            if hasattr(msg, "tool_calls") and msg.tool_calls:
                m["tool_calls"] = [
                    {
                        "id": tc.id,
                        "type": "function",
                        "function": {
                            "name": tc.name,
                            "arguments": json.dumps(tc.args) if isinstance(tc.args, dict) else tc.args,
                        },
                    }
                    for tc in msg.tool_calls
                ]
            messages.append(m)
        elif msg.role == "tool":
            messages.append({
                "role": "tool",
                "tool_call_id": msg.tool_call_id,
                "content": msg.content or "",
            })

    # Build tools in OpenAI function-calling format
    tools = []
    if request.tools:
        for t in request.tools:
            tools.append({
                "type": "function",
                "function": {
                    "name": t.name,
                    "description": t.description,
                    "parameters": t.parameters,
                },
            })

    payload = {
        "model": GATEWAY_MODEL,
        "max_tokens": request.max_tokens or 4096,
        "messages": messages,
        "stream": True,
    }
    if tools:
        payload["tools"] = tools

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {_get_token()}",
    }

    # Streaming: read timeout only needs to cover the gap between successive
    # SSE chunks, not the entire generation. 60s is generous for inter-token
    # silence; connect/write/pool stay tight.
    timeout = httpx.Timeout(connect=10.0, read=60.0, write=10.0, pool=5.0)

    # Accumulator for tool calls arriving as fragments across SSE chunks.
    # Key: tool call index → {id, name, arguments_json}
    pending_tool_calls: dict[int, dict] = {}

    async with httpx.AsyncClient(timeout=timeout) as client:
        async with client.stream(
            "POST",
            f"{GATEWAY_URL}/chat/completions",
            headers=headers,
            json=payload,
        ) as resp:
            resp.raise_for_status()

            async for line in resp.aiter_lines():
                # SSE format: "data: {...}" or "data: [DONE]"
                if not line.startswith("data: "):
                    continue
                data = line[6:]
                if data.strip() == "[DONE]":
                    break

                try:
                    chunk = json.loads(data)
                except json.JSONDecodeError:
                    logger.warning("Skipping malformed SSE chunk: %s", data[:120])
                    continue

                choices = chunk.get("choices") or []
                if not choices:
                    continue
                delta = choices[0].get("delta") or {}

                # Text content — yield each fragment immediately
                text = delta.get("content")
                if text:
                    yield StreamChunk(delta=text)

                # Tool calls — accumulate fragments, yield when complete
                for tc in (delta.get("tool_calls") or []):
                    idx = tc.get("index", 0)
                    if idx not in pending_tool_calls:
                        pending_tool_calls[idx] = {
                            "id": tc.get("id", ""),
                            "name": (tc.get("function") or {}).get("name", ""),
                            "arguments": "",
                        }
                    entry = pending_tool_calls[idx]
                    if tc.get("id"):
                        entry["id"] = tc["id"]
                    func = tc.get("function") or {}
                    if func.get("name"):
                        entry["name"] = func["name"]
                    if func.get("arguments"):
                        entry["arguments"] += func["arguments"]

    # Yield fully-assembled tool calls after the stream ends
    for idx in sorted(pending_tool_calls):
        entry = pending_tool_calls[idx]
        try:
            args = json.loads(entry["arguments"]) if entry["arguments"] else {}
        except json.JSONDecodeError:
            logger.error("Malformed tool call args for %s: %s", entry["name"], entry["arguments"][:200])
            args = {}
        yield StreamChunk(tool_call=ToolCallResult(
            id=entry["id"] or f"call_{idx}",
            name=entry["name"],
            args=args,
        ))

    yield StreamChunk(done=True)


# ── Main ────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    claude_agent.run(
        go_binary=os.environ.get("WICK_SERVER_BINARY"),
        go_port=8000,
        sidecar_port=9100,
        extra_agents=[ollama_agent],
    )

"""ASCII flow diagram renderer for the wick agent loop.

Usage::

    from wick_deep_agent import WickClient

    client = WickClient("http://localhost:8000")
    client.print_flow("default")

Or standalone::

    from wick_deep_agent.flow import print_flow
    flow_data = client.get_flow("default")
    print_flow(flow_data)
"""

from __future__ import annotations

from typing import Any


def flow_to_dict(flow_data: dict[str, Any]) -> dict[str, Any]:
    """Return a structured summary of the agent flow for programmatic use."""
    hooks_by_phase: dict[str, list[str]] = {}
    for hook in flow_data.get("hooks", []):
        for phase in hook.get("phases", []):
            hooks_by_phase.setdefault(phase, []).append(hook["name"])

    return {
        "agent_id": flow_data.get("agent_id", ""),
        "model": flow_data.get("model", ""),
        "max_iterations": flow_data.get("max_iterations", 25),
        "hooks_by_phase": hooks_by_phase,
        "hook_names": [h["name"] for h in flow_data.get("hooks", [])],
        "tools": flow_data.get("tools", []),
    }


def print_flow(flow_data: dict[str, Any]) -> None:
    """Print an ASCII flow diagram of the agent loop to stdout."""
    summary = flow_to_dict(flow_data)
    agent_id = summary["agent_id"]
    model = summary["model"]
    max_iter = summary["max_iterations"]
    hooks_by_phase = summary["hooks_by_phase"]
    tools = summary["tools"]

    # Outer box inner content width
    OW = 51
    # Inner box inner content width (OW - 6 for "│  " prefix + "  │" suffix)
    IW = OW - 6

    def box(title: str, content_lines: list[str], width: int) -> list[str]:
        """Render a titled box with the given inner width."""
        out: list[str] = []
        header = f" {title} "
        dashes = width - len(header)
        if dashes < 0:
            dashes = 0
        out.append(f"\u250c\u2500{header}{'─' * dashes}\u2510")
        for line in content_lines:
            padded = line[:width].ljust(width)
            out.append(f"\u2502  {padded}\u2502")
        out.append(f"\u2514{'─' * (width + 2)}\u2518")
        return out

    def arrow() -> list[str]:
        return ["           \u2502", "           \u25bc"]

    def join_names(names: list[str]) -> str:
        return " \u2192 ".join(names) if names else "(none)"

    def pad_outer(text: str) -> str:
        """Pad a line to fit inside the outer loop box."""
        return f"\u2502  {text[:OW].ljust(OW)}\u2502"

    # Header
    lines: list[str] = []
    lines.append(f"Agent: {agent_id} ({model})")
    lines.append(f"Max iterations: {max_iter}")
    lines.append("")

    # BeforeAgent box (standalone, uses OW)
    ba_hooks = hooks_by_phase.get("before_agent", [])
    lines.extend(box("BeforeAgent", [join_names(ba_hooks)], OW))
    lines.extend(arrow())

    # Loop box (outer)
    loop_header = f" Loop (max {max_iter} iterations) "
    loop_dashes = OW - len(loop_header)
    if loop_dashes < 0:
        loop_dashes = 0
    lines.append(f"\u250c\u2500{loop_header}{'─' * loop_dashes}\u2510")
    lines.append(f"\u2502{' ' * (OW + 2)}\u2502")

    # ModifyRequest (nested box, uses IW)
    mr_hooks = hooks_by_phase.get("modify_request", [])
    for row in box("ModifyRequest", [join_names(mr_hooks)], IW):
        lines.append(pad_outer(row))

    lines.append(pad_outer("         \u2502"))
    lines.append(pad_outer("         \u25bc"))

    # LLM Call (nested box, uses IW)
    wmc_hooks = hooks_by_phase.get("wrap_model_call", [])
    for row in box("LLM Call", [
        f"model: {model}",
        f"wraps: {join_names(wmc_hooks)}",
    ], IW):
        lines.append(pad_outer(row))

    lines.append(pad_outer("         \u2502"))
    lines.append(pad_outer("     \u250c\u2500\u2500\u2500\u2534\u2500\u2500\u2500\u2510"))
    lines.append(pad_outer("     \u2502       \u2502  (parallel)"))

    # Tool Calls (nested box, uses IW)
    wtc_hooks = hooks_by_phase.get("wrap_tool_call", [])
    tool_display = ", ".join(tools[:5])
    if len(tools) > 5:
        tool_display += ", ..."
    for row in box("Tool Calls", [
        f"wraps: {join_names(wtc_hooks)}",
        f"tools: {tool_display}",
    ], IW):
        lines.append(pad_outer(row))

    lines.append(pad_outer("         \u2502"))
    lines.append(pad_outer("   no tool calls? \u2500\u2500yes\u2500\u2500\u25b6 exit loop"))
    lines.append(pad_outer("         \u2502 no"))
    lines.append(pad_outer("         \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500 next iteration"))

    # Close loop box
    lines.append(f"\u2514{'─' * (OW + 2)}\u2518")

    # Done
    lines.extend(arrow())
    lines.append("        \u2713 Done")

    print("\n".join(lines))

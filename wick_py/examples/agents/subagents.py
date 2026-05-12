"""Sub-agent catalog — one `build_*` function per sub-agent.

Each factory returns a fresh `Agent` instance so supervisors never share
mutable sub-agent state. Which sub-agents a supervisor uses is a
supervisor-level decision — see `server.py`.

## Delegation mode

Each sub-agent's `mode` decides how supervisors can invoke it:

    mode="sync"   — via delegate_to_agent (blocks supervisor until done).
                    Default. Best for short tasks (< 10s).
    mode="async"  — via start_async_task / check_async_task / ...
                    Background task, supervisor continues immediately.
                    Best for long-running or parallelizable work.
    mode="both"   — both tools available. Supervisor picks per call.

Rule of thumb: if the expected runtime is < 10s, use "sync". If the
task takes minutes or you want N of them in parallel, use "async".
"""

from __future__ import annotations

from wick import Agent

from . import prompts


def build_math_agent() -> Agent:
    return Agent(
        "math",
        name="Math Assistant",
        system_prompt=prompts.load("math"),
        builtin_tools=["calculate"],
        mode="sync",  # fast single-step arithmetic
    )


def build_report_agent() -> Agent:
    return Agent(
        "report-generator",
        name="Report Generator",
        system_prompt=prompts.load("report_generator"),
        builtin_tools=["read_file", "write_file", "ls", "glob"],
        mode="sync",  # final step — supervisor blocks once, returns "report ready"
    )


def build_batch_processor() -> Agent:
    return Agent(
        "batch-processor",
        name="Batch Processor",
        system_prompt=prompts.load("batch_processor"),
        builtin_tools=["execute", "read_file", "write_file"],
        mode="async",  # launched many-at-once to process batches in parallel
    )


def build_summarizer() -> Agent:
    return Agent(
        "summarizer",
        name="Summarizer",
        system_prompt=prompts.load("summarizer"),
        builtin_tools=["read_file", "write_file", "glob", "ls"],
        mode="both",  # short summaries sync, large multi-file runs async
    )


def build_scenario_modeler() -> Agent:
    """Sub-agent for the Smart Planner — runs one what-if scenario per
    invocation by calling the deterministic `launch-planner` CLI. The
    supervisor fans out N scenarios in parallel via start_async_task."""
    return Agent(
        "scenario-modeler",
        name="Scenario Modeler",
        system_prompt=prompts.load("scenario_modeler"),
        builtin_tools=["execute", "write_file", "read_file"],
        mode="async",  # parallel what-if exploration
    )

"""Sub-agent catalog — one `build_*` function per sub-agent.

Each factory returns a fresh `Agent` instance so supervisors never share
mutable sub-agent state. Which sub-agents a given supervisor uses is a
supervisor-level decision — see `supervisors.py`.
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
    )


def build_report_agent() -> Agent:
    return Agent(
        "report-generator",
        name="Report Generator",
        system_prompt=prompts.load("report_generator"),
        builtin_tools=["read_file", "write_file", "ls", "glob"],
    )


def build_batch_processor() -> Agent:
    return Agent(
        "batch-processor",
        name="Batch Processor",
        system_prompt=prompts.load("batch_processor"),
        builtin_tools=["execute", "read_file", "write_file"],
    )


def build_summarizer() -> Agent:
    return Agent(
        "summarizer",
        name="Summarizer",
        system_prompt=prompts.load("summarizer"),
        builtin_tools=["read_file", "write_file", "glob", "ls"],
    )

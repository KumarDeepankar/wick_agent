"""Tracing & callback infrastructure for full agent visibility.

Captures every step of agent execution:
  - Input prompt / messages sent to LLM
  - Model invocations (start, stream, end) with timing
  - Tool calls and results with timing
  - Graph node transitions
  - Errors

Used by both invoke (returns trace in response) and stream (emits trace events).
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from typing import Any


# ═══════════════════════════════════════════════════════════════════════════
# Trace event types
# ═══════════════════════════════════════════════════════════════════════════


@dataclass
class TraceEvent:
    """A single trace event in the agent execution timeline."""
    event_type: str            # e.g. "input_prompt", "llm_start", "tool_call"
    timestamp: float           # time.time()
    data: dict[str, Any]       # event-specific payload
    duration_ms: float | None = None  # filled on *_end events
    run_id: str = ""           # links start/end events

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {
            "event_type": self.event_type,
            "timestamp": self.timestamp,
            "data": self.data,
        }
        if self.duration_ms is not None:
            d["duration_ms"] = round(self.duration_ms, 2)
        if self.run_id:
            d["run_id"] = self.run_id
        return d


@dataclass
class AgentTrace:
    """Full trace of an agent invocation."""
    trace_id: str = field(default_factory=lambda: str(uuid.uuid4())[:8])
    agent_id: str = ""
    thread_id: str = ""
    events: list[TraceEvent] = field(default_factory=list)
    _start_times: dict[str, float] = field(default_factory=dict, repr=False)
    started_at: float = field(default_factory=time.time)

    def add(self, event_type: str, data: dict[str, Any], run_id: str = "") -> TraceEvent:
        evt = TraceEvent(
            event_type=event_type,
            timestamp=time.time(),
            data=data,
            run_id=run_id,
        )
        self.events.append(evt)
        return evt

    def start_timer(self, key: str) -> None:
        self._start_times[key] = time.time()

    def stop_timer(self, key: str) -> float | None:
        start = self._start_times.pop(key, None)
        if start is not None:
            return (time.time() - start) * 1000
        return None

    def add_timed(
        self, event_type: str, data: dict[str, Any], timer_key: str, run_id: str = "",
    ) -> TraceEvent:
        duration = self.stop_timer(timer_key)
        evt = TraceEvent(
            event_type=event_type,
            timestamp=time.time(),
            data=data,
            duration_ms=duration,
            run_id=run_id,
        )
        self.events.append(evt)
        return evt

    @property
    def total_duration_ms(self) -> float:
        return round((time.time() - self.started_at) * 1000, 2)

    def to_dict(self) -> dict[str, Any]:
        return {
            "trace_id": self.trace_id,
            "agent_id": self.agent_id,
            "thread_id": self.thread_id,
            "total_duration_ms": self.total_duration_ms,
            "event_count": len(self.events),
            "events": [e.to_dict() for e in self.events],
        }

    def summary(self) -> dict[str, Any]:
        """Compact summary — event types + timing, no payloads."""
        return {
            "trace_id": self.trace_id,
            "total_duration_ms": self.total_duration_ms,
            "event_count": len(self.events),
            "event_types": [e.event_type for e in self.events],
        }


# ═══════════════════════════════════════════════════════════════════════════
# Helpers for formatting messages for trace output
# ═══════════════════════════════════════════════════════════════════════════


def _extract_content_text(content: Any) -> str:
    """Extract plain text from LangChain message content.

    Content can be:
      - str: return as-is
      - list of {"type": "text", "text": "..."} dicts: join text parts
      - anything else: str() fallback
    """
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for part in content:
            if isinstance(part, dict) and part.get("type") == "text":
                parts.append(part.get("text", ""))
            elif isinstance(part, str):
                parts.append(part)
        if parts:
            return "\n\n".join(parts)
    return str(content)


def format_messages_for_trace(messages: Any) -> list[dict[str, Any]]:
    """Convert LangChain message objects to serializable dicts for tracing.

    Preserves the FULL content — no truncation — so the UI can show
    exactly what the LLM received.
    """
    formatted: list[dict[str, Any]] = []
    if not messages:
        return formatted

    # LangGraph astream_events v2 may pass input as a dict like
    # {"messages": [msg1, msg2, ...]} instead of a plain list.
    if isinstance(messages, dict):
        messages = messages.get("messages", list(messages.values()))
        # If the extracted value is still not a list, bail
        if not isinstance(messages, list):
            return formatted

    # Handle nested list (batch) format
    if isinstance(messages, list) and messages and isinstance(messages[0], list):
        messages = messages[0]

    for msg in messages:
        if isinstance(msg, dict):
            # Dict messages may also have multi-part content
            entry = dict(msg)
            if "content" in entry:
                entry["content"] = _extract_content_text(entry["content"])
            formatted.append(entry)
        elif hasattr(msg, "type") and hasattr(msg, "content"):
            entry: dict[str, Any] = {
                "role": getattr(msg, "type", "unknown"),
                "content": _extract_content_text(msg.content),
            }
            if hasattr(msg, "tool_calls") and msg.tool_calls:
                entry["tool_calls"] = [
                    {
                        "name": getattr(tc, "name", tc.get("name", "")) if isinstance(tc, dict) else getattr(tc, "name", ""),
                        "args": getattr(tc, "args", tc.get("args", {})) if isinstance(tc, dict) else getattr(tc, "args", {}),
                    }
                    for tc in msg.tool_calls
                ]
            formatted.append(entry)
        else:
            formatted.append({"role": "unknown", "content": str(msg)})
    return formatted

"""Typed message classes and Messages chain for wick_deep_agent.

Mirrors the Go agent.Messages API with a LangChain-style interface::

    from wick_deep_agent.messages import SystemMessage, HumanMessage, Messages

    chain = SystemMessage("You are helpful.") + HumanMessage("Hi!")
    chain.validate()
    chain.to_list()  # [{"role": "system", "content": "..."}, ...]
"""

from __future__ import annotations

import abc
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "AIMessage",
    "BaseMessage",
    "HumanMessage",
    "Messages",
    "SystemMessage",
    "ToolMessage",
]


# ---------------------------------------------------------------------------
# Role constants
# ---------------------------------------------------------------------------

ROLE_SYSTEM = "system"
ROLE_USER = "user"
ROLE_ASSISTANT = "assistant"
ROLE_TOOL = "tool"

_VALID_ROLES = {ROLE_SYSTEM, ROLE_USER, ROLE_ASSISTANT, ROLE_TOOL}
_USER_INPUT_ROLES = {ROLE_SYSTEM, ROLE_USER}

# ---------------------------------------------------------------------------
# Message classes
# ---------------------------------------------------------------------------


@dataclass
class BaseMessage(abc.ABC):
    """Abstract base chat message. Subclasses must set ``role`` via ``__post_init__``."""

    content: str
    role: str = ""

    def __init_subclass__(cls, **kwargs: Any) -> None:
        super().__init_subclass__(**kwargs)

    def __post_init__(self) -> None:
        if type(self) is BaseMessage:
            raise TypeError("BaseMessage cannot be instantiated directly; use a subclass")

    def to_dict(self) -> dict[str, Any]:
        return {"role": self.role, "content": self.content}

    def validate(self) -> None:
        if self.role not in _VALID_ROLES:
            raise ValueError(f"invalid role '{self.role}': must be one of {sorted(_VALID_ROLES)}")
        if self.role in (ROLE_USER, ROLE_SYSTEM) and not self.content:
            raise ValueError(f"{self.role} message must have non-empty content")

    # Chain operators ---------------------------------------------------------

    def __add__(self, other: "BaseMessage | Messages") -> "Messages":
        if isinstance(other, Messages):
            return Messages(self, *other._msgs)
        if isinstance(other, BaseMessage):
            return Messages(self, other)
        return NotImplemented

    def __radd__(self, other: "BaseMessage | Messages") -> "Messages":
        if isinstance(other, Messages):
            return Messages(*other._msgs, self)
        if isinstance(other, BaseMessage):
            return Messages(other, self)
        return NotImplemented

    def __repr__(self) -> str:
        content_preview = self.content[:60] + "..." if len(self.content) > 60 else self.content
        return f"{self.__class__.__name__}({content_preview!r})"


@dataclass
class SystemMessage(BaseMessage):
    """System-role message."""

    content: str = ""

    def __post_init__(self) -> None:
        self.role = ROLE_SYSTEM


@dataclass
class HumanMessage(BaseMessage):
    """User-role message."""

    content: str = ""

    def __post_init__(self) -> None:
        self.role = ROLE_USER


@dataclass
class AIMessage(BaseMessage):
    """Assistant-role message, optionally carrying tool calls."""

    content: str = ""
    tool_calls: list[dict[str, Any]] = field(default_factory=list)

    def __post_init__(self) -> None:
        self.role = ROLE_ASSISTANT

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {"role": self.role, "content": self.content}
        if self.tool_calls:
            d["tool_calls"] = self.tool_calls
        return d

    def validate(self) -> None:
        if self.role not in _VALID_ROLES:
            raise ValueError(f"invalid role '{self.role}'")
        if not self.content and not self.tool_calls:
            raise ValueError("assistant message must have content or tool_calls")


@dataclass
class ToolMessage(BaseMessage):
    """Tool result message."""

    content: str = ""
    tool_call_id: str = ""
    name: str = ""

    def __post_init__(self) -> None:
        self.role = ROLE_TOOL

    def to_dict(self) -> dict[str, Any]:
        return {
            "role": self.role,
            "content": self.content,
            "tool_call_id": self.tool_call_id,
            "name": self.name,
        }

    def validate(self) -> None:
        if not self.tool_call_id:
            raise ValueError("tool message must have tool_call_id")
        if not self.name:
            raise ValueError("tool message must have name")


# ---------------------------------------------------------------------------
# Messages chain
# ---------------------------------------------------------------------------

_ROLE_LABEL = {
    ROLE_SYSTEM: "System",
    ROLE_USER: "Human",
    ROLE_ASSISTANT: "AI",
    ROLE_TOOL: "Tool",
}


class Messages:
    """Ordered chain of chat messages with builder, filter, and validation API.

    Build via positional args::

        chain = Messages(SystemMessage("..."), HumanMessage("..."))

    Or via fluent builder::

        chain = Messages().system("...").human("...").ai("...").tool(...)

    Or via ``+`` operator::

        chain = SystemMessage("...") + HumanMessage("...")
    """

    __slots__ = ("_msgs",)

    def __init__(self, *msgs: BaseMessage) -> None:
        self._msgs: list[BaseMessage] = list(msgs)

    # -- Fluent builder ------------------------------------------------------

    def system(self, content: str) -> Messages:
        self._msgs.append(SystemMessage(content))
        return self

    def human(self, content: str) -> Messages:
        self._msgs.append(HumanMessage(content))
        return self

    def ai(self, content: str, tool_calls: list[dict[str, Any]] | None = None) -> Messages:
        self._msgs.append(AIMessage(content, tool_calls=tool_calls or []))
        return self

    def tool(self, tool_call_id: str, name: str, content: str) -> Messages:
        self._msgs.append(ToolMessage(content=content, tool_call_id=tool_call_id, name=name))
        return self

    def add(self, *msgs: BaseMessage) -> Messages:
        self._msgs.extend(msgs)
        return self

    def concat(self, other: Messages) -> Messages:
        """Return a *new* chain merging self and other."""
        return Messages(*self._msgs, *other._msgs)

    # -- Operators -----------------------------------------------------------

    def __add__(self, other: "BaseMessage | Messages") -> Messages:
        if isinstance(other, Messages):
            return Messages(*self._msgs, *other._msgs)
        if isinstance(other, BaseMessage):
            return Messages(*self._msgs, other)
        return NotImplemented

    def __radd__(self, other: "BaseMessage | Messages") -> Messages:
        if isinstance(other, BaseMessage):
            return Messages(other, *self._msgs)
        return NotImplemented

    # -- Accessors -----------------------------------------------------------

    def last(self) -> BaseMessage | None:
        return self._msgs[-1] if self._msgs else None

    def last_content(self) -> str:
        return self._msgs[-1].content if self._msgs else ""

    def roles(self) -> list[str]:
        return [m.role for m in self._msgs]

    def __len__(self) -> int:
        return len(self._msgs)

    def __iter__(self):
        return iter(self._msgs)

    def __getitem__(self, idx):
        return self._msgs[idx]

    # -- Conversion ----------------------------------------------------------

    def to_list(self) -> list[dict[str, Any]]:
        """Convert to list-of-dicts for API submission."""
        return [m.to_dict() for m in self._msgs]

    # -- Filtering -----------------------------------------------------------

    def _by_role(self, role: str) -> Messages:
        return Messages(*[m for m in self._msgs if m.role == role])

    def system_messages(self) -> Messages:
        return self._by_role(ROLE_SYSTEM)

    def user_messages(self) -> Messages:
        return self._by_role(ROLE_USER)

    def ai_messages(self) -> Messages:
        return self._by_role(ROLE_ASSISTANT)

    def tool_messages(self) -> Messages:
        return self._by_role(ROLE_TOOL)

    # -- Validation ----------------------------------------------------------

    def validate(self) -> None:
        """Validate the full chain (all roles allowed)."""
        if not self._msgs:
            raise ValueError("messages chain is empty")
        for m in self._msgs:
            m.validate()

    def validate_input(self) -> None:
        """Validate for API submission: only user/system roles, non-empty."""
        if not self._msgs:
            raise ValueError("messages list is empty")
        for m in self._msgs:
            if m.role not in _USER_INPUT_ROLES:
                raise ValueError(
                    f"role '{m.role}' not allowed in user input; "
                    f"only {sorted(_USER_INPUT_ROLES)} are accepted"
                )
            if not m.content:
                raise ValueError(f"{m.role} message must have non-empty content")

    # -- Display -------------------------------------------------------------

    def pretty_print(self) -> str:
        lines: list[str] = []
        for m in self._msgs:
            label = _ROLE_LABEL.get(m.role, m.role)
            if m.role == ROLE_TOOL and isinstance(m, ToolMessage):
                label = f"Tool:{m.name}"
            header = f"[{label}]"

            lines.append(header)
            lines.append(m.content)

            if isinstance(m, AIMessage) and m.tool_calls:
                for tc in m.tool_calls:
                    lines.append(f"  -> tool_call: {tc.get('name', '?')}({tc.get('id', '')})")

            lines.append("")
        return "\n".join(lines)

    def __str__(self) -> str:
        return self.pretty_print()

    def __repr__(self) -> str:
        return f"Messages(len={len(self._msgs)}, roles={self.roles()})"

    # -- Token estimation ----------------------------------------------------

    def estimate_tokens(self) -> int:
        """Rough token estimate using len/4 heuristic (matches Go implementation)."""
        total = 0
        for m in self._msgs:
            total += len(m.content) // 4
            if isinstance(m, AIMessage):
                for tc in m.tool_calls:
                    args = tc.get("args", {})
                    if isinstance(args, dict):
                        for v in args.values():
                            total += len(str(v)) // 4
        return max(total, 1) if self._msgs else 0

"""Global tool registry — define tools once, select per agent via builtin_tools.

Usage:
    from wick import tool

    @tool(description="Get current date and time")
    def current_datetime() -> str:
        from datetime import datetime
        return datetime.now().isoformat()

    @tool(description="Calculate a math expression")
    def calculate(expression: str) -> str:
        return str(eval(expression))

    # Then select per agent:
    agent = Agent("my-agent", builtin_tools=["current_datetime", "calculate"])
"""

from __future__ import annotations

import inspect
from collections.abc import Callable
from typing import Any


class ToolDef:
    """A tool definition in the global registry."""

    __slots__ = ("name", "description", "parameters", "fn")

    def __init__(
        self,
        name: str,
        description: str,
        parameters: dict[str, Any],
        fn: Callable,
    ) -> None:
        self.name = name
        self.description = description
        self.parameters = parameters
        self.fn = fn


# Module-level registry: name → ToolDef
_REGISTRY: dict[str, ToolDef] = {}


def tool(
    name: str | None = None,
    description: str = "",
    parameters: dict[str, Any] | None = None,
) -> Callable:
    """Decorator to register a tool in the global pool.

    Usage:
        @tool(description="Add two numbers")
        def add(a: float, b: float) -> str:
            return str(a + b)
    """
    def decorator(fn: Callable) -> Callable:
        tool_name = name or fn.__name__
        tool_desc = description or fn.__doc__ or ""
        tool_params = parameters or _infer_parameters(fn)
        _REGISTRY[tool_name] = ToolDef(tool_name, tool_desc, tool_params, fn)
        return fn
    return decorator


def get_tool(name: str) -> ToolDef | None:
    """Look up a tool by name."""
    return _REGISTRY.get(name)


def all_tools() -> dict[str, ToolDef]:
    """Return all registered tools."""
    return dict(_REGISTRY)


def _infer_parameters(fn: Callable) -> dict[str, Any]:
    """Infer JSON Schema parameters from function type hints."""
    sig = inspect.signature(fn)
    properties: dict[str, Any] = {}
    required: list[str] = []

    type_map = {
        str: "string",
        int: "integer",
        float: "number",
        bool: "boolean",
    }

    for param_name, param in sig.parameters.items():
        annotation = param.annotation
        json_type = type_map.get(annotation, "string")
        properties[param_name] = {"type": json_type}

        if param.default is inspect.Parameter.empty:
            required.append(param_name)

    schema: dict[str, Any] = {
        "type": "object",
        "properties": properties,
    }
    if required:
        schema["required"] = required

    return schema

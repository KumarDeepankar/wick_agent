"""Custom LLM model registration — @model decorator and ModelDef.

Usage::

    from wick_deep_agent import model

    @model(name="my-bedrock-claude")
    class MyBedrockModel:
        def call(self, request):
            # Full control: custom headers, body transform, response parsing
            return {"content": "Hello!", "tool_calls": []}

        def stream(self, request):
            for word in "Hello world".split():
                yield {"delta": word + " "}
            yield {"done": True}
"""

from __future__ import annotations

from typing import Any, Callable, Generator


class ModelDef:
    """Wraps a user-defined model handler (class or function) with metadata."""

    def __init__(self, handler: Any, name: str) -> None:
        self.name = name
        self._handler = handler
        # Instantiate class-based handlers
        if isinstance(handler, type):
            self._instance = handler()
        else:
            self._instance = handler
        self.has_stream = hasattr(self._instance, "stream") and callable(
            getattr(self._instance, "stream")
        )

    def call(self, request: dict[str, Any]) -> dict[str, Any]:
        """Dispatch to the handler's call method or the function itself."""
        if hasattr(self._instance, "call"):
            return self._instance.call(request)
        # Function-based handler: the function IS the call
        if callable(self._instance):
            return self._instance(request)
        raise TypeError(f"Model handler {self.name!r} has no call() method")

    def stream(self, request: dict[str, Any]) -> Generator[dict[str, Any], None, None]:
        """Dispatch to the handler's stream method."""
        if not self.has_stream:
            raise TypeError(
                f"Model {self.name!r} does not support streaming "
                f"(no stream() method on handler)"
            )
        yield from self._instance.stream(request)


def model(
    cls: type | Callable | None = None,
    *,
    name: str | None = None,
) -> ModelDef | Callable[[type | Callable], ModelDef]:
    """Decorator to define a custom LLM model handler.

    Can be used as ``@model`` or ``@model(name="...")``.

    Class-based::

        @model(name="my-model")
        class MyModel:
            def call(self, request): ...
            def stream(self, request): ...  # optional

    Function-based (call only, no streaming)::

        @model(name="my-model")
        def my_model(request):
            return {"content": "...", "tool_calls": []}
    """
    if cls is not None:
        # Called as @model (no arguments)
        model_name = name or getattr(cls, "__name__", str(cls))
        return ModelDef(cls, name=model_name)

    # Called as @model(name="...")
    def wrapper(c: type | Callable) -> ModelDef:
        model_name = name or getattr(c, "__name__", str(c))
        return ModelDef(c, name=model_name)

    return wrapper

# When this directory shadows the installed package (e.g., IDE sets CWD to
# the repo root), redirect all imports to the real inner package.
# This ensures `wick_deep_agent.messages.HumanMessage` is the same class
# object everywhere, preventing isinstance() failures.
import importlib as _importlib
import sys as _sys

_inner = _importlib.import_module("wick_deep_agent.wick_deep_agent")

# Replace this module entry so all future imports resolve via the inner package.
_sys.modules[__name__] = _inner

# Alias submodules so `from wick_deep_agent.messages import X` works.
for _name in ("messages", "client", "launcher", "cli"):
    _inner_key = f"wick_deep_agent.wick_deep_agent.{_name}"
    if _inner_key in _sys.modules:
        _sys.modules[f"wick_deep_agent.{_name}"] = _sys.modules[_inner_key]

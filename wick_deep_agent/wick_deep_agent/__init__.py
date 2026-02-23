"""wick_deep_agent — Pythonic client for the wick_go agent server.

Primary API::

    from wick_deep_agent import WickClient, WickServer
    from wick_deep_agent.messages import HumanMessage, SystemMessage, Messages
"""

from __future__ import annotations

from .client import WickClient
from .launcher import WickServer

__version__ = "0.1.0"

__all__ = [
    "__version__",
    "WickClient",
    "WickServer",
]

"""Task-source loaders. The planner can read tasks either from OpenSearch
(via the product_opensearch_cli client) or directly from the NDJSON file
the seed script produces. The file path makes the planner runnable in
demos with no OpenSearch instance available.
"""

from __future__ import annotations

import json
import os
from datetime import date
from typing import Any


def load_from_opensearch(launch_id: str, *, url: str | None = None,
                         index: str | None = None) -> tuple[list[dict[str, Any]], date]:
    from product_opensearch_cli.client import (
        DEFAULT_INDEX,
        get_launch_tasks,
        make_client,
    )
    client = make_client(url=url or os.environ.get("OPENSEARCH_URL") or "http://localhost:9200")
    idx = index or os.environ.get("PRODUCT_LAUNCH_INDEX") or DEFAULT_INDEX
    docs = get_launch_tasks(client, idx, launch_id)
    if not docs:
        raise LookupError(f"no tasks found for launch {launch_id} in index {idx}")
    target = date.fromisoformat(docs[0]["target_launch_date"])
    return docs, target


def load_from_file(launch_id: str, path: str) -> tuple[list[dict[str, Any]], date]:
    docs: list[dict[str, Any]] = []
    with open(path, "r", encoding="utf-8") as f:
        for ln in f:
            ln = ln.strip()
            if not ln:
                continue
            d = json.loads(ln)
            if d.get("launch_id") == launch_id:
                docs.append(d)
    if not docs:
        raise LookupError(f"no tasks found for launch {launch_id} in {path}")
    target = date.fromisoformat(docs[0]["target_launch_date"])
    return docs, target

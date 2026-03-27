"""Wick agent tool wrappers for OpenSearch CLI.

These functions are designed to be registered as wick agent tools.
They use the opensearch_cli client directly (in-process, no subprocess).
"""

from __future__ import annotations

import json
from typing import Any

from .client import (
    count_documents,
    fetch_batch,
    get_index_mapping,
    list_indices,
    make_client,
    search_documents,
)

# Module-level client — initialized on first use
_client = None


def _get_client() -> Any:
    global _client
    if _client is None:
        import os
        user = os.environ.get("OPENSEARCH_USER")
        passwd = os.environ.get("OPENSEARCH_PASSWORD")
        auth = (user, passwd) if user and passwd else None
        url = os.environ.get("OPENSEARCH_URL")
        if url:
            _client = make_client(url=url, auth=auth)
        else:
            host = os.environ.get("OPENSEARCH_HOST", "localhost")
            port = int(os.environ.get("OPENSEARCH_PORT", "9200"))
            _client = make_client(host=host, port=port, auth=auth)
    return _client


def os_count(index: str) -> str:
    """Get the total document count for an OpenSearch index."""
    client = _get_client()
    total = count_documents(client, index)
    return json.dumps({"index": index, "count": total})


def os_list_indices() -> str:
    """List all OpenSearch indices with their document counts and sizes."""
    client = _get_client()
    indices = list_indices(client)
    return json.dumps(indices, indent=2)


def os_fetch_batch(index: str, batch_size: int = 50, offset: int = 0) -> str:
    """Fetch a batch of documents from an OpenSearch index.

    Args:
        index: Index name to fetch from.
        batch_size: Number of documents to fetch (default 50).
        offset: Starting document offset for pagination.
    """
    client = _get_client()
    docs = fetch_batch(client, index, batch_size=batch_size, from_offset=offset)
    return json.dumps({
        "index": index,
        "offset": offset,
        "batch_size": batch_size,
        "fetched": len(docs),
        "documents": docs,
    }, indent=2)


def os_search(index: str, query: str, field: str = "_all", size: int = 50, offset: int = 0) -> str:
    """Search documents in an OpenSearch index.

    Args:
        index: Index name to search.
        query: Search query string.
        field: Field to search (default: all fields).
        size: Max results per page.
        offset: Starting offset.
    """
    client = _get_client()
    docs = search_documents(client, index, query, field=field, size=size, from_offset=offset)
    return json.dumps({
        "index": index,
        "query": query,
        "count": len(docs),
        "documents": docs,
    }, indent=2)


def os_mapping(index: str) -> str:
    """Get the field mapping for an OpenSearch index."""
    client = _get_client()
    mapping = get_index_mapping(client, index)
    return json.dumps(mapping, indent=2)

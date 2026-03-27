"""OpenSearch client wrapper using requests (no opensearch-py dependency)."""

from __future__ import annotations

from typing import Any

import requests


class OSClient:
    """Lightweight OpenSearch client backed by requests."""

    def __init__(
        self,
        base_url: str = "http://localhost:9200",
        auth: tuple[str, str] | None = None,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})
        if auth:
            self.session.auth = auth

    def _url(self, path: str) -> str:
        return f"{self.base_url}/{path.lstrip('/')}"

    def _get(self, path: str, **kwargs) -> Any:
        resp = self.session.get(self._url(path), **kwargs)
        resp.raise_for_status()
        return resp.json()

    def _post(self, path: str, json_body: dict | None = None) -> Any:
        resp = self.session.post(self._url(path), json=json_body)
        resp.raise_for_status()
        return resp.json()


def make_client(
    host: str = "localhost",
    port: int = 9200,
    scheme: str = "http",
    url: str | None = None,
    auth: tuple[str, str] | None = None,
) -> OSClient:
    """Create an OpenSearch client.

    Args:
        url: Full URL (e.g. http://host.docker.internal:9200). Overrides host/port/scheme.
        host: OpenSearch host (default: localhost).
        port: OpenSearch port (default: 9200).
        scheme: http or https.
        auth: (username, password) tuple.
    """
    base_url = url if url else f"{scheme}://{host}:{port}"
    return OSClient(base_url=base_url, auth=auth)


def count_documents(client: OSClient, index: str) -> int:
    """Return the total number of documents in an index."""
    resp = client._get(f"/{index}/_count")
    return resp["count"]


def list_indices(client: OSClient) -> list[dict[str, Any]]:
    """List all indices with doc counts and sizes."""
    resp = client._get("/_cat/indices", params={"format": "json"})
    return [
        {
            "index": idx["index"],
            "docs_count": idx.get("docs.count", "0"),
            "store_size": idx.get("store.size", "0"),
            "status": idx.get("status", "unknown"),
            "health": idx.get("health", "unknown"),
        }
        for idx in resp
        if not idx["index"].startswith(".")
    ]


def fetch_batch(
    client: OSClient,
    index: str,
    batch_size: int = 50,
    from_offset: int = 0,
) -> list[dict[str, Any]]:
    """Fetch a batch of documents using from/size pagination."""
    resp = client._post(
        f"/{index}/_search",
        json_body={
            "query": {"match_all": {}},
            "size": batch_size,
            "from": from_offset,
            "sort": [{"_doc": "asc"}],
        },
    )
    hits = resp.get("hits", {}).get("hits", [])
    return [
        {"_id": h["_id"], **h["_source"]}
        for h in hits
    ]


def search_documents(
    client: OSClient,
    index: str,
    query: str,
    field: str = "_all",
    size: int = 50,
    from_offset: int = 0,
) -> list[dict[str, Any]]:
    """Search documents with a query string."""
    if field == "_all":
        body: dict[str, Any] = {"query": {"query_string": {"query": query}}}
    else:
        body = {"query": {"match": {field: query}}}

    body["size"] = size
    body["from"] = from_offset
    body["sort"] = [{"_doc": "asc"}]

    resp = client._post(f"/{index}/_search", json_body=body)
    hits = resp.get("hits", {}).get("hits", [])
    return [
        {"_id": h["_id"], "_score": h.get("_score"), **h["_source"]}
        for h in hits
    ]


def get_index_mapping(client: OSClient, index: str) -> dict[str, Any]:
    """Get the field mapping for an index."""
    resp = client._get(f"/{index}/_mapping")
    return resp.get(index, {}).get("mappings", {})


# Types that support exact-match filtering in OpenSearch
_FILTERABLE_TYPES = {"keyword", "integer", "long", "short", "byte", "float",
                     "double", "boolean", "date", "ip"}


def filterable_fields(client: OSClient, index: str) -> list[dict[str, Any]]:
    """Return fields that can be used as filters (indexed, filterable types)."""
    mapping = get_index_mapping(client, index)
    properties = mapping.get("properties", {})
    result = []
    for name, meta in properties.items():
        ftype = meta.get("type", "")
        indexed = meta.get("index", True)  # default is True in OpenSearch
        if ftype in _FILTERABLE_TYPES and indexed is not False:
            entry: dict[str, Any] = {"field": name, "type": ftype}
            if ftype == "date" and "format" in meta:
                entry["format"] = meta["format"]
            result.append(entry)
    return result


def _build_filter_clauses(filters: list[str]) -> list[dict[str, Any]]:
    """Parse filter strings into OpenSearch bool filter clauses.

    Supported filter formats:
        field=value           → term match (exact)
        field!=value          → must_not term
        field>value           → range gt
        field>=value          → range gte
        field<value           → range lt
        field<=value          → range lte
        field=val1,val2,val3  → terms match (any of)
    """
    must: list[dict[str, Any]] = []
    must_not: list[dict[str, Any]] = []

    for f in filters:
        if ">=" in f:
            field, val = f.split(">=", 1)
            must.append({"range": {field.strip(): {"gte": _cast_value(val.strip())}}})
        elif "<=" in f:
            field, val = f.split("<=", 1)
            must.append({"range": {field.strip(): {"lte": _cast_value(val.strip())}}})
        elif "!=" in f:
            field, val = f.split("!=", 1)
            must_not.append({"term": {field.strip(): _cast_value(val.strip())}})
        elif ">" in f:
            field, val = f.split(">", 1)
            must.append({"range": {field.strip(): {"gt": _cast_value(val.strip())}}})
        elif "<" in f:
            field, val = f.split("<", 1)
            must.append({"range": {field.strip(): {"lt": _cast_value(val.strip())}}})
        elif "=" in f:
            field, val = f.split("=", 1)
            field = field.strip()
            val = val.strip()
            if "," in val:
                values = [_cast_value(v.strip()) for v in val.split(",")]
                must.append({"terms": {field: values}})
            else:
                must.append({"term": {field: _cast_value(val)}})

    return must, must_not


def _cast_value(val: str) -> Any:
    """Try to cast a string value to int or float, else keep as string."""
    try:
        return int(val)
    except ValueError:
        pass
    try:
        return float(val)
    except ValueError:
        pass
    return val


def _build_query(
    filters: list[str] | None = None,
    query: str | None = None,
    field: str = "_all",
) -> dict[str, Any]:
    """Build an OpenSearch query body from filters and/or a text query."""
    has_filters = filters and len(filters) > 0
    has_query = query and query.strip()

    if not has_filters and not has_query:
        return {"match_all": {}}

    bool_query: dict[str, Any] = {}

    if has_filters:
        must, must_not = _build_filter_clauses(filters)
        if must:
            bool_query["filter"] = must
        if must_not:
            bool_query["must_not"] = must_not

    if has_query:
        if field == "_all":
            match_clause = {"query_string": {"query": query}}
        else:
            match_clause = {"match": {field: query}}
        bool_query["must"] = [match_clause]

    return {"bool": bool_query}


def query_documents(
    client: OSClient,
    index: str,
    filters: list[str] | None = None,
    query: str | None = None,
    field: str = "_all",
    size: int = 50,
    from_offset: int = 0,
) -> dict[str, Any]:
    """Fetch documents with optional filters and/or text query.

    Args:
        filters: List of filter strings, e.g. ["year=2024", "country=India"]
        query: Optional text search query
        field: Field for text query (default: all fields)
        size: Batch size
        from_offset: Pagination offset

    Returns:
        Dict with total count and documents.
    """
    q = _build_query(filters=filters, query=query, field=field)
    body = {
        "query": q,
        "size": size,
        "from": from_offset,
        "sort": [{"_doc": "asc"}],
    }

    resp = client._post(f"/{index}/_search", json_body=body)
    total = resp.get("hits", {}).get("total", {}).get("value", 0)
    hits = resp.get("hits", {}).get("hits", [])
    docs = [{"_id": h["_id"], **h["_source"]} for h in hits]

    return {"total": total, "fetched": len(docs), "documents": docs}


def count_with_filters(
    client: OSClient,
    index: str,
    filters: list[str] | None = None,
) -> int:
    """Count documents matching filters."""
    q = _build_query(filters=filters)
    resp = client._post(f"/{index}/_count", json_body={"query": q})
    return resp["count"]


# Aggregation types by field type
_NUMERIC_TYPES = {"integer", "long", "short", "byte", "float", "double"}
_DATE_TYPES = {"date"}
_KEYWORD_TYPES = {"keyword", "ip", "boolean"}


def aggregate_fields(
    client: OSClient,
    index: str,
    fields: list[str] | None = None,
    filters: list[str] | None = None,
    top_n: int = 20,
) -> dict[str, Any]:
    """Run aggregations on filterable fields to show value distributions.

    For each field, produces the appropriate aggregation:
      - keyword/boolean/ip → top N terms + doc counts
      - integer/long/float → min, max, avg, sum, count
      - date → min, max + yearly histogram

    Args:
        fields: Specific fields to aggregate. If None, aggregates all filterable fields.
        filters: Optional filters to scope the aggregation.
        top_n: Number of top terms for keyword fields (default 20).
    """
    # Get field types from mapping
    field_types = {}
    f_fields = filterable_fields(client, index)
    for f in f_fields:
        field_types[f["field"]] = f["type"]

    # Determine which fields to aggregate
    if fields:
        target_fields = {name: field_types[name] for name in fields if name in field_types}
    else:
        target_fields = field_types

    if not target_fields:
        return {"index": index, "aggregations": {}}

    # Build aggregation body
    aggs: dict[str, Any] = {}
    for name, ftype in target_fields.items():
        if ftype in _KEYWORD_TYPES:
            aggs[name] = {"terms": {"field": name, "size": top_n}}
        elif ftype in _NUMERIC_TYPES:
            aggs[f"{name}_stats"] = {"stats": {"field": name}}
            aggs[f"{name}_histogram"] = {
                "histogram": {"field": name, "interval": _auto_interval(client, index, name, filters)},
            }
        elif ftype in _DATE_TYPES:
            aggs[f"{name}_range"] = {"stats": {"field": name}}
            aggs[f"{name}_yearly"] = {
                "date_histogram": {"field": name, "calendar_interval": "year", "format": "yyyy"},
            }

    q = _build_query(filters=filters)
    body: dict[str, Any] = {"query": q, "size": 0, "aggs": aggs}

    resp = client._post(f"/{index}/_search", json_body=body)
    raw_aggs = resp.get("aggregations", {})
    total = resp.get("hits", {}).get("total", {}).get("value", 0)

    # Format results
    result: dict[str, Any] = {}
    for name, ftype in target_fields.items():
        if ftype in _KEYWORD_TYPES:
            buckets = raw_aggs.get(name, {}).get("buckets", [])
            result[name] = {
                "type": ftype,
                "values": [{"value": b["key"], "count": b["doc_count"]} for b in buckets],
                "other_count": raw_aggs.get(name, {}).get("sum_other_doc_count", 0),
            }
        elif ftype in _NUMERIC_TYPES:
            stats = raw_aggs.get(f"{name}_stats", {})
            hist_buckets = raw_aggs.get(f"{name}_histogram", {}).get("buckets", [])
            result[name] = {
                "type": ftype,
                "min": stats.get("min"),
                "max": stats.get("max"),
                "avg": round(stats.get("avg", 0), 2) if stats.get("avg") is not None else None,
                "sum": stats.get("sum"),
                "count": stats.get("count"),
                "distribution": [{"range": b["key"], "count": b["doc_count"]} for b in hist_buckets if b["doc_count"] > 0],
            }
        elif ftype in _DATE_TYPES:
            stats = raw_aggs.get(f"{name}_range", {})
            yearly = raw_aggs.get(f"{name}_yearly", {}).get("buckets", [])
            result[name] = {
                "type": ftype,
                "min": stats.get("min_as_string", stats.get("min")),
                "max": stats.get("max_as_string", stats.get("max")),
                "count": stats.get("count"),
                "by_year": [{"year": b["key_as_string"], "count": b["doc_count"]} for b in yearly if b["doc_count"] > 0],
            }

    return {"index": index, "total_matching": total, "aggregations": result}


def _auto_interval(client: OSClient, index: str, field: str, filters: list[str] | None) -> int:
    """Compute a reasonable histogram interval for a numeric field."""
    q = _build_query(filters=filters)
    body: dict[str, Any] = {"query": q, "size": 0, "aggs": {"s": {"stats": {"field": field}}}}
    resp = client._post(f"/{index}/_search", json_body=body)
    stats = resp.get("aggregations", {}).get("s", {})
    min_val = stats.get("min", 0) or 0
    max_val = stats.get("max", 0) or 0
    spread = max_val - min_val
    if spread == 0:
        return 1
    # Aim for ~10 buckets
    interval = spread / 10
    # Round to a clean number
    if interval >= 1:
        return max(1, int(round(interval)))
    return round(interval, 2) or 1

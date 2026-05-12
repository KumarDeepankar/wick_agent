"""Lightweight OpenSearch client for the launch-planning dataset.

Self-contained (no dependency on the sibling `opensearch_cli` package) so
the two CLIs can move independently.
"""

from __future__ import annotations

import json
from typing import Any

import requests

DEFAULT_INDEX = "product_launch_plans"


# Mapping for the launch-planning index. Date fields are explicitly typed
# so range filters work; identifiers and categorical fields are keywords
# so exact-match filters work; task_name is text+keyword so we can both
# search and aggregate.
LAUNCH_PLAN_MAPPING: dict[str, Any] = {
    "properties": {
        "launch_id":           {"type": "keyword"},
        "product_type":        {"type": "keyword"},
        "target_launch_date":  {"type": "date"},
        "market_region":       {"type": "keyword"},
        "task_id":             {"type": "keyword"},
        "task_name": {
            "type": "text",
            "fields": {"keyword": {"type": "keyword"}},
        },
        "task_category":       {"type": "keyword"},
        "lead_time_days":      {"type": "integer"},
        "dependency_task_id":  {"type": "keyword"},
        "earliest_start_date": {"type": "date"},
        "latest_finish_date":  {"type": "date"},
        "task_owner_role":     {"type": "keyword"},
        "criticality":         {"type": "keyword"},
        "status":              {"type": "keyword"},
    }
}


class OSClient:
    def __init__(self, base_url: str = "http://localhost:9200", auth: tuple[str, str] | None = None) -> None:
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})
        if auth:
            self.session.auth = auth

    def url(self, path: str) -> str:
        return f"{self.base_url}/{path.lstrip('/')}"


def make_client(url: str | None = None, host: str = "localhost", port: int = 9200,
                scheme: str = "http", auth: tuple[str, str] | None = None) -> OSClient:
    base = url if url else f"{scheme}://{host}:{port}"
    return OSClient(base_url=base, auth=auth)


# --- index lifecycle ------------------------------------------------------

def create_index(client: OSClient, index: str, drop_if_exists: bool = False) -> dict[str, Any]:
    if drop_if_exists:
        d = client.session.delete(client.url(f"/{index}"))
        if d.status_code not in (200, 404):
            d.raise_for_status()
    r = client.session.put(client.url(f"/{index}"), json={"mappings": LAUNCH_PLAN_MAPPING})
    r.raise_for_status()
    return r.json()


def index_exists(client: OSClient, index: str) -> bool:
    r = client.session.head(client.url(f"/{index}"))
    return r.status_code == 200


def delete_index(client: OSClient, index: str) -> dict[str, Any]:
    r = client.session.delete(client.url(f"/{index}"))
    if r.status_code == 404:
        return {"acknowledged": False, "missing": True}
    r.raise_for_status()
    return r.json()


# --- bulk load ------------------------------------------------------------

def load_ndjson(client: OSClient, index: str, ndjson_path: str,
                id_fields: tuple[str, ...] = ("launch_id", "task_id"),
                chunk_size: int = 500) -> dict[str, Any]:
    """Load an NDJSON file (one doc per line) into the index via _bulk."""
    docs: list[dict[str, Any]] = []
    with open(ndjson_path, "r", encoding="utf-8") as f:
        for ln in f:
            ln = ln.strip()
            if ln:
                docs.append(json.loads(ln))

    indexed = 0
    errors: list[dict[str, Any]] = []
    for start in range(0, len(docs), chunk_size):
        chunk = docs[start : start + chunk_size]
        lines: list[str] = []
        for doc in chunk:
            action: dict[str, Any] = {"index": {"_index": index}}
            if id_fields:
                action["index"]["_id"] = ":".join(str(doc[f]) for f in id_fields)
            lines.append(json.dumps(action))
            lines.append(json.dumps(doc))
        body = "\n".join(lines) + "\n"
        r = client.session.post(
            client.url("/_bulk"),
            data=body,
            headers={"Content-Type": "application/x-ndjson"},
        )
        r.raise_for_status()
        result = r.json()
        for item in result.get("items", []):
            op = next(iter(item.values()))
            if op.get("error"):
                errors.append(op)
            else:
                indexed += 1
    # Make the new docs visible to search before returning.
    client.session.post(client.url(f"/{index}/_refresh"))
    return {"indexed": indexed, "error_count": len(errors), "errors": errors[:5]}


# --- queries --------------------------------------------------------------

def list_launches(client: OSClient, index: str) -> list[dict[str, Any]]:
    """One row per launch — distinct (launch_id, target_launch_date, region)."""
    body = {
        "size": 0,
        "aggs": {
            "by_launch": {
                "terms": {"field": "launch_id", "size": 1000},
                "aggs": {
                    "region": {"terms": {"field": "market_region", "size": 1}},
                    "target": {"terms": {"field": "target_launch_date", "size": 1}},
                    "task_count": {"value_count": {"field": "task_id"}},
                },
            }
        },
    }
    r = client.session.post(client.url(f"/{index}/_search"), json=body)
    r.raise_for_status()
    out = []
    for b in r.json().get("aggregations", {}).get("by_launch", {}).get("buckets", []):
        region_buckets = b["region"]["buckets"]
        target_buckets = b["target"]["buckets"]
        out.append({
            "launch_id": b["key"],
            "market_region": region_buckets[0]["key"] if region_buckets else None,
            "target_launch_date": target_buckets[0]["key_as_string"] if target_buckets else None,
            "task_count": int(b["task_count"]["value"]),
        })
    out.sort(key=lambda x: x["launch_id"])
    return out


def get_launch_tasks(client: OSClient, index: str, launch_id: str) -> list[dict[str, Any]]:
    """Return all task documents for a launch, sorted by task_id."""
    body = {
        "size": 1000,
        "query": {"term": {"launch_id": launch_id}},
        "sort": [{"task_id": "asc"}],
    }
    r = client.session.post(client.url(f"/{index}/_search"), json=body)
    r.raise_for_status()
    return [h["_source"] for h in r.json().get("hits", {}).get("hits", [])]


def filter_tasks(client: OSClient, index: str, filters: list[str], size: int = 200) -> list[dict[str, Any]]:
    """Run filtered query (subset of operators sufficient for the use case)."""
    must: list[dict[str, Any]] = []
    must_not: list[dict[str, Any]] = []
    for f in filters:
        if "!=" in f:
            field, val = f.split("!=", 1)
            must_not.append({"term": {field.strip(): _cast(val.strip())}})
        elif ">=" in f:
            field, val = f.split(">=", 1)
            must.append({"range": {field.strip(): {"gte": _cast(val.strip())}}})
        elif "<=" in f:
            field, val = f.split("<=", 1)
            must.append({"range": {field.strip(): {"lte": _cast(val.strip())}}})
        elif ">" in f:
            field, val = f.split(">", 1)
            must.append({"range": {field.strip(): {"gt": _cast(val.strip())}}})
        elif "<" in f:
            field, val = f.split("<", 1)
            must.append({"range": {field.strip(): {"lt": _cast(val.strip())}}})
        elif "=" in f:
            field, val = f.split("=", 1)
            field, val = field.strip(), val.strip()
            if "," in val:
                must.append({"terms": {field: [_cast(v.strip()) for v in val.split(",")]}})
            else:
                must.append({"term": {field: _cast(val)}})
    body = {
        "size": size,
        "query": {"bool": {"filter": must, "must_not": must_not}},
        "sort": [{"launch_id": "asc"}, {"task_id": "asc"}],
    }
    r = client.session.post(client.url(f"/{index}/_search"), json=body)
    r.raise_for_status()
    return [h["_source"] for h in r.json().get("hits", {}).get("hits", [])]


def index_summary(client: OSClient, index: str) -> dict[str, Any]:
    """High-level overview: doc count, launches, regions, categories."""
    body = {
        "size": 0,
        "aggs": {
            "by_region":     {"terms": {"field": "market_region", "size": 50}},
            "by_category":   {"terms": {"field": "task_category", "size": 50}},
            "by_criticality":{"terms": {"field": "criticality",   "size": 10}},
            "launch_count":  {"cardinality": {"field": "launch_id"}},
        },
    }
    r = client.session.post(client.url(f"/{index}/_search"), json=body)
    r.raise_for_status()
    j = r.json()
    return {
        "index": index,
        "doc_count": j["hits"]["total"]["value"],
        "launches": int(j["aggregations"]["launch_count"]["value"]),
        "regions": [
            {"value": b["key"], "count": b["doc_count"]}
            for b in j["aggregations"]["by_region"]["buckets"]
        ],
        "categories": [
            {"value": b["key"], "count": b["doc_count"]}
            for b in j["aggregations"]["by_category"]["buckets"]
        ],
        "criticality": [
            {"value": b["key"], "count": b["doc_count"]}
            for b in j["aggregations"]["by_criticality"]["buckets"]
        ],
    }


def _cast(val: str) -> Any:
    try:
        return int(val)
    except ValueError:
        pass
    try:
        return float(val)
    except ValueError:
        pass
    return val

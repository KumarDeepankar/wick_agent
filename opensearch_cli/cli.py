#!/usr/bin/env python3
"""OpenSearch CLI — command-line interface for OpenSearch operations.

Usage:
    opensearch-cli list-indices
    opensearch-cli count --index my-index
    opensearch-cli count --index my-index --filter year=2024
    opensearch-cli filterable-fields --index my-index
    opensearch-cli fetch --index my-index --batch-size 50 --offset 0
    opensearch-cli query --index my-index --filter year=2024 --filter country=India
    opensearch-cli query --index my-index --filter year>=2023 --query "summit" --field event_title
    opensearch-cli search --index my-index --query "error logs"
    opensearch-cli mapping --index my-index
"""

from __future__ import annotations

import json
import sys

import click

from .client import (
    aggregate_fields,
    count_documents,
    count_with_filters,
    fetch_batch,
    filterable_fields,
    get_index_mapping,
    list_indices,
    make_client,
    query_documents,
    search_documents,
)


@click.group()
@click.option("--host", default=None, help="OpenSearch host (default: OPENSEARCH_HOST env or localhost)")
@click.option("--port", default=None, type=int, help="OpenSearch port (default: OPENSEARCH_PORT env or 9200)")
@click.option("--url", default=None, help="Full OpenSearch URL (e.g. http://host.docker.internal:9200). Overrides --host/--port/--ssl")
@click.option("--ssl/--no-ssl", default=False, help="Use SSL")
@click.option("--user", default=None, help="Auth username")
@click.option("--password", default=None, help="Auth password")
@click.pass_context
def cli(ctx, host: str | None, port: int | None, url: str | None, ssl: bool, user: str | None, password: str | None):
    """OpenSearch CLI for interacting with an OpenSearch cluster."""
    import os

    resolved_user = user or os.environ.get("OPENSEARCH_USER")
    resolved_pass = password or os.environ.get("OPENSEARCH_PASSWORD")
    auth = (resolved_user, resolved_pass) if resolved_user and resolved_pass else None
    ctx.ensure_object(dict)

    # Priority: --url flag > OPENSEARCH_URL env > --host/--port flags > individual env vars > defaults
    full_url = url or os.environ.get("OPENSEARCH_URL")
    if full_url:
        ctx.obj["client"] = make_client(url=full_url, auth=auth)
    else:
        resolved_host = host or os.environ.get("OPENSEARCH_HOST", "localhost")
        resolved_port = port or int(os.environ.get("OPENSEARCH_PORT", "9200"))
        scheme = "https" if ssl else "http"
        ctx.obj["client"] = make_client(host=resolved_host, port=resolved_port, scheme=scheme, auth=auth)


@cli.command("count")
@click.option("--index", required=True, help="Index name")
@click.option("--filter", "-f", "filters", multiple=True, help="Filter (e.g. year=2024, country=India). Repeatable.")
@click.pass_context
def cmd_count(ctx, index: str, filters: tuple[str, ...]):
    """Get the document count, optionally filtered."""
    client = ctx.obj["client"]
    if filters:
        total = count_with_filters(client, index, list(filters))
    else:
        total = count_documents(client, index)
    click.echo(json.dumps({"index": index, "filters": list(filters), "count": total}))


@cli.command("list-indices")
@click.pass_context
def cmd_list_indices(ctx):
    """List all indices with doc counts and sizes."""
    client = ctx.obj["client"]
    indices = list_indices(client)
    click.echo(json.dumps(indices, indent=2))


@cli.command("filterable-fields")
@click.option("--index", required=True, help="Index name")
@click.pass_context
def cmd_filterable_fields(ctx, index: str):
    """List fields that can be used as filters (indexed keyword, number, date, etc.)."""
    client = ctx.obj["client"]
    fields = filterable_fields(client, index)
    click.echo(json.dumps({"index": index, "filterable_fields": fields}, indent=2))


@cli.command("fetch")
@click.option("--index", required=True, help="Index name")
@click.option("--batch-size", default=50, type=int, help="Number of documents per batch")
@click.option("--offset", default=0, type=int, help="Starting offset")
@click.pass_context
def cmd_fetch(ctx, index: str, batch_size: int, offset: int):
    """Fetch a batch of documents from an index (no filters)."""
    client = ctx.obj["client"]
    docs = fetch_batch(client, index, batch_size=batch_size, from_offset=offset)
    click.echo(json.dumps({"index": index, "offset": offset, "count": len(docs), "documents": docs}, indent=2))


@cli.command("query")
@click.option("--index", required=True, help="Index name")
@click.option("--filter", "-f", "filters", multiple=True, help="Filter (repeatable). Formats: field=value, field!=value, field>value, field>=value, field<value, field<=value, field=val1,val2,val3")
@click.option("--query", "-q", "query", default=None, help="Optional text search query")
@click.option("--field", default="_all", help="Field for text query (default: all fields)")
@click.option("--batch-size", default=50, type=int, help="Number of documents per batch")
@click.option("--offset", default=0, type=int, help="Starting offset")
@click.pass_context
def cmd_query(ctx, index: str, filters: tuple[str, ...], query: str | None, field: str, batch_size: int, offset: int):
    """Query documents with filters and/or text search.

    Examples:

      opensearch-cli query --index events -f year=2024

      opensearch-cli query --index events -f year=2024 -f country=India

      opensearch-cli query --index events -f year>=2023 -f year<=2025

      opensearch-cli query --index events -f country=India,USA,Japan

      opensearch-cli query --index events -f year=2024 -q "summit" --field event_title

      opensearch-cli query --index events -f country!=China --batch-size 100
    """
    client = ctx.obj["client"]
    result = query_documents(
        client, index,
        filters=list(filters) if filters else None,
        query=query,
        field=field,
        size=batch_size,
        from_offset=offset,
    )
    click.echo(json.dumps({
        "index": index,
        "filters": list(filters),
        "query": query,
        "total": result["total"],
        "offset": offset,
        "fetched": result["fetched"],
        "documents": result["documents"],
    }, indent=2))


@cli.command("aggs")
@click.option("--index", required=True, help="Index name")
@click.option("--field", "-F", "fields", multiple=True, help="Specific fields to aggregate (repeatable). If omitted, aggregates all filterable fields.")
@click.option("--filter", "-f", "filters", multiple=True, help="Filter to scope aggregations (repeatable)")
@click.option("--top", default=20, type=int, help="Number of top terms for keyword fields (default 20)")
@click.pass_context
def cmd_aggs(ctx, index: str, fields: tuple[str, ...], filters: tuple[str, ...], top: int):
    """Aggregate filterable fields to show value distributions.

    Shows what values exist and how many documents each value has.
    Helps decide which filters to apply before fetching documents.

    Examples:

      opensearch-cli aggs --index events

      opensearch-cli aggs --index events -F year -F country

      opensearch-cli aggs --index events -F country -f year=2023

      opensearch-cli aggs --index events -F event_theme --top 10
    """
    client = ctx.obj["client"]
    result = aggregate_fields(
        client, index,
        fields=list(fields) if fields else None,
        filters=list(filters) if filters else None,
        top_n=top,
    )
    click.echo(json.dumps(result, indent=2))


@cli.command("search")
@click.option("--index", required=True, help="Index name")
@click.option("--query", required=True, help="Search query")
@click.option("--field", default="_all", help="Field to search (default: all fields)")
@click.option("--size", default=50, type=int, help="Max results")
@click.option("--offset", default=0, type=int, help="Starting offset")
@click.pass_context
def cmd_search(ctx, index: str, query: str, field: str, size: int, offset: int):
    """Search documents (text search only, no filters). Use 'query' for filters."""
    client = ctx.obj["client"]
    docs = search_documents(client, index, query, field=field, size=size, from_offset=offset)
    click.echo(json.dumps({"index": index, "query": query, "count": len(docs), "documents": docs}, indent=2))


@cli.command("mapping")
@click.option("--index", required=True, help="Index name")
@click.pass_context
def cmd_mapping(ctx, index: str):
    """Get the field mapping for an index."""
    client = ctx.obj["client"]
    mapping = get_index_mapping(client, index)
    click.echo(json.dumps(mapping, indent=2))


if __name__ == "__main__":
    cli()

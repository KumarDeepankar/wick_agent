#!/usr/bin/env python3
"""product-opensearch-cli — CLI for the launch-planning OpenSearch index.

Commands
--------
    product-opensearch-cli init       --drop-if-exists
    product-opensearch-cli load       --file launch_tasks.ndjson
    product-opensearch-cli summary
    product-opensearch-cli launches
    product-opensearch-cli tasks      --launch L-STD-001
    product-opensearch-cli filter     -f market_region=EU -f criticality=High
    product-opensearch-cli drop
"""

from __future__ import annotations

import json
import os
import sys

import click

from .client import (
    DEFAULT_INDEX,
    create_index,
    delete_index,
    filter_tasks,
    get_launch_tasks,
    index_exists,
    index_summary,
    list_launches,
    load_ndjson,
    make_client,
)


@click.group()
@click.option("--url", default=None, help="OpenSearch URL (default: env OPENSEARCH_URL or http://localhost:9200)")
@click.option("--index", default=None, help=f"Index name (default: env PRODUCT_LAUNCH_INDEX or {DEFAULT_INDEX})")
@click.option("--user", default=None, help="Auth username (default: env OPENSEARCH_USER)")
@click.option("--password", default=None, help="Auth password (default: env OPENSEARCH_PASSWORD)")
@click.pass_context
def cli(ctx: click.Context, url: str | None, index: str | None,
        user: str | None, password: str | None) -> None:
    """OpenSearch CLI for the product-launch-planning dataset."""
    ctx.ensure_object(dict)
    resolved_url = url or os.environ.get("OPENSEARCH_URL") or "http://localhost:9200"
    resolved_user = user or os.environ.get("OPENSEARCH_USER")
    resolved_pass = password or os.environ.get("OPENSEARCH_PASSWORD")
    auth = (resolved_user, resolved_pass) if resolved_user and resolved_pass else None
    ctx.obj["client"] = make_client(url=resolved_url, auth=auth)
    ctx.obj["index"] = index or os.environ.get("PRODUCT_LAUNCH_INDEX") or DEFAULT_INDEX


@cli.command("init")
@click.option("--drop-if-exists", is_flag=True, help="Delete the index first if it already exists.")
@click.pass_context
def cmd_init(ctx: click.Context, drop_if_exists: bool) -> None:
    """Create the launch-planning index with the correct mapping."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    if index_exists(client, index) and not drop_if_exists:
        click.echo(json.dumps({"index": index, "status": "already_exists",
                               "hint": "rerun with --drop-if-exists to recreate"}))
        return
    resp = create_index(client, index, drop_if_exists=drop_if_exists)
    click.echo(json.dumps({"index": index, "created": True, **resp}))


@cli.command("load")
@click.option("--file", "ndjson_file", required=True,
              type=click.Path(exists=True, dir_okay=False),
              help="NDJSON file (one task document per line).")
@click.option("--auto-init/--no-auto-init", default=True,
              help="Create the index if it does not exist (default: on).")
@click.pass_context
def cmd_load(ctx: click.Context, ndjson_file: str, auto_init: bool) -> None:
    """Bulk-load task documents from an NDJSON file."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    if not index_exists(client, index):
        if auto_init:
            create_index(client, index)
        else:
            click.echo(json.dumps({"error": f"index {index} does not exist; run init first"}), err=True)
            sys.exit(1)
    result = load_ndjson(client, index, ndjson_file)
    click.echo(json.dumps({"index": index, "file": ndjson_file, **result}, indent=2))


@cli.command("summary")
@click.pass_context
def cmd_summary(ctx: click.Context) -> None:
    """Show counts of launches, regions, categories, criticality."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    click.echo(json.dumps(index_summary(client, index), indent=2))


@cli.command("launches")
@click.pass_context
def cmd_launches(ctx: click.Context) -> None:
    """List every launch with target date, region, task count."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    click.echo(json.dumps(list_launches(client, index), indent=2))


@cli.command("tasks")
@click.option("--launch", "launch_id", required=True, help="Launch ID, e.g. L-STD-001")
@click.pass_context
def cmd_tasks(ctx: click.Context, launch_id: str) -> None:
    """List all tasks for a single launch (sorted by task_id)."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    tasks = get_launch_tasks(client, index, launch_id)
    click.echo(json.dumps({"launch_id": launch_id, "task_count": len(tasks), "tasks": tasks}, indent=2))


@cli.command("filter")
@click.option("--filter", "-f", "filters", multiple=True, required=True,
              help="Filter expr (repeatable). Supports = != > >= < <= and comma-OR. "
                   "Examples: -f market_region=EU  -f criticality=High,Medium  -f lead_time_days>=30")
@click.option("--size", default=200, type=int, help="Max docs to return (default 200)")
@click.pass_context
def cmd_filter(ctx: click.Context, filters: tuple[str, ...], size: int) -> None:
    """Filter task docs across all launches."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    docs = filter_tasks(client, index, list(filters), size=size)
    click.echo(json.dumps({"filters": list(filters), "count": len(docs), "tasks": docs}, indent=2))


@cli.command("drop")
@click.confirmation_option(prompt="Delete the index? This cannot be undone.")
@click.pass_context
def cmd_drop(ctx: click.Context) -> None:
    """Delete the launch-planning index."""
    client, index = ctx.obj["client"], ctx.obj["index"]
    click.echo(json.dumps({"index": index, **delete_index(client, index)}))


if __name__ == "__main__":
    cli()

#!/usr/bin/env python3
"""launch-planner — deterministic CPM scheduler for the Smart Planner Agent.

Subcommands map onto the five sample queries in the brief:

    plan          create a feasible plan (Query 1)
    feasibility   check whether a target date is feasible (Query 2)
    replan        apply lead-time / dependency overrides + replan (Query 3)
    critical-path explain why a task is on the critical path (Query 4)
    risks         list at-risk tasks for a human-override decision (Query 5)
"""

from __future__ import annotations

import json
import os
from datetime import date
from typing import Any

import click

_DEFAULT_NDJSON = (
    os.environ.get("LAUNCH_PLAN_NDJSON")
    or "data/launch_planning/launch_tasks.ndjson"
)

from .scheduler import (
    Schedule,
    Task,
    apply_overrides,
    at_risk_tasks,
    explain_task,
    schedule,
)
from .sources import load_from_file, load_from_opensearch


# --- shared options -------------------------------------------------------


def _source_options(f):
    f = click.option("--launch", "launch_id", required=True, help="Launch ID, e.g. L-STD-001")(f)
    f = click.option("--source", type=click.Choice(["opensearch", "file"]), default="opensearch",
                     help="Where to load tasks from (default: opensearch)")(f)
    f = click.option("--file", "ndjson_file", default=_DEFAULT_NDJSON,
                     type=click.Path(), help="NDJSON file (used when --source=file). "
                                             "Defaults to $LAUNCH_PLAN_NDJSON if set.")(f)
    f = click.option("--today", "today_str", default=None,
                     help="Reference date for the forward pass (default: system date). "
                          "Use a fixed value to make demos reproducible.")(f)
    return f


def _load_tasks(launch_id: str, source: str, ndjson_file: str) -> tuple[list[dict[str, Any]], date]:
    if source == "opensearch":
        return load_from_opensearch(launch_id)
    return load_from_file(launch_id, ndjson_file)


def _resolve_today(today_str: str | None) -> date:
    return date.fromisoformat(today_str) if today_str else date.today()


def _resolve_target(target_str: str | None, fallback: date) -> date:
    return date.fromisoformat(target_str) if target_str else fallback


def _serialize_schedule(s: Schedule) -> dict[str, Any]:
    return {
        "launch_id": s.launch_id,
        "today": s.today.isoformat(),
        "target_launch_date": s.target_launch_date.isoformat(),
        "earliest_feasible_date": s.earliest_feasible_date.isoformat(),
        "feasible": s.feasible,
        "buffer_days": s.buffer_days,
        "tasks": [
            {
                "task_id":         st.task.task_id,
                "task_name":       st.task.task_name,
                "category":        st.task.task_category,
                "owner_role":      st.task.task_owner_role,
                "lead_time_days":  st.task.lead_time_days,
                "depends_on":      st.task.dependency_task_id,
                "criticality":     st.task.criticality,
                "earliest_start":  st.earliest_start.isoformat(),
                "earliest_finish": st.earliest_finish.isoformat(),
                "latest_start":    st.latest_start.isoformat(),
                "latest_finish":   st.latest_finish.isoformat(),
                "slack_days":      st.slack_days,
                "is_critical":     st.is_critical,
            }
            for st in s.tasks
        ],
        "critical_path": [st.task.task_id for st in s.critical_path()],
    }


# --- CLI ------------------------------------------------------------------


@click.group()
def cli() -> None:
    """Deterministic CPM scheduler for product-launch plans."""


@cli.command("plan")
@_source_options
@click.option("--target", "target_str", default=None,
              help="Override the launch's stored target date (YYYY-MM-DD).")
def cmd_plan(launch_id: str, source: str, ndjson_file: str, today_str: str | None,
             target_str: str | None) -> None:
    """Produce a feasible plan for the launch."""
    docs, stored_target = _load_tasks(launch_id, source, ndjson_file)
    today = _resolve_today(today_str)
    target = _resolve_target(target_str, stored_target)
    sched = schedule((Task.from_dict(d) for d in docs), target=target, today=today)
    sched.launch_id = launch_id
    out = _serialize_schedule(sched)
    out["explanation"] = _explanation_for_plan(sched)
    click.echo(json.dumps(out, indent=2))


@cli.command("feasibility")
@_source_options
@click.option("--target", "target_str", required=True, help="Target launch date (YYYY-MM-DD)")
def cmd_feasibility(launch_id: str, source: str, ndjson_file: str, today_str: str | None,
                    target_str: str) -> None:
    """Decide whether a target date is feasible. Returns earliest feasible date."""
    docs, _ = _load_tasks(launch_id, source, ndjson_file)
    today = _resolve_today(today_str)
    target = _resolve_target(target_str, today)
    sched = schedule((Task.from_dict(d) for d in docs), target=target, today=today)
    sched.launch_id = launch_id
    bottleneck = _bottleneck_task(sched)
    click.echo(json.dumps({
        "launch_id": launch_id,
        "today": today.isoformat(),
        "requested_target": target.isoformat(),
        "feasible": sched.feasible,
        "earliest_feasible_date": sched.earliest_feasible_date.isoformat(),
        "buffer_days": sched.buffer_days,
        "bottleneck_task": bottleneck,
        "critical_path": [st.task.task_id for st in sched.critical_path()],
        "explanation": _explanation_for_feasibility(sched, bottleneck),
    }, indent=2))


@cli.command("replan")
@_source_options
@click.option("--target", "target_str", default=None,
              help="Override the launch's stored target date (YYYY-MM-DD).")
@click.option("--override", "overrides", multiple=True,
              help="Override (repeatable). Syntax: 'TASK:lead_time=N' or 'TASK:dependency=ID|none'.")
def cmd_replan(launch_id: str, source: str, ndjson_file: str, today_str: str | None,
               target_str: str | None, overrides: tuple[str, ...]) -> None:
    """Apply overrides and replan. Reports the schedule delta vs. the baseline."""
    docs, stored_target = _load_tasks(launch_id, source, ndjson_file)
    today = _resolve_today(today_str)
    target = _resolve_target(target_str, stored_target)

    base_tasks = [Task.from_dict(d) for d in docs]
    baseline = schedule(base_tasks, target=target, today=today)
    baseline.launch_id = launch_id

    new_tasks, override_log = apply_overrides(base_tasks, list(overrides))
    revised = schedule(new_tasks, target=target, today=today)
    revised.launch_id = launch_id

    delta = {
        "buffer_days_before": baseline.buffer_days,
        "buffer_days_after":  revised.buffer_days,
        "buffer_delta_days":  revised.buffer_days - baseline.buffer_days,
        "earliest_feasible_before": baseline.earliest_feasible_date.isoformat(),
        "earliest_feasible_after":  revised.earliest_feasible_date.isoformat(),
        "feasible_before": baseline.feasible,
        "feasible_after":  revised.feasible,
    }
    click.echo(json.dumps({
        "launch_id": launch_id,
        "today": today.isoformat(),
        "target_launch_date": target.isoformat(),
        "overrides": override_log,
        "delta": delta,
        "schedule": _serialize_schedule(revised),
        "explanation": _explanation_for_replan(baseline, revised, override_log),
    }, indent=2))


@cli.command("critical-path")
@_source_options
@click.option("--target", "target_str", default=None,
              help="Override the launch's stored target date (YYYY-MM-DD).")
@click.option("--task", "task_id", default=None,
              help="If set, explain only this task's place on the path.")
def cmd_critical_path(launch_id: str, source: str, ndjson_file: str, today_str: str | None,
                      target_str: str | None, task_id: str | None) -> None:
    """Show the critical path (and optionally explain a single task)."""
    docs, stored_target = _load_tasks(launch_id, source, ndjson_file)
    today = _resolve_today(today_str)
    target = _resolve_target(target_str, stored_target)
    sched = schedule((Task.from_dict(d) for d in docs), target=target, today=today)
    sched.launch_id = launch_id
    payload: dict[str, Any] = {
        "launch_id": launch_id,
        "target_launch_date": target.isoformat(),
        "critical_path": [
            {
                "task_id": st.task.task_id,
                "task_name": st.task.task_name,
                "lead_time_days": st.task.lead_time_days,
                "earliest_finish": st.earliest_finish.isoformat(),
            }
            for st in sched.critical_path()
        ],
    }
    if task_id:
        payload["task"] = explain_task(sched, task_id)
        payload["explanation"] = _explanation_for_critical_task(sched, payload["task"])
    else:
        payload["explanation"] = _explanation_for_critical_path(sched)
    click.echo(json.dumps(payload, indent=2))


@cli.command("risks")
@_source_options
@click.option("--target", "target_str", default=None,
              help="Target date (defaults to the launch's stored target).")
@click.option("--threshold", default=5, type=int,
              help="Slack threshold in days — tasks at or below this are flagged (default 5).")
def cmd_risks(launch_id: str, source: str, ndjson_file: str, today_str: str | None,
              target_str: str | None, threshold: int) -> None:
    """List at-risk tasks for a human-override decision."""
    docs, stored_target = _load_tasks(launch_id, source, ndjson_file)
    today = _resolve_today(today_str)
    target = _resolve_target(target_str, stored_target)
    sched = schedule((Task.from_dict(d) for d in docs), target=target, today=today)
    sched.launch_id = launch_id
    flagged = at_risk_tasks(sched, threshold_days=threshold)
    click.echo(json.dumps({
        "launch_id": launch_id,
        "target_launch_date": target.isoformat(),
        "feasible": sched.feasible,
        "buffer_days": sched.buffer_days,
        "threshold_days": threshold,
        "at_risk_tasks": flagged,
        "explanation": _explanation_for_risks(sched, flagged, threshold),
    }, indent=2))


# --- explanations (plain language, deterministic) -------------------------


def _bottleneck_task(s: Schedule) -> dict[str, Any] | None:
    """Task that drives the earliest-feasible date — the last task to finish on the forward pass."""
    if not s.tasks:
        return None
    last = max(s.tasks, key=lambda st: st.earliest_finish)
    return {
        "task_id": last.task.task_id,
        "task_name": last.task.task_name,
        "earliest_finish": last.earliest_finish.isoformat(),
    }


def _explanation_for_plan(s: Schedule) -> str:
    n = len(s.tasks)
    cats = sorted({st.task.task_category for st in s.tasks})
    if s.feasible:
        finish = s.earliest_feasible_date.isoformat()
        buf = s.buffer_days
        head = (f"Created a feasible launch plan with {n} tasks across "
                f"{', '.join(cats)}. The plan completes on {finish}, "
                f"{buf} day(s) ahead of the {s.target_launch_date.isoformat()} target.")
    else:
        head = (f"The {s.target_launch_date.isoformat()} target is NOT feasible. "
                f"The earliest feasible launch date is "
                f"{s.earliest_feasible_date.isoformat()}.")
    cp = s.critical_path()
    if cp:
        cp_names = " → ".join(st.task.task_name for st in cp)
        head += f" Critical path: {cp_names}."
    return head


def _explanation_for_feasibility(s: Schedule, bottleneck: dict[str, Any] | None) -> str:
    if s.feasible:
        return (f"A launch on {s.target_launch_date.isoformat()} IS feasible. "
                f"The earliest feasible finish is "
                f"{s.earliest_feasible_date.isoformat()} "
                f"({s.buffer_days} day(s) of buffer).")
    msg = (f"A launch on {s.target_launch_date.isoformat()} is NOT feasible. "
           f"The earliest feasible launch date is "
           f"{s.earliest_feasible_date.isoformat()}.")
    if bottleneck:
        msg += (f" The driving task is {bottleneck['task_name']} "
                f"({bottleneck['task_id']}), which finishes on "
                f"{bottleneck['earliest_finish']}.")
    return msg


def _explanation_for_replan(before: Schedule, after: Schedule, override_log: list[dict[str, Any]]) -> str:
    if not override_log:
        return "No overrides provided — the plan is unchanged from the baseline."
    parts = []
    for o in override_log:
        parts.append(f"{o['task_id']}.{o['field']}: {o['from']} → {o['to']}")
    delta = after.buffer_days - before.buffer_days
    direction = "improving" if delta > 0 else ("reducing" if delta < 0 else "leaving unchanged")
    msg = (f"Applied override(s) [{'; '.join(parts)}]. "
           f"The new plan completes on "
           f"{after.earliest_feasible_date.isoformat()}, "
           f"{direction} schedule buffer by {abs(delta)} day(s).")
    if not after.feasible:
        msg += " Note: the revised plan is still infeasible against the requested target."
    return msg


def _explanation_for_critical_path(s: Schedule) -> str:
    cp = s.critical_path()
    if not cp:
        return "No critical-path tasks identified — every task has slack."
    names = " → ".join(st.task.task_name for st in cp)
    total = sum(st.task.lead_time_days for st in cp)
    return (f"The critical path runs through {len(cp)} task(s) "
            f"totaling {total} days: {names}. "
            f"Any delay on these tasks delays the launch one-for-one.")


def _explanation_for_critical_task(s: Schedule, task: dict[str, Any]) -> str:
    if not task["is_critical"]:
        return (f"{task['task_name']} ({task['task_id']}) is not on the critical path "
                f"— it has {task['slack_days']} day(s) of slack.")
    downstream = task.get("downstream_tasks") or []
    if downstream:
        names = ", ".join(d["task_name"] for d in downstream)
        return (f"{task['task_name']} ({task['task_id']}) is on the critical path "
                f"because {names} cannot proceed until it completes. "
                f"Any delay here directly delays the launch.")
    return (f"{task['task_name']} ({task['task_id']}) is on the critical path "
            f"as the final activity before launch — any delay here "
            f"shifts the launch date by the same amount.")


def _explanation_for_risks(s: Schedule, flagged: list[dict[str, Any]], threshold: int) -> str:
    if not s.feasible:
        return (f"The {s.target_launch_date.isoformat()} target is infeasible — "
                f"earliest feasible is {s.earliest_feasible_date.isoformat()}. "
                f"Proceeding requires expediting the critical path.")
    if not flagged:
        return (f"No tasks have less than {threshold + 1} day(s) of slack — "
                f"the plan has comfortable buffer everywhere.")
    cats = sorted({r["category"] for r in flagged})
    return (f"Proceeding with the {s.target_launch_date.isoformat()} date carries "
            f"elevated risk in {', '.join(cats)} — {len(flagged)} task(s) sit at or below "
            f"{threshold} day(s) of slack. Expediting these tasks may be needed if delays occur.")


if __name__ == "__main__":
    cli()

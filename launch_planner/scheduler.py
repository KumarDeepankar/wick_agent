"""Critical-path scheduling for a single launch.

Inputs are normalized task dicts:
    {
        "task_id": "T-001",
        "task_name": "Regulatory Submission Complete",
        "task_category": "Regulatory",
        "lead_time_days": 90,
        "dependency_task_id": None,
        "criticality": "High",
        "task_owner_role": "Regulatory Affairs",
    }

The scheduler runs a backward pass from the target launch date to derive
latest_start / latest_finish, and a forward pass from `today` to derive
earliest_start / earliest_finish. Slack = latest_start − earliest_start;
tasks with slack == 0 sit on the critical path.

Feasibility:
    feasible iff every latest_start >= today AND every dependency-respecting
    schedule completes on or before the target. The forward pass also
    yields the earliest-feasible launch date — used when the requested
    target is too tight.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, timedelta
from typing import Any, Iterable


@dataclass
class Task:
    task_id: str
    task_name: str
    task_category: str
    lead_time_days: int
    dependency_task_id: str | None
    criticality: str
    task_owner_role: str

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Task":
        return cls(
            task_id=d["task_id"],
            task_name=d.get("task_name", d["task_id"]),
            task_category=d.get("task_category", "Unknown"),
            lead_time_days=int(d["lead_time_days"]),
            dependency_task_id=d.get("dependency_task_id") or None,
            criticality=d.get("criticality", "Medium"),
            task_owner_role=d.get("task_owner_role", "Unknown"),
        )


@dataclass
class ScheduledTask:
    task: Task
    earliest_start: date
    earliest_finish: date
    latest_start: date
    latest_finish: date
    is_critical: bool = False

    @property
    def slack_days(self) -> int:
        return (self.latest_start - self.earliest_start).days


@dataclass
class Schedule:
    launch_id: str
    target_launch_date: date
    today: date
    tasks: list[ScheduledTask]
    earliest_feasible_date: date
    feasible: bool

    @property
    def buffer_days(self) -> int:
        """Days between project finish and target. Negative => infeasible."""
        return (self.target_launch_date - self.earliest_feasible_date).days

    def by_id(self) -> dict[str, ScheduledTask]:
        return {st.task.task_id: st for st in self.tasks}

    def critical_path(self) -> list[ScheduledTask]:
        """Critical-path tasks in dependency order."""
        critical = [st for st in self.tasks if st.is_critical]
        return _topo_sort(critical)


def _topo_sort(scheduled: list[ScheduledTask]) -> list[ScheduledTask]:
    by_id = {st.task.task_id: st for st in scheduled}
    visited: set[str] = set()
    out: list[ScheduledTask] = []

    def visit(tid: str) -> None:
        if tid in visited or tid not in by_id:
            return
        st = by_id[tid]
        dep = st.task.dependency_task_id
        if dep:
            visit(dep)
        visited.add(tid)
        out.append(st)

    for st in scheduled:
        visit(st.task.task_id)
    return out


def schedule(tasks: Iterable[Task], target: date, today: date) -> Schedule:
    task_list = list(tasks)
    by_id = {t.task_id: t for t in task_list}
    dependents: dict[str, list[str]] = {t.task_id: [] for t in task_list}
    for t in task_list:
        if t.dependency_task_id and t.dependency_task_id in dependents:
            dependents[t.dependency_task_id].append(t.task_id)

    # --- forward pass: earliest_start / earliest_finish from `today` ---
    earliest_start: dict[str, date] = {}
    earliest_finish: dict[str, date] = {}

    def compute_es(tid: str) -> None:
        if tid in earliest_start:
            return
        t = by_id[tid]
        if not t.dependency_task_id:
            es = today
        else:
            compute_es(t.dependency_task_id)
            es = earliest_finish[t.dependency_task_id]
        earliest_start[tid] = es
        earliest_finish[tid] = es + timedelta(days=t.lead_time_days)

    for t in task_list:
        compute_es(t.task_id)

    earliest_feasible = max(earliest_finish.values())

    # --- backward pass: latest_finish / latest_start from `target` ---
    latest_finish: dict[str, date] = {}
    latest_start: dict[str, date] = {}

    def compute_ls(tid: str) -> None:
        if tid in latest_start:
            return
        t = by_id[tid]
        downstream = dependents[tid]
        if not downstream:
            lf = target
        else:
            for d in downstream:
                compute_ls(d)
            lf = min(latest_start[d] for d in downstream)
        latest_finish[tid] = lf
        latest_start[tid] = lf - timedelta(days=t.lead_time_days)

    for t in task_list:
        compute_ls(t.task_id)

    feasible = earliest_feasible <= target and all(
        latest_start[t.task_id] >= today for t in task_list
    )

    scheduled = [
        ScheduledTask(
            task=t,
            earliest_start=earliest_start[t.task_id],
            earliest_finish=earliest_finish[t.task_id],
            latest_start=latest_start[t.task_id],
            latest_finish=latest_finish[t.task_id],
        )
        for t in task_list
    ]
    # Critical-path = tasks with the minimum slack across the schedule
    # (handles both tight and buffered plans correctly).
    if scheduled:
        min_slack = min(st.slack_days for st in scheduled)
        for st in scheduled:
            st.is_critical = st.slack_days == min_slack
    scheduled = _topo_sort(scheduled)

    return Schedule(
        launch_id="",  # set by caller
        target_launch_date=target,
        today=today,
        tasks=scheduled,
        earliest_feasible_date=earliest_feasible,
        feasible=feasible,
    )


# --- overrides ------------------------------------------------------------
# Override syntax used by the CLI: "T-005:lead_time=20" or
# "T-005:dependency=T-003" (use "none" / "null" / empty string to clear).


def apply_overrides(tasks: list[Task], overrides: list[str]) -> tuple[list[Task], list[dict[str, Any]]]:
    """Return a new task list with overrides applied + a record of changes."""
    by_id = {t.task_id: t for t in tasks}
    log: list[dict[str, Any]] = []
    for spec in overrides:
        if ":" not in spec or "=" not in spec:
            raise ValueError(f"bad override syntax: {spec!r} (expected 'TASK:field=value')")
        tid, rest = spec.split(":", 1)
        field, raw_value = rest.split("=", 1)
        tid, field, raw_value = tid.strip(), field.strip(), raw_value.strip()
        if tid not in by_id:
            raise ValueError(f"unknown task_id in override: {tid!r}")
        t = by_id[tid]
        if field in ("lead_time", "lead_time_days"):
            old = t.lead_time_days
            new = int(raw_value)
            by_id[tid] = Task(**{**t.__dict__, "lead_time_days": new})
            log.append({"task_id": tid, "field": "lead_time_days", "from": old, "to": new})
        elif field in ("dependency", "dependency_task_id"):
            old = t.dependency_task_id
            new_dep = None if raw_value.lower() in ("", "none", "null") else raw_value
            by_id[tid] = Task(**{**t.__dict__, "dependency_task_id": new_dep})
            log.append({"task_id": tid, "field": "dependency_task_id", "from": old, "to": new_dep})
        else:
            raise ValueError(f"unsupported override field: {field!r}")
    # Preserve original ordering.
    return [by_id[t.task_id] for t in tasks], log


# --- explanation helpers --------------------------------------------------


def explain_task(schedule_obj: Schedule, task_id: str) -> dict[str, Any]:
    by_id = schedule_obj.by_id()
    if task_id not in by_id:
        raise KeyError(task_id)
    st = by_id[task_id]
    blockers = []
    if st.task.dependency_task_id:
        dep = by_id.get(st.task.dependency_task_id)
        if dep:
            blockers.append({
                "task_id": dep.task.task_id,
                "task_name": dep.task.task_name,
                "earliest_finish": dep.earliest_finish.isoformat(),
            })
    downstream = [
        s for s in schedule_obj.tasks
        if s.task.dependency_task_id == task_id
    ]
    return {
        "task_id": st.task.task_id,
        "task_name": st.task.task_name,
        "category": st.task.task_category,
        "lead_time_days": st.task.lead_time_days,
        "earliest_start": st.earliest_start.isoformat(),
        "earliest_finish": st.earliest_finish.isoformat(),
        "latest_start": st.latest_start.isoformat(),
        "latest_finish": st.latest_finish.isoformat(),
        "slack_days": st.slack_days,
        "is_critical": st.is_critical,
        "blockers": blockers,
        "downstream_tasks": [
            {"task_id": s.task.task_id, "task_name": s.task.task_name}
            for s in downstream
        ],
    }


def at_risk_tasks(schedule_obj: Schedule, threshold_days: int = 5) -> list[dict[str, Any]]:
    """Tasks whose slack is below threshold — exposes risk for human override scenarios."""
    rows = []
    for st in schedule_obj.tasks:
        if st.slack_days <= threshold_days:
            rows.append({
                "task_id": st.task.task_id,
                "task_name": st.task.task_name,
                "category": st.task.task_category,
                "criticality": st.task.criticality,
                "owner_role": st.task.task_owner_role,
                "slack_days": st.slack_days,
                "is_critical": st.is_critical,
            })
    rows.sort(key=lambda r: (r["slack_days"], r["task_id"]))
    return rows

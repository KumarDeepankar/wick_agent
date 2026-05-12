#!/usr/bin/env python3
"""Generate dummy launch-planning dataset (NDJSON).

Each line is one task document for one launch. The full set covers ten
"Standard" product launches across EU / US / JP / APAC / LATAM, with a
shared 10-task template (Regulatory → Supply → Quality → Logistics →
Commercial).

Run:
    python generate.py                     # writes launch_tasks.ndjson
    python generate.py --out custom.ndjson
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from datetime import date, timedelta
from pathlib import Path

# --- Task template ---------------------------------------------------------
# Single dependency per task (matches the schema in the brief). Lead times
# and criticality are illustrative and align with the sample explanations.


@dataclass(frozen=True)
class TaskTemplate:
    task_id: str
    task_name: str
    task_category: str
    lead_time_days: int
    dependency_task_id: str | None
    task_owner_role: str
    criticality: str


TEMPLATE: list[TaskTemplate] = [
    TaskTemplate("T-001", "Regulatory Submission Complete", "Regulatory", 90, None,    "Regulatory Affairs", "High"),
    TaskTemplate("T-002", "API Manufacturing",              "Supply",     45, "T-001", "Manufacturing Lead", "High"),
    TaskTemplate("T-003", "Drug Product Manufacturing",     "Supply",     30, "T-002", "Manufacturing Lead", "High"),
    TaskTemplate("T-004", "Quality Batch Release",          "Quality",    21, "T-003", "QA Manager",         "High"),
    TaskTemplate("T-005", "Final Packaging",                "Logistics",  30, "T-004", "Packaging Lead",     "Medium"),
    TaskTemplate("T-006", "Serialization & Labeling",       "Quality",    14, "T-005", "QA Manager",         "Medium"),
    TaskTemplate("T-007", "Distribution to Markets",        "Logistics",  14, "T-006", "Logistics Lead",     "Medium"),
    TaskTemplate("T-008", "Commercial Launch Readiness",    "Commercial", 60, "T-001", "Commercial Lead",    "Medium"),
    TaskTemplate("T-009", "Marketing Material Approval",    "Commercial", 21, "T-008", "Marketing Manager",  "Low"),
    TaskTemplate("T-010", "Launch Day Activities",          "Commercial",  1, "T-007", "Launch Lead",        "Medium"),
]


# --- Launches --------------------------------------------------------------
# Mix of comfortable target dates and one deliberately-tight launch
# (L-STD-006) so the feasibility query in the brief has something to
# fail against.


@dataclass(frozen=True)
class Launch:
    launch_id: str
    target_launch_date: date
    market_region: str


LAUNCHES: list[Launch] = [
    Launch("L-STD-001", date(2026,  9, 30), "EU"),
    Launch("L-STD-002", date(2026, 10, 15), "US"),
    Launch("L-STD-003", date(2026, 11, 30), "JP"),
    Launch("L-STD-004", date(2026, 12, 15), "APAC"),
    Launch("L-STD-005", date(2027,  1, 31), "EU"),
    Launch("L-STD-006", date(2026,  8, 15), "US"),
    Launch("L-STD-007", date(2027,  3,  1), "EU"),
    Launch("L-STD-008", date(2026, 10, 30), "LATAM"),
    Launch("L-STD-009", date(2027,  2, 28), "JP"),
    Launch("L-STD-010", date(2026, 11, 15), "US"),
]


def _backward_schedule(launch: Launch) -> dict[str, tuple[date, date]]:
    """Compute (earliest_start, latest_finish) for each task by backward
    pass from the target launch date. These are illustrative constraints
    written into the dataset — the planner CLI re-derives them at runtime."""
    finish: dict[str, date] = {}
    start: dict[str, date] = {}

    by_id = {t.task_id: t for t in TEMPLATE}
    # Sinks first: tasks no one depends on must finish by launch date.
    dependents: dict[str, list[str]] = {t.task_id: [] for t in TEMPLATE}
    for t in TEMPLATE:
        if t.dependency_task_id:
            dependents[t.dependency_task_id].append(t.task_id)

    # Topological order (template is already sorted, but be explicit).
    order = list(reversed(TEMPLATE))
    for t in order:
        downstream = dependents[t.task_id]
        if not downstream:
            latest_finish = launch.target_launch_date
        else:
            latest_finish = min(start[d] for d in downstream)
        latest_start = latest_finish - timedelta(days=t.lead_time_days)
        finish[t.task_id] = latest_finish
        start[t.task_id] = latest_start

    return {tid: (start[tid], finish[tid]) for tid in start}


def emit(path: Path) -> int:
    n = 0
    with path.open("w", encoding="utf-8") as f:
        for launch in LAUNCHES:
            window = _backward_schedule(launch)
            for t in TEMPLATE:
                est, lft = window[t.task_id]
                doc = {
                    "launch_id": launch.launch_id,
                    "product_type": "Standard",
                    "target_launch_date": launch.target_launch_date.isoformat(),
                    "market_region": launch.market_region,
                    "task_id": t.task_id,
                    "task_name": t.task_name,
                    "task_category": t.task_category,
                    "lead_time_days": t.lead_time_days,
                    "dependency_task_id": t.dependency_task_id,
                    "earliest_start_date": est.isoformat(),
                    "latest_finish_date": lft.isoformat(),
                    "task_owner_role": t.task_owner_role,
                    "criticality": t.criticality,
                    "status": "Planned",
                }
                f.write(json.dumps(doc) + "\n")
                n += 1
    return n


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="launch_tasks.ndjson", help="Output NDJSON path")
    args = ap.parse_args()
    out = Path(args.out)
    n = emit(out)
    print(f"wrote {n} task documents to {out}")


if __name__ == "__main__":
    main()

# Launch Planning Dummy Dataset

Demonstrator data for the Smart Planner Agent. **All data is illustrative.**

## What's here

```
data/launch_planning/
├── generate.py              # deterministic generator script
├── launch_tasks.ndjson      # 100 task documents (10 launches × 10 tasks)
└── README.md                # this file
```

Each line of `launch_tasks.ndjson` is one task document matching the schema in
the brief:

```json
{
  "launch_id": "L-STD-001",
  "product_type": "Standard",
  "target_launch_date": "2026-09-30",
  "market_region": "EU",
  "task_id": "T-001",
  "task_name": "Regulatory Submission Complete",
  "task_category": "Regulatory",
  "lead_time_days": 90,
  "dependency_task_id": null,
  "earliest_start_date": "2026-01-28",
  "latest_finish_date": "2026-04-28",
  "task_owner_role": "Regulatory Affairs",
  "criticality": "High",
  "status": "Planned"
}
```

## Launches

Ten launches, mostly comfortable, with one deliberately tight (`L-STD-006`,
target 2026-08-15) so the feasibility query in the brief has something to
fail against.

| launch_id  | target       | region |
|------------|--------------|--------|
| L-STD-001  | 2026-09-30   | EU     |
| L-STD-002  | 2026-10-15   | US     |
| L-STD-003  | 2026-11-30   | JP     |
| L-STD-004  | 2026-12-15   | APAC   |
| L-STD-005  | 2027-01-31   | EU     |
| L-STD-006  | 2026-08-15   | US     |  ← tight on purpose
| L-STD-007  | 2027-03-01   | EU     |
| L-STD-008  | 2026-10-30   | LATAM  |
| L-STD-009  | 2027-02-28   | JP     |
| L-STD-010  | 2026-11-15   | US     |

## Task template (10 tasks per launch)

```
T-001 Regulatory Submission Complete  Regulatory   90d  (no dep)         High
T-002 API Manufacturing                Supply       45d  ← T-001          High
T-003 Drug Product Manufacturing       Supply       30d  ← T-002          High
T-004 Quality Batch Release            Quality      21d  ← T-003          High
T-005 Final Packaging                  Logistics    30d  ← T-004          Medium
T-006 Serialization & Labeling         Quality      14d  ← T-005          Medium
T-007 Distribution to Markets          Logistics    14d  ← T-006          Medium
T-008 Commercial Launch Readiness      Commercial   60d  ← T-001          Medium
T-009 Marketing Material Approval      Commercial   21d  ← T-008          Low
T-010 Launch Day Activities            Commercial    1d  ← T-007          Medium
```

The longest chain (T-001 → T-002 → T-003 → T-004 → T-005 → T-006 → T-007 →
T-010) sums to **245 days**, which is the critical path on every launch.

## Loading into OpenSearch

Use the `product-opensearch-cli` (separate from the generic `opensearch_cli`):

```bash
# 1. Regenerate the NDJSON (idempotent)
python data/launch_planning/generate.py

# 2. Create the index with the right mapping
product-opensearch-cli init --drop-if-exists

# 3. Bulk-load
product-opensearch-cli load --file data/launch_planning/launch_tasks.ndjson

# 4. Sanity check
product-opensearch-cli summary
product-opensearch-cli launches
product-opensearch-cli tasks --launch L-STD-001
```

If `OPENSEARCH_URL` isn't `http://localhost:9200`, pass `--url` or set the
env var.

## Running the planner without OpenSearch

The deterministic scheduler can also read directly from this NDJSON file —
useful for demos with no running OpenSearch:

```bash
launch-planner plan --launch L-STD-001 \
  --source file --file data/launch_planning/launch_tasks.ndjson \
  --today 2026-01-01
```

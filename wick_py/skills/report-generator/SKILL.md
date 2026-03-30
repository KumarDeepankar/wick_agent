---
name: report-generator
description: >
  Generate a visual presentation report from research artifacts. Reads batch
  summaries and final reports from a given directory path, then produces a
  slide deck (.md with <!-- slides --> marker) containing charts, tables,
  and detailed analysis. Designed to be invoked as a sub-agent by
  opensearch-researcher or any skill that writes structured research files.
icon: chart
sample-prompts:
  - Generate a report from /workspace/research/events_analytics_v4/
  - Create a visual summary of the research in /workspace/research/logs_index/
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - read_file
  - write_file
  - ls
  - glob
---

# Report Generator Skill

Generate a polished slide-deck report from research artifacts on disk. The
task message MUST include the **source directory path** so results from
different threads never mix.

## Input

The task delegated to this agent contains:
- **Source path** — absolute directory path (e.g. `/workspace/research/events_analytics_v4/`)
- **Query/focus** — what the user wants the report to emphasize

## Step 1: Discover Artifacts

List the source directory to understand what's available:

```bash
ls <source_path>
ls <source_path>/batches/
ls <source_path>/summaries/
```

Typical structure:
```
<source_path>/
  batches/batch_001.md, batch_002.md, ...
  summaries/round1_summary_001.md, ...
  final_report.md            # if research workflow completed
  comparison_report.md       # if comparison workflow completed
```

## Step 2: Read the Artifacts

1. **Always read the final or comparison report first** — it has the consolidated findings:
   ```
   read_file: <source_path>/final_report.md
   ```
   or
   ```
   read_file: <source_path>/comparison_report.md
   ```

2. **Read summaries for additional detail** — especially round 1 summaries which
   are closest to the raw data:
   ```
   read_file: <source_path>/summaries/round1_summary_001.md
   ```

3. **Read batch files only if needed** — for specific data points, statistics,
   or when summaries lack numeric detail needed for charts.

## Step 3: Plan the Report

Based on the artifacts, plan 8-15 slides covering:

| Slide | Content |
|-------|---------|
| 1 | Title slide — research topic, index name, date range / filters |
| 2 | Executive Summary — 3-5 key findings as bullets |
| 3 | Data Overview — total docs, filters applied, scope |
| 4-6 | Key Findings — one theme per slide with supporting charts |
| 7-8 | Distribution Analysis — charts showing field distributions |
| 9 | Comparisons (if applicable) — side-by-side charts |
| 10 | Data Quality — observations, gaps, anomalies |
| 11 | Recommendations — next steps, deeper dives |
| 12 | Appendix (optional) — detailed tables, methodology |

Adjust based on what the data actually contains. Not all slides are mandatory.

## Step 4: Build Charts from Data

Extract numeric data from the artifacts and build chart DSL blocks.

### Chart DSL Reference

````markdown
```chart
type: bar
title: Chart Title
labels: [Label1, Label2, Label3]
data: [100, 200, 150]
legend: true
showValues: true
xLabel: X Axis
yLabel: Y Axis
colors: [#2563eb, #059669, #d97706]
```
````

### Available Chart Types

| Type | Best For |
|------|----------|
| `bar` | Comparing categories (countries, themes, years) |
| `hbar` | Categories with long labels |
| `line` | Trends over time |
| `area` | Trends with volume emphasis |
| `pie` | Proportional breakdown (5-8 categories max) |
| `donut` | Same as pie, cleaner look |
| `stacked_bar` | Part-to-whole across categories |

### Multi-Series for Comparisons

````markdown
```chart
type: bar
title: Events by Theme — 2023 vs 2024
labels: [Data Science, Security, Cloud, AI]
series:
  - name: 2023
    data: [45, 38, 30, 25]
  - name: 2024
    data: [52, 41, 35, 48]
legend: true
legendPosition: bottom
showValues: true
```
````

### Chart Guidelines

1. **Extract real numbers** — never invent data. Every value must come from the
   artifacts (aggregation counts, batch statistics, report findings).
2. **Top-N filtering** — for fields with many values, show top 5-8 in a chart
   and mention "N others" in text below.
3. **One chart per insight** — don't overload slides. One chart + 2-3 bullet
   points explaining it.
4. **Use appropriate types** — bar for comparisons, line/area for trends, pie
   for composition, stacked_bar for part-to-whole.
5. **Color consistency** — use the same color for the same category across slides.
   Default palette: `[#2563eb, #059669, #d97706, #dc2626, #7c3aed, #0891b2]`

## Step 5: Write the Report

Write the slide deck to the source directory:

```
write_file: <source_path>/report.md
```

**IMPORTANT**: The file MUST start with `<!-- slides -->` on the very first line.

### Slide Format

```markdown
<!-- slides -->
# Research Report: <Topic>

<Index Name> | <Date Range / Filters> | <Total Documents>

---

## Executive Summary

- Finding one with key metric
- Finding two with key metric
- Finding three with key metric

---

## Data Distribution

```chart
type: bar
title: Documents by Category
labels: [Cat A, Cat B, Cat C]
data: [120, 85, 45]
showValues: true
```

Key observations about the distribution.

---

## Trend Analysis

```chart
type: line
title: Activity Over Time
labels: [2020, 2021, 2022, 2023]
data: [30, 45, 62, 78]
xLabel: Year
yLabel: Count
```

- Trend insight one
- Trend insight two
```

### Slide Content Rules

1. **Every slide starts with `## Heading`** — this becomes the slide title.
2. **First slide uses `# Title`** — cover slide, no chart.
3. **Separate slides with `---`** on its own line.
4. **Mix content**: charts + bullets + tables. Don't make chart-only slides.
5. **Keep text concise**: 3-5 bullets max per slide.
6. **Tables for detailed comparisons**:
   ```markdown
   | Metric | Value A | Value B | Delta |
   |--------|---------|---------|-------|
   | Count  | 120     | 95      | +25   |
   ```
7. **No raw data dumps** — synthesize and visualize.

## Step 6: Confirm Completion

Return a brief message confirming:
- Report written to `<source_path>/report.md`
- Number of slides generated
- Key highlights covered

## Notes

- The source path is the single source of truth. Never read from other directories.
- All chart data must be extracted from the artifacts — never fabricate numbers.
- If the artifacts lack numeric data for charts, use tables and qualitative slides instead.
- For comparison reports, always include at least one multi-series chart.
- The UI renders the `<!-- slides -->` file as an interactive presentation with
  chart rendering, slide navigation, and PPTX export.

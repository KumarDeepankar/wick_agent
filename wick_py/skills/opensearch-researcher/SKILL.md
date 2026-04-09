---
name: opensearch-researcher
description: >
  Research, analyze, and compare documents stored in an OpenSearch index. Uses
  aggregations to understand data distribution, applies filters to scope queries
  precisely, supports comparison mode for side-by-side analysis (e.g. 2023 vs 2024).
  Fans out result sets to parallel batch-processor sub-agents using dynamic batch
  sizing — batch size scales with document count, capped at 4 batches total — then
  delegates to a single summarizer to consolidate findings into a final report, and
  to report-generator for visual slides. Use this skill when the user asks to research, analyze, compare,
  or summarize data in an OpenSearch index.
icon: search
sample-prompts:
  - Research the events_analytics_v4 index
  - Summarize all incidents for year 2024
  - Analyze AI events in India from events_analytics_v4
  - Compare events of 2023 vs 2022
metadata:
  author: wick-agent
  version: "4.0"
allowed-tools:
  - read_file
  - write_file
  - execute
  - ls
  - glob
  - delegate_to_agent
---

# OpenSearch Researcher Skill

This skill orchestrates a multi-phase research pipeline over an OpenSearch index.
It uses aggregations to understand the data, applies filters to scope the query,
reads documents in up to 4 parallel batches, analyzes each batch, writes findings
to disk, then consolidates the findings into a single final report in one
summarization pass.

## File Paths — IMPORTANT

**Always use relative paths** for all file operations (write_file, read_file, ls,
glob, etc.). The tools automatically resolve relative paths to the correct
workspace directory. For example:

- `write_file` to `research/my_index/batches/batch_001.md` → works
- `read_file` from `research/my_index/final_report.md` → works
- `ls` on `research/my_index/batches/` → works

**Never use absolute paths** like `/workspace/...`, `/app/...`, or any path
starting with `/`. Absolute paths will fail with "outside workspace" errors.

This applies to you AND to all sub-agent task descriptions — always pass
relative paths to batch-processor, summarizer, and report-generator.

`write_file` automatically creates parent directories — no `mkdir` needed.

## Prerequisites

The `opensearch-cli` command must be available in PATH. It connects to OpenSearch
using the `OPENSEARCH_URL` environment variable (defaults to `http://localhost:9200`).

## CLI Reference

```bash
# List all indices
opensearch-cli list-indices

# Count documents (with optional filters)
opensearch-cli count --index <name>
opensearch-cli count --index <name> -f year=2024
opensearch-cli count --index <name> -f year=2024 -f country=India

# Get filterable fields (shows which fields support filtering)
opensearch-cli filterable-fields --index <name>

# Aggregations — shows value distributions per field
opensearch-cli aggs --index <name>                              # all fields
opensearch-cli aggs --index <name> -F year -F country           # specific fields
opensearch-cli aggs --index <name> -F country -f year=2023      # scoped by filter

# Query documents with filters and/or text search (paginated)
opensearch-cli query --index <name> -f year=2024 --batch-size 50 --offset 0
opensearch-cli query --index <name> -f year=2024 -f country=India --batch-size 50 --offset 50
opensearch-cli query --index <name> -f "event_count>=500" -q "summit" --field event_title.words

# Fetch all documents (no filters, paginated)
opensearch-cli fetch --index <name> --batch-size 50 --offset 0

# Get index mapping
opensearch-cli mapping --index <name>
```

### Filter syntax

Filters are passed via `-f` flag (repeatable for multiple filters):

| Format | Meaning | Example |
|--------|---------|---------|
| `field=value` | Exact match | `-f year=2023` |
| `field=v1,v2,v3` | Any of (OR) | `-f country=India,USA,Japan` |
| `field!=value` | Exclude | `-f country!=China` |
| `field>value` | Greater than | `-f event_count>500` |
| `field>=value` | Greater or equal | `-f year>=2022` |
| `field<value` | Less than | `-f event_count<100` |
| `field<=value` | Less or equal | `-f year<=2023` |

Multiple `-f` flags are combined with AND logic.

## Mode Detection

Before starting, analyze the user's request to determine the mode:

- **Research mode** — single scope analysis (e.g., "summarize events for 2023",
  "analyze AI events in India", "research this index")
- **Comparison mode** — side-by-side analysis of two or more groups (e.g.,
  "compare 2023 vs 2024", "how do India and USA events differ",
  "compare Security vs Cloud themes")

**Comparison keywords**: "compare", "vs", "versus", "differ", "difference between",
"side by side", "how does X compare to Y"

If comparison mode is detected, follow the **Comparison Workflow** below.
Otherwise, follow the **Research Workflow**.

---

## Comparison Workflow

Use this when the user wants to compare two or more groups side by side.
This is aggregation-driven — fast and efficient, no batching needed.

### Step 1: Discovery

1. **Get the index name**. If not specified:
   ```bash
   opensearch-cli list-indices
   ```

2. **Get filterable fields**:
   ```bash
   opensearch-cli filterable-fields --index <index_name>
   ```

3. **Run baseline aggregations** to understand the full data:
   ```bash
   opensearch-cli aggs --index <index_name>
   ```

### Step 2: Identify comparison groups

From the user's request, identify:
- **Comparison field**: the field being compared (e.g., `year`, `country`, `event_theme`)
- **Group values**: the specific values (e.g., `2023` and `2024`, or `India` and `USA`)

Check the baseline aggregations to verify the requested values exist.
If a value does not exist (e.g., year=2024 but data only has 2021-2023):
- Inform the user which values are available
- Suggest the closest available alternatives
- Ask if they want to proceed with the alternatives

### Step 3: Run scoped aggregations for each group

Run one `aggs` command per group, scoping to that group's filter:

```bash
# Group A
opensearch-cli aggs --index <index_name> -f <comparison_field>=<value_A>

# Group B
opensearch-cli aggs --index <index_name> -f <comparison_field>=<value_B>
```

For example, "compare 2023 vs 2022":
```bash
opensearch-cli aggs --index events_analytics_v4 -f year=2023
opensearch-cli aggs --index events_analytics_v4 -f year=2022
```

For "compare India vs USA":
```bash
opensearch-cli aggs --index events_analytics_v4 -f country=India
opensearch-cli aggs --index events_analytics_v4 -f country=USA
```

You can also narrow the comparison with additional filters. For example,
"compare India vs USA for AI events":
```bash
opensearch-cli aggs --index events_analytics_v4 -f country=India -f event_theme=Artificial Intelligence
opensearch-cli aggs --index events_analytics_v4 -f country=USA -f event_theme=Artificial Intelligence
```

### Step 4: Write comparison report

Write the comparison report (write_file creates parent directories automatically):
```
write_file: research/<index_name>/comparison_report.md
```

The comparison report MUST include:

1. **Overview**
   - What is being compared (field, values)
   - Document counts for each group

2. **Side-by-side comparison table** for each aggregated field:
   ```
   | Metric          | Group A (2023) | Group B (2022) | Delta    |
   |-----------------|----------------|----------------|----------|
   | Total docs      | 63             | 54             | +9       |
   | Top country     | India (10)     | India (8)      | +2       |
   | Top theme       | Data Science(8)| Security (7)   | -        |
   | Avg event_count | 512            | 480            | +32      |
   ```

3. **Key differences** — what stands out between the groups:
   - Fields where distributions shifted significantly
   - Values that appear in one group but not the other
   - Numeric fields where averages/totals changed notably

4. **Similarities** — what stayed consistent across groups

5. **Insights** — meaningful observations from the comparison

### Step 5: Visual Report

Delegate to report-generator to create an interactive slide-deck with
comparison charts:
```
delegate_to_agent: report-generator
task: "Source directory: research/<index_name>/
Focus: comparison of <group A> vs <group B> — <user's original query>
Primary data: research/<index_name>/comparison_report.md
Output: research/<index_name>/report.md"
```

### Step 6: Notify user

Tell the user the comparison is complete. Include:
- Groups compared and their document counts
- Path to the comparison report (`comparison_report.md`)
- Path to the visual report (`report.md`) — viewable as an interactive
  presentation in the canvas panel with PPTX export
- Top 3-5 most significant differences found

---

## Research Workflow

Use this for single-scope analysis (no comparison).

Follow these steps precisely. Do NOT skip any step.

### Phase 1: Discovery & Scoping

1. **Get the index name** from the user's message. If not specified, list indices:
   ```bash
   opensearch-cli list-indices
   ```
   Ask the user which index to research.

2. **Get filterable fields** to understand what filters are available:
   ```bash
   opensearch-cli filterable-fields --index <index_name>
   ```

3. **Run aggregations** to understand the data distribution:
   ```bash
   opensearch-cli aggs --index <index_name>
   ```
   This shows you:
   - **Keyword fields**: what values exist and how many documents each has
   - **Numeric fields**: min, max, avg, distribution
   - **Date fields**: date range and yearly breakdown

   Study the aggregation output carefully. This is your map of the data.

4. **Determine filters** based on the user's request and the aggregation results.

   Examples of how to translate user intent to filters:
   - "incidents for year 2024" → `-f year=2024`
   - "AI events in India" → `-f event_theme=Artificial Intelligence -f country=India`
   - "large events over 500 attendees" → `-f event_count>=500`
   - "events in 2022 and 2023" → `-f year=2022,2023`

   If the user's request is broad (e.g., "analyze this index"), do NOT filter — analyze everything.

   If the user mentions a value that doesn't exist in the aggregations (e.g., year=2024
   but aggs show only 2021-2023), inform the user and suggest alternatives based on
   what the aggregations show.

5. **Count the scoped documents**:
   ```bash
   opensearch-cli count --index <index_name> -f <filter1> -f <filter2>
   ```
   If count is 0, tell the user no matching documents were found and show the
   aggregation results so they can refine their query.

6. **If filters are applied, run scoped aggregations** to give yourself context
   about the filtered subset:
   ```bash
   opensearch-cli aggs --index <index_name> -f <filter1> -f <filter2>
   ```
   This tells you the distribution within the filtered data. Use these insights
   during your batch analysis.

### Phase 2: Parallel Batch Processing

Always delegate batch reading and analysis to **batch-processor** sub-agents.
This keeps the main agent's context clean regardless of document count.

7. **Calculate dynamic batch sizing**. The total number of batches is hard-capped
   at **4** to keep fan-out predictable. Compute:

   ```
   MAX_BATCHES    = 4
   MIN_BATCH_SIZE = 10
   batch_size     = max(MIN_BATCH_SIZE, ceil(count / MAX_BATCHES))
   num_batches    = ceil(count / batch_size)      # always ≤ 4
   ```

   Worked examples:

   | Scoped count | batch_size | num_batches |
   |--------------|------------|-------------|
   | 6            | 10         | 1           |
   | 25           | 10         | 3           |
   | 80           | 20         | 4           |
   | 200          | 50         | 4           |
   | 410          | 103        | 4           |
   | 5,000        | 1,250      | 4           |

   For very large scoped counts the per-batch document count grows — that is the
   intended trade-off of capping fan-out at 4.

8. **Build the filter string** for the CLI command. For example, if filters are
   `year=2024` and `country=India`, the filter string is `-f year=2024 -f country=India`.

9. **Fan out all batches in a single response**. Because `num_batches ≤ 4`, emit
   all `delegate_to_agent` calls **in the same response** (this is critical — the
   harness runs tool calls from the same response in parallel). No multi-round
   logic is needed.

   For each batch index `i` in `0 .. num_batches - 1`:
   - `offset = i * batch_size`
   - The last batch may return fewer documents than `batch_size` — that is fine.

   ```
   delegate_to_agent: batch-processor
   task: "Execute: `opensearch-cli query --index <index_name> <filter_string> --batch-size <batch_size> --offset <offset>`
   Analyze the output for: key themes and patterns, notable data points and outliers, common value distributions across fields, data quality issues.
   Write structured findings to: research/<index_name>/batches/batch_<NNN>.md
   Include in the file: batch number (<NNN>), document range (offset=<offset>, requested size=<batch_size>; the final batch may return fewer), filters applied, document count actually returned, key findings.
   IMPORTANT: Keep the batch file under ~400 words. Use compact bullets, not prose. Lead with numbers (counts, top values) rather than narrative."
   ```
   Where `<NNN>` is zero-padded (001, 002, 003, 004).

   **IMPORTANT**: Use relative paths (no leading `/`) in the task description.
   The sub-agent's write_file resolves relative paths to the correct workspace.

### Phase 3: Verify Batch Files

10. **Count the batch files on disk**:
    ```
    ls: research/<index_name>/batches/
    ```
    Verify the count matches the expected `num_batches`. If a batch file is
    missing, re-delegate that specific batch before proceeding.

### Phase 4: Final Report (Single Summarization Pass)

Because `num_batches ≤ 4`, no multi-round map-reduce is needed. A single
**summarizer** call consolidates all batch files directly into the final report.

11. **Delegate to summarizer** with the full list of batch file paths:
    ```
    delegate_to_agent: summarizer
    task: "Read these files: research/<index_name>/batches/batch_001.md, research/<index_name>/batches/batch_002.md, ... (list every batch file explicitly)
    Summarization query: Produce a concise final research report with these sections only:
    - Executive summary (2-3 sentences)
    - Scope: <index_name>, scoped count: <count>, filters: <filters>, batches: <num_batches> × <batch_size>
    - Key findings (4-7 bullets, organized by theme — merge common patterns across batches)
    - Statistical highlights (top values, counts, distributions — use a small table if helpful)
    Write to: research/<index_name>/final_report.md
    IMPORTANT: Use bullets, not paragraphs. Total length 600-900 words. Use relative paths (no leading /) for all file operations."
    ```

    If `num_batches == 1`, still delegate to summarizer with that single batch
    file — it normalizes the batch findings into the final report structure.

### Phase 5: Visual Report

12. **Delegate to report-generator** to create an interactive slide-deck report
    with charts and visualizations from the research artifacts:
    ```
    delegate_to_agent: report-generator
    task: "Source directory: research/<index_name>/
    Focus: <user's original query/focus>
    Primary data: research/<index_name>/final_report.md
    Output: research/<index_name>/report.md"
    ```
    The report-generator reads final_report.md first, extracts real data,
    and writes a `<!-- slides -->` presentation with charts to report.md.

13. **Notify the user** that research is complete. Include:
    - The index name and filters applied
    - Total documents analyzed (scoped count, not full index count)
    - `batch_size` and `num_batches` used (all ran in parallel)
    - Path to the final report (`final_report.md`)
    - Path to the visual report (`report.md`) — viewable as an interactive
      presentation in the canvas panel with PPTX export
    - A brief (3-5 bullet) highlight of the most important findings

## Output Structure

```
research/<index_name>/
  batches/
    batch_001.md
    batch_002.md
    ...                       # up to batch_004.md
  final_report.md           # text report (Research workflow)
  comparison_report.md      # text report (Comparison workflow)
  report.md                 # visual slide-deck with charts (both workflows)
```

## Notes

- Always use `opensearch-cli` for OpenSearch operations — do not use curl or other tools.
- Always run `aggs` before fetching — it gives you context that makes analysis better.
- Always use `query` with filters when the user specifies criteria — do NOT use `fetch` and filter client-side.
- Use `count` with the same filters before batching to know exactly how many docs to expect.
- **ALWAYS use relative paths** (no leading `/`) for write_file, read_file, ls, glob. This applies to you AND to all sub-agent task descriptions.
- **Parallel sub-agents**: Always emit multiple `delegate_to_agent` calls in the **same response** to run them in parallel. Do NOT emit them one at a time across separate responses.
- **Max batches**: hard cap of 4 batches per research run. `batch_size` is computed as `max(10, ceil(count / 4))` so all batches fan out in one parallel response.
- Each batch analysis should be substantive (not just restating raw data).
- The summarizer reduces all batch files in a single pass — focus its task on synthesis (patterns across batches), not concatenation.
- If `opensearch-cli` returns an error, report it to the user and stop.
- If the scoped count is 0, show the aggregation results and suggest alternative filters.
- For comparisons, prefer the **Comparison Workflow** (aggregation-driven, 2-3 tool calls) over running the full Research Workflow twice. Only use the Research Workflow for comparisons when the user explicitly asks for document-level deep-dive on both sides.
- Comparison mode can compare more than 2 groups (e.g., "compare India vs USA vs Japan") — run one scoped `aggs` per group.

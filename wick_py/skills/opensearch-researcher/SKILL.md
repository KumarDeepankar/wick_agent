---
name: opensearch-researcher
description: >
  Research, analyze, and compare documents stored in an OpenSearch index. Uses
  aggregations to understand data distribution, applies filters to scope queries
  precisely, supports comparison mode for side-by-side analysis (e.g. 2023 vs 2024),
  batches large result sets (50 docs per batch), processes each batch with LLM
  analysis, writes findings to disk, then progressively summarizes batch files
  (10 at a time) until a single final report remains. Use this skill when the
  user asks to research, analyze, compare, or summarize data in an OpenSearch index.
icon: search
sample-prompts:
  - Research the events_analytics_v4 index
  - Summarize all incidents for year 2024
  - Analyze AI events in India from events_analytics_v4
  - Compare events of 2023 vs 2022
metadata:
  author: wick-agent
  version: "3.0"
allowed-tools:
  - read_file
  - write_file
  - execute
  - ls
  - glob
---

# OpenSearch Researcher Skill

This skill orchestrates a multi-phase research pipeline over an OpenSearch index.
It uses aggregations to understand the data, applies filters to scope the query,
reads documents in batches, analyzes each batch, writes findings to disk, then
progressively reduces the findings into a single final report.

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

Create a working directory and write the report:
```bash
mkdir -p /workspace/research/<index_name>
```

Write the comparison report:
```
write_file: /workspace/research/<index_name>/comparison_report.md
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

### Step 5: Notify user

Tell the user the comparison is complete. Include:
- Groups compared and their document counts
- Path to the report
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

7. **Create a working directory** for this research:
   ```bash
   mkdir -p /workspace/research/<index_name>/batches
   mkdir -p /workspace/research/<index_name>/summaries
   ```

### Phase 2: Batched Reading & Analysis

If the document count is **100 or fewer**, fetch all in a single batch and
skip to Phase 3 with one batch file.

If the document count is **more than 100**, batch the reads at **50 documents per batch**:

8. **Calculate batches**: `total_batches = ceil(count / 50)`

9. **For each batch** (i = 0, 1, 2, ...):

   a. Fetch the batch (use `query` with filters, not `fetch`):
      ```bash
      opensearch-cli query --index <index_name> -f <filter1> -f <filter2> --batch-size 50 --offset <i * 50>
      ```
      The `query` command returns `total` (total matching docs) and `fetched` (docs in this batch).
      Use `total` to verify your batch count is correct.

   b. Analyze the batch output. For each batch, identify:
      - Key themes and patterns in the documents
      - Notable data points, outliers, or anomalies
      - Common values and distributions across fields
      - Relationships between fields
      - Any data quality issues (missing fields, duplicates)
      - How this batch relates to the aggregation context from Phase 1

   c. Write your findings to a batch file:
      ```
      write_file: /workspace/research/<index_name>/batches/batch_<NNN>.md
      ```
      Where `<NNN>` is zero-padded (001, 002, 003, ...).

      Each batch file MUST contain:
      - Batch number and document range (e.g., "Batch 1: documents 0-49")
      - Filters applied
      - Number of documents in this batch
      - Key findings and patterns
      - Notable individual documents (if any)
      - Field-level observations

   **Important:** Process each batch fully before moving to the next. Do NOT
   fetch all batches first and analyze later.

### Phase 3: Verify Batch Files

10. **Count the batch files on disk**:
    ```bash
    ls /workspace/research/<index_name>/batches/
    ```
    Verify the count matches the expected `total_batches`.

### Phase 4: Progressive Summarization (Map-Reduce)

Now reduce the batch files into a single final report by reading **10 files at a time**.

11. **Set up reduction loop**:
    - `input_dir` = `/workspace/research/<index_name>/batches/`
    - `output_dir` = `/workspace/research/<index_name>/summaries/`
    - `round` = 1

12. **List files** in `input_dir` and count them.

13. **If only 1 file remains**: This is the final report. Skip to Phase 5.

14. **If more than 1 file**: Process in groups of 10:

    a. Read up to 10 files from `input_dir` using `read_file`.

    b. Synthesize a summary that:
       - Merges common themes across the batch findings
       - Identifies the most significant patterns across all batches in this group
       - Highlights key statistics (counts, distributions, top values)
       - Notes contradictions or variations between batches
       - Preserves important specific findings
       - References the aggregation context from Phase 1

    c. Write the summary to `output_dir`:
       ```
       write_file: /workspace/research/<index_name>/summaries/round<R>_summary_<NNN>.md
       ```
       Where `<R>` is the round number and `<NNN>` is zero-padded.

    d. Repeat for the next group of 10 files until all files in `input_dir` are processed.

15. **Prepare next round**:
    - Set `input_dir` = current `output_dir`
    - Set `output_dir` = `/workspace/research/<index_name>/summaries/round<R+1>/`
    - Create the new output directory
    - Increment `round`
    - Go back to step 12.

### Phase 5: Final Report

16. **Read the single remaining file** — this is the consolidated research.

17. **Write the final report**:
    ```
    write_file: /workspace/research/<index_name>/final_report.md
    ```
    The final report should include:
    - Executive summary (2-3 sentences)
    - Index overview (name, total documents, schema)
    - Filters applied and scoped document count
    - Aggregation context (data distribution highlights from Phase 1)
    - Key findings organized by theme
    - Data quality assessment
    - Statistical highlights
    - Recommendations or areas for deeper investigation

18. **Notify the user** that research is complete. Include:
    - The index name and filters applied
    - Total documents analyzed (scoped count, not full index count)
    - Number of batches processed
    - Number of summarization rounds performed
    - Path to the final report
    - A brief (3-5 bullet) highlight of the most important findings

## Output Structure

```
/workspace/research/<index_name>/
  batches/
    batch_001.md
    batch_002.md
    ...
  summaries/
    round1_summary_001.md
    round1_summary_002.md
    ...
    round2/
      round2_summary_001.md
      ...
  final_report.md
```

## Notes

- Always use `opensearch-cli` for OpenSearch operations — do not use curl or other tools.
- Always run `aggs` before fetching — it gives you context that makes analysis better.
- Always use `query` with filters when the user specifies criteria — do NOT use `fetch` and filter client-side.
- Use `count` with the same filters before batching to know exactly how many docs to expect.
- Process batches sequentially — do not skip ahead.
- Each batch analysis should be substantive (not just restating raw data).
- During summarization, focus on synthesis — find patterns across batches, not just concatenation.
- If `opensearch-cli` returns an error, report it to the user and stop.
- If the scoped count is 0, show the aggregation results and suggest alternative filters.
- For comparisons, prefer the **Comparison Workflow** (aggregation-driven, 2-3 tool calls) over running the full Research Workflow twice. Only use the Research Workflow for comparisons when the user explicitly asks for document-level deep-dive on both sides.
- Comparison mode can compare more than 2 groups (e.g., "compare India vs USA vs Japan") — run one scoped `aggs` per group.

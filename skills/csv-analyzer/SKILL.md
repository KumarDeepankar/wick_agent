---
name: csv-analyzer
description: >
  Analyze CSV files by running the bundled analyze.py script. Computes
  descriptive statistics, detects data quality issues (missing values,
  duplicates), and produces a formatted report. Use this skill when the
  user asks to analyze a CSV, compute statistics on tabular data, profile
  a dataset, or check data quality.
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - read_file
  - write_file
  - execute
  - ls
---

# CSV Analyzer Skill

This skill includes **`analyze.py`** — a standalone Python script that
reads a CSV file and produces a full statistical report.

## Bundled Script

**File:** `/skills/csv-analyzer/analyze.py`

### Usage

```bash
python /skills/csv-analyzer/analyze.py <csv_path> [--output <path>] [--format markdown|json]
```

### Arguments

| Argument | Required | Description |
|----------|----------|-------------|
| `csv_path` | Yes | Path to the CSV file to analyze |
| `--output`, `-o` | No | Write report to file (default: stdout) |
| `--format`, `-f` | No | `markdown` (default) or `json` |

### What it computes

**Per-column statistics:**
- Numeric columns: count, mean, median, std dev, min, max, P25, P75, sum
- Categorical columns: unique count, mode, top-5 values with percentages

**Data quality:**
- Missing values per column (count and percentage)
- Overall completeness percentage
- Duplicate row detection

**Output includes:**
- Overview section (row count, column count, completeness)
- Per-column breakdown with type detection (numeric vs categorical)
- Sample rows table (first 5 rows)

## Workflow

Follow these steps when the user asks to analyze a CSV file:

1. **Locate the CSV**: Use `ls` to find the CSV file, or ask the user for the path.

2. **Run the script**: Execute the analyzer:
   ```bash
   python /skills/csv-analyzer/analyze.py /path/to/data.csv --format markdown
   ```
   For JSON output (useful for further processing):
   ```bash
   python /skills/csv-analyzer/analyze.py /path/to/data.csv --format json
   ```

3. **Save the report**: Write to a file if the user wants to keep it:
   ```bash
   python /skills/csv-analyzer/analyze.py /path/to/data.csv -o /workspace/analysis.md
   ```

4. **Interpret the results**: After running the script, read the output and provide:
   - A plain-language summary of key findings
   - Notable data quality issues
   - Interesting patterns or anomalies in the statistics
   - Suggestions for follow-up analysis

## Example

User: "Analyze the sales data in sales.csv"

```bash
python /skills/csv-analyzer/analyze.py /workspace/sales.csv --format markdown -o /workspace/sales_analysis.md
```

Then read the report, summarize findings, and highlight:
- Which columns have missing data
- Statistical outliers (values far from mean)
- Dominant categories in categorical columns
- Total/average for key numeric columns

## Notes

- The script uses only Python stdlib — no external dependencies needed.
- It handles UTF-8 and UTF-8-BOM encoded files.
- Maximum recommended file size: ~100 MB (for reasonable execution time).
- The script auto-detects column types (numeric vs categorical) based on value parsing.

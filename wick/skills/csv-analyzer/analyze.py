#!/usr/bin/env python3
"""CSV Analyzer — standalone script invoked by the deep agent.

Usage:
    python analyze.py <csv_path> [--output <output_path>] [--format markdown|json]

Reads a CSV file, computes descriptive statistics, detects data quality
issues, and writes a structured report.

Requires only Python stdlib + csv module (no pandas needed).
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import math
import sys
from collections import Counter
from pathlib import Path


# ── Helpers ──────────────────────────────────────────────────────────────


def _is_numeric(value: str) -> bool:
    try:
        float(value)
        return True
    except (ValueError, TypeError):
        return False


def _to_float(value: str) -> float | None:
    try:
        return float(value)
    except (ValueError, TypeError):
        return None


def _mean(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def _median(values: list[float]) -> float:
    if not values:
        return 0.0
    s = sorted(values)
    n = len(s)
    mid = n // 2
    if n % 2 == 0:
        return (s[mid - 1] + s[mid]) / 2
    return s[mid]


def _stdev(values: list[float]) -> float:
    if len(values) < 2:
        return 0.0
    m = _mean(values)
    variance = sum((x - m) ** 2 for x in values) / (len(values) - 1)
    return math.sqrt(variance)


def _percentile(values: list[float], p: float) -> float:
    if not values:
        return 0.0
    s = sorted(values)
    k = (len(s) - 1) * (p / 100)
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return s[int(k)]
    return s[f] * (c - k) + s[c] * (k - f)


def _mode(values: list) -> str:
    if not values:
        return "N/A"
    counter = Counter(values)
    most_common = counter.most_common(1)
    val, count = most_common[0]
    if count == 1:
        return "No mode (all unique)"
    return f"{val} (count: {count})"


# ── Core analysis ────────────────────────────────────────────────────────


def analyze_csv(csv_path: str) -> dict:
    """Read a CSV and produce a full analysis report dict."""

    path = Path(csv_path)
    if not path.exists():
        return {"error": f"File not found: {csv_path}"}

    with open(path, newline="", encoding="utf-8-sig") as f:
        reader = csv.DictReader(f)
        headers = reader.fieldnames or []
        rows = list(reader)

    total_rows = len(rows)
    total_cols = len(headers)

    if total_rows == 0:
        return {
            "file": str(path.name),
            "total_rows": 0,
            "total_columns": total_cols,
            "columns": headers,
            "error": "CSV file is empty (no data rows).",
        }

    # Per-column analysis
    column_stats = {}
    data_quality = {"missing_cells": 0, "total_cells": total_rows * total_cols}

    for col in headers:
        values = [row.get(col, "") for row in rows]
        non_empty = [v for v in values if v.strip()]
        missing = total_rows - len(non_empty)
        data_quality["missing_cells"] += missing

        numeric_values = [_to_float(v) for v in non_empty if _is_numeric(v)]
        numeric_values = [v for v in numeric_values if v is not None]
        is_numeric_col = len(numeric_values) > len(non_empty) * 0.5

        stats: dict = {
            "total": total_rows,
            "non_null": len(non_empty),
            "missing": missing,
            "missing_pct": round(missing / total_rows * 100, 1),
            "unique": len(set(non_empty)),
        }

        if is_numeric_col and numeric_values:
            stats["type"] = "numeric"
            stats["mean"] = round(_mean(numeric_values), 4)
            stats["median"] = round(_median(numeric_values), 4)
            stats["std_dev"] = round(_stdev(numeric_values), 4)
            stats["min"] = round(min(numeric_values), 4)
            stats["max"] = round(max(numeric_values), 4)
            stats["p25"] = round(_percentile(numeric_values, 25), 4)
            stats["p75"] = round(_percentile(numeric_values, 75), 4)
            stats["sum"] = round(sum(numeric_values), 4)
        else:
            stats["type"] = "categorical"
            top_values = Counter(non_empty).most_common(5)
            stats["top_values"] = [
                {"value": val, "count": cnt, "pct": round(cnt / len(non_empty) * 100, 1)}
                for val, cnt in top_values
            ]
            stats["mode"] = _mode(non_empty)

        column_stats[col] = stats

    # Duplicate rows
    row_tuples = [tuple(row.get(h, "") for h in headers) for row in rows]
    duplicate_count = total_rows - len(set(row_tuples))

    # Sample rows (first 5)
    sample = rows[:5]

    report = {
        "file": str(path.name),
        "total_rows": total_rows,
        "total_columns": total_cols,
        "columns": headers,
        "column_stats": column_stats,
        "data_quality": {
            "missing_cells": data_quality["missing_cells"],
            "total_cells": data_quality["total_cells"],
            "completeness_pct": round(
                (1 - data_quality["missing_cells"] / data_quality["total_cells"]) * 100, 1
            ),
            "duplicate_rows": duplicate_count,
        },
        "sample_rows": sample,
    }
    return report


# ── Output formatters ────────────────────────────────────────────────────


def to_markdown(report: dict) -> str:
    """Convert an analysis report dict to a markdown string."""
    if "error" in report and report.get("total_rows", 0) == 0:
        return f"# CSV Analysis: {report.get('file', 'unknown')}\n\n**Error:** {report['error']}\n"

    lines = []
    lines.append(f"# CSV Analysis: {report['file']}")
    lines.append("")
    lines.append("## Overview")
    lines.append(f"- **Rows:** {report['total_rows']:,}")
    lines.append(f"- **Columns:** {report['total_columns']}")
    dq = report["data_quality"]
    lines.append(f"- **Completeness:** {dq['completeness_pct']}%")
    lines.append(f"- **Missing cells:** {dq['missing_cells']:,} / {dq['total_cells']:,}")
    lines.append(f"- **Duplicate rows:** {dq['duplicate_rows']:,}")
    lines.append("")

    lines.append("## Column Statistics")
    lines.append("")
    for col, stats in report["column_stats"].items():
        lines.append(f"### `{col}` ({stats['type']})")
        lines.append(f"- Non-null: {stats['non_null']:,} | Missing: {stats['missing']} ({stats['missing_pct']}%)")
        lines.append(f"- Unique values: {stats['unique']:,}")

        if stats["type"] == "numeric":
            lines.append(f"- Mean: {stats['mean']} | Median: {stats['median']} | Std Dev: {stats['std_dev']}")
            lines.append(f"- Min: {stats['min']} | P25: {stats['p25']} | P75: {stats['p75']} | Max: {stats['max']}")
            lines.append(f"- Sum: {stats['sum']}")
        else:
            lines.append(f"- Mode: {stats['mode']}")
            if stats.get("top_values"):
                lines.append("- Top values:")
                for tv in stats["top_values"]:
                    lines.append(f"  - `{tv['value']}`: {tv['count']} ({tv['pct']}%)")
        lines.append("")

    # Sample rows as table
    lines.append("## Sample Rows (first 5)")
    lines.append("")
    cols = report["columns"]
    lines.append("| " + " | ".join(cols) + " |")
    lines.append("| " + " | ".join(["---"] * len(cols)) + " |")
    for row in report.get("sample_rows", []):
        lines.append("| " + " | ".join(str(row.get(c, "")) for c in cols) + " |")
    lines.append("")

    return "\n".join(lines)


def to_json(report: dict) -> str:
    return json.dumps(report, indent=2, default=str)


# ── CLI ──────────────────────────────────────────────────────────────────


def main():
    parser = argparse.ArgumentParser(description="Analyze a CSV file and produce a report.")
    parser.add_argument("csv_path", help="Path to the CSV file")
    parser.add_argument("--output", "-o", help="Output file path (default: stdout)")
    parser.add_argument(
        "--format", "-f",
        choices=["markdown", "json"],
        default="markdown",
        help="Output format (default: markdown)",
    )
    args = parser.parse_args()

    report = analyze_csv(args.csv_path)

    if "error" in report and "column_stats" not in report:
        print(f"Error: {report['error']}", file=sys.stderr)
        sys.exit(1)

    if args.format == "json":
        output = to_json(report)
    else:
        output = to_markdown(report)

    if args.output:
        Path(args.output).write_text(output, encoding="utf-8")
        print(f"Report written to {args.output}", file=sys.stderr)
    else:
        print(output)


if __name__ == "__main__":
    main()

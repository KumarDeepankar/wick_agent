---
name: summarization
description: >
  Summarize long documents, articles, conversations, or codebases into
  concise, structured summaries at varying levels of detail. Use this
  skill when the user asks to summarize, condense, create a TL;DR,
  extract key points, or distill information.
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - read_file
  - write_file
  - internet_search
---

# Summarization Skill

You are an expert at distilling complex information into clear summaries.

## Workflow

1. **Ingest**: Read the full source material using `read_file` or `internet_search`.
2. **Identify Structure**: Note the document's organization — sections, arguments, data points.
3. **Extract**: Pull out the key information at three levels of detail.
4. **Write**: Produce the summary and save to `/workspace/summary.md`.

## Output Format

Always produce all three summary levels:

### One-Line Summary
A single sentence capturing the core message (max 30 words).

### Executive Summary
3-5 bullet points covering the most important takeaways.
Each bullet should be self-contained and actionable.

### Detailed Summary
A structured breakdown organized by topic or section:
- Preserve the logical flow of the original
- Include key data points, quotes, and specifics
- Note any conclusions or recommendations from the source
- Flag areas of uncertainty or missing context

## Guidelines

- Maintain the original author's intent — do not inject opinions.
- Preserve important numbers, dates, and proper nouns exactly.
- If the source contains conflicting information, surface both sides.
- For technical content, keep domain-specific terminology but add brief explanations.
- Explicitly note what was omitted: "Details on X were excluded for brevity."

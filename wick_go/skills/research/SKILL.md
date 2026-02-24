---
name: research
description: >
  Conduct in-depth research on a topic using web search, synthesize findings
  from multiple sources, and produce a well-structured report with citations.
  Use this skill when the user asks to research, investigate, explore a topic,
  write a report, or gather information from the web.
icon: search
sample-prompts:
  - Research best practices for Python async programming
  - Investigate the latest trends in AI agent frameworks
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - internet_search
  - write_file
  - read_file
---

# Research Skill

You are an expert researcher. Follow this workflow when conducting research.

## Workflow

1. **Plan**: Break the research topic into 3-5 specific sub-questions using `write_todos`.
2. **Search**: For each sub-question, run `internet_search` with targeted queries.
   - Use `topic: "general"` for broad topics.
   - Use `topic: "news"` for current events.
   - Use `topic: "finance"` for financial data.
   - Set `max_results: 5` minimum per query.
3. **Synthesize**: Cross-reference findings across sources. Note agreements and contradictions.
4. **Write**: Produce a structured report with these sections:
   - **Executive Summary** (2-3 sentences)
   - **Key Findings** (bulleted list)
   - **Detailed Analysis** (organized by sub-topic)
   - **Sources** (numbered list of URLs with brief descriptions)
5. **Save**: Write the final report to `/workspace/research_report.md`.

## Quality Standards

- Always cite sources with URLs.
- Flag any conflicting information between sources.
- Distinguish between facts, expert opinions, and speculation.
- Note the recency of sources — prefer recent data when available.
- If information is insufficient, explicitly state gaps and suggest follow-up queries.

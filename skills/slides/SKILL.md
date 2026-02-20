---
name: slides
description: >
  Create presentation slide decks as markdown files with slide separators.
  The system renders a live preview in the canvas panel and can export to
  editable PowerPoint (.pptx) files.
icon: slides
sample-prompts:
  - Create a 5-slide presentation about Python best practices
  - Make a slide deck on AI trends in 2025
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - write_file
  - edit_file
  - read_file
---

# Slides Skill

Create beautiful slide presentations by writing a markdown file. The system
automatically detects slide content and renders a live preview with navigation.
Users can export to `.pptx` (PowerPoint) with one click.

## Markdown Format

Write a `.md` file using `---` on its own line to separate slides:

```markdown
# Presentation Title

Optional subtitle text

---

## Slide Heading

- First bullet point
- Second bullet point
- Third bullet point

---

## Another Slide

1. Numbered item one
2. Numbered item two
3. Numbered item three

---

## Code Example

```python
def hello():
    print("Hello, World!")
```

---

## Data Table

| Metric | Value | Change |
|--------|-------|--------|
| Users  | 1.2M  | +15%   |
| Revenue| $4.8M | +22%   |

---

## Key Quote

> "The best way to predict the future is to create it."

Final thoughts and conclusion text.
```

## Formatting Reference

| Markdown | Rendered As |
|----------|-------------|
| `# Title` | Large title (use for first slide / section headers) |
| `## Heading` | Slide heading |
| `### Subheading` | Smaller heading |
| `- bullet` or `* bullet` | Bulleted list |
| `1. item` | Numbered list |
| `` ```code``` `` | Code block (monospace) |
| `> quote` | Blockquote (italic) |
| `**bold**` | Bold text |
| `*italic*` | Italic text |
| `| table |` | Data table |
| Plain text | Body paragraph |
| `---` | Slide separator (must be on its own line) |

## Charts

Embed data charts directly in slides using ` ```chart ` fenced code blocks. Charts
render as interactive SVGs in the live preview and as **native editable charts** in
the exported `.pptx` file.

### Chart DSL Format

````markdown
```chart
type: bar
title: Revenue by Quarter
labels: [Q1, Q2, Q3, Q4]
data: [100, 150, 200, 180]
legend: true
showValues: true
xLabel: Quarter
yLabel: Revenue ($K)
colors: [#2563eb, #059669]
```
````

### Multi-Series Example

````markdown
```chart
type: bar
title: Year-over-Year Comparison
labels: [Q1, Q2, Q3, Q4]
series:
  - name: 2024
    data: [100, 150, 200, 180]
  - name: 2025
    data: [120, 180, 240, 210]
legend: true
legendPosition: bottom
```
````

### Chart Types

| Type | Description |
|------|-------------|
| `bar` | Vertical bar chart (grouped for multi-series) |
| `hbar` | Horizontal bar chart |
| `line` | Line chart with markers |
| `area` | Filled area chart |
| `pie` | Pie chart |
| `donut` | Donut chart (pie with inner cutout) |
| `stacked_bar` | Stacked vertical bar chart |

### Chart Properties

| Property | Type | Description |
|----------|------|-------------|
| `type` | string | Chart type (see table above). Default: `bar` |
| `title` | string | Chart title displayed above the chart |
| `labels` | array | Category labels `[Q1, Q2, Q3, Q4]` |
| `data` | array | Data values for single-series `[100, 150, 200]` |
| `series` | list | Multi-series data (each with `name` and `data`) |
| `legend` | boolean | Show legend. Default: `false` |
| `legendPosition` | string | Legend position: `top`, `bottom`, `right`. Default: `bottom` |
| `showValues` | boolean | Show data value labels. Default: `false` |
| `xLabel` | string | X-axis label |
| `yLabel` | string | Y-axis label |
| `colors` | array | Custom hex colors `[#2563eb, #059669]` |

### Cross-Chart Filtering (Preview Only)

When a slide contains multiple charts sharing the same labels, clicking a data
point in one chart highlights that category across ALL charts on the slide.
Click the same data point again to clear the filter. This is a live-preview
feature — exported PPTX files contain standard static charts.

## Guidelines for Good Slides

1. **First slide**: Use only `# Title` and an optional subtitle line — this
   becomes the cover/title slide.

2. **Keep slides focused**: Each slide should cover one idea. Aim for 3-5
   bullet points maximum per slide.

3. **Use headings**: Start each slide with `## Heading` so the exported PPTX
   has proper slide titles.

4. **Mix content types**: Alternate between bullets, code blocks, tables, and
   quotes to keep the presentation engaging.

5. **Typical deck length**: 5-10 slides for a focused presentation, up to 20
   for comprehensive topics.

## Workflow

1. **Create the deck**: Write the markdown file to the workspace:
   ```
   write_file("/workspace/presentation.md", content)
   ```

2. **Preview**: The canvas panel automatically detects the slide format and
   shows a live preview with slide navigation.

3. **Iterate**: Users can ask to modify specific slides. Use `edit_file` to
   update individual slides without rewriting the entire file.

4. **Export**: Users click "Export PPTX" in the canvas panel to download an
   editable PowerPoint file.

## Example

User: "Create a 5-slide presentation about Python best practices"

Write a file like `/workspace/python-best-practices.md` with:
- Slide 1: Title slide (`# Python Best Practices`)
- Slide 2: Code style (PEP 8, type hints)
- Slide 3: Error handling (try/except patterns)
- Slide 4: Testing (pytest, coverage)
- Slide 5: Summary / key takeaways

## Notes

- The file must have at least 2 `---` separators to be auto-detected as slides.
- The first non-empty line should start with `#` for proper detection.
- The `.pptx` export produces editable text — not images — so users can
  customize in PowerPoint or Google Slides after downloading.
- Use `/workspace/` as the base path for slide files.

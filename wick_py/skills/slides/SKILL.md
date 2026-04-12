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

**IMPORTANT**: Always start the file with `<!-- slides -->` on the very first line.
This marker tells the system to render the content as a slide deck, even if it
contains only one slide. Write a `.md` file using `---` on its own line to
separate slides:

```markdown
<!-- slides -->
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

## Themes

Pick a deck-wide visual theme by adding **`<!-- theme: name -->`** anywhere
in the file (typically right after the `<!-- slides -->` marker). The theme
controls slide background, title color, accent stripe, footer style, and the
default chart palette — so unstyled charts automatically match the slide
chrome. If you omit the directive, `corporate` is used.

| Theme | Style |
|-------|-------|
| `corporate` | White background, navy + teal accents, Georgia title — default, business reports |
| `editorial` | Warm cream background, serif throughout, rust + amber accents — long-form essays |
| `dark` | Near-black background, cyan + violet accents, sans-serif — technical decks, demos |
| `vibrant` | White background, magenta + orange accents — pitches, marketing |

```markdown
<!-- slides -->
<!-- theme: dark -->
# Q4 Engineering Review
```

When you set a theme, **omit the per-chart `colors:` field** unless you have
a specific reason to override — the theme palette is curated for harmony.

## Slide Layouts

Each slide can opt into a specific layout by adding **`<!-- layout: name -->`**
inside the slide block (before the `# Title`). Without a directive, the slide
uses the default `content` layout (title + bullets + optional charts).

| Layout | Use for | Notes |
|--------|---------|-------|
| `title` | Cover slide / hero | Centered title (54pt) + first body line as subtitle, accent stripe below. No charts. |
| `section` | Section dividers between major parts of the deck | Large left-aligned title, kicker text above (UPPER-CASED from first body line), vertical accent bar on the left. |
| `content` | Standard slide (default) | Title + bullets + optional charts at the bottom. Used when no directive is given. |
| `content_chart` | Chart-emphasized slide | Caption-sized body line, chart row takes ~70% of the slide. Use when the chart is the point. |
| `two_column` | Side-by-side comparison | Title across top, two text columns below. Use `:::col1` / `:::col2` fences (see below). |

### Title slide example

```markdown
<!-- layout: title -->
# Annual Report 2026

A look at the year ahead
```

### Section divider example

```markdown
<!-- layout: section -->
# Findings

Part Two
```

The `Part Two` line becomes a small uppercase kicker above the title.

### Two-column slide

Use **pandoc-style fenced divs** to separate the columns:

```markdown
<!-- layout: two_column -->
# Pros and Cons

:::col1
- Faster iteration
- Lower hosting cost
- Easier to debug
:::

:::col2
- Less mature ecosystem
- Smaller talent pool
- Migration risk
:::
```

If you forget the `<!-- layout: two_column -->` directive, the parser still
auto-detects the layout from the `:::col1`/`:::col2` fences.

### Content-chart (chart-emphasized) example

```markdown
<!-- layout: content_chart -->
# Revenue Growth

Year-over-year revenue across all product lines.

```chart
type: line
labels: [2021, 2022, 2023, 2024, 2025]
data: [12, 18, 25, 38, 52]
```
```

## Guidelines for Good Slides

1. **First slide**: Use `<!-- layout: title -->` with `# Title` and an optional
   subtitle line — this becomes the cover slide.

2. **Section dividers**: For decks of 8+ slides, insert
   `<!-- layout: section -->` slides between major parts to create visual
   chapter breaks.

3. **Keep slides focused**: Each slide should cover one idea. Aim for 3-5
   bullet points maximum per slide.

4. **Use headings**: Start each slide with `## Heading` so the exported PPTX
   has proper slide titles.

5. **Mix content types**: Alternate between bullets, code blocks, tables,
   charts, and two-column comparisons to keep the presentation engaging.

6. **Pick a theme once**: Set `<!-- theme: name -->` near the top of the file.
   Don't override per-chart colors unless you have a specific design reason.

7. **Typical deck length**: 5-10 slides for a focused presentation, up to 20
   for comprehensive topics.

## Workflow

1. **Create the deck**: Write the markdown file to the workspace. Always start
   the content with `<!-- slides -->` on the first line:
   ```
   write_file("presentation.md", "<!-- slides -->\n# Title\n\n...")
   ```

2. **Preview**: The canvas panel automatically detects the slide format and
   shows a live preview with slide navigation.

3. **Iterate**: Users can ask to modify specific slides. Use `edit_file` to
   update individual slides without rewriting the entire file.

4. **Export**: Users click "Export PPTX" in the canvas panel to download an
   editable PowerPoint file.

## Example

User: "Create a 5-slide presentation about Python best practices"

Write a file like `python-best-practices.md` starting with
`<!-- slides -->` and a theme directive on the next line, then:
- Slide 1: `<!-- layout: title -->` cover slide
- Slide 2: Code style (PEP 8, type hints) — default content layout
- Slide 3: Error handling (try/except patterns)
- Slide 4: Testing (pytest, coverage) — could use `two_column` for "good vs bad"
- Slide 5: Summary / key takeaways

```markdown
<!-- slides -->
<!-- theme: corporate -->

<!-- layout: title -->
# Python Best Practices

A 2026 field guide

---

## Code Style
...
```

## Notes

- Always include `<!-- slides -->` as the first line of the file. This ensures
  the system renders it as a slide deck regardless of how many slides there are.
- Without the marker, the system falls back to heuristic detection (requires
  at least one `---` separator and a `#` heading on the first line).
- The `.pptx` export produces editable text — not images — so users can
  customize in PowerPoint or Google Slides after downloading.
- Use relative paths for slide files (e.g. `presentation.md`, not `/workspace/presentation.md`).

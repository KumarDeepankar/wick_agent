---
name: slides
description: >
  Create presentation slide decks. Write slides as a markdown file with ---
  separators between slides. The system renders a live preview in the canvas
  panel and can export to editable PowerPoint (.pptx) files.
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

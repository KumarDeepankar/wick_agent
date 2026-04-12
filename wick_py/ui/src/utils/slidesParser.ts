// Slides parser — extracts theme + per-slide layout + column blocks from
// the markdown source. TS mirror of parseMarkdownSlides in
// wick_deep_agent/server/handlers/pptx.go. Keep the two in sync.

export type SlideLayout =
  | 'title'
  | 'section'
  | 'content'
  | 'content_chart'
  | 'two_column';

export interface ParsedSlide {
  /** Layout name; defaults to 'content' if no directive present. */
  layout: SlideLayout;
  /** Slide markdown with the <!-- layout --> directive removed. */
  markdown: string;
  /** Two-column slides: left column markdown (empty otherwise). */
  col1Markdown: string;
  /** Two-column slides: right column markdown. */
  col2Markdown: string;
  /** First-line H1 or H2 heading text, stripped of markdown markers. */
  title: string;
}

export interface ParsedDeck {
  theme: string;            // empty string if no directive (caller falls back)
  slides: ParsedSlide[];
}

const THEME_DIRECTIVE = /^\s*<!--\s*theme:\s*([a-zA-Z0-9_-]+)\s*-->\s*\n?/m;
const LAYOUT_DIRECTIVE = /^\s*<!--\s*layout:\s*([a-zA-Z0-9_-]+)\s*-->\s*\n?/m;
const SLIDES_MARKER = /^\s*<!--\s*slides\s*-->\s*\n?/m;

const KNOWN_LAYOUTS = new Set<SlideLayout>([
  'title',
  'section',
  'content',
  'content_chart',
  'two_column',
]);

function asLayout(name: string): SlideLayout {
  return KNOWN_LAYOUTS.has(name as SlideLayout) ? (name as SlideLayout) : 'content';
}

function extractTitle(md: string): string {
  for (const raw of md.split('\n')) {
    const line = raw.trim();
    if (line.startsWith('# ')) return line.slice(2).trim();
    if (line.startsWith('## ')) return line.slice(3).trim();
  }
  return '';
}

/**
 * Pull `:::col1 ... ::: ... :::col2 ... :::` blocks out of a slide's markdown
 * and return both the column contents and the markdown with those blocks
 * removed (so the remaining md can still feed marked.js for the title etc).
 */
function extractColumns(md: string): { col1: string; col2: string; rest: string } {
  const lines = md.split('\n');
  const col1: string[] = [];
  const col2: string[] = [];
  const rest: string[] = [];
  let target: 'col1' | 'col2' | 'rest' = 'rest';

  for (const raw of lines) {
    const t = raw.trim();
    if (t === ':::col1') {
      target = 'col1';
      continue;
    }
    if (t === ':::col2') {
      target = 'col2';
      continue;
    }
    if (t === ':::') {
      target = 'rest';
      continue;
    }
    if (target === 'col1') col1.push(raw);
    else if (target === 'col2') col2.push(raw);
    else rest.push(raw);
  }

  return {
    col1: col1.join('\n').trim(),
    col2: col2.join('\n').trim(),
    rest: rest.join('\n').trim(),
  };
}

/**
 * Set or replace the deck-wide `<!-- theme: name -->` directive in a slides
 * markdown string. Used by the canvas theme switcher to persist a theme
 * change back to the source file.
 *
 * Behavior:
 *  - If a theme directive already exists, its value is replaced.
 *  - If no theme directive exists but a `<!-- slides -->` marker does, the
 *    new directive is inserted on the line after the marker.
 *  - If neither marker exists, the new directive is prepended to the file.
 *  - Passing an empty/undefined name removes the directive entirely.
 */
export function setDeckTheme(content: string, name: string): string {
  // Remove any existing theme directive first.
  const stripped = content.replace(THEME_DIRECTIVE, '');
  if (!name) return stripped;

  const directive = `<!-- theme: ${name} -->`;
  const slidesMatch = stripped.match(SLIDES_MARKER);
  if (slidesMatch && slidesMatch.index !== undefined) {
    // Insert right after the slides marker line.
    const idx = slidesMatch.index + slidesMatch[0].length;
    return stripped.slice(0, idx) + directive + '\n' + stripped.slice(idx);
  }
  return directive + '\n' + stripped;
}

export function parseSlidesContent(content: string): ParsedDeck {
  // Extract deck-wide theme directive (first match wins).
  let theme = '';
  const themeMatch = content.match(THEME_DIRECTIVE);
  if (themeMatch) {
    theme = themeMatch[1] || '';
    content = content.replace(THEME_DIRECTIVE, '');
  }

  // Strip the <!-- slides --> marker so it doesn't show up in the first slide.
  content = content.replace(SLIDES_MARKER, '');

  const slides: ParsedSlide[] = content
    .split(/\n---\n/)
    .map((b) => b.trim())
    .filter((b) => b.length > 0)
    .map((block) => {
      let layout: SlideLayout = 'content';
      const layoutMatch = block.match(LAYOUT_DIRECTIVE);
      if (layoutMatch) {
        layout = asLayout(layoutMatch[1] || 'content');
        block = block.replace(LAYOUT_DIRECTIVE, '').trim();
      }

      // Pull column blocks if present. Even if the layout directive wasn't
      // set, the presence of :::col fences implies two_column (matches the
      // Go parser behavior).
      const { col1, col2, rest } = extractColumns(block);
      const hasColumns = col1.length > 0 || col2.length > 0;
      if (hasColumns && layout === 'content') {
        layout = 'two_column';
      }

      return {
        layout,
        markdown: hasColumns ? rest : block,
        col1Markdown: col1,
        col2Markdown: col2,
        title: extractTitle(block),
      };
    });

  return { theme, slides };
}

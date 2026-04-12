import { useState, useMemo, useEffect, useCallback, useRef } from 'react';
import { Marked } from 'marked';
import { renderChartSVG } from '../../utils/chartRenderer';
import { exportSlidesAsPptx, saveFileContent } from '../../api';
import { htmlToMarkdown } from '../../utils/htmlToMarkdown';
import { getDisplayName } from '../../utils/canvasUtils';
import { EditToolbar } from './EditToolbar';
import { parseSlidesContent, setDeckTheme, type ParsedSlide } from '../../utils/slidesParser';
import { resolveSlidesTheme, themeCssVars, SLIDES_THEMES, type SlidesTheme } from '../../utils/slidesTheme';

interface Props {
  content: string;
  fileName: string;
  filePath: string;
  onContentUpdate?: (filePath: string, content: string) => void;
}

/**
 * Apply cross-chart filter directly on the DOM. Walks any element with a
 * data-label attribute and dims those that don't match.
 */
function applyFilterToDOM(container: HTMLElement, label: string | null) {
  const elements = container.querySelectorAll<SVGElement | HTMLElement>('[data-label]');
  if (!label) {
    elements.forEach((el) => {
      el.style.opacity = '';
      el.style.stroke = '';
      el.style.strokeWidth = '';
    });
    return;
  }
  elements.forEach((el) => {
    const elLabel = el.getAttribute('data-label');
    if (elLabel === label) {
      el.style.opacity = '1';
      el.style.stroke = 'var(--text-primary)';
      el.style.strokeWidth = '2';
    } else {
      el.style.opacity = '0.15';
      el.style.stroke = '';
      el.style.strokeWidth = '';
    }
  });
}

/**
 * Build a Marked instance whose ```chart code blocks are pre-rendered to
 * SVG using the active deck theme's chart palette. Each call resets the
 * chart index counter so chart-N IDs stay deterministic per slide.
 */
function buildMarkedForTheme(theme: SlidesTheme): Marked {
  let chartIdx = 0;
  return new Marked({
    renderer: {
      code({ text, lang }: { text: string; lang?: string }) {
        if (lang === 'chart') {
          return renderChartSVG(text, chartIdx++, undefined, theme.chartColors);
        }
        return `<pre><code class="language-${lang || ''}">${text
          .replace(/</g, '&lt;')
          .replace(/>/g, '&gt;')}</code></pre>`;
      },
    },
  });
}

/**
 * SlideStage dispatches on slide.layout and renders the appropriate JSX
 * structure. Each layout has its own DOM shape so the CSS in App.css can
 * style them independently. Body markdown is rendered through marked once
 * per slide; the result is a string of HTML we drop into a div.
 */
function SlideStage({
  slide,
  theme,
  marked,
  refEl,
}: {
  slide: ParsedSlide;
  theme: SlidesTheme;
  marked: Marked;
  refEl?: React.RefObject<HTMLDivElement | null>;
}) {
  // Each layout gets its own marked render of the appropriate markdown chunk.
  const html = useMemo(
    () => marked.parse(slide.markdown, { async: false }) as string,
    [slide.markdown, marked],
  );
  const col1Html = useMemo(
    () => (slide.col1Markdown ? (marked.parse(slide.col1Markdown, { async: false }) as string) : ''),
    [slide.col1Markdown, marked],
  );
  const col2Html = useMemo(
    () => (slide.col2Markdown ? (marked.parse(slide.col2Markdown, { async: false }) as string) : ''),
    [slide.col2Markdown, marked],
  );

  // Strip the leading H1/H2 from the body html since the title is rendered
  // separately by some layouts. We let marked render the heading first to
  // pick up any inline formatting, then strip it from the html string.
  const bodyHtmlMinusTitle = useMemo(() => {
    if (!slide.title) return html;
    return html.replace(/^\s*<h[12][^>]*>[^<]*<\/h[12]>\s*/i, '');
  }, [html, slide.title]);

  const titleStyle: React.CSSProperties = {
    color: 'var(--slides-title)',
    fontFamily: 'var(--slides-title-font)',
  };

  switch (slide.layout) {
    case 'title': {
      // Centered hero: big title, subtitle from first paragraph, accent stripe.
      const subtitleMatch = bodyHtmlMinusTitle.match(/<p[^>]*>([\s\S]*?)<\/p>/i);
      const subtitleHtml = subtitleMatch ? subtitleMatch[1] : '';
      return (
        <div ref={refEl} className="slides-layout-title message-content" style={themeCssVars(theme)}>
          <div className="slides-title-stack">
            <h1 className="slides-title-big" style={titleStyle}>
              {slide.title}
            </h1>
            <div
              className="slides-title-stripe"
              style={{ background: `var(--slides-accent1)` }}
            />
            {subtitleHtml && (
              <div
                className="slides-title-subtitle"
                style={{ color: 'var(--slides-muted)' }}
                dangerouslySetInnerHTML={{ __html: subtitleHtml }}
              />
            )}
          </div>
        </div>
      );
    }

    case 'section': {
      // Left-aligned divider with kicker (uppercased first body line) + side bar.
      const kickerMatch = bodyHtmlMinusTitle.match(/<p[^>]*>([\s\S]*?)<\/p>/i);
      const kicker = kickerMatch && kickerMatch[1] ? kickerMatch[1].replace(/<[^>]+>/g, '') : '';
      return (
        <div ref={refEl} className="slides-layout-section message-content" style={themeCssVars(theme)}>
          <div className="slides-section-bar" style={{ background: `var(--slides-accent1)` }} />
          <div className="slides-section-text">
            {kicker && (
              <div className="slides-section-kicker" style={{ color: 'var(--slides-accent1)' }}>
                {kicker.toUpperCase()}
              </div>
            )}
            <h1 className="slides-section-title" style={titleStyle}>
              {slide.title}
            </h1>
          </div>
        </div>
      );
    }

    case 'two_column': {
      return (
        <div ref={refEl} className="slides-layout-two-column message-content" style={themeCssVars(theme)}>
          <h2 className="slides-section-title" style={titleStyle}>
            {slide.title}
          </h2>
          <div className="slides-two-column-grid">
            <div className="slides-two-column-cell" dangerouslySetInnerHTML={{ __html: col1Html }} />
            <div className="slides-two-column-cell" dangerouslySetInnerHTML={{ __html: col2Html }} />
          </div>
        </div>
      );
    }

    case 'content_chart': {
      return (
        <div ref={refEl} className="slides-layout-content-chart message-content" style={themeCssVars(theme)}>
          <h2 className="slides-content-title" style={titleStyle}>
            {slide.title}
          </h2>
          <div
            className="slides-content-chart-body"
            dangerouslySetInnerHTML={{ __html: bodyHtmlMinusTitle }}
          />
        </div>
      );
    }

    default: {
      // 'content' or unknown — render the slide as-is, with the accent stripe
      // under the title to mirror the slide master in the exported pptx.
      return (
        <div ref={refEl} className="slides-layout-content message-content" style={themeCssVars(theme)}>
          <h2 className="slides-content-title" style={titleStyle}>
            {slide.title}
          </h2>
          <div
            className="slides-content-stripe"
            style={{ background: `var(--slides-accent1)` }}
          />
          <div
            className="slides-content-body"
            dangerouslySetInnerHTML={{ __html: bodyHtmlMinusTitle }}
          />
        </div>
      );
    }
  }
}

export function SlidesViewer({ content, fileName, filePath, onContentUpdate }: Props) {
  // Parse the deck once per content change: extracts theme + per-slide
  // layout/columns. The result drives both rendering and the export button
  // (export goes through the Go server which re-parses the same string).
  const deck = useMemo(() => parseSlidesContent(content), [content]);
  const theme = useMemo(() => resolveSlidesTheme(deck.theme), [deck.theme]);

  const slides = deck.slides;

  const [currentSlide, setCurrentSlide] = useState(0);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [activeFilter, setActiveFilter] = useState<string | null>(null);

  // Edit mode state
  const [editMode, setEditMode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Theme switching is a persistent action (writes back to disk) so we
  // track it separately from edit-mode saves to disable the switcher
  // while a write is in flight.
  const [switchingTheme, setSwitchingTheme] = useState(false);
  const [themeError, setThemeError] = useState<string | null>(null);

  const slideRef = useRef<HTMLDivElement>(null);
  const editRef = useRef<HTMLDivElement>(null);
  // For two_column layout we render three editable surfaces — a title input
  // and two column contentEditables — so layout/column structure survives a
  // round-trip through edit mode. The title ref points at the <input>.
  const editTitleRef = useRef<HTMLInputElement>(null);
  const editCol1Ref = useRef<HTMLDivElement>(null);
  const editCol2Ref = useRef<HTMLDivElement>(null);

  // Marked instance is theme-scoped so chart svgs pick up the right palette.
  // Re-create per render to keep chart-N counters deterministic per slide.
  const marked = useMemo(() => buildMarkedForTheme(theme), [theme]);

  // Thumbnails: render each slide's main markdown as compact HTML.
  const thumbnailHtmls = useMemo(() => {
    return slides.map((s) => {
      const m = buildMarkedForTheme(theme);
      // For thumbnails we just render the full slide markdown (cols + body).
      const full = [s.markdown, s.col1Markdown, s.col2Markdown].filter(Boolean).join('\n\n');
      return m.parse(full, { async: false }) as string;
    });
  }, [slides, theme]);

  const goNext = useCallback(() => {
    setCurrentSlide((s) => Math.min(s + 1, slides.length - 1));
  }, [slides.length]);

  const goPrev = useCallback(() => {
    setCurrentSlide((s) => Math.max(s - 1, 0));
  }, []);

  const handleExport = useCallback(async () => {
    setExporting(true);
    setExportError(null);
    try {
      const blob = await exportSlidesAsPptx(filePath);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const base = fileName.replace(/\.[^.]+$/, '');
      a.href = url;
      a.download = `${base}.pptx`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Export failed';
      setExportError(msg);
      setTimeout(() => setExportError(null), 4000);
    } finally {
      setExporting(false);
    }
  }, [filePath, fileName]);

  // Scoped keyboard navigation
  const containerRef = useRef<HTMLDivElement>(null);
  const handleContainerKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (editMode) return;
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') { e.preventDefault(); goNext(); }
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') { e.preventDefault(); goPrev(); }
    },
    [goNext, goPrev, editMode],
  );

  // Reset slide index when slides count changes
  useEffect(() => {
    setCurrentSlide((s) => Math.min(s, Math.max(slides.length - 1, 0)));
  }, [slides.length]);

  // Reset filter when changing slides
  useEffect(() => {
    setActiveFilter(null);
  }, [currentSlide]);

  // Apply filter via DOM
  useEffect(() => {
    const el = slideRef.current;
    if (!el || editMode) return;
    applyFilterToDOM(el, activeFilter);
  }, [activeFilter, editMode]);

  // Cross-chart filtering click delegation
  useEffect(() => {
    const el = slideRef.current;
    if (!el || editMode) return;

    const handleClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      const clickable = target.closest('[data-label]') as HTMLElement | null;
      if (!clickable) return;
      const label = clickable.getAttribute('data-label');
      if (!label) return;
      setActiveFilter((prev) => (prev === label ? null : label));
    };

    el.addEventListener('click', handleClick);
    return () => el.removeEventListener('click', handleClick);
  }, [editMode]);

  // Edit mode populates either a single pane (default) or three panes
  // (two_column: title input + col1 + col2 contentEditables). Two-column
  // edits preserve the layout/col structure on save instead of flattening
  // them out. For non-two_column, the editable area shows the full slide
  // markdown rendered (title + body) so the user can edit everything.
  const isTwoCol = (slides[currentSlide]?.layout ?? '') === 'two_column';

  const singlePaneHtml = useMemo(() => {
    const s = slides[currentSlide];
    if (!s) return '';
    return marked.parse(s.markdown, { async: false }) as string;
  }, [slides, currentSlide, marked]);

  const col1Html = useMemo(() => {
    const s = slides[currentSlide];
    if (!s || !s.col1Markdown) return '';
    return marked.parse(s.col1Markdown, { async: false }) as string;
  }, [slides, currentSlide, marked]);

  const col2Html = useMemo(() => {
    const s = slides[currentSlide];
    if (!s || !s.col2Markdown) return '';
    return marked.parse(s.col2Markdown, { async: false }) as string;
  }, [slides, currentSlide, marked]);

  // Populate editable surfaces when edit mode opens (or when the slide changes
  // while editing).
  useEffect(() => {
    if (!editMode) return;
    if (isTwoCol) {
      const s = slides[currentSlide];
      if (editTitleRef.current && s) editTitleRef.current.value = s.title;
      if (editCol1Ref.current) editCol1Ref.current.innerHTML = col1Html;
      if (editCol2Ref.current) editCol2Ref.current.innerHTML = col2Html;
      editTitleRef.current?.focus();
    } else if (editRef.current) {
      editRef.current.innerHTML = singlePaneHtml;
      editRef.current.focus();
    }
  }, [editMode, isTwoCol, singlePaneHtml, col1Html, col2Html, currentSlide, slides]);

  // Edit mode handlers
  const enterEditMode = useCallback(() => {
    setSaveError(null);
    setEditMode(true);
  }, []);

  const cancelEdit = useCallback(() => {
    setEditMode(false);
    setSaveError(null);
  }, []);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const slide = slides[currentSlide];
      if (!slide) throw new Error('no slide to save');

      // Reconstruct the edited slide markdown, preserving the layout
      // directive and (for two_column) column fences. Skip the directive
      // when layout is empty or 'content' (the default) to avoid noise.
      const layoutPrefix =
        slide.layout && slide.layout !== 'content'
          ? `<!-- layout: ${slide.layout} -->\n`
          : '';

      let editedSlideMd: string;
      if (slide.layout === 'two_column') {
        const title = (editTitleRef.current?.value ?? '').trim();
        const col1Md = editCol1Ref.current
          ? htmlToMarkdown(editCol1Ref.current.innerHTML).trim()
          : '';
        const col2Md = editCol2Ref.current
          ? htmlToMarkdown(editCol2Ref.current.innerHTML).trim()
          : '';
        const titleLine = title ? `# ${title}\n\n` : '';
        editedSlideMd = `${layoutPrefix}${titleLine}:::col1\n${col1Md}\n:::\n\n:::col2\n${col2Md}\n:::`;
      } else {
        if (!editRef.current) throw new Error('edit area missing');
        const bodyMd = htmlToMarkdown(editRef.current.innerHTML).trim();
        editedSlideMd = `${layoutPrefix}${bodyMd}`;
      }

      // Splice the edited slide back into the deck markdown, preserving
      // the deck-level <!-- slides --> + <!-- theme --> directives.
      const stripped = content
        .replace(/^\s*<!--\s*slides\s*-->\s*\n?/m, '')
        .replace(/^\s*<!--\s*theme:\s*[a-zA-Z0-9_-]+\s*-->\s*\n?/m, '');
      const allRaw = stripped.split(/\n---\n/).map((b) => b.trim());
      allRaw[currentSlide] = editedSlideMd;

      const headerLines: string[] = [];
      if (content.match(/<!--\s*slides\s*-->/)) headerLines.push('<!-- slides -->');
      if (deck.theme) headerLines.push(`<!-- theme: ${deck.theme} -->`);
      const newContent =
        (headerLines.length ? headerLines.join('\n') + '\n\n' : '') +
        allRaw.join('\n\n---\n\n');

      await saveFileContent(filePath, newContent);
      onContentUpdate?.(filePath, newContent);
      setEditMode(false);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [filePath, slides, currentSlide, content, deck.theme, onContentUpdate]);

  // Persist a theme change back to the source file. Reads the current
  // file content (which may include unsaved deck-level edits the user
  // made elsewhere — unlikely, but cheap to be safe), rewrites the
  // <!-- theme: name --> directive, and saves.
  const handleThemeChange = useCallback(
    async (newTheme: string) => {
      if (newTheme === (deck.theme || '') || switchingTheme) return;
      setSwitchingTheme(true);
      setThemeError(null);
      try {
        const newContent = setDeckTheme(content, newTheme);
        await saveFileContent(filePath, newContent);
        onContentUpdate?.(filePath, newContent);
      } catch (err) {
        setThemeError(err instanceof Error ? err.message : 'Theme switch failed');
        setTimeout(() => setThemeError(null), 4000);
      } finally {
        setSwitchingTheme(false);
      }
    },
    [content, deck.theme, filePath, onContentUpdate, switchingTheme],
  );

  const clearFilter = useCallback(() => setActiveFilter(null), []);

  const handleEditKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        handleSave();
      }
    },
    [handleSave],
  );

  const currentSlideData = slides[currentSlide];

  return (
    <div className="slides-viewer" ref={containerRef} tabIndex={0} onKeyDown={handleContainerKeyDown}>
      <div className="slides-viewer-header">
        <span className="slides-viewer-filename">{getDisplayName(fileName, 'slides')}</span>
        <select
          className="slides-viewer-theme-select"
          value={theme.name}
          onChange={(e) => handleThemeChange(e.target.value)}
          disabled={editMode || switchingTheme}
          title={`Deck theme: ${theme.displayName}`}
          aria-label="Deck theme"
        >
          {Object.values(SLIDES_THEMES).map((t) => (
            <option key={t.name} value={t.name}>
              {t.displayName}
            </option>
          ))}
        </select>
        {themeError && <span className="slides-export-error">{themeError}</span>}
        {!editMode && (
          <span className="slides-viewer-counter">
            {currentSlide + 1} / {slides.length}
          </span>
        )}
        {editMode && (
          <span className="slides-viewer-counter">
            Editing slide {currentSlide + 1}
          </span>
        )}
        <button
          className="canvas-edit-btn"
          onClick={editMode ? cancelEdit : enterEditMode}
          title={editMode ? 'Preview' : 'Edit'}
        >
          {editMode ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
            </svg>
          )}
          {editMode ? 'Preview' : 'Edit'}
        </button>
        <button
          className="slides-export-btn"
          onClick={handleExport}
          disabled={exporting || editMode}
          title="Export as PowerPoint"
        >
          {exporting ? (
            <span className="slides-export-spinner" />
          ) : (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
          )}
          {exporting ? 'Exporting...' : 'Export PPTX'}
        </button>
        {exportError && <span className="slides-export-error">{exportError}</span>}
      </div>

      {editMode ? (
        <div className="canvas-edit-area">
          <EditToolbar
            onSave={handleSave}
            onCancel={cancelEdit}
            saving={saving}
            saveError={saveError}
          />
          {currentSlideData && currentSlideData.layout && currentSlideData.layout !== 'content' && (
            <div className="slides-edit-layout-banner">
              Layout: <strong>{currentSlideData.layout}</strong>
              <span className="slides-edit-layout-hint">
                {currentSlideData.layout === 'two_column'
                  ? 'editing title + each column separately; structure preserved on save'
                  : 'directive will be reattached on save'}
              </span>
            </div>
          )}
          <div className="slides-viewer-body">
            <div
              className="slides-viewer-stage"
              style={{ ...themeCssVars(theme), background: `#${theme.bgColor}` }}
            >
              {isTwoCol ? (
                <div className="slides-edit-two-column">
                  <input
                    ref={editTitleRef}
                    className="slides-edit-title-input"
                    type="text"
                    placeholder="Slide title"
                    onKeyDown={(e) => {
                      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
                        e.preventDefault();
                        handleSave();
                      }
                    }}
                  />
                  <div className="slides-edit-column-grid">
                    <div className="slides-edit-column">
                      <div className="slides-edit-column-label">Column 1</div>
                      <div
                        ref={editCol1Ref}
                        className="message-content canvas-editable slides-edit-column-body"
                        contentEditable
                        suppressContentEditableWarning
                        onKeyDown={handleEditKeyDown}
                      />
                    </div>
                    <div className="slides-edit-column">
                      <div className="slides-edit-column-label">Column 2</div>
                      <div
                        ref={editCol2Ref}
                        className="message-content canvas-editable slides-edit-column-body"
                        contentEditable
                        suppressContentEditableWarning
                        onKeyDown={handleEditKeyDown}
                      />
                    </div>
                  </div>
                </div>
              ) : (
                <div
                  ref={editRef}
                  className="slides-viewer-slide message-content canvas-editable"
                  contentEditable
                  suppressContentEditableWarning
                  onKeyDown={handleEditKeyDown}
                />
              )}
            </div>
          </div>
        </div>
      ) : (
        <>
          <div className="slides-viewer-body">
            <div
              className="slides-viewer-stage"
              style={{ ...themeCssVars(theme), background: `#${theme.bgColor}` }}
            >
              {currentSlideData && (
                <SlideStage
                  slide={currentSlideData}
                  theme={theme}
                  marked={marked}
                  refEl={slideRef}
                />
              )}
            </div>
          </div>
          {activeFilter && (
            <div className="slides-filter-indicator">
              <span>Filtered: <strong>{activeFilter}</strong></span>
              <button
                className="slides-filter-clear"
                onClick={clearFilter}
                title="Clear filter"
              >
                &times;
              </button>
            </div>
          )}
          {slides.length > 1 && (
            <div className="slides-thumbnails">
              {thumbnailHtmls.map((thumbHtml, idx) => (
                <button
                  key={idx}
                  className={`slides-thumbnail ${idx === currentSlide ? 'active' : ''}`}
                  onClick={() => setCurrentSlide(idx)}
                  title={`Slide ${idx + 1}`}
                >
                  <div
                    className="slides-thumbnail-content"
                    dangerouslySetInnerHTML={{ __html: thumbHtml }}
                  />
                  <span className="slides-thumbnail-number">{idx + 1}</span>
                </button>
              ))}
            </div>
          )}
          <div className="slides-viewer-nav">
            <button
              className="slides-nav-btn"
              onClick={goPrev}
              disabled={currentSlide === 0}
            >
              Previous
            </button>
            <button
              className="slides-nav-btn"
              onClick={goNext}
              disabled={currentSlide === slides.length - 1}
            >
              Next
            </button>
          </div>
        </>
      )}
    </div>
  );
}

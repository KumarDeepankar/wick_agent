import { useState, useMemo, useEffect, useCallback, useRef } from 'react';
import { Marked } from 'marked';
import { renderChartSVG } from '../../utils/chartRenderer';
import { exportSlidesAsPptx, saveFileContent } from '../../api';
import { htmlToMarkdown } from '../../utils/htmlToMarkdown';
import { EditToolbar } from './EditToolbar';

interface Props {
  content: string;
  fileName: string;
  filePath: string;
  onContentUpdate?: (filePath: string, content: string) => void;
}

/**
 * Apply cross-chart filter directly on the DOM.
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

export function SlidesViewer({ content, fileName, filePath, onContentUpdate }: Props) {
  const slides = useMemo(() => {
    return content
      .split(/\n---\n/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
  }, [content]);

  const [currentSlide, setCurrentSlide] = useState(0);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [activeFilter, setActiveFilter] = useState<string | null>(null);

  // Edit mode state
  const [editMode, setEditMode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const slideRef = useRef<HTMLDivElement>(null);
  const editRef = useRef<HTMLDivElement>(null);

  // Build slide HTML
  const slidesMarked = useMemo(() => {
    let chartIdx = 0;
    return new Marked({
      renderer: {
        code({ text, lang }: { text: string; lang?: string }) {
          if (lang === 'chart') {
            return renderChartSVG(text, chartIdx++);
          }
          return `<pre><code class="language-${lang || ''}">${text.replace(/</g, '&lt;').replace(/>/g, '&gt;')}</code></pre>`;
        },
      },
    });
  }, []);

  const slideHtml = useMemo(() => {
    if (slides[currentSlide] === undefined) return '';
    return slidesMarked.parse(slides[currentSlide]!, { async: false }) as string;
  }, [slides, currentSlide, slidesMarked]);

  // Thumbnails
  const thumbnailHtmls = useMemo(() => {
    const marked = new Marked({
      renderer: {
        code({ text, lang }: { text: string; lang?: string }) {
          if (lang === 'chart') {
            return renderChartSVG(text, 0);
          }
          return `<pre><code class="language-${lang || ''}">${text.replace(/</g, '&lt;').replace(/>/g, '&gt;')}</code></pre>`;
        },
      },
    });
    return slides.map((s) => marked.parse(s, { async: false }) as string);
  }, [slides]);

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

  // Set editable content when entering edit mode
  useEffect(() => {
    if (editMode && editRef.current) {
      editRef.current.innerHTML = slideHtml;
      editRef.current.focus();
    }
  }, [editMode, currentSlide]); // eslint-disable-line react-hooks/exhaustive-deps

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
    if (!editRef.current) return;
    setSaving(true);
    setSaveError(null);
    try {
      // Convert the currently edited slide's HTML back to markdown
      const editedSlidemd = htmlToMarkdown(editRef.current.innerHTML);
      // Reconstruct full content with the edited slide
      const newSlides = [...slides];
      newSlides[currentSlide] = editedSlidemd;
      const newContent = newSlides.join('\n\n---\n\n');
      await saveFileContent(filePath, newContent);
      onContentUpdate?.(filePath, newContent);
      setEditMode(false);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [filePath, slides, currentSlide, onContentUpdate]);

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

  return (
    <div className="slides-viewer" ref={containerRef} tabIndex={0} onKeyDown={handleContainerKeyDown}>
      <div className="slides-viewer-header">
        <span className="slides-viewer-filename">{fileName}</span>
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
          <div className="slides-viewer-body">
            <div className="slides-viewer-stage">
              <div
                ref={editRef}
                className="slides-viewer-slide message-content canvas-editable"
                contentEditable
                suppressContentEditableWarning
                onKeyDown={handleEditKeyDown}
              />
            </div>
          </div>
        </div>
      ) : (
        <>
          <div className="slides-viewer-body">
            <div className="slides-viewer-stage">
              <div
                ref={slideRef}
                className="slides-viewer-slide message-content"
                dangerouslySetInnerHTML={{ __html: slideHtml }}
              />
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

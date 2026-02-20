import { useState, useMemo, useEffect, useCallback } from 'react';
import { marked } from 'marked';
import { exportSlidesAsPptx } from '../../api';

interface Props {
  content: string;
  fileName: string;
  filePath: string;
}

export function SlidesViewer({ content, fileName, filePath }: Props) {
  const slides = useMemo(() => {
    return content
      .split(/\n---\n/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
  }, [content]);

  const [currentSlide, setCurrentSlide] = useState(0);
  const [exporting, setExporting] = useState(false);

  const slideHtml = useMemo(() => {
    if (slides[currentSlide] === undefined) return '';
    return marked.parse(slides[currentSlide], { async: false }) as string;
  }, [slides, currentSlide]);

  // Generate thumbnail HTML for each slide
  const thumbnailHtmls = useMemo(() => {
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
      console.error('PPTX export failed:', err);
    } finally {
      setExporting(false);
    }
  }, [filePath, fileName]);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') goNext();
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') goPrev();
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [goNext, goPrev]);

  // Reset slide index when slides count changes
  useEffect(() => {
    setCurrentSlide((s) => Math.min(s, Math.max(slides.length - 1, 0)));
  }, [slides.length]);

  return (
    <div className="slides-viewer">
      <div className="slides-viewer-header">
        <span className="slides-viewer-filename">{fileName}</span>
        <span className="slides-viewer-counter">
          {currentSlide + 1} / {slides.length}
        </span>
        <button
          className="slides-export-btn"
          onClick={handleExport}
          disabled={exporting}
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
      </div>
      <div className="slides-viewer-body">
        <div className="slides-viewer-stage">
          <div
            className="slides-viewer-slide message-content"
            dangerouslySetInnerHTML={{ __html: slideHtml }}
          />
        </div>
      </div>
      {/* Thumbnail strip */}
      {slides.length > 1 && (
        <div className="slides-thumbnails">
          {thumbnailHtmls.map((html, idx) => (
            <button
              key={idx}
              className={`slides-thumbnail ${idx === currentSlide ? 'active' : ''}`}
              onClick={() => setCurrentSlide(idx)}
              title={`Slide ${idx + 1}`}
            >
              <div
                className="slides-thumbnail-content"
                dangerouslySetInnerHTML={{ __html: html }}
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
    </div>
  );
}

import { useState, useMemo, useEffect, useCallback } from 'react';
import { marked } from 'marked';

interface Props {
  content: string;
  fileName: string;
}

export function SlidesViewer({ content, fileName }: Props) {
  const slides = useMemo(() => {
    return content
      .split(/\n---\n/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
  }, [content]);

  const [currentSlide, setCurrentSlide] = useState(0);

  const slideHtml = useMemo(() => {
    if (slides[currentSlide] === undefined) return '';
    return marked.parse(slides[currentSlide], { async: false }) as string;
  }, [slides, currentSlide]);

  const goNext = useCallback(() => {
    setCurrentSlide((s) => Math.min(s + 1, slides.length - 1));
  }, [slides.length]);

  const goPrev = useCallback(() => {
    setCurrentSlide((s) => Math.max(s - 1, 0));
  }, []);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') goNext();
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') goPrev();
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [goNext, goPrev]);

  return (
    <div className="slides-viewer">
      <div className="slides-viewer-header">
        <span className="slides-viewer-filename">{fileName}</span>
        <span className="slides-viewer-counter">
          {currentSlide + 1} / {slides.length}
        </span>
      </div>
      <div className="slides-viewer-body">
        <div
          className="slides-viewer-slide message-content"
          dangerouslySetInnerHTML={{ __html: slideHtml }}
        />
      </div>
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

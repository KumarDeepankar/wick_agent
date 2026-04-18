import { useEffect, useRef, useCallback } from 'react';
import type { AsyncTask } from '../types';
import { AsyncTaskPanel } from './AsyncTaskPanel';

interface Props {
  tasks: AsyncTask[];
  isOpen: boolean;
  onClose: () => void;
}

/** Slide-over panel — mirrors TraceOverlay's keyboard and focus behavior. */
export function AsyncTaskOverlay({ tasks, isOpen, onClose }: Props) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  useEffect(() => {
    if (isOpen) panelRef.current?.focus();
  }, [isOpen]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key !== 'Tab') return;
    const panel = panelRef.current;
    if (!panel) return;
    const focusable = panel.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );
    if (focusable.length === 0) return;
    const first = focusable[0]!;
    const last = focusable[focusable.length - 1]!;
    if (e.shiftKey) {
      if (document.activeElement === first || document.activeElement === panel) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }, []);

  if (!isOpen) return null;

  return (
    <div className="async-task-overlay" role="dialog" aria-modal="true" aria-label="Background tasks">
      <div className="async-task-overlay-backdrop" onClick={onClose} />
      <div className="async-task-overlay-panel" ref={panelRef} tabIndex={-1} onKeyDown={handleKeyDown}>
        <div className="async-task-overlay-close-row">
          <button className="async-task-overlay-close" onClick={onClose} aria-label="Close background tasks">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="2" y1="2" x2="12" y2="12" />
              <line x1="12" y1="2" x2="2" y2="12" />
            </svg>
          </button>
        </div>
        <AsyncTaskPanel tasks={tasks} />
      </div>
    </div>
  );
}

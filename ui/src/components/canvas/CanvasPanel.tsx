import { useState, useEffect, useCallback } from 'react';
import type { CanvasArtifact, StreamStatus } from '../../types';
import { WelcomeView } from './WelcomeView';
import { CodeViewer } from './CodeViewer';
import { DataViewer } from './DataViewer';
import { DocumentViewer } from './DocumentViewer';
import { SlidesViewer } from './SlidesViewer';
import { BinaryDownload } from './BinaryDownload';
import { DownloadButton } from './DownloadButton';

interface Props {
  artifacts: CanvasArtifact[];
  onPromptClick: (prompt: string) => void;
  status: StreamStatus;
  onContentUpdate?: (filePath: string, content: string) => void;
  onRemoveArtifact?: (artifactId: string) => void;
  isFullscreen?: boolean;
  onToggleFullscreen?: () => void;
}

export function CanvasPanel({ artifacts, onPromptClick, onContentUpdate, onRemoveArtifact, isFullscreen, onToggleFullscreen }: Props) {
  const [activeIndex, setActiveIndex] = useState(0);

  // Auto-select latest artifact when new ones arrive
  useEffect(() => {
    if (artifacts.length > 0) {
      setActiveIndex(artifacts.length - 1);
    }
  }, [artifacts.length]);

  const handleClose = useCallback(
    (e: React.MouseEvent, artifactId: string, idx: number) => {
      e.stopPropagation();
      onRemoveArtifact?.(artifactId);
      // Adjust active index if needed
      if (idx <= activeIndex && activeIndex > 0) {
        setActiveIndex((i) => i - 1);
      }
    },
    [onRemoveArtifact, activeIndex],
  );

  if (artifacts.length === 0) {
    return (
      <div className="canvas-panel">
        <WelcomeView onPromptClick={onPromptClick} />
      </div>
    );
  }

  const active = artifacts[activeIndex];

  const renderViewer = () => {
    if (!active) return null;

    switch (active.contentType) {
      case 'code':
        return <CodeViewer content={active.content ?? ''} language={active.language} fileName={active.fileName} />;
      case 'data':
        return <DataViewer content={active.content ?? ''} fileName={active.fileName} />;
      case 'document':
        return <DocumentViewer content={active.content ?? ''} fileName={active.fileName} />;
      case 'slides':
        return <SlidesViewer content={active.content ?? ''} fileName={active.fileName} filePath={active.filePath} onContentUpdate={onContentUpdate} />;
      case 'binary':
        return <BinaryDownload artifact={active} />;
      default:
        return <DocumentViewer content={active.content ?? ''} fileName={active.fileName} />;
    }
  };

  return (
    <div className="canvas-panel">
      <div className="canvas-header">
        <div className="canvas-tabs" role="tablist" aria-label="Canvas artifacts">
          {artifacts.map((artifact, idx) => (
            <button
              key={artifact.id}
              role="tab"
              aria-selected={idx === activeIndex}
              className={`canvas-tab ${idx === activeIndex ? 'active' : ''}`}
              onClick={() => setActiveIndex(idx)}
              title={artifact.filePath}
            >
              <span className="canvas-tab-name">{artifact.fileName}</span>
              {onRemoveArtifact && (
                <span
                  className="canvas-tab-close"
                  onClick={(e) => handleClose(e, artifact.id, idx)}
                  role="button"
                  aria-label={`Close ${artifact.fileName}`}
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      e.stopPropagation();
                      onRemoveArtifact(artifact.id);
                      if (idx <= activeIndex && activeIndex > 0) setActiveIndex((i) => i - 1);
                    }
                  }}
                >
                  &times;
                </span>
              )}
            </button>
          ))}
        </div>
        <div className="canvas-header-actions">
          {active && <DownloadButton artifact={active} />}
          {onToggleFullscreen && (
            <button
              className={`canvas-fullscreen-btn ${isFullscreen ? 'active' : ''}`}
              onClick={onToggleFullscreen}
              title={isFullscreen ? 'Exit fullscreen' : 'Fullscreen canvas'}
              aria-label={isFullscreen ? 'Exit fullscreen canvas' : 'Fullscreen canvas'}
            >
              {isFullscreen ? (
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4 1 4 4 1 4" />
                  <polyline points="10 13 10 10 13 10" />
                  <polyline points="13 4 10 4 10 1" />
                  <polyline points="1 10 4 10 4 13" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="1 5 1 1 5 1" />
                  <polyline points="13 9 13 13 9 13" />
                  <polyline points="9 1 13 1 13 5" />
                  <polyline points="5 13 1 13 1 9" />
                </svg>
              )}
            </button>
          )}
        </div>
      </div>
      <div className="canvas-content" role="tabpanel">
        {renderViewer()}
      </div>
    </div>
  );
}

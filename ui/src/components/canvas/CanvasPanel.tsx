import { useState, useEffect } from 'react';
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
}

export function CanvasPanel({ artifacts, onPromptClick, onContentUpdate }: Props) {
  const [activeIndex, setActiveIndex] = useState(0);

  // Auto-select latest artifact when new ones arrive
  useEffect(() => {
    if (artifacts.length > 0) {
      setActiveIndex(artifacts.length - 1);
    }
  }, [artifacts.length]);

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
        <div className="canvas-tabs">
          {artifacts.map((artifact, idx) => (
            <button
              key={artifact.id}
              className={`canvas-tab ${idx === activeIndex ? 'active' : ''}`}
              onClick={() => setActiveIndex(idx)}
              title={artifact.filePath}
            >
              {artifact.fileName}
            </button>
          ))}
        </div>
        {active && <DownloadButton artifact={active} />}
      </div>
      <div className="canvas-content">
        {renderViewer()}
      </div>
    </div>
  );
}

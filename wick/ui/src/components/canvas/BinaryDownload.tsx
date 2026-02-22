import type { CanvasArtifact } from '../../types';
import { getDisplayName } from '../../utils/canvasUtils';
import { DownloadButton } from './DownloadButton';

interface Props {
  artifact: CanvasArtifact;
}

const TYPE_LABELS: Record<string, string> = {
  '.xlsx': 'Excel Spreadsheet',
  '.docx': 'Word Document',
  '.pdf': 'PDF Document',
  '.zip': 'ZIP Archive',
  '.tar': 'TAR Archive',
  '.gz': 'Gzip Archive',
  '.png': 'PNG Image',
  '.jpg': 'JPEG Image',
  '.jpeg': 'JPEG Image',
  '.gif': 'GIF Image',
  '.svg': 'SVG Image',
  '.mp3': 'MP3 Audio',
  '.mp4': 'MP4 Video',
  '.pptx': 'PowerPoint Presentation',
};

export function BinaryDownload({ artifact }: Props) {
  const typeLabel = TYPE_LABELS[artifact.extension] ?? 'Binary File';

  return (
    <div className="binary-download">
      <div className="binary-download-card">
        <div className="binary-download-icon">
          <svg width="48" height="48" viewBox="0 0 48 48" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M28 4H12a4 4 0 0 0-4 4v32a4 4 0 0 0 4 4h24a4 4 0 0 0 4-4V16z" />
            <polyline points="28 4 28 16 40 16" />
          </svg>
        </div>
        <h3 className="binary-download-name">{getDisplayName(artifact.fileName, artifact.contentType)}</h3>
        <p className="binary-download-type">{typeLabel}</p>
        <DownloadButton artifact={artifact} label="Download File" />
      </div>
    </div>
  );
}

import type { CanvasArtifact } from '../../types';
import { fetchFileDownload } from '../../api';

interface Props {
  artifact: CanvasArtifact;
  label?: string;
}

export function DownloadButton({ artifact, label = 'Download' }: Props) {
  const handleDownload = async () => {
    try {
      if (!artifact.isBinary && artifact.content) {
        // Text file: create blob from content
        const blob = new Blob([artifact.content], { type: 'text/plain' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = artifact.fileName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
      } else {
        // Binary file: fetch from backend
        const blob = await fetchFileDownload(artifact.filePath);
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = artifact.fileName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
      }
    } catch (err) {
      console.error('Download failed:', err);
    }
  };

  return (
    <button className="download-btn" onClick={handleDownload} title={`Download ${artifact.fileName}`}>
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M7 1v9" />
        <path d="M3.5 6.5 7 10l3.5-3.5" />
        <path d="M1.5 12.5h11" />
      </svg>
      <span>{label}</span>
    </button>
  );
}

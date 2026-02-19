import { useMemo, useState } from 'react';
import { marked } from 'marked';

interface Props {
  content: string;
  fileName: string;
}

export function DocumentViewer({ content, fileName }: Props) {
  const [copied, setCopied] = useState(false);

  const html = useMemo(() => {
    return marked.parse(content, { async: false }) as string;
  }, [content]);

  const handleCopy = async () => {
    await navigator.clipboard.writeText(content);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="document-viewer">
      <div className="document-viewer-header">
        <span className="document-viewer-filename">{fileName}</span>
        <button className={`copy-btn ${copied ? 'copied' : ''}`} onClick={handleCopy}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <div
        className="document-viewer-body message-content"
        dangerouslySetInnerHTML={{ __html: html }}
      />
    </div>
  );
}

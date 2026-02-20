import { useMemo, useState, useCallback, useRef, useEffect } from 'react';
import { marked } from 'marked';
import { saveFileContent } from '../../api';
import { htmlToMarkdown } from '../../utils/htmlToMarkdown';
import { EditToolbar } from './EditToolbar';

interface Props {
  content: string;
  fileName: string;
  filePath: string;
  onContentUpdate?: (filePath: string, content: string) => void;
}

export function DocumentViewer({ content, fileName, filePath, onContentUpdate }: Props) {
  const [copied, setCopied] = useState(false);
  const [editMode, setEditMode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const editRef = useRef<HTMLDivElement>(null);

  const html = useMemo(() => {
    return marked.parse(content, { async: false }) as string;
  }, [content]);

  // Set editable content when entering edit mode
  useEffect(() => {
    if (editMode && editRef.current) {
      editRef.current.innerHTML = html;
      editRef.current.focus();
    }
  }, [editMode]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleCopy = async () => {
    await navigator.clipboard.writeText(content);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

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
      const md = htmlToMarkdown(editRef.current.innerHTML);
      await saveFileContent(filePath, md);
      onContentUpdate?.(filePath, md);
      setEditMode(false);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [filePath, onContentUpdate]);

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
    <div className="document-viewer">
      <div className="document-viewer-header">
        <span className="document-viewer-filename">{fileName}</span>
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
        {!editMode && (
          <button className={`copy-btn ${copied ? 'copied' : ''}`} onClick={handleCopy}>
            {copied ? 'Copied' : 'Copy'}
          </button>
        )}
      </div>
      {editMode ? (
        <div className="canvas-edit-area">
          <EditToolbar
            onSave={handleSave}
            onCancel={cancelEdit}
            saving={saving}
            saveError={saveError}
          />
          <div
            ref={editRef}
            className="document-viewer-body message-content canvas-editable"
            contentEditable
            suppressContentEditableWarning
            onKeyDown={handleEditKeyDown}
          />
        </div>
      ) : (
        <div
          className="document-viewer-body message-content"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      )}
    </div>
  );
}

import { useCallback } from 'react';

interface Props {
  onSave: () => void;
  onCancel: () => void;
  saving?: boolean;
  saveError?: string | null;
}

function execCmd(command: string, value?: string) {
  document.execCommand(command, false, value);
}

export function EditToolbar({ onSave, onCancel, saving, saveError }: Props) {
  const bold = useCallback(() => execCmd('bold'), []);
  const italic = useCallback(() => execCmd('italic'), []);
  const heading = useCallback((level: number) => {
    execCmd('formatBlock', `h${level}`);
  }, []);
  const paragraph = useCallback(() => execCmd('formatBlock', 'p'), []);
  const bulletList = useCallback(() => execCmd('insertUnorderedList'), []);
  const numberList = useCallback(() => execCmd('insertOrderedList'), []);
  const blockquote = useCallback(() => execCmd('formatBlock', 'blockquote'), []);

  return (
    <div className="edit-toolbar">
      <div className="edit-toolbar-group">
        <button
          className="edit-toolbar-btn"
          onClick={bold}
          title="Bold (Ctrl+B)"
          aria-label="Bold"
        >
          <strong>B</strong>
        </button>
        <button
          className="edit-toolbar-btn"
          onClick={italic}
          title="Italic (Ctrl+I)"
          aria-label="Italic"
        >
          <em>I</em>
        </button>
      </div>
      <span className="edit-toolbar-sep" />
      <div className="edit-toolbar-group">
        <button className="edit-toolbar-btn" onClick={() => heading(1)} title="Heading 1">
          H1
        </button>
        <button className="edit-toolbar-btn" onClick={() => heading(2)} title="Heading 2">
          H2
        </button>
        <button className="edit-toolbar-btn" onClick={() => heading(3)} title="Heading 3">
          H3
        </button>
        <button className="edit-toolbar-btn" onClick={paragraph} title="Paragraph">
          P
        </button>
      </div>
      <span className="edit-toolbar-sep" />
      <div className="edit-toolbar-group">
        <button className="edit-toolbar-btn" onClick={bulletList} title="Bullet list">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="8" y1="6" x2="21" y2="6" />
            <line x1="8" y1="12" x2="21" y2="12" />
            <line x1="8" y1="18" x2="21" y2="18" />
            <circle cx="4" cy="6" r="1" fill="currentColor" />
            <circle cx="4" cy="12" r="1" fill="currentColor" />
            <circle cx="4" cy="18" r="1" fill="currentColor" />
          </svg>
        </button>
        <button className="edit-toolbar-btn" onClick={numberList} title="Numbered list">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="10" y1="6" x2="21" y2="6" />
            <line x1="10" y1="12" x2="21" y2="12" />
            <line x1="10" y1="18" x2="21" y2="18" />
            <text x="3" y="8" fontSize="8" fill="currentColor" stroke="none" fontWeight="700">1</text>
            <text x="3" y="14" fontSize="8" fill="currentColor" stroke="none" fontWeight="700">2</text>
            <text x="3" y="20" fontSize="8" fill="currentColor" stroke="none" fontWeight="700">3</text>
          </svg>
        </button>
        <button className="edit-toolbar-btn" onClick={blockquote} title="Blockquote">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M6 17h3l2-4V7H5v6h3zm8 0h3l2-4V7h-6v6h3z" />
          </svg>
        </button>
      </div>
      <div className="edit-toolbar-spacer" />
      {saveError && <span className="canvas-save-error">{saveError}</span>}
      <button className="canvas-cancel-btn" onClick={onCancel} disabled={saving}>
        Cancel
      </button>
      <button className="canvas-save-btn" onClick={onSave} disabled={saving}>
        {saving ? 'Saving...' : 'Save'}
      </button>
    </div>
  );
}

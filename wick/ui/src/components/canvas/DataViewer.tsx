import { useState, useMemo, useCallback, useRef } from 'react';
import { saveFileContent } from '../../api';
import { getDisplayName } from '../../utils/canvasUtils';

interface Props {
  content: string;
  fileName: string;
  filePath: string;
  onContentUpdate?: (filePath: string, content: string) => void;
}

function parseCSV(raw: string, delimiter: string = ','): string[][] {
  const rows: string[][] = [];
  let current = '';
  let inQuotes = false;
  let row: string[] = [];

  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i];
    if (inQuotes) {
      if (ch === '"' && raw[i + 1] === '"') {
        current += '"';
        i++;
      } else if (ch === '"') {
        inQuotes = false;
      } else {
        current += ch;
      }
    } else {
      if (ch === '"') {
        inQuotes = true;
      } else if (ch === delimiter) {
        row.push(current.trim());
        current = '';
      } else if (ch === '\n' || (ch === '\r' && raw[i + 1] === '\n')) {
        row.push(current.trim());
        if (row.some((c) => c !== '')) rows.push(row);
        row = [];
        current = '';
        if (ch === '\r') i++;
      } else {
        current += ch;
      }
    }
  }
  row.push(current.trim());
  if (row.some((c) => c !== '')) rows.push(row);

  return rows;
}

function escapeCSVField(field: string, delimiter: string): string {
  if (field.includes('"') || field.includes(delimiter) || field.includes('\n')) {
    return '"' + field.replace(/"/g, '""') + '"';
  }
  return field;
}

function serializeToCSV(rows: string[][], delimiter: string): string {
  return rows.map((row) => row.map((f) => escapeCSVField(f, delimiter)).join(delimiter)).join('\n');
}

const PAGE_SIZE = 200;

export function DataViewer({ content, fileName, filePath, onContentUpdate }: Props) {
  const delimiter = fileName.endsWith('.tsv') ? '\t' : ',';
  const allRows = useMemo(() => parseCSV(content, delimiter), [content, delimiter]);

  const [sortCol, setSortCol] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState(true);
  const [page, setPage] = useState(0);
  const [editMode, setEditMode] = useState(false);
  const [editData, setEditData] = useState<string[][]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);


  const headers = editMode ? editData[0] : allRows[0];
  const dataRows = editMode ? editData.slice(1) : allRows.slice(1);

  const enterEditMode = useCallback(() => {
    // Deep copy all rows for editing
    setEditData(allRows.map((row) => [...row]));
    setSaveError(null);
    setEditMode(true);
  }, [allRows]);

  const cancelEdit = useCallback(() => {
    setEditMode(false);
    setSaveError(null);
  }, []);

  const handleCellBlur = useCallback(
    (rowIdx: number, colIdx: number, e: React.FocusEvent<HTMLTableCellElement>) => {
      const newVal = e.currentTarget.textContent ?? '';
      setEditData((prev) => {
        const copy = prev.map((r) => [...r]);
        // rowIdx is data row index (0-based), +1 for header
        const actualRow = rowIdx + 1;
        if (copy[actualRow]) {
          copy[actualRow]![colIdx] = newVal;
        }
        return copy;
      });
    },
    [],
  );

  const handleHeaderBlur = useCallback(
    (colIdx: number, e: React.FocusEvent<HTMLTableCellElement>) => {
      const newVal = e.currentTarget.textContent ?? '';
      setEditData((prev) => {
        const copy = prev.map((r) => [...r]);
        if (copy[0]) {
          copy[0]![colIdx] = newVal;
        }
        return copy;
      });
    },
    [],
  );

  const handleCellKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTableCellElement>) => {
      // Tab to next cell, Enter to confirm
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        (e.currentTarget as HTMLElement).blur();
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        handleSaveRef.current();
      }
    },
    [],
  );

  const handleSave = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const csv = serializeToCSV(editData, delimiter);
      await saveFileContent(filePath, csv);
      onContentUpdate?.(filePath, csv);
      setEditMode(false);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [editData, delimiter, filePath, onContentUpdate]);

  // Ref to latest handleSave for keydown handler
  const handleSaveRef = useRef(handleSave);
  handleSaveRef.current = handleSave;

  if (!headers || (editMode ? editData.length : allRows.length) === 0) {
    return <div className="data-viewer-empty">No data to display</div>;
  }

  const sorted = useMemo(() => {
    if (editMode || sortCol === null) return dataRows;
    return [...dataRows].sort((a, b) => {
      const va = a[sortCol] ?? '';
      const vb = b[sortCol] ?? '';
      const na = parseFloat(va);
      const nb = parseFloat(vb);
      if (!isNaN(na) && !isNaN(nb)) {
        return sortAsc ? na - nb : nb - na;
      }
      return sortAsc ? va.localeCompare(vb) : vb.localeCompare(va);
    });
  }, [dataRows, sortCol, sortAsc, editMode]);

  const totalPages = Math.ceil(sorted.length / PAGE_SIZE);
  const pageRows = sorted.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  const handleSort = (col: number) => {
    if (editMode) return; // no sorting in edit mode
    if (sortCol === col) {
      setSortAsc(!sortAsc);
    } else {
      setSortCol(col);
      setSortAsc(true);
    }
    setPage(0);
  };

  return (
    <div className="data-viewer">
      <div className="data-viewer-edit-header">
        <span className="data-viewer-filename">{getDisplayName(fileName, 'data')}</span>
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
        {editMode && (
          <>
            {saveError && <span className="canvas-save-error">{saveError}</span>}
            <button className="canvas-cancel-btn" onClick={cancelEdit} disabled={saving}>
              Cancel
            </button>
            <button className="canvas-save-btn" onClick={handleSave} disabled={saving}>
              {saving ? 'Saving...' : 'Save'}
            </button>
          </>
        )}
      </div>
      <div className="data-viewer-scroll">
        <table className={`data-table ${editMode ? 'data-table-editable' : ''}`}>
          <thead>
            <tr>
              {headers.map((h, i) => (
                <th
                  key={i}
                  onClick={() => handleSort(i)}
                  className={editMode ? 'data-th-editable' : 'data-th-sortable'}
                  contentEditable={editMode}
                  suppressContentEditableWarning
                  onBlur={editMode ? (e) => handleHeaderBlur(i, e) : undefined}
                  onKeyDown={editMode ? handleCellKeyDown : undefined}
                >
                  {h}
                  {!editMode && sortCol === i && (
                    <span className="sort-indicator">{sortAsc ? ' \u25B2' : ' \u25BC'}</span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {pageRows.map((row, ri) => (
              <tr key={ri}>
                {headers.map((_, ci) => (
                  <td
                    key={ci}
                    contentEditable={editMode}
                    suppressContentEditableWarning
                    onBlur={editMode ? (e) => handleCellBlur(page * PAGE_SIZE + ri, ci, e) : undefined}
                    onKeyDown={editMode ? handleCellKeyDown : undefined}
                    className={editMode ? 'data-td-editable' : ''}
                  >
                    {row[ci] ?? ''}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="data-viewer-footer">
        <span>
          {dataRows.length} row{dataRows.length !== 1 ? 's' : ''} &middot; {headers.length} column{headers.length !== 1 ? 's' : ''}
        </span>
        {totalPages > 1 && (
          <div className="data-pagination">
            <button
              className="data-page-btn"
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0}
              aria-label="Previous page"
            >
              Prev
            </button>
            <span className="data-page-info">
              {page + 1} / {totalPages}
            </span>
            <button
              className="data-page-btn"
              onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
              disabled={page >= totalPages - 1}
              aria-label="Next page"
            >
              Next
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

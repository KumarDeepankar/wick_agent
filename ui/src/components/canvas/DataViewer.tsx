import { useState, useMemo } from 'react';

interface Props {
  content: string;
  fileName: string;
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
  // Last field
  row.push(current.trim());
  if (row.some((c) => c !== '')) rows.push(row);

  return rows;
}

const PAGE_SIZE = 200;

export function DataViewer({ content, fileName }: Props) {
  const delimiter = fileName.endsWith('.tsv') ? '\t' : ',';
  const allRows = useMemo(() => parseCSV(content, delimiter), [content, delimiter]);

  const [sortCol, setSortCol] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState(true);
  const [page, setPage] = useState(0);

  const headers = allRows[0];

  if (!headers || allRows.length === 0) {
    return <div className="data-viewer-empty">No data to display</div>;
  }

  const dataRows = allRows.slice(1);

  const sorted = useMemo(() => {
    if (sortCol === null) return dataRows;
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
  }, [dataRows, sortCol, sortAsc]);

  const totalPages = Math.ceil(sorted.length / PAGE_SIZE);
  const pageRows = sorted.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  const handleSort = (col: number) => {
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
      <div className="data-viewer-scroll">
        <table className="data-table">
          <thead>
            <tr>
              {headers.map((h, i) => (
                <th key={i} onClick={() => handleSort(i)} className="data-th-sortable">
                  {h}
                  {sortCol === i && (
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
                  <td key={ci}>{row[ci] ?? ''}</td>
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

import { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { getToken, listContainerFiles, readContainerFile } from '../api';
import type { FileEntry } from '../api';
import '@xterm/xterm/css/xterm.css';

type Tab = 'terminal' | 'files';

interface Props {
  agentId: string;
  onClose: () => void;
  theme: 'light' | 'dark';
  height?: number;
}

export function TerminalPanel({ agentId, onClose, theme, height = 280 }: Props) {
  const [activeTab, setActiveTab] = useState<Tab>('terminal');

  return (
    <div className="terminal-panel" style={{ height }}>
      <div className="terminal-header">
        <div className="terminal-tabs">
          <button
            className={`terminal-tab ${activeTab === 'terminal' ? 'active' : ''}`}
            onClick={() => setActiveTab('terminal')}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 17 10 11 4 5" />
              <line x1="12" y1="19" x2="20" y2="19" />
            </svg>
            Terminal
          </button>
          <button
            className={`terminal-tab ${activeTab === 'files' ? 'active' : ''}`}
            onClick={() => setActiveTab('files')}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
            </svg>
            Files
          </button>
        </div>
        <button className="terminal-close" onClick={onClose} aria-label="Close panel">
          <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="2" y1="2" x2="12" y2="12" />
            <line x1="12" y1="2" x2="2" y2="12" />
          </svg>
        </button>
      </div>
      {activeTab === 'terminal' ? (
        <TerminalView agentId={agentId} theme={theme} />
      ) : (
        <FileBrowser agentId={agentId} />
      )}
    </div>
  );
}

/* ─── Terminal View ─────────────────────────────────────────────────────── */

const DARK_THEME = { background: '#09090b', foreground: '#f1f1f4', cursor: '#f1f1f4' };
const LIGHT_THEME = { background: '#1a1a2e', foreground: '#e0e0e0', cursor: '#e0e0e0' };

function TerminalView({ agentId, theme }: { agentId: string; theme: 'light' | 'dark' }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  // Connection lifecycle — only depends on agentId
  useEffect(() => {
    if (!containerRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "'JetBrains Mono', 'SF Mono', 'Fira Code', monospace",
      theme: theme === 'dark' ? DARK_THEME : LIGHT_THEME,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(containerRef.current);
    fitAddon.fit();
    termRef.current = term;

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
    const ws = new WebSocket(`${proto}//${window.location.host}/agents/${agentId}/terminal${tokenParam}`);
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;

    ws.onopen = () => {
      term.writeln('\x1b[32mConnected to container.\x1b[0m\r');
      fitAddon.fit();
    };

    ws.onmessage = (ev) => {
      const data = ev.data instanceof ArrayBuffer
        ? new TextDecoder().decode(ev.data)
        : ev.data;
      term.write(data);
    };

    ws.onclose = () => {
      term.writeln('\r\n\x1b[31mConnection closed.\x1b[0m');
    };

    ws.onerror = () => {
      term.writeln('\r\n\x1b[31mWebSocket error.\x1b[0m');
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
    });
    resizeObserver.observe(containerRef.current);

    return () => {
      resizeObserver.disconnect();
      ws.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
    };
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Theme update — does NOT tear down the connection
  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.theme = theme === 'dark' ? DARK_THEME : LIGHT_THEME;
    }
  }, [theme]);

  return <div className="terminal-body" ref={containerRef} />;
}

/* ─── File Browser ──────────────────────────────────────────────────────── */

function FileBrowser({ agentId }: { agentId: string }) {
  const [cwd, setCwd] = useState('/workspace');
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<{ path: string; content: string } | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const loadDir = useCallback((path: string) => {
    setLoading(true);
    setError(null);
    setPreview(null);
    listContainerFiles(agentId, path)
      .then((data) => {
        setCwd(data.path);
        setEntries(data.entries);
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, [agentId]);

  useEffect(() => {
    loadDir(cwd);
  }, [agentId]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleClick = useCallback((entry: FileEntry) => {
    if (entry.type === 'dir') {
      loadDir(entry.path);
    } else {
      setPreviewLoading(true);
      readContainerFile(agentId, entry.path)
        .then((data) => setPreview(data))
        .catch((e) => setPreview({ path: entry.path, content: `Error: ${e.message}` }))
        .finally(() => setPreviewLoading(false));
    }
  }, [agentId, loadDir]);

  const handleUp = useCallback(() => {
    const parent = cwd.replace(/\/[^/]+\/?$/, '') || '/';
    loadDir(parent);
  }, [cwd, loadDir]);

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  return (
    <div className="file-browser">
      {/* Breadcrumb bar */}
      <div className="file-browser-bar">
        <button className="file-browser-up" onClick={handleUp} disabled={cwd === '/'} title="Go up">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="15 18 9 12 15 6" />
          </svg>
        </button>
        <span className="file-browser-path">{cwd}</span>
        <button className="file-browser-refresh" onClick={() => loadDir(cwd)} title="Refresh">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="23 4 23 10 17 10" />
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          </svg>
        </button>
      </div>

      <div className="file-browser-content">
        {/* File list */}
        <div className={`file-browser-list ${preview ? 'with-preview' : ''}`}>
          {loading && <div className="file-browser-msg">Loading...</div>}
          {error && <div className="file-browser-msg file-browser-error">{error}</div>}
          {!loading && !error && entries.length === 0 && (
            <div className="file-browser-msg">Empty directory</div>
          )}
          {!loading && entries.map((entry) => (
            <button
              key={entry.path}
              className={`file-entry ${entry.type} ${preview?.path === entry.path ? 'selected' : ''}`}
              onClick={() => handleClick(entry)}
            >
              <span className="file-entry-icon">
                {entry.type === 'dir' ? (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="var(--accent-orange)" stroke="none">
                    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                  </svg>
                ) : (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                    <polyline points="14 2 14 8 20 8" />
                  </svg>
                )}
              </span>
              <span className="file-entry-name">{entry.name}</span>
              {entry.type === 'file' && (
                <span className="file-entry-size">{formatSize(entry.size)}</span>
              )}
            </button>
          ))}
        </div>

        {/* File preview */}
        {preview && (
          <div className="file-preview">
            <div className="file-preview-header">
              <span className="file-preview-path">{preview.path.split('/').pop()}</span>
              <button className="file-preview-close" onClick={() => setPreview(null)}>
                <svg width="10" height="10" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                  <line x1="2" y1="2" x2="12" y2="12" />
                  <line x1="12" y1="2" x2="2" y2="12" />
                </svg>
              </button>
            </div>
            <pre className="file-preview-content">
              {previewLoading ? 'Loading...' : preview.content}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}

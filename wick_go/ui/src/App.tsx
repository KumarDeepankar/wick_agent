import { useState, useEffect, useCallback, useRef } from 'react';
import { ChatPanel } from './components/ChatPanel';
import { LoginPage } from './components/LoginPage';
import { TraceToggleButton } from './components/TraceToggleButton';
import { TraceOverlay } from './components/TraceOverlay';
import { CanvasPanel } from './components/canvas/CanvasPanel';
import { SettingsPanel } from './components/SettingsPanel';
import { TerminalPanel } from './components/TerminalPanel';
import { useAgentStream } from './hooks/useAgentStream';
import { fetchHealth, fetchAgents, fetchMe, clearToken, getToken } from './api';
import type { AuthUser } from './api';
import type { AgentInfo } from './types';

type Theme = 'light' | 'dark';

function getInitialTheme(): Theme {
  const saved = localStorage.getItem('wick-theme');
  if (saved === 'light' || saved === 'dark') return saved;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export default function App() {
  const [agentId, setAgentId] = useState('');
  const [healthy, setHealthy] = useState<boolean | null>(null);
  const [theme, setTheme] = useState<Theme>(getInitialTheme);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [authChecked, setAuthChecked] = useState(false);
  const [traceOpen, setTraceOpen] = useState(false);
  const [pendingPrompt, setPendingPrompt] = useState<string | undefined>();
  const [canvasWidth, setCanvasWidth] = useState(520);
  const [dragging, setDragging] = useState(false);
  const [canvasCollapsed, setCanvasCollapsed] = useState(false);
  const [canvasFullscreen, setCanvasFullscreen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [chatPopupOpen, setChatPopupOpen] = useState(false);
  const [popupPos, setPopupPos] = useState({ x: 0, y: 0 });
  const [popupSize, setPopupSize] = useState({ w: 380, h: 520 });
  const isDragging = useRef(false);
  const popupDragRef = useRef(false);
  const popupResizeRef = useRef(false);
  const popupPosRef = useRef({ x: 0, y: 0 });
  const popupSizeRef = useRef({ w: 380, h: 520 });

  // ── Auth: validate existing token on mount ──
  useEffect(() => {
    if (!getToken()) {
      // Check if auth is even required by trying /auth/me
      // If gateway is not configured, 501 → treat as no-auth mode
      fetch('/auth/me')
        .then((res) => {
          if (res.status === 501) {
            // Auth not configured — skip login
            setUser({ username: 'local', role: 'admin' });
          }
          // 401 or other → need login
        })
        .catch(() => {
          // Network error calling /auth/me — assume auth not configured
          setUser({ username: 'local', role: 'admin' });
        })
        .finally(() => setAuthChecked(true));
      return;
    }
    fetchMe()
      .then((u) => setUser(u))
      .catch(() => clearToken())
      .finally(() => setAuthChecked(true));
  }, []);

  // Listen for 401 events dispatched by authFetch
  useEffect(() => {
    const handler = () => {
      setUser(null);
    };
    window.addEventListener('wick-auth-expired', handler);
    return () => window.removeEventListener('wick-auth-expired', handler);
  }, []);

  const handleLogout = useCallback(() => {
    clearToken();
    setUser(null);
  }, []);

  const { messages, traceEvents, canvasArtifacts, status, threadId, error, send, stop, reset, updateArtifactContent, removeArtifact } =
    useAgentStream();

  // Auto-expand canvas when first artifact arrives
  useEffect(() => {
    if (canvasArtifacts.length > 0 && canvasCollapsed) {
      setCanvasCollapsed(false);
    }
  }, [canvasArtifacts.length]); // eslint-disable-line react-hooks/exhaustive-deps

  const isActive = status === 'connecting' || status === 'streaming';

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    localStorage.setItem('wick-theme', theme);
  }, [theme]);

  useEffect(() => {
    fetchHealth()
      .then(() => setHealthy(true))
      .catch(() => setHealthy(false));
    // Only fetch agents once authenticated (avoids 401 before login)
    if (!user) return;
    const loadAgents = () => {
      fetchAgents()
        .then((data) => {
          setAgents(data);
          if (data.length > 0 && !agentId) setAgentId(data[0]!.agent_id);
        })
        .catch(() => {});
    };
    loadAgents();

    // SSE listener for container_status changes
    const token = getToken();
    const url = token
      ? `/agents/events?token=${encodeURIComponent(token)}`
      : '/agents/events';
    const es = new EventSource(url);
    es.addEventListener('container_status', loadAgents);
    return () => es.close();
  }, [user]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggleTheme = useCallback(() => {
    setTheme((t) => (t === 'light' ? 'dark' : 'light'));
  }, []);

  const currentAgent = agents.find((a) => a.agent_id === agentId);
  const containerLaunched = currentAgent?.container_status === 'launched';

  // Auto-close terminal when container is no longer launched
  useEffect(() => {
    if (!containerLaunched && terminalOpen) {
      setTerminalOpen(false);
    }
  }, [containerLaunched, terminalOpen]);

  const handleOpenTerminal = useCallback(() => {
    setTerminalOpen(true);
  }, []);

  const handleCloseTerminal = useCallback(() => {
    setTerminalOpen(false);
  }, []);

  const handleSend = (content: string) => {
    send(content, agentId || undefined);
  };

  const handlePromptClick = useCallback((prompt: string) => {
    setPendingPrompt(prompt);
  }, []);

  const handlePromptConsumed = useCallback(() => {
    setPendingPrompt(undefined);
  }, []);

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    isDragging.current = true;
    setDragging(true);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const handleMouseMove = (ev: MouseEvent) => {
      if (!isDragging.current) return;
      const newWidth = window.innerWidth - ev.clientX;
      setCanvasWidth(Math.max(300, Math.min(newWidth, window.innerWidth * 0.7)));
    };

    const handleMouseUp = () => {
      isDragging.current = false;
      setDragging(false);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  }, []);

  const handleResizeKeyDown = useCallback((e: React.KeyboardEvent) => {
    const step = e.shiftKey ? 50 : 20;
    if (e.key === 'ArrowLeft') { e.preventDefault(); setCanvasWidth((w) => Math.min(w + step, window.innerWidth * 0.7)); }
    if (e.key === 'ArrowRight') { e.preventDefault(); setCanvasWidth((w) => Math.max(300, w - step)); }
  }, []);

  const toggleCanvas = useCallback(() => {
    setCanvasCollapsed((c) => !c);
  }, []);

  const toggleCanvasFullscreen = useCallback(() => {
    setCanvasFullscreen((f) => {
      if (!f) setChatPopupOpen(false); // close popup when entering fullscreen
      return !f;
    });
  }, []);

  const toggleChatPopup = useCallback(() => {
    setChatPopupOpen((o) => !o);
  }, []);

  // Reset popup position/size when it opens
  useEffect(() => {
    if (chatPopupOpen) {
      const w = 380;
      const h = 520;
      const pos = {
        x: window.innerWidth - w - 24,
        y: window.innerHeight - h - 84,
      };
      setPopupSize({ w, h });
      setPopupPos(pos);
      popupSizeRef.current = { w, h };
      popupPosRef.current = pos;
    }
  }, [chatPopupOpen]);

  const handlePopupDragStart = useCallback((e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('button')) return;
    e.preventDefault();
    popupDragRef.current = true;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'grabbing';

    const startX = e.clientX;
    const startY = e.clientY;
    const startPos = { ...popupPosRef.current };

    const handleMove = (ev: MouseEvent) => {
      if (!popupDragRef.current) return;
      const newPos = {
        x: Math.max(0, Math.min(startPos.x + (ev.clientX - startX), window.innerWidth - 200)),
        y: Math.max(0, Math.min(startPos.y + (ev.clientY - startY), window.innerHeight - 60)),
      };
      popupPosRef.current = newPos;
      setPopupPos(newPos);
    };

    const handleUp = () => {
      popupDragRef.current = false;
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
      document.removeEventListener('mousemove', handleMove);
      document.removeEventListener('mouseup', handleUp);
    };

    document.addEventListener('mousemove', handleMove);
    document.addEventListener('mouseup', handleUp);
  }, []);

  const handlePopupResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    popupResizeRef.current = true;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'nwse-resize';

    const startX = e.clientX;
    const startY = e.clientY;
    const startSize = { ...popupSizeRef.current };

    const handleMove = (ev: MouseEvent) => {
      if (!popupResizeRef.current) return;
      const newSize = {
        w: Math.max(300, Math.min(startSize.w + (ev.clientX - startX), window.innerWidth - 48)),
        h: Math.max(280, Math.min(startSize.h + (ev.clientY - startY), window.innerHeight - 80)),
      };
      popupSizeRef.current = newSize;
      setPopupSize(newSize);
    };

    const handleUp = () => {
      popupResizeRef.current = false;
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
      document.removeEventListener('mousemove', handleMove);
      document.removeEventListener('mouseup', handleUp);
    };

    document.addEventListener('mousemove', handleMove);
    document.addEventListener('mouseup', handleUp);
  }, []);

  // ── Auth gate ──
  if (!authChecked) {
    return <div className="login-page"><div className="login-card" style={{ textAlign: 'center', color: 'var(--text-muted)' }}>Loading...</div></div>;
  }
  if (!user) {
    return <LoginPage onLoginSuccess={(u) => setUser(u)} />;
  }

  return (
    <div className="app">
      <header className="app-header">
        <div className="app-brand">
          <img src="/logo.png" alt="Wick Agent" className="app-logo" />
          <h1 className="app-title">Wick Agent</h1>
        </div>
        <div className="app-controls">
          <div className="settings-anchor">
            <button
              className={`settings-gear-btn ${settingsOpen ? 'active' : ''}`}
              onClick={() => setSettingsOpen((o) => !o)}
              title="Settings"
              aria-label="Open settings"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>
            <SettingsPanel
              isOpen={settingsOpen}
              onClose={() => setSettingsOpen(false)}
              selectedAgent={agentId}
              onSelectAgent={setAgentId}
              theme={theme}
              onToggleTheme={toggleTheme}
              disabled={isActive}
              onOpenTerminal={handleOpenTerminal}
            />
          </div>
          {containerLaunched && (
            <button
              className={`terminal-toggle-btn ${terminalOpen ? 'active' : ''}`}
              onClick={() => setTerminalOpen((o) => !o)}
              title={terminalOpen ? 'Close terminal' : 'Open terminal'}
              aria-label={terminalOpen ? 'Close terminal' : 'Open terminal'}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="4 17 10 11 4 5" />
                <line x1="12" y1="19" x2="20" y2="19" />
              </svg>
              Terminal
            </button>
          )}
          <button
            className={`canvas-collapse-toggle ${canvasCollapsed ? 'collapsed' : ''}`}
            onClick={toggleCanvas}
            title={canvasCollapsed ? 'Show canvas' : 'Hide canvas'}
            aria-label={canvasCollapsed ? 'Show canvas panel' : 'Hide canvas panel'}
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="2" y="2" width="12" height="12" rx="2" />
              <line x1="9" y1="2" x2="9" y2="14" />
            </svg>
          </button>
          <TraceToggleButton
            eventCount={traceEvents.length}
            isStreaming={isActive}
            isOpen={traceOpen}
            onClick={() => setTraceOpen((o) => !o)}
          />
          <span
            className={`health-dot ${healthy === true ? 'ok' : healthy === false ? 'err' : 'loading'}`}
            title={
              healthy === true
                ? 'Backend connected'
                : healthy === false
                  ? 'Backend unreachable'
                  : 'Checking...'
            }
          />
          {user && user.username !== 'local' && (
            <div className="user-badge">
              <span className="user-badge-name">{user.username}</span>
              <span className="user-badge-role">{user.role}</span>
              <button className="user-badge-logout" onClick={handleLogout} title="Logout">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
                  <polyline points="16 17 21 12 16 7" />
                  <line x1="21" y1="12" x2="9" y2="12" />
                </svg>
              </button>
            </div>
          )}
        </div>
      </header>

      <main className={`app-main ${canvasFullscreen ? 'canvas-fullscreen' : ''}`}>
        {!canvasFullscreen && (
          <ChatPanel
            messages={messages}
            status={status}
            error={error}
            threadId={threadId}
            onSend={handleSend}
            onStop={stop}
            onReset={reset}
            pendingPrompt={pendingPrompt}
            onPromptConsumed={handlePromptConsumed}
          />
        )}
        {!canvasCollapsed && !canvasFullscreen && (
          <div
            className={`resize-handle${dragging ? ' dragging' : ''}`}
            onMouseDown={handleMouseDown}
            onKeyDown={handleResizeKeyDown}
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize panels"
            tabIndex={0}
          />
        )}
        {!canvasCollapsed && (
          <div
            className="canvas-panel-wrapper"
            style={canvasFullscreen ? undefined : { width: canvasWidth }}
          >
            <CanvasPanel
              artifacts={canvasArtifacts}
              onPromptClick={handlePromptClick}
              status={status}
              onContentUpdate={updateArtifactContent}
              onRemoveArtifact={removeArtifact}
              isFullscreen={canvasFullscreen}
              onToggleFullscreen={toggleCanvasFullscreen}
            />
          </div>
        )}
      </main>

      {terminalOpen && agentId && containerLaunched && (
        <TerminalPanel
          agentId={agentId}
          onClose={handleCloseTerminal}
          theme={theme}
        />
      )}

      {/* Floating chat in fullscreen canvas mode */}
      {canvasFullscreen && (
        <>
          {chatPopupOpen && (
            <div
              className="chat-popup"
              style={{
                left: popupPos.x,
                top: popupPos.y,
                width: popupSize.w,
                height: popupSize.h,
              }}
            >
              <div
                className="chat-popup-header"
                onMouseDown={handlePopupDragStart}
              >
                <span className="chat-popup-title">Chat</span>
                <button
                  className="chat-popup-close"
                  onClick={toggleChatPopup}
                  aria-label="Minimize chat"
                >
                  <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                    <line x1="2" y1="10" x2="10" y2="10" />
                  </svg>
                </button>
              </div>
              <ChatPanel
                messages={messages}
                status={status}
                error={error}
                threadId={threadId}
                onSend={handleSend}
                onStop={stop}
                onReset={reset}
                pendingPrompt={pendingPrompt}
                onPromptConsumed={handlePromptConsumed}
              />
              <div
                className="chat-popup-resize-grip"
                onMouseDown={handlePopupResizeStart}
                aria-label="Resize chat window"
              />
            </div>
          )}
          <button
            className={`chat-fab ${chatPopupOpen ? 'open' : ''} ${isActive ? 'active' : ''}`}
            onClick={toggleChatPopup}
            aria-label={chatPopupOpen ? 'Minimize chat' : 'Open chat'}
          >
            {chatPopupOpen ? (
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <line x1="5" y1="5" x2="15" y2="15" />
                <line x1="15" y1="5" x2="5" y2="15" />
              </svg>
            ) : (
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
              </svg>
            )}
            {!chatPopupOpen && isActive && <span className="chat-fab-pulse" />}
          </button>
        </>
      )}

      <TraceOverlay
        events={traceEvents}
        status={status}
        isOpen={traceOpen}
        onClose={() => setTraceOpen(false)}
      />
    </div>
  );
}

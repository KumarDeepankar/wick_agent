import { useState, useEffect, useCallback, useRef } from 'react';
import { AgentSelector } from './components/AgentSelector';
import { ChatPanel } from './components/ChatPanel';
import { TraceToggleButton } from './components/TraceToggleButton';
import { TraceOverlay } from './components/TraceOverlay';
import { CanvasPanel } from './components/canvas/CanvasPanel';
import { useAgentStream } from './hooks/useAgentStream';
import { fetchHealth } from './api';

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
  const [traceOpen, setTraceOpen] = useState(false);
  const [pendingPrompt, setPendingPrompt] = useState<string | undefined>();
  const [canvasWidth, setCanvasWidth] = useState(520);
  const [dragging, setDragging] = useState(false);
  const [canvasCollapsed, setCanvasCollapsed] = useState(false);
  const [canvasFullscreen, setCanvasFullscreen] = useState(false);
  const [chatPopupOpen, setChatPopupOpen] = useState(false);
  const isDragging = useRef(false);

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
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme((t) => (t === 'light' ? 'dark' : 'light'));
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

  return (
    <div className="app">
      <header className="app-header">
        <div className="app-brand">
          <img src="/logo.png" alt="Wick Agent" className="app-logo" />
          <h1 className="app-title">Wick Agent</h1>
        </div>
        <div className="app-controls">
          <AgentSelector
            selected={agentId}
            onSelect={setAgentId}
            disabled={isActive}
          />
          <button
            className="theme-toggle"
            onClick={toggleTheme}
            title={`Switch to ${theme === 'light' ? 'dark' : 'light'} mode`}
            aria-label={`Switch to ${theme === 'light' ? 'dark' : 'light'} mode`}
          >
            {theme === 'light' ? '\u263E' : '\u2600'}
          </button>
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

      {/* Floating chat in fullscreen canvas mode */}
      {canvasFullscreen && (
        <>
          {chatPopupOpen && (
            <div className="chat-popup">
              <div className="chat-popup-header">
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

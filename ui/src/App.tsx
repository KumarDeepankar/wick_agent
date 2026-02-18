import { useState, useEffect, useCallback } from 'react';
import { AgentSelector } from './components/AgentSelector';
import { ChatPanel } from './components/ChatPanel';
import { TracePanel } from './components/TracePanel';
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

  const { messages, traceEvents, status, threadId, error, send, stop, reset } =
    useAgentStream();

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
          >
            {theme === 'light' ? '\u263E' : '\u2600'}
          </button>
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

      <main className="app-main">
        <ChatPanel
          messages={messages}
          status={status}
          error={error}
          threadId={threadId}
          onSend={handleSend}
          onStop={stop}
          onReset={reset}
        />
        <TracePanel events={traceEvents} status={status} />
      </main>
    </div>
  );
}

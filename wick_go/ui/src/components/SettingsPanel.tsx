import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import type { AgentInfo } from '../types';
import { fetchAgents, fetchTools, updateAgentBackend, getToken, fetchHooks, updateAgentHooks } from '../api';
import type { HookInfo, ToolInfo } from '../api';

type Theme = 'light' | 'dark';
type SandboxMode = 'local' | 'remote';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  selectedAgent: string;
  onSelectAgent: (agentId: string) => void;
  theme: Theme;
  onToggleTheme: () => void;
  disabled: boolean;
  onOpenTerminal?: () => void;
}

export function SettingsPanel({
  isOpen,
  onClose,
  selectedAgent,
  onSelectAgent,
  theme,
  onToggleTheme,
  disabled,
  onOpenTerminal,
}: Props) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [allTools, setAllTools] = useState<ToolInfo[]>([]);
  const [allHooks, setAllHooks] = useState<HookInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [toolFilter, setToolFilter] = useState('');
  const [updating, setUpdating] = useState(false);
  // Optimistic hook set — updated instantly on toggle, reconciled with server
  const [optimisticHooks, setOptimisticHooks] = useState<Set<string> | null>(null);
  const [sandboxUrl, setSandboxUrl] = useState('');
  const panelRef = useRef<HTMLDivElement>(null);

  const loadData = useCallback(() => {
    setLoading(true);
    Promise.all([fetchAgents(), fetchTools(), fetchHooks()])
      .then(([agentsData, toolsData, hooksData]) => {
        setAgents(agentsData);
        setAllTools(toolsData);
        setAllHooks(hooksData);
        if (agentsData.length > 0 && !selectedAgent) {
          onSelectAgent(agentsData[0]!.agent_id);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (isOpen) loadData();
  }, [isOpen, loadData]);

  // Refresh agents + tools + hooks — fetches are independent so one failure
  // doesn't block the other from updating.
  const refreshData = useCallback(() => {
    setOptimisticHooks(null);
    fetchAgents()
      .then((agentsData) => setAgents(agentsData))
      .catch(() => {});
    fetchTools()
      .then((toolsData) => setAllTools(toolsData))
      .catch(() => {});
    fetchHooks()
      .then((hooksData) => setAllHooks(hooksData))
      .catch(() => {});
  }, []);

  // SSE for instant updates + 5s polling as reliable fallback
  useEffect(() => {
    if (!isOpen) return;

    // SSE — fires instantly when gateway config changes
    const token = getToken();
    const url = token
      ? `/agents/events?token=${encodeURIComponent(token)}`
      : '/agents/events';
    const es = new EventSource(url);
    es.addEventListener('config_changed', refreshData);
    es.addEventListener('container_status', refreshData);

    // Polling — reliable fallback, same interval as the original
    const interval = setInterval(refreshData, 5_000);

    return () => {
      es.close();
      clearInterval(interval);
    };
  }, [isOpen, refreshData]);

  // Close on click outside
  useEffect(() => {
    if (!isOpen) return;
    const handler = (e: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    const timer = setTimeout(() => document.addEventListener('mousedown', handler), 0);
    return () => {
      clearTimeout(timer);
      document.removeEventListener('mousedown', handler);
    };
  }, [isOpen, onClose]);

  // Close on Escape
  useEffect(() => {
    if (!isOpen) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [isOpen, onClose]);

  const currentAgent = agents.find((a) => a.agent_id === selectedAgent);

  // Tools grouped by source for read-only display
  const filteredTools = useMemo(() => {
    if (!toolFilter.trim()) return allTools;
    const q = toolFilter.toLowerCase();
    return allTools.filter((t) => t.name.toLowerCase().includes(q));
  }, [allTools, toolFilter]);

  const toolsBySource = useMemo(() => {
    const groups: Record<string, ToolInfo[]> = {};
    for (const tool of filteredTools) {
      const src = tool.source;
      if (!groups[src]) groups[src] = [];
      groups[src].push(tool);
    }
    return groups;
  }, [filteredTools]);

  // ── Hooks ──────────────────────────────────────────────────────────
  const serverHooks = useMemo(() => new Set(currentAgent?.hooks ?? []), [currentAgent]);
  const agentHooks = optimisticHooks ?? serverHooks;

  // Clear optimistic state when server state catches up
  useEffect(() => {
    setOptimisticHooks(null);
  }, [serverHooks]);

  const handleToggleHook = useCallback(
    async (hookName: string) => {
      if (!currentAgent || updating || disabled) return;
      const isActive = agentHooks.has(hookName);
      const next = new Set(agentHooks);
      if (isActive) {
        next.delete(hookName);
      } else {
        next.add(hookName);
      }

      setOptimisticHooks(next);
      setUpdating(true);

      try {
        const payload = isActive
          ? { remove: [hookName] }
          : { add: [hookName] };
        const result = await updateAgentHooks(currentAgent.agent_id, payload);
        setAgents((prev) =>
          prev.map((a) =>
            a.agent_id === currentAgent.agent_id
              ? { ...a, hooks: result.hooks }
              : a,
          ),
        );
      } catch {
        setOptimisticHooks(null);
        loadData();
      } finally {
        setUpdating(false);
      }
    },
    [currentAgent, updating, disabled, agentHooks, loadData],
  );

  // ── Sandbox mode ────────────────────────────────────────────────────
  const isSandboxAgent = currentAgent?.backend_type === 'docker' || currentAgent?.backend_type === 'local';
  const sandboxMode: SandboxMode = currentAgent?.backend_type === 'local' ? 'local' : 'remote';

  // Sync sandbox URL from server state
  useEffect(() => {
    setSandboxUrl(currentAgent?.sandbox_url ?? '');
  }, [currentAgent?.sandbox_url]);

  const handleModeSwitch = useCallback(async (mode: SandboxMode) => {
    if (!currentAgent || disabled || updating) return;
    if (mode === sandboxMode) return;
    setUpdating(true);
    try {
      if (mode === 'local') {
        await updateAgentBackend(currentAgent.agent_id, { mode: 'local' });
      } else {
        await updateAgentBackend(currentAgent.agent_id, {
          mode: 'remote',
          sandbox_url: sandboxUrl.trim() || null,
        });
      }
      loadData();
    } catch {
      // revert — loadData will refresh
      loadData();
    } finally {
      setUpdating(false);
    }
  }, [currentAgent, disabled, updating, sandboxMode, sandboxUrl, loadData]);

  const handleSaveSandboxUrl = useCallback(async () => {
    if (!currentAgent || disabled || sandboxMode !== 'remote') return;
    const newUrl = sandboxUrl.trim() || null;
    if (newUrl === (currentAgent.sandbox_url ?? null)) return; // no change
    setUpdating(true);
    try {
      await updateAgentBackend(currentAgent.agent_id, {
        mode: 'remote',
        sandbox_url: newUrl,
      });
      loadData();
    } catch {
      setSandboxUrl(currentAgent.sandbox_url ?? ''); // revert
    } finally {
      setUpdating(false);
    }
  }, [currentAgent, sandboxUrl, disabled, sandboxMode, loadData]);

  if (!isOpen) return null;

  return (
    <div className="settings-panel" ref={panelRef}>
      <div className="settings-header">
        <h2 className="settings-title">Settings</h2>
        <button className="settings-close" onClick={onClose} aria-label="Close settings">
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="2" y1="2" x2="12" y2="12" />
            <line x1="12" y1="2" x2="2" y2="12" />
          </svg>
        </button>
      </div>

      {loading ? (
        <div className="settings-loading">Loading...</div>
      ) : (
        <div className="settings-body">
          {/* Agent / Model Selection */}
          <section className="settings-section">
            <label className="settings-label">Agent</label>
            <select
              className="settings-select"
              value={selectedAgent}
              onChange={(e) => onSelectAgent(e.target.value)}
              disabled={disabled}
            >
              {agents.map((a) => (
                <option key={a.agent_id} value={a.agent_id}>
                  {a.name ?? a.agent_id} ({a.model})
                </option>
              ))}
            </select>
            {currentAgent && (
              <span className="settings-hint">Model: {currentAgent.model}</span>
            )}
          </section>

          {/* Sandbox Mode (local/docker agents only) */}
          {isSandboxAgent && (
            <section className="settings-section">
              <label className="settings-label">Execution Mode</label>
              <div className="settings-theme-toggle">
                <button
                  className={`settings-theme-btn ${sandboxMode === 'local' ? 'active' : ''}`}
                  onClick={() => handleModeSwitch('local')}
                  disabled={updating || disabled}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
                    <line x1="8" y1="21" x2="16" y2="21" />
                    <line x1="12" y1="17" x2="12" y2="21" />
                  </svg>
                  Local
                </button>
                <button
                  className={`settings-theme-btn ${sandboxMode === 'remote' ? 'active' : ''}`}
                  onClick={() => handleModeSwitch('remote')}
                  disabled={updating || disabled}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
                    <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
                    <line x1="6" y1="6" x2="6.01" y2="6" />
                    <line x1="6" y1="18" x2="6.01" y2="18" />
                  </svg>
                  Remote
                </button>
              </div>
              <span className="settings-hint">
                {sandboxMode === 'local'
                  ? 'Commands run directly on the host machine.'
                  : 'Commands run in a Docker container.'}
              </span>

              {/* Remote Docker URL input */}
              {sandboxMode === 'remote' && (
                <>
                  <label className="settings-label" style={{ marginTop: '8px' }}>Docker Host</label>
                  <input
                    className="settings-filter"
                    type="text"
                    placeholder="tcp://192.168.1.50:2375"
                    value={sandboxUrl}
                    onChange={(e) => setSandboxUrl(e.target.value)}
                    onBlur={handleSaveSandboxUrl}
                    onKeyDown={(e) => e.key === 'Enter' && handleSaveSandboxUrl()}
                    disabled={updating || disabled}
                    style={{ marginBottom: 0 }}
                  />
                  <span className="settings-hint">
                    Optional. Remote Docker daemon URL (e.g. tcp://host:2375). Leave empty for local Docker.
                  </span>

                  {/* Container status indicator */}
                  {currentAgent && currentAgent.backend_type === 'docker' && (
                    <div className={`container-status container-status--${currentAgent.container_status ?? 'idle'}`}>
                      <span className="container-status-dot" />
                      <span className="container-status-text">
                        {(!currentAgent.container_status || currentAgent.container_status === 'idle') && 'No container'}
                        {currentAgent.container_status === 'launching' && 'Launching container...'}
                        {currentAgent.container_status === 'launched' && 'Container running'}
                        {currentAgent.container_status === 'error' && (currentAgent.container_error || 'Container error')}
                      </span>
                      {currentAgent.container_status === 'launched' && onOpenTerminal && (
                        <button
                          className="container-terminal-btn"
                          onClick={onOpenTerminal}
                          title="Open terminal"
                        >
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <polyline points="4 17 10 11 4 5" />
                            <line x1="12" y1="19" x2="20" y2="19" />
                          </svg>
                          Terminal
                        </button>
                      )}
                    </div>
                  )}
                </>
              )}
            </section>
          )}

          {/* Tools (read-only — controlled via hooks) */}
          <section className="settings-section">
            <div className="settings-tools-header">
              <label className="settings-label">
                Tools
                <span className="settings-tools-count">{allTools.length}</span>
              </label>
            </div>
            <input
              className="settings-filter"
              type="text"
              placeholder="Filter tools..."
              value={toolFilter}
              onChange={(e) => setToolFilter(e.target.value)}
            />
            <div className="settings-tools-list">
              {filteredTools.length === 0 && (
                <span className="settings-hint">
                  {allTools.length === 0 ? 'No tools available' : 'No matching tools'}
                </span>
              )}
              {Object.entries(toolsBySource).map(([source, tools]) => (
                <div key={source} className="settings-tool-group">
                  <span className="settings-tool-group-label">{source}</span>
                  {tools.map((tool) => {
                    const isHookTool = tool.source !== 'builtin';
                    const hookActive = isHookTool ? agentHooks.has(tool.source) : true;
                    return (
                      <div
                        key={tool.name}
                        className={`settings-tool-row ${hookActive ? 'active' : 'inactive'}`}
                      >
                        <div className="settings-tool-info">
                          {isHookTool && (
                            <span className="settings-tool-badge">system</span>
                          )}
                          <span className="settings-tool-name">{tool.name}</span>
                        </div>
                        {!hookActive && (
                          <span className="settings-tool-disabled-hint">
                            hook off
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              ))}
            </div>
          </section>

          {/* Hooks */}
          <section className="settings-section">
            <div className="settings-tools-header">
              <label className="settings-label">
                Hooks
                <span className="settings-tools-count">
                  {allHooks.filter((h) => agentHooks.has(h.name)).length}/{allHooks.length}
                </span>
              </label>
            </div>
            <div className="settings-tools-list">
              {allHooks.length === 0 && (
                <span className="settings-hint">No hooks available</span>
              )}
              {allHooks.map((hook) => {
                const active = agentHooks.has(hook.name);
                return (
                  <div key={hook.name} className="settings-hook-entry">
                    <label
                      className={`settings-tool-row ${active ? 'active' : 'inactive'}`}
                      title={hook.description}
                    >
                      <div className="settings-tool-info">
                        <span className="settings-hook-phases">
                          {hook.phases.length}p
                        </span>
                        <span className="settings-tool-name">{hook.name}</span>
                      </div>
                      <button
                        className={`settings-tool-toggle ${active ? 'on' : 'off'}`}
                        onClick={() => handleToggleHook(hook.name)}
                        disabled={updating || disabled}
                        aria-label={`${active ? 'Disable' : 'Enable'} ${hook.name} hook`}
                      >
                        <span className="settings-toggle-knob" />
                      </button>
                    </label>
                    {hook.tools.length > 0 && (
                      <span className="settings-hook-tools-hint">
                        provides: {hook.tools.join(', ')}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
            {currentAgent && agentHooks.size > 0 && (
              <span className="settings-hint">
                Active: {[...agentHooks].join(' \u2192 ')}
              </span>
            )}
          </section>

          {/* Color Mode */}
          <section className="settings-section">
            <label className="settings-label">Color Mode</label>
            <div className="settings-theme-toggle">
              <button
                className={`settings-theme-btn ${theme === 'light' ? 'active' : ''}`}
                onClick={() => theme !== 'light' && onToggleTheme()}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="12" cy="12" r="5" />
                  <line x1="12" y1="1" x2="12" y2="3" />
                  <line x1="12" y1="21" x2="12" y2="23" />
                  <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
                  <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
                  <line x1="1" y1="12" x2="3" y2="12" />
                  <line x1="21" y1="12" x2="23" y2="12" />
                  <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
                  <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
                </svg>
                Light
              </button>
              <button
                className={`settings-theme-btn ${theme === 'dark' ? 'active' : ''}`}
                onClick={() => theme !== 'dark' && onToggleTheme()}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
                </svg>
                Dark
              </button>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

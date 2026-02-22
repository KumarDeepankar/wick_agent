import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import type { AgentInfo } from '../types';
import { fetchAgents, fetchTools, updateAgentTools } from '../api';

type Theme = 'light' | 'dark';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  selectedAgent: string;
  onSelectAgent: (agentId: string) => void;
  theme: Theme;
  onToggleTheme: () => void;
  disabled: boolean;
}

export function SettingsPanel({
  isOpen,
  onClose,
  selectedAgent,
  onSelectAgent,
  theme,
  onToggleTheme,
  disabled,
}: Props) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [allTools, setAllTools] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [toolFilter, setToolFilter] = useState('');
  const [updating, setUpdating] = useState(false);
  // Optimistic tool set — updated instantly on toggle, reconciled with server
  const [optimisticTools, setOptimisticTools] = useState<Set<string> | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const loadData = useCallback(() => {
    setLoading(true);
    Promise.all([fetchAgents(), fetchTools()])
      .then(([agentsData, toolsData]) => {
        setAgents(agentsData);
        setAllTools(toolsData);
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
  const serverTools = useMemo(() => new Set(currentAgent?.tools ?? []), [currentAgent]);
  // Use optimistic state for instant visual feedback, fall back to server state
  const agentTools = optimisticTools ?? serverTools;

  // Clear optimistic state when server state catches up
  useEffect(() => {
    setOptimisticTools(null);
  }, [serverTools]);

  const filteredTools = useMemo(() => {
    if (!toolFilter.trim()) return allTools;
    const q = toolFilter.toLowerCase();
    return allTools.filter((t) => t.toLowerCase().includes(q));
  }, [allTools, toolFilter]);

  const handleToggleTool = useCallback(
    async (tool: string) => {
      if (!currentAgent || updating || disabled) return;
      const next = new Set(agentTools);
      if (next.has(tool)) {
        next.delete(tool);
      } else {
        next.add(tool);
      }

      // Optimistic: update toggle position immediately
      setOptimisticTools(next);
      setUpdating(true);

      try {
        const result = await updateAgentTools(currentAgent.agent_id, [...next]);
        setAgents((prev) =>
          prev.map((a) =>
            a.agent_id === currentAgent.agent_id
              ? { ...a, tools: result.tools }
              : a,
          ),
        );
      } catch {
        // Revert optimistic state on failure
        setOptimisticTools(null);
        loadData();
      } finally {
        setUpdating(false);
      }
    },
    [currentAgent, updating, disabled, agentTools, loadData],
  );

  if (!isOpen) return null;

  const activeCount = agentTools.size;
  const totalCount = allTools.length;

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

          {/* Tools */}
          <section className="settings-section">
            <div className="settings-tools-header">
              <label className="settings-label">
                Tools
                <span className="settings-tools-count">{activeCount}/{totalCount}</span>
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
              {filteredTools.map((tool) => {
                const active = agentTools.has(tool);
                const isMcp = tool.startsWith('mcp_');
                return (
                  <label
                    key={tool}
                    className={`settings-tool-row ${active ? 'active' : 'inactive'}`}
                  >
                    <div className="settings-tool-info">
                      {isMcp && <span className="settings-tool-badge">MCP</span>}
                      <span className="settings-tool-name">{tool}</span>
                    </div>
                    <button
                      className={`settings-tool-toggle ${active ? 'on' : 'off'}`}
                      onClick={() => handleToggleTool(tool)}
                      disabled={updating || disabled}
                      aria-label={`${active ? 'Disable' : 'Enable'} ${tool}`}
                    >
                      <span className="settings-toggle-knob" />
                    </button>
                  </label>
                );
              })}
            </div>
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

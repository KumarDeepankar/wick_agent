import { useState, useEffect, useCallback } from 'react';
import type { AgentInfo } from '../types';
import { fetchAgents } from '../api';

interface Props {
  selected: string;
  onSelect: (agentId: string) => void;
  disabled: boolean;
}

export function AgentSelector({ selected, onSelect, disabled }: Props) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadAgents = useCallback(() => {
    setLoading(true);
    setError(null);
    fetchAgents()
      .then((data) => {
        setAgents(data);
        if (data.length > 0 && !selected) {
          onSelect(data[0]!.agent_id);
        }
      })
      .catch((err) => {
        setError(err.message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    loadAgents();
  }, [loadAgents]);

  if (loading) return <span className="agent-selector-loading">Loading agents...</span>;
  if (error) {
    return (
      <span className="agent-selector-error">
        Failed to load agents
        <button className="agent-retry-btn" onClick={loadAgents} aria-label="Retry loading agents">
          Retry
        </button>
      </span>
    );
  }
  if (agents.length === 0) return <span className="agent-selector-empty">No agents found</span>;

  return (
    <select
      className="agent-selector"
      value={selected}
      onChange={(e) => onSelect(e.target.value)}
      disabled={disabled}
      aria-label="Select agent"
    >
      {agents.map((a) => (
        <option key={a.agent_id} value={a.agent_id}>
          {a.name ?? a.agent_id} ({a.model})
        </option>
      ))}
    </select>
  );
}

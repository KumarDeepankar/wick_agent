import { useState, useEffect } from 'react';
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

  useEffect(() => {
    let cancelled = false;
    fetchAgents()
      .then((data) => {
        if (!cancelled) {
          setAgents(data);
          if (data.length > 0 && !selected) {
            onSelect(data[0]!.agent_id);
          }
        }
      })
      .catch((err) => {
        if (!cancelled) setError(err.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) return <span className="agent-selector-loading">Loading agents...</span>;
  if (error) return <span className="agent-selector-error">Error: {error}</span>;
  if (agents.length === 0) return <span className="agent-selector-empty">No agents found</span>;

  return (
    <select
      className="agent-selector"
      value={selected}
      onChange={(e) => onSelect(e.target.value)}
      disabled={disabled}
    >
      {agents.map((a) => (
        <option key={a.agent_id} value={a.agent_id}>
          {a.name ?? a.agent_id} ({a.model})
        </option>
      ))}
    </select>
  );
}

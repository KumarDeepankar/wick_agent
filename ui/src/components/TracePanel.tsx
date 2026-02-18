import { useState, useRef, useEffect } from 'react';
import type { TraceEvent, StreamStatus } from '../types';
import { TraceEventCard } from './TraceEvent';

interface Props {
  events: TraceEvent[];
  status: StreamStatus;
}

const EVENT_CATEGORIES: Record<string, string[]> = {
  all: [],
  setup: ['agent_start', 'input_prompt', 'files_seeded'],
  llm: ['on_chat_model_start', 'on_chat_model_stream', 'on_chat_model_end'],
  tools: ['on_tool_start', 'on_tool_end'],
  chain: ['on_chain_start', 'on_chain_end', 'on_chain_stream'],
  status: ['done', 'error'],
};

export function TracePanel({ events, status }: Props) {
  const [filter, setFilter] = useState('all');
  const [autoScroll, setAutoScroll] = useState(true);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered =
    filter === 'all'
      ? events
      : events.filter((e) => EVENT_CATEGORIES[filter]?.includes(e.eventType));

  useEffect(() => {
    if (autoScroll && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [filtered.length, autoScroll]);

  const handleScroll = () => {
    const el = listRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setAutoScroll(atBottom);
  };

  return (
    <div className="trace-panel">
      <div className="trace-header">
        <span className="trace-title">
          Trace Events ({filtered.length}/{events.length})
        </span>
        {(status === 'streaming' || status === 'connecting') && (
          <span className="trace-live">LIVE</span>
        )}
      </div>

      <div className="trace-filters">
        {Object.keys(EVENT_CATEGORIES).map((cat) => (
          <button
            key={cat}
            className={`trace-filter-btn ${filter === cat ? 'active' : ''}`}
            onClick={() => setFilter(cat)}
          >
            {cat}
          </button>
        ))}
      </div>

      <div className="trace-list" ref={listRef} onScroll={handleScroll}>
        {filtered.length === 0 && (
          <div className="trace-empty">No events yet</div>
        )}
        {filtered.map((e) => (
          <TraceEventCard key={e.id} event={e} />
        ))}
      </div>
    </div>
  );
}

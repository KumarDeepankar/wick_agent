import { useState, useRef, useEffect, useMemo } from 'react';
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

const FILTER_LABELS: Record<string, string> = {
  all: 'All',
  setup: 'Setup',
  llm: 'LLM',
  tools: 'Tools',
  chain: 'Chain',
  status: 'Status',
};

interface DisplayItem {
  type: 'event' | 'collapsed';
  event?: TraceEvent;
  count?: number;
  id: string;
}

/** Collapse consecutive stream token events into a single summary row */
function collapseStreamTokens(events: TraceEvent[]): DisplayItem[] {
  const items: DisplayItem[] = [];
  let streamRun = 0;
  let streamStartId = '';

  for (let i = 0; i < events.length; i++) {
    const e = events[i]!;
    if (e.eventType === 'on_chat_model_stream') {
      if (streamRun === 0) streamStartId = e.id;
      streamRun++;
    } else {
      if (streamRun > 0) {
        if (streamRun <= 3) {
          // Show individual events if only a few
          for (let j = i - streamRun; j < i; j++) {
            items.push({ type: 'event', event: events[j]!, id: events[j]!.id });
          }
        } else {
          items.push({ type: 'collapsed', count: streamRun, id: `collapsed-${streamStartId}` });
        }
        streamRun = 0;
      }
      items.push({ type: 'event', event: e, id: e.id });
    }
  }
  // Flush trailing stream tokens
  if (streamRun > 0) {
    if (streamRun <= 3) {
      for (let j = events.length - streamRun; j < events.length; j++) {
        items.push({ type: 'event', event: events[j]!, id: events[j]!.id });
      }
    } else {
      items.push({ type: 'collapsed', count: streamRun, id: `collapsed-${streamStartId}` });
    }
  }
  return items;
}

const TRACE_PAGE_SIZE = 500;

export function TracePanel({ events, status }: Props) {
  const [filter, setFilter] = useState('all');
  const [autoScroll, setAutoScroll] = useState(true);
  const [page, setPage] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered = useMemo(() =>
    filter === 'all'
      ? events
      : events.filter((e) => EVENT_CATEGORIES[filter]?.includes(e.eventType)),
    [events, filter],
  );

  const displayItems = useMemo(() => collapseStreamTokens(filtered), [filtered]);

  const totalPages = Math.ceil(displayItems.length / TRACE_PAGE_SIZE);
  // In auto-scroll mode, show last page; otherwise show selected page
  const effectivePage = autoScroll ? Math.max(0, totalPages - 1) : page;
  const pageItems = displayItems.slice(
    effectivePage * TRACE_PAGE_SIZE,
    (effectivePage + 1) * TRACE_PAGE_SIZE,
  );

  useEffect(() => {
    if (autoScroll && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [displayItems.length, autoScroll]);

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
            {FILTER_LABELS[cat] ?? cat}
          </button>
        ))}
      </div>

      <div className="trace-list" ref={listRef} onScroll={handleScroll}>
        {displayItems.length === 0 && (
          <div className="trace-empty">No events yet</div>
        )}
        {totalPages > 1 && (
          <div className="trace-pagination">
            <button
              className="data-page-btn"
              onClick={() => { setAutoScroll(false); setPage(Math.max(0, effectivePage - 1)); }}
              disabled={effectivePage === 0}
            >
              Prev
            </button>
            <span className="data-page-info">{effectivePage + 1} / {totalPages}</span>
            <button
              className="data-page-btn"
              onClick={() => { setPage(Math.min(totalPages - 1, effectivePage + 1)); if (effectivePage + 1 >= totalPages - 1) setAutoScroll(true); }}
              disabled={effectivePage >= totalPages - 1}
            >
              Next
            </button>
          </div>
        )}
        {pageItems.map((item) =>
          item.type === 'event' ? (
            <TraceEventCard key={item.id} event={item.event!} />
          ) : (
            <div key={item.id} className="trace-collapsed-tokens">
              {item.count} stream tokens (collapsed)
            </div>
          ),
        )}
      </div>
    </div>
  );
}

import { useState, useCallback } from 'react';
import type { TraceEvent as TraceEventType } from '../types';

interface Props {
  event: TraceEventType;
}

const EVENT_COLORS: Record<string, string> = {
  on_chat_model_start: '#3fb950',
  on_chat_model_stream: '#3fb950',
  on_chat_model_end: '#3fb950',
  on_tool_start: '#d29922',
  on_tool_end: '#d29922',
  on_chain_start: '#bc8cff',
  on_chain_end: '#bc8cff',
  on_chain_stream: '#bc8cff',
  done: '#39d3c5',
  error: '#f85149',
};

function getColor(eventType: string): string {
  return EVENT_COLORS[eventType] ?? '#8b949e';
}

function getSummary(event: TraceEventType): string {
  const d = event.data;
  const name = d.name as string ?? '';

  switch (event.eventType) {
    case 'on_chat_model_start':
      return `${name}`;
    case 'on_chat_model_stream':
      return ((d.data as Record<string, unknown>)?.chunk as Record<string, unknown>)?.content as string || '[chunk]';
    case 'on_chat_model_end':
      return `${name}`;
    case 'on_tool_start':
      return `${name}`;
    case 'on_tool_end':
      return `${name}`;
    case 'on_chain_start':
    case 'on_chain_end':
    case 'on_chain_stream':
      return `${name}`;
    case 'done':
      return `Thread: ${((d.thread_id as string) ?? '').slice(0, 8)}... | ${d.total_duration_ms}ms`;
    case 'error':
      return d.error as string;
    default:
      return name || event.eventType;
  }
}

/** Copy button with brief "Copied!" feedback */
function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      navigator.clipboard.writeText(text).then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      });
    },
    [text],
  );

  return (
    <button
      className={`copy-btn ${copied ? 'copied' : ''}`}
      onClick={handleCopy}
      title={label}
    >
      {copied ? 'Copied!' : label}
    </button>
  );
}

export function TraceEventCard({ event }: Props) {
  const [expanded, setExpanded] = useState(false);
  const color = getColor(event.eventType);

  // Token stream events: not expandable (too noisy)
  const isStreamToken = event.eventType === 'on_chat_model_stream';

  const jsonText = JSON.stringify(event.data, null, 2);

  return (
    <div
      className="trace-event-card"
      style={{ borderLeftColor: color }}
      onClick={() => !isStreamToken && setExpanded(!expanded)}
    >
      <div className="trace-event-header">
        <span className="trace-event-type" style={{ color }}>
          {event.eventType}
        </span>
        {!isStreamToken && (
          <span className="trace-expand-hint">
            {expanded ? '▾' : '▸'}
          </span>
        )}
        <span className="trace-event-time">
          {new Date(event.timestamp).toLocaleTimeString()}
        </span>
        <CopyButton text={jsonText} label="Copy" />
      </div>
      <div className="trace-event-summary">{getSummary(event)}</div>

      {expanded && !isStreamToken && (
        <pre className="trace-event-data">{jsonText}</pre>
      )}
    </div>
  );
}

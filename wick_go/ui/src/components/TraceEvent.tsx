import { useState, useCallback, useMemo } from 'react';
import type { TraceEvent as TraceEventType } from '../types';

interface Props {
  event: TraceEventType;
}

const EVENT_VAR_MAP: Record<string, string> = {
  on_chat_model_start: 'var(--trace-llm)',
  on_chat_model_stream: 'var(--trace-llm)',
  on_chat_model_end: 'var(--trace-llm)',
  on_tool_start: 'var(--trace-tool)',
  on_tool_end: 'var(--trace-tool)',
  on_chain_start: 'var(--trace-chain)',
  on_chain_end: 'var(--trace-chain)',
  on_chain_stream: 'var(--trace-chain)',
  done: 'var(--trace-done)',
  error: 'var(--trace-error)',
};

function getColor(eventType: string): string {
  return EVENT_VAR_MAP[eventType] ?? 'var(--trace-default)';
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
      }).catch(() => {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
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

  // Defer JSON serialization until needed
  const jsonText = useMemo(() => {
    return JSON.stringify(event.data, null, 2);
  }, [event.data]);

  const handleToggle = useCallback(() => {
    if (!isStreamToken) setExpanded((e) => !e);
  }, [isStreamToken]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleToggle();
    }
  }, [handleToggle]);

  return (
    <div
      className="trace-event-card"
      style={{ borderLeftColor: color }}
      onClick={handleToggle}
      onKeyDown={!isStreamToken ? handleKeyDown : undefined}
      role={!isStreamToken ? 'button' : undefined}
      tabIndex={!isStreamToken ? 0 : undefined}
      aria-expanded={!isStreamToken ? expanded : undefined}
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

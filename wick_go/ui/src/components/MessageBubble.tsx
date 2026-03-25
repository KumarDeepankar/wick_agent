import { useMemo, useState, useCallback } from 'react';
import { Marked } from 'marked';
import hljs from 'highlight.js';
import type { ChatMessage, StreamStatus, Iteration, ToolCallInfo } from '../types';

const renderer = new Marked({
  breaks: true,
  gfm: true,
  renderer: {
    code({ text, lang }: { text: string; lang?: string }) {
      let highlighted: string;
      if (lang && hljs.getLanguage(lang)) {
        highlighted = hljs.highlight(text, { language: lang }).value;
      } else {
        highlighted = hljs.highlightAuto(text).value;
      }
      return `<pre><code class="hljs">${highlighted}</code></pre>`;
    },
  },
});

function renderMarkdown(text: string): string {
  return renderer.parse(text, { async: false }) as string;
}

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function ToolIcon({ status }: { status: string }) {
  if (status === 'running') {
    return <span className="tool-spinner" />;
  }
  if (status === 'error') {
    return (
      <span className="tool-status-icon error">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" />
          <path d="M5.5 5.5l5 5M10.5 5.5l-5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      </span>
    );
  }
  return (
    <span className="tool-status-icon success">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
        <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" />
        <path d="M5 8.5l2 2 4-4.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </span>
  );
}

function formatArgs(args: Record<string, unknown> | null): string {
  if (!args) return '';
  const str = JSON.stringify(args);
  return str.length > 120 ? str.slice(0, 120) + '\u2026' : str;
}

function ToolCallCard({ tool }: { tool: ToolCallInfo }) {
  const [expanded, setExpanded] = useState(false);
  const isRunning = tool.status === 'running';
  const canExpand = !isRunning && !!tool.output;

  return (
    <div className={`tool-call-card ${tool.status}`}>
      <div className="tool-call-header" onClick={() => canExpand && setExpanded(!expanded)}>
        <ToolIcon status={tool.status} />
        <span className="tool-call-name">{tool.name}</span>
        {tool.args && <span className="tool-call-args">{formatArgs(tool.args)}</span>}
        {canExpand && (
          <span className={`tool-call-chevron ${expanded ? 'open' : ''}`}>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
              <path d="M3 4.5l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </span>
        )}
      </div>
      {expanded && tool.output && (
        <pre className="tool-output-content">{tool.output}</pre>
      )}
    </div>
  );
}

/** Spinner shown at the bottom of the message while the agent is working */
function AgentActivityIndicator({ iteration }: { iteration: Iteration | undefined }) {
  if (!iteration) return null;
  const st = iteration.status;
  if (st === 'done') return null;

  const label =
    st === 'thinking' ? 'Thinking...' :
    st === 'streaming' ? 'Generating...' :
    st === 'tool_running' ? 'Running tools...' :
    'Working...';

  return (
    <div className="agent-activity">
      <span className="agent-activity-spinner">
        <span className="spinner-dot dot-1" />
        <span className="spinner-dot dot-2" />
        <span className="spinner-dot dot-3" />
      </span>
      <span className="agent-activity-label">{label}</span>
    </div>
  );
}

function IterationGroup({ iteration, isFinal, isLast, isStreaming }: {
  iteration: Iteration;
  isFinal: boolean;
  isLast: boolean;
  isStreaming: boolean;
}) {
  const [reasoningExpanded, setReasoningExpanded] = useState(false);
  const html = useMemo(() => {
    if (!iteration.content) return '';
    return renderMarkdown(iteration.content);
  }, [iteration.content]);

  const hasTools = iteration.toolCalls.length > 0;
  const isIntermediate = !isFinal;

  return (
    <div className={`iteration-group ${isIntermediate ? 'intermediate' : 'final'}`}>
      {iteration.content && isIntermediate && hasTools ? (
        <div className="iteration-reasoning-block">
          <button
            className="iteration-reasoning-toggle"
            onClick={() => setReasoningExpanded(!reasoningExpanded)}
          >
            <span className={`iteration-reasoning-chevron ${reasoningExpanded ? 'open' : ''}`}>
              <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
                <path d="M4.5 3l3 3-3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </span>
            <span className="iteration-reasoning-label">Reasoning</span>
          </button>
          <div className={`iteration-reasoning-content ${reasoningExpanded ? 'expanded' : ''}`}>
            <div className="iteration-text has-reasoning">
              <span dangerouslySetInnerHTML={{ __html: html }} />
            </div>
          </div>
        </div>
      ) : iteration.content ? (
        <div className="iteration-text">
          <span dangerouslySetInnerHTML={{ __html: html }} />
          {isLast && isStreaming && iteration.status === 'streaming' && (
            <span className="streaming-cursor" />
          )}
        </div>
      ) : null}
      {hasTools && (
        <div className="tool-cards">
          {iteration.toolCalls.map((tc) => (
            <ToolCallCard key={tc.id} tool={tc} />
          ))}
        </div>
      )}
    </div>
  );
}

interface Props {
  message: ChatMessage;
  isStreaming: boolean;
  status: StreamStatus;
}

export function MessageBubble({ message, isStreaming, status }: Props) {
  const [copied, setCopied] = useState(false);
  const isUser = message.role === 'user';
  const isActiveStream = isStreaming && status === 'streaming';
  const isConnecting = isStreaming && status === 'connecting';

  const iterations = message.iterations ?? [];
  const hasIterations = iterations.length > 0;
  const lastIter = hasIterations ? iterations[iterations.length - 1] : undefined;

  const renderedHtml = useMemo(() => {
    if (hasIterations || !message.content || isUser) return '';
    return renderMarkdown(message.content);
  }, [message.content, isUser, hasIterations]);

  const handleCopy = useCallback(() => {
    if (!message.content) return;
    navigator.clipboard.writeText(message.content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }).catch(() => {
      const ta = document.createElement('textarea');
      ta.value = message.content;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }, [message.content]);

  if (isUser) {
    return (
      <div className="message-row user">
        <div className="message-bubble user">
          <div className="message-content">{message.content}</div>
        </div>
      </div>
    );
  }

  return (
    <div className="message-row assistant">
      <div className="message-avatar">
        <img src="/logo.png" alt="Wick" width="20" height="20" />
      </div>
      <div className={`message-bubble assistant ${isActiveStream ? 'streaming' : ''}`}>
        <div className="message-meta">
          <span className="message-role">Assistant</span>
          <span className="message-time">{formatTime(message.timestamp)}</span>
          {message.content && (
            <button
              className={`message-copy-btn ${copied ? 'copied' : ''}`}
              onClick={handleCopy}
              aria-label="Copy message"
            >
              {copied ? 'Copied' : 'Copy'}
            </button>
          )}
        </div>
        <div className="message-content">
          {hasIterations ? (
            <>
              {iterations.map((iter, i) => {
                const isFinal = i === iterations.length - 1 && iter.toolCalls.length === 0;
                return (
                  <IterationGroup
                    key={iter.index}
                    iteration={iter}
                    isFinal={isFinal}
                    isLast={i === iterations.length - 1}
                    isStreaming={isActiveStream}
                  />
                );
              })}
              {isActiveStream && <AgentActivityIndicator iteration={lastIter} />}
            </>
          ) : message.content ? (
            <>
              <span dangerouslySetInnerHTML={{ __html: renderedHtml }} />
              {isActiveStream && <span className="streaming-cursor" />}
            </>
          ) : (isConnecting || isActiveStream) ? (
            <div className="agent-activity">
              <span className="agent-activity-spinner">
                <span className="spinner-dot dot-1" />
                <span className="spinner-dot dot-2" />
                <span className="spinner-dot dot-3" />
              </span>
              <span className="agent-activity-label">Thinking...</span>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

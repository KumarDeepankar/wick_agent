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
  const filtered: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(args)) {
    if (typeof value === 'string' && value.length > 200) continue;
    filtered[key] = value;
  }
  return JSON.stringify(filtered);
}

function SubAgentIterations({ tool }: { tool: ToolCallInfo }) {
  const subIters = tool.subIterations ?? [];
  if (!subIters.length && tool.subStatus === 'running') {
    return (
      <div className="subagent-activity">
        <span className="subagent-spinner">
          <span className="subagent-dot" />
          <span className="subagent-dot" />
          <span className="subagent-dot" />
        </span>
        <span className="subagent-activity-label">Starting {tool.subAgentName ?? 'sub-agent'}...</span>
      </div>
    );
  }

  return (
    <div className="subagent-iterations">
      {subIters.map((iter, i) => {
        const isFinal = i === subIters.length - 1 && !iter.toolCalls.length;
        const isStreaming = iter.status === 'streaming' && i === subIters.length - 1;

        return (
          <div key={i} className={`subagent-iteration ${isFinal ? 'final' : 'intermediate'}`}>
            {iter.content && !isFinal && iter.toolCalls.length > 0 ? (
              <div className="subagent-reasoning">{iter.content}</div>
            ) : iter.content ? (
              <div className="subagent-text">
                <span dangerouslySetInnerHTML={{ __html: renderMarkdown(iter.content) }} />
                {isStreaming && <span className="streaming-cursor" />}
              </div>
            ) : null}
            {iter.toolCalls.length > 0 && (
              <div className="subagent-tools">
                {iter.toolCalls.map((tc) => (
                  <ToolCallCard key={tc.id} tool={tc} />
                ))}
              </div>
            )}
          </div>
        );
      })}
      {tool.subStatus === 'running' && (
        <div className="subagent-activity">
          <span className="subagent-spinner">
            <span className="subagent-dot" />
            <span className="subagent-dot" />
            <span className="subagent-dot" />
          </span>
          <span className="subagent-activity-label">
            {subIters[subIters.length - 1]?.status === 'tool_running' ? 'Running tools...' : 'Thinking...'}
          </span>
        </div>
      )}
    </div>
  );
}

function ToolCallCard({ tool }: { tool: ToolCallInfo }) {
  const isSubAgent = tool.name === 'delegate_to_agent' && (tool.subIterations || tool.subStatus);
  const [expanded, setExpanded] = useState(!!isSubAgent);
  const hasOutput = !!tool.output;

  return (
    <div className={`tool-call-pill ${tool.status} ${isSubAgent ? 'subagent-card' : ''}`}>
      <div
        className="tool-call-header"
        onClick={() => { if (hasOutput || isSubAgent) setExpanded(!expanded); }}
        role={hasOutput ? 'button' : undefined}
      >
        <ToolIcon status={isSubAgent ? (tool.subStatus ?? 'running') : tool.status} />
        <span className="tool-call-name">{tool.name}</span>
        {tool.args && <span className="tool-call-args">{formatArgs(tool.args)}</span>}
        {hasOutput && (
          <span className={`tool-call-chevron ${expanded ? 'open' : ''}`}>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
              <path d="M3 4.5l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </span>
        )}
      </div>
      {expanded && isSubAgent && <SubAgentIterations tool={tool} />}
      {expanded && !isSubAgent && hasOutput && (
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

function ForkIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" className="fork-icon">
      <path d="M8 2v4M8 6L4 10M8 6l4 4M4 10v2M12 10v2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="8" cy="2" r="1" fill="currentColor" />
      <circle cx="4" cy="13" r="1" fill="currentColor" />
      <circle cx="12" cy="13" r="1" fill="currentColor" />
    </svg>
  );
}

function ParallelAgentFork({ tools }: { tools: ToolCallInfo[] }) {
  const runningCount = tools.filter(t => (t.subStatus ?? t.status) === 'running').length;
  const doneCount = tools.filter(t => (t.subStatus ?? t.status) === 'done').length;
  const allDone = doneCount === tools.length;

  return (
    <div className={`parallel-fork ${allDone ? 'done' : 'running'}`}>
      <div className="parallel-fork-header">
        <ForkIcon />
        <span className="parallel-fork-label">
          {allDone
            ? `${tools.length} parallel agents completed`
            : `${runningCount} of ${tools.length} agents running`}
        </span>
      </div>
      <div className="parallel-fork-lanes">
        {tools.map((tc) => (
          <div key={tc.id} className="parallel-fork-lane">
            <ToolCallCard tool={tc} />
          </div>
        ))}
      </div>
    </div>
  );
}

function IterationGroup({ iteration, isFinal, isStreaming }: {
  iteration: Iteration;
  isFinal: boolean;
  isStreaming: boolean;
}) {
  const html = useMemo(() => {
    if (!iteration.content) return '';
    return renderMarkdown(iteration.content);
  }, [iteration.content]);

  const hasTools = iteration.toolCalls.length > 0;

  // Separate parallel sub-agent calls from regular tool calls
  const parallelSubAgents: ToolCallInfo[] = [];
  const regularTools: ToolCallInfo[] = [];

  if (hasTools) {
    const subAgentCalls = iteration.toolCalls.filter(
      tc => tc.name === 'delegate_to_agent' && (tc.subIterations || tc.subStatus)
    );
    if (subAgentCalls.length > 1) {
      // Multiple sub-agents in same iteration = parallel fork
      for (const tc of iteration.toolCalls) {
        if (tc.name === 'delegate_to_agent' && (tc.subIterations || tc.subStatus)) {
          parallelSubAgents.push(tc);
        } else {
          regularTools.push(tc);
        }
      }
    } else {
      regularTools.push(...iteration.toolCalls);
    }
  }

  return (
    <div className={`iteration-group ${isFinal ? 'final' : 'intermediate'}`}>
      {iteration.content && (
        <div className={`iteration-text ${!isFinal && hasTools ? 'reasoning-inline' : ''}`}>
          <span dangerouslySetInnerHTML={{ __html: html }} />
          {isStreaming && iteration.status === 'streaming' && (
            <span className="streaming-cursor" />
          )}
        </div>
      )}
      {regularTools.length > 0 && (
        <div className="tool-calls-flow">
          {regularTools.map((tc) => (
            <ToolCallCard key={tc.id} tool={tc} />
          ))}
        </div>
      )}
      {parallelSubAgents.length > 0 && (
        <ParallelAgentFork tools={parallelSubAgents} />
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
      <div className={`message-bubble assistant ${isActiveStream ? 'streaming' : ''}`}>
        <div className="message-meta">
          <span className="message-role">
            <img src="/logo.png" alt="" className="message-role-icon" />
            Assistant
          </span>
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

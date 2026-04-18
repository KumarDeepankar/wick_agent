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

// Async-lifecycle tools managed by the SubAgentHook. Multiple consecutive
// iterations that call ONLY these (e.g. the supervisor polling check_async_task
// repeatedly while a batch runs) collapse into a single PollingRunGroup so
// the chat doesn't flood with identical "2 parallel tools completed" cards.
const ASYNC_LIFECYCLE_TOOLS = new Set([
  'start_async_task',
  'check_async_task',
  'update_async_task',
  'cancel_async_task',
  'list_async_tasks',
]);

function isPollingIteration(iter: Iteration): boolean {
  if (iter.toolCalls.length === 0) return false;
  return iter.toolCalls.every((tc) => ASYNC_LIFECYCLE_TOOLS.has(tc.name));
}

type IterationChunk =
  | { type: 'single'; iter: Iteration }
  | { type: 'polling-run'; iters: Iteration[] };

function chunkIterations(iters: Iteration[]): IterationChunk[] {
  const chunks: IterationChunk[] = [];
  let pollingBuffer: Iteration[] = [];
  const flush = () => {
    if (pollingBuffer.length === 0) return;
    // Only collapse when 2+ polling iterations are consecutive — a single poll
    // stays as a normal iteration so the user still sees the tool call clearly.
    if (pollingBuffer.length >= 2) {
      chunks.push({ type: 'polling-run', iters: pollingBuffer });
    } else {
      for (const it of pollingBuffer) chunks.push({ type: 'single', iter: it });
    }
    pollingBuffer = [];
  };
  for (const it of iters) {
    if (isPollingIteration(it)) {
      pollingBuffer.push(it);
    } else {
      flush();
      chunks.push({ type: 'single', iter: it });
    }
  }
  flush();
  return chunks;
}

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function ToolIcon({ status }: { status: string }) {
  if (status === 'pending') {
    return (
      <span className="tool-status-icon pending" aria-label="queued">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2.5 2.5" />
        </svg>
      </span>
    );
  }
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
  const isSubAgent = tool.name === 'delegate_to_agent';
  const hasSubAgentState = !!(tool.subIterations || tool.subStatus);
  const [expanded, setExpanded] = useState(isSubAgent);
  const hasOutput = !!tool.output;
  const iconStatus = isSubAgent ? (tool.subStatus ?? tool.status) : tool.status;

  return (
    <div className={`tool-call-pill ${tool.status} ${isSubAgent ? 'subagent-card' : ''}`}>
      <div
        className="tool-call-header"
        onClick={() => { if (hasOutput || isSubAgent) setExpanded(!expanded); }}
        role={hasOutput ? 'button' : undefined}
      >
        <ToolIcon status={iconStatus} />
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
      {expanded && isSubAgent && hasSubAgentState && <SubAgentIterations tool={tool} />}
      {expanded && isSubAgent && !hasSubAgentState && (
        <div className="subagent-activity">
          <span className="subagent-activity-label">Queued...</span>
        </div>
      )}
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

function ViewPromptButton({ traceId, onViewPrompt }: { traceId?: string; onViewPrompt?: (id: string) => void }) {
  if (!traceId || !onViewPrompt) return null;
  return (
    <button
      className="view-prompt-btn"
      onClick={() => onViewPrompt(traceId)}
      title="View the exact LLM prompt for this response"
    >
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M1 8s3-5 7-5 7 5 7 5-3 5-7 5-7-5-7-5z" />
        <circle cx="8" cy="8" r="2" />
      </svg>
      View Prompt
    </button>
  );
}

function ParallelToolGroup({ tools, traceId, onViewPrompt }: { tools: ToolCallInfo[]; traceId?: string; onViewPrompt?: (id: string) => void }) {
  const doneCount = tools.filter(t => t.status === 'done' || t.status === 'error').length;
  const runningCount = tools.filter(t => t.status === 'running').length;
  const allDone = doneCount === tools.length;
  const phase: 'initialized' | 'running' | 'done' =
    allDone ? 'done' : (runningCount === 0 && doneCount === 0) ? 'initialized' : 'running';
  const label =
    phase === 'done'
      ? `${tools.length} parallel tools completed`
      : phase === 'initialized'
        ? `${tools.length} parallel tools — initialized`
        : `${doneCount} of ${tools.length} tools complete`;

  return (
    <div className={`parallel-fork ${phase}`}>
      <div className="parallel-fork-header">
        <ForkIcon />
        <span className="parallel-fork-label">{label}</span>
        <ViewPromptButton traceId={traceId} onViewPrompt={onViewPrompt} />
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

function ParallelAgentFork({ tools, traceId, onViewPrompt }: { tools: ToolCallInfo[]; traceId?: string; onViewPrompt?: (id: string) => void }) {
  // Effective status: subStatus wins once the sub-agent has started
  // streaming; until then we fall back to the parent tool's status so
  // 'pending' (pre-seeded from on_llm_output) is surfaced.
  const effStatus = (t: ToolCallInfo) => t.subStatus ?? t.status;
  const doneCount = tools.filter(t => effStatus(t) === 'done' || effStatus(t) === 'error').length;
  const runningCount = tools.filter(t => effStatus(t) === 'running').length;
  const allDone = doneCount === tools.length;
  const phase: 'initialized' | 'running' | 'done' =
    allDone ? 'done' : (runningCount === 0 && doneCount === 0) ? 'initialized' : 'running';
  const label =
    phase === 'done'
      ? `${tools.length} parallel agents completed`
      : phase === 'initialized'
        ? `${tools.length} parallel agents — initialized`
        : `${runningCount} running, ${doneCount} of ${tools.length} complete`;

  return (
    <div className={`parallel-fork ${phase}`}>
      <div className="parallel-fork-header">
        <ForkIcon />
        <span className="parallel-fork-label">{label}</span>
        <ViewPromptButton traceId={traceId} onViewPrompt={onViewPrompt} />
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

function ClockIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="8" cy="8" r="6" />
      <polyline points="8,5 8,8 10.5,9.5" />
    </svg>
  );
}

function PollingRunGroup({ iters, onViewPrompt, isStreaming }: {
  iters: Iteration[];
  onViewPrompt?: (traceId: string) => void;
  isStreaming: boolean;
}) {
  const [expanded, setExpanded] = useState(false);

  // Count total async lifecycle operations across the run.
  const ops = iters.reduce((n, it) => n + it.toolCalls.length, 0);
  const doneOps = iters.reduce(
    (n, it) => n + it.toolCalls.filter((tc) => tc.status === 'done').length,
    0,
  );

  // Count distinct task_ids referenced across calls so the header shows
  // "Polling 2 tasks" rather than a raw op count alone.
  const taskIds = new Set<string>();
  for (const it of iters) {
    for (const tc of it.toolCalls) {
      const id = (tc.args as Record<string, unknown> | null)?.task_id as string | undefined;
      if (id) taskIds.add(id);
    }
  }

  const allDone = doneOps === ops && ops > 0;
  const taskWord = taskIds.size === 1 ? 'task' : 'tasks';
  const taskSummary = taskIds.size > 0 ? ` ${taskIds.size} ${taskWord}` : '';
  const headerLabel = allDone
    ? `Polled background${taskSummary} — ${ops} ops across ${iters.length} turns`
    : `Polling background${taskSummary} — ${ops} ops across ${iters.length} turns`;

  return (
    <div className={`polling-run ${allDone ? 'done' : 'running'}`}>
      <button
        type="button"
        className="polling-run-header"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
      >
        <ClockIcon />
        <span className="polling-run-label">{headerLabel}</span>
        <span className={`polling-run-caret ${expanded ? 'open' : ''}`} aria-hidden="true">
          ▸
        </span>
      </button>
      {expanded && (
        <div className="polling-run-body">
          {iters.map((iter, i) => (
            <IterationGroup
              key={iter.index}
              iteration={iter}
              nextLlmInputTraceId={iters[i + 1]?.llmInputTraceId}
              isFinal={false}
              isStreaming={isStreaming && i === iters.length - 1}
              onViewPrompt={onViewPrompt}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function IterationGroup({ iteration, nextLlmInputTraceId, isFinal, isStreaming, onViewPrompt }: {
  iteration: Iteration;
  nextLlmInputTraceId?: string;
  isFinal: boolean;
  isStreaming: boolean;
  onViewPrompt?: (traceId: string) => void;
}) {
  const html = useMemo(() => {
    if (!iteration.content) return '';
    return renderMarkdown(iteration.content);
  }, [iteration.content]);

  const hasTools = iteration.toolCalls.length > 0;

  // Separate tool calls into three groups: parallel sub-agents, parallel
  // regular tools, and solo tools. Any group with 2+ calls gets a visual
  // "parallel fork" container; solo tools render as individual cards.
  const parallelSubAgents: ToolCallInfo[] = [];
  const parallelRegularTools: ToolCallInfo[] = [];
  const soloTools: ToolCallInfo[] = [];

  if (hasTools) {
    const subAgentCalls: ToolCallInfo[] = [];
    const otherCalls: ToolCallInfo[] = [];

    for (const tc of iteration.toolCalls) {
      // Classify by name only — a pre-seeded 'pending' delegate_to_agent
      // call still belongs in the sub-agent fork, even before its first
      // on_subagent_* event arrives.
      if (tc.name === 'delegate_to_agent') {
        subAgentCalls.push(tc);
      } else {
        otherCalls.push(tc);
      }
    }

    // Sub-agents: group when 2+
    if (subAgentCalls.length > 1) {
      parallelSubAgents.push(...subAgentCalls);
    } else {
      otherCalls.push(...subAgentCalls);
    }

    // Regular tools: group when 2+
    if (otherCalls.length > 1) {
      parallelRegularTools.push(...otherCalls);
    } else {
      soloTools.push(...otherCalls);
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
      {soloTools.length > 0 && (
        <div className="tool-calls-flow">
          {soloTools.map((tc) => (
            <ToolCallCard key={tc.id} tool={tc} />
          ))}
          <ViewPromptButton traceId={nextLlmInputTraceId} onViewPrompt={onViewPrompt} />
        </div>
      )}
      {parallelRegularTools.length > 0 && (
        <ParallelToolGroup tools={parallelRegularTools} traceId={nextLlmInputTraceId} onViewPrompt={onViewPrompt} />
      )}
      {parallelSubAgents.length > 0 && (
        <ParallelAgentFork tools={parallelSubAgents} traceId={nextLlmInputTraceId} onViewPrompt={onViewPrompt} />
      )}
    </div>
  );
}

interface Props {
  message: ChatMessage;
  isStreaming: boolean;
  status: StreamStatus;
  onViewPrompt?: (traceId: string) => void;
}

export function MessageBubble({ message, isStreaming, status, onViewPrompt }: Props) {
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
              {chunkIterations(iterations).map((chunk, idx, chunks) => {
                if (chunk.type === 'polling-run') {
                  return (
                    <PollingRunGroup
                      key={`poll-run-${chunk.iters[0]!.index}`}
                      iters={chunk.iters}
                      isStreaming={isActiveStream && idx === chunks.length - 1}
                      onViewPrompt={onViewPrompt}
                    />
                  );
                }
                const iter = chunk.iter;
                // Find the next iteration across chunks to get its llm trace id.
                let nextTraceId: string | undefined;
                if (idx + 1 < chunks.length) {
                  const nextChunk = chunks[idx + 1]!;
                  const nextIter = nextChunk.type === 'single' ? nextChunk.iter : nextChunk.iters[0]!;
                  nextTraceId = nextIter.llmInputTraceId;
                }
                const isFinal = idx === chunks.length - 1 && iter.toolCalls.length === 0;
                return (
                  <IterationGroup
                    key={iter.index}
                    iteration={iter}
                    nextLlmInputTraceId={nextTraceId}
                    isFinal={isFinal}
                    isStreaming={isActiveStream && idx === chunks.length - 1}
                    onViewPrompt={onViewPrompt}
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

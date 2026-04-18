import { useMemo } from 'react';
import type { AsyncTask, AsyncTaskStatus } from '../types';

interface Props {
  tasks: AsyncTask[];
}

const STATUS_LABEL: Record<AsyncTaskStatus, string> = {
  running: 'Running',
  done: 'Done',
  error: 'Error',
  cancelled: 'Cancelled',
};

function formatTime(ts: number): string {
  const d = new Date(ts);
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function elapsed(from: number, to: number): string {
  const ms = to - from;
  if (ms < 1000) return `${ms} ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  const rs = s % 60;
  return `${m} m ${rs} s`;
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n) + '…';
}

export function AsyncTaskPanel({ tasks }: Props) {
  // Newest-first, mirror the server's list_async_tasks ordering.
  const ordered = useMemo(
    () => [...tasks].sort((a, b) => b.startedAt - a.startedAt),
    [tasks],
  );

  const running = ordered.filter((t) => t.status === 'running').length;

  return (
    <div className="async-task-panel">
      <div className="async-task-header">
        <span className="async-task-title">
          Background tasks ({ordered.length})
        </span>
        {running > 0 && <span className="async-task-live">{running} running</span>}
      </div>

      <div className="async-task-list">
        {ordered.length === 0 && (
          <div className="async-task-empty">
            No background sub-agent tasks yet. They appear here when the supervisor
            calls <code>start_async_task</code>.
          </div>
        )}

        {ordered.map((t) => (
          <AsyncTaskCard key={t.taskId} task={t} />
        ))}
      </div>
    </div>
  );
}

function AsyncTaskCard({ task }: { task: AsyncTask }) {
  const isRunning = task.status === 'running';
  const statusClass = `async-task-status async-task-status-${task.status}`;
  const runtime = elapsed(task.startedAt, task.updatedAt);

  return (
    <div className={`async-task-card ${isRunning ? 'running' : ''}`}>
      <div className="async-task-card-head">
        <span className="async-task-agent">{task.agentName || 'sub-agent'}</span>
        <span className={statusClass}>{STATUS_LABEL[task.status]}</span>
      </div>

      <div className="async-task-meta">
        <code className="async-task-id">{task.taskId}</code>
        <span className="async-task-sep">·</span>
        <span className="async-task-time">started {formatTime(task.startedAt)}</span>
        <span className="async-task-sep">·</span>
        <span className="async-task-time">{runtime}</span>
      </div>

      {task.task && (
        <div className="async-task-prompt">
          <span className="async-task-label">Task</span>
          <span className="async-task-prompt-body">{truncate(task.task, 400)}</span>
        </div>
      )}

      {task.toolCalls.length > 0 && (
        <div className="async-task-section">
          <span className="async-task-label">Tool calls ({task.toolCalls.length})</span>
          <ul className="async-task-tools">
            {task.toolCalls.map((tc) => (
              <li key={tc.id} className={`async-task-tool async-task-tool-${tc.status}`}>
                <span className="async-task-tool-name">{tc.name}</span>
                {tc.output != null && (
                  <span className="async-task-tool-output">{truncate(tc.output, 160)}</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {task.streamedContent && (
        <div className="async-task-section">
          <span className="async-task-label">Output</span>
          <div className="async-task-output">{truncate(task.streamedContent, 1500)}</div>
        </div>
      )}

      {task.updates.length > 0 && (
        <div className="async-task-section">
          <span className="async-task-label">Updates sent ({task.updates.length})</span>
          <ul className="async-task-updates">
            {task.updates.map((u, i) => (
              <li key={i}>{truncate(u, 200)}</li>
            ))}
          </ul>
        </div>
      )}

      {task.error && (
        <div className="async-task-section async-task-error">
          <span className="async-task-label">Error</span>
          <div>{task.error}</div>
        </div>
      )}
    </div>
  );
}

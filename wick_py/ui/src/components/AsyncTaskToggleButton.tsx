interface Props {
  totalCount: number;
  runningCount: number;
  isOpen: boolean;
  onClick: () => void;
}

export function AsyncTaskToggleButton({ totalCount, runningCount, isOpen, onClick }: Props) {
  const title = isOpen
    ? 'Hide background tasks'
    : runningCount > 0
      ? `${runningCount} background ${runningCount === 1 ? 'task' : 'tasks'} running`
      : 'Show background tasks';
  return (
    <button
      className={`async-task-toggle ${isOpen ? 'active' : ''}`}
      onClick={onClick}
      title={title}
      aria-label={title}
    >
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="8" cy="8" r="6" />
        <polyline points="8,5 8,8 10.5,9.5" />
      </svg>
      {totalCount > 0 && (
        <span className={`async-task-toggle-badge ${runningCount > 0 ? 'live' : ''}`}>
          {totalCount > 99 ? '99+' : totalCount}
        </span>
      )}
    </button>
  );
}

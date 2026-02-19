interface Props {
  eventCount: number;
  isStreaming: boolean;
  isOpen: boolean;
  onClick: () => void;
}

export function TraceToggleButton({ eventCount, isStreaming, isOpen, onClick }: Props) {
  return (
    <button
      className={`trace-toggle ${isOpen ? 'active' : ''}`}
      onClick={onClick}
      title={isOpen ? 'Hide trace events' : 'Show trace events'}
    >
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <rect x="2" y="2" width="12" height="12" rx="2" />
        <line x1="5" y1="5.5" x2="11" y2="5.5" />
        <line x1="5" y1="8" x2="11" y2="8" />
        <line x1="5" y1="10.5" x2="9" y2="10.5" />
      </svg>
      {isStreaming && eventCount > 0 && (
        <span className="trace-toggle-badge">{eventCount > 99 ? '99+' : eventCount}</span>
      )}
    </button>
  );
}

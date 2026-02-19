import type { TraceEvent, StreamStatus } from '../types';
import { TracePanel } from './TracePanel';

interface Props {
  events: TraceEvent[];
  status: StreamStatus;
  isOpen: boolean;
  onClose: () => void;
}

export function TraceOverlay({ events, status, isOpen, onClose }: Props) {
  if (!isOpen) return null;

  return (
    <div className="trace-overlay">
      <div className="trace-overlay-backdrop" onClick={onClose} />
      <div className="trace-overlay-panel">
        <div className="trace-overlay-close-row">
          <button className="trace-overlay-close" onClick={onClose} title="Close trace">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="2" y1="2" x2="12" y2="12" />
              <line x1="12" y1="2" x2="2" y2="12" />
            </svg>
          </button>
        </div>
        <TracePanel events={events} status={status} />
      </div>
    </div>
  );
}

import { useState, useRef, useEffect } from 'react';
import type { ChatMessage, StreamStatus } from '../types';
import { MessageBubble } from './MessageBubble';

interface Props {
  messages: ChatMessage[];
  status: StreamStatus;
  error: string | null;
  threadId: string | null;
  onSend: (content: string) => void;
  onStop: () => void;
  onReset: () => void;
}

export function ChatPanel({
  messages,
  status,
  error,
  threadId,
  onSend,
  onStop,
  onReset,
}: Props) {
  const [input, setInput] = useState('');
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const isActive = status === 'connecting' || status === 'streaming';

  // Auto-scroll on new messages / streaming tokens
  useEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const handleSubmit = () => {
    if (!input.trim() || isActive) return;
    onSend(input);
    setInput('');
    inputRef.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  // Find the last assistant message to mark as streaming
  const lastAssistantId =
    [...messages].reverse().find((m) => m.role === 'assistant')?.id ?? null;

  return (
    <div className="chat-panel">
      <div className="chat-header">
        <span className="chat-title">Chat</span>
        {threadId && (
          <span className="thread-id" title={threadId}>
            Thread: {threadId.slice(0, 8)}...
          </span>
        )}
        <button className="btn-reset" onClick={onReset} disabled={isActive}>
          New Thread
        </button>
      </div>

      <div className="chat-messages" ref={listRef}>
        {messages.length === 0 && (
          <div className="chat-empty">Send a message to begin</div>
        )}
        {messages.map((m) => (
          <MessageBubble
            key={m.id}
            message={m}
            isStreaming={isActive && m.id === lastAssistantId}
            status={status}
          />
        ))}
        {error && <div className="chat-error">Error: {error}</div>}
      </div>

      <div className="chat-input-area">
        <textarea
          ref={inputRef}
          className="chat-input"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
          rows={2}
          disabled={isActive}
        />
        <div className="chat-actions">
          {isActive ? (
            <button className="btn-stop" onClick={onStop}>
              Stop
            </button>
          ) : (
            <button
              className="btn-send"
              onClick={handleSubmit}
              disabled={!input.trim()}
            >
              Send
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

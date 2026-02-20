import { useState, useRef, useEffect, useCallback } from 'react';
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
  pendingPrompt?: string;
  onPromptConsumed?: () => void;
}

export function ChatPanel({
  messages,
  status,
  error,
  threadId,
  onSend,
  onStop,
  onReset,
  pendingPrompt,
  onPromptConsumed,
}: Props) {
  const [input, setInput] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const [errorDismissed, setErrorDismissed] = useState(false);
  const [confirmingReset, setConfirmingReset] = useState(false);
  const [threadCopied, setThreadCopied] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const isActive = status === 'connecting' || status === 'streaming';

  // Smart auto-scroll: only scroll to bottom if user hasn't scrolled up
  useEffect(() => {
    if (autoScroll && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [messages, autoScroll]);

  const handleScroll = useCallback(() => {
    const el = listRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    setAutoScroll(atBottom);
  }, []);

  // Fill input from WelcomeView prompt click
  useEffect(() => {
    if (pendingPrompt) {
      setInput(pendingPrompt);
      inputRef.current?.focus();
      onPromptConsumed?.();
    }
  }, [pendingPrompt, onPromptConsumed]);

  // Reset error dismissed state when a new error arrives
  useEffect(() => {
    if (error) setErrorDismissed(false);
  }, [error]);

  // Auto-grow textarea
  const handleInputChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value);
    const el = e.target;
    el.style.height = 'auto';
    el.style.height = el.scrollHeight + 'px';
  }, []);

  const handleSubmit = () => {
    if (!input.trim() || isActive) return;
    onSend(input);
    setInput('');
    // Reset textarea height
    if (inputRef.current) {
      inputRef.current.style.height = 'auto';
    }
    inputRef.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const handleReset = useCallback(() => {
    if (messages.length > 0 && !confirmingReset) {
      setConfirmingReset(true);
      return;
    }
    setConfirmingReset(false);
    onReset();
  }, [messages.length, onReset, confirmingReset]);

  const cancelReset = useCallback(() => setConfirmingReset(false), []);

  const handleCopyThreadId = useCallback(() => {
    if (!threadId) return;
    navigator.clipboard.writeText(threadId).then(() => {
      setThreadCopied(true);
      setTimeout(() => setThreadCopied(false), 1500);
    }).catch(() => {});
  }, [threadId]);

  const handleRetry = useCallback(() => {
    const lastUserMsg = [...messages].reverse().find((m) => m.role === 'user');
    if (lastUserMsg) {
      setErrorDismissed(true);
      onSend(lastUserMsg.content);
    }
  }, [messages, onSend]);

  // Find the last assistant message to mark as streaming
  const lastAssistantId =
    [...messages].reverse().find((m) => m.role === 'assistant')?.id ?? null;

  return (
    <div className="chat-panel">
      <div className="chat-header">
        <span className="chat-title">Chat</span>
        {threadId && (
          <button
            className={`thread-id ${threadCopied ? 'copied' : ''}`}
            onClick={handleCopyThreadId}
            title="Click to copy thread ID"
            aria-label="Copy thread ID"
          >
            {threadCopied ? 'Copied!' : `Thread: ${threadId.slice(0, 8)}...`}
          </button>
        )}
        {confirmingReset ? (
          <span className="reset-confirm">
            <span className="reset-confirm-text">Discard conversation?</span>
            <button className="btn-reset-yes" onClick={handleReset}>Yes</button>
            <button className="btn-reset-no" onClick={cancelReset}>No</button>
          </span>
        ) : (
          <button className="btn-reset" onClick={handleReset} disabled={isActive} aria-label="Start new thread">
            New Thread
          </button>
        )}
      </div>

      <div className="chat-messages" ref={listRef} onScroll={handleScroll}>
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
        {error && !errorDismissed && (
          <div className="chat-error">
            <span>Error: {error}</span>
            <div className="chat-error-actions">
              <button className="chat-error-retry" onClick={handleRetry} aria-label="Retry last message">
                Retry
              </button>
              <button
                className="chat-error-dismiss"
                onClick={() => setErrorDismissed(true)}
                aria-label="Dismiss error"
              >
                &times;
              </button>
            </div>
          </div>
        )}
      </div>

      {!autoScroll && messages.length > 0 && (
        <button
          className="scroll-to-bottom"
          onClick={() => {
            setAutoScroll(true);
            listRef.current?.scrollTo({ top: listRef.current.scrollHeight, behavior: 'smooth' });
          }}
          aria-label="Scroll to bottom"
        >
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M7 2v10" />
            <path d="M3 8l4 4 4-4" />
          </svg>
        </button>
      )}

      <div className="chat-input-area">
        <textarea
          ref={inputRef}
          className="chat-input"
          value={input}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
          rows={1}
          disabled={isActive}
        />
        <div className="chat-actions">
          {isActive ? (
            <button className="btn-stop" onClick={onStop} aria-label="Stop generation">
              Stop
            </button>
          ) : (
            <button
              className="btn-send"
              onClick={handleSubmit}
              disabled={!input.trim()}
              aria-label="Send message"
            >
              Send
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

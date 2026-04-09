import { useState, useRef, useEffect, useCallback } from 'react';
import type { ChatMessage, StreamStatus } from '../types';
import { MessageBubble } from './MessageBubble';
import { WelcomeView } from './canvas/WelcomeView';

interface Props {
  messages: ChatMessage[];
  status: StreamStatus;
  error: string | null;
  threadId: string | null;
  onSend: (content: string) => void;
  onStop: () => void;
  onReset?: () => void;
  pendingPrompt?: string;
  onPromptConsumed?: () => void;
  onPromptClick?: (prompt: string) => void;
  onViewPrompt?: (traceId: string) => void;
}

const SendIcon = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
    <path d="M5 12h14" />
    <path d="M12 5l7 7-7 7" />
  </svg>
);

const StopIcon = () => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
    <rect x="4" y="4" width="16" height="16" rx="3" />
  </svg>
);

export function ChatPanel({
  messages,
  status,
  error,
  threadId,
  onSend,
  onStop,
  pendingPrompt,
  onPromptConsumed,
  onPromptClick,
  onViewPrompt,
}: Props) {
  const [input, setInput] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const [errorDismissed, setErrorDismissed] = useState(false);
  const [threadCopied, setThreadCopied] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const isActive = status === 'connecting' || status === 'streaming';
  const isEmpty = messages.length === 0;

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

  useEffect(() => {
    if (pendingPrompt) {
      setInput(pendingPrompt);
      inputRef.current?.focus();
      onPromptConsumed?.();
    }
  }, [pendingPrompt, onPromptConsumed]);

  useEffect(() => {
    if (error) setErrorDismissed(false);
  }, [error]);

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

  const lastAssistantId =
    [...messages].reverse().find((m) => m.role === 'assistant')?.id ?? null;

  // Shared input widget — textarea with icon button inside
  const inputWidget = (
    <div className="chat-input-wrap">
      <textarea
        ref={inputRef}
        className="chat-input"
        value={input}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        placeholder={isEmpty ? 'Ask anything...' : 'Type a message...'}
        rows={1}
        disabled={isActive}
      />
      {isActive ? (
        <button className="btn-input-action btn-stop-icon" onClick={onStop} aria-label="Stop generation">
          <StopIcon />
        </button>
      ) : (
        <button
          className="btn-input-action btn-send-icon"
          onClick={handleSubmit}
          disabled={!input.trim()}
          aria-label="Send message"
        >
          <SendIcon />
        </button>
      )}
    </div>
  );

  // ── Empty state: centered input with skill chips below ──
  if (isEmpty && onPromptClick) {
    return (
      <div className="chat-panel chat-panel--welcome">
        <div className="welcome-center">
          <h2 className="welcome-title">What can I help you with?</h2>
          {inputWidget}
          <WelcomeView onPromptClick={onPromptClick} />
        </div>
      </div>
    );
  }

  // ── Normal chat state ──
  return (
    <div className="chat-panel">
      <div className="chat-messages" ref={listRef} onScroll={handleScroll}>
        <div className="chat-container">
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
          </div>

          {isEmpty && (
            <div className="chat-empty">Send a message to begin</div>
          )}
          {messages.map((m) => (
            <MessageBubble
              key={m.id}
              message={m}
              isStreaming={isActive && m.id === lastAssistantId}
              status={status}
              onViewPrompt={onViewPrompt}
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

      <div className="chat-container">
        <div className="chat-input-area">
          {inputWidget}
        </div>
      </div>
    </div>
  );
}

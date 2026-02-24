import { useMemo, useState, useCallback } from 'react';
import { Marked } from 'marked';
import hljs from 'highlight.js';
import type { ChatMessage, StreamStatus } from '../types';

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

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
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

  const renderedHtml = useMemo(() => {
    if (!message.content || isUser) return '';
    return renderer.parse(message.content, { async: false }) as string;
  }, [message.content, isUser]);

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

  const bubbleClass = [
    'message-bubble',
    isUser ? 'user' : 'assistant',
    isActiveStream ? 'streaming' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={bubbleClass}>
      <div className="message-meta">
        <span className="message-role">{isUser ? 'You' : 'Assistant'}</span>
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
        {isUser ? (
          message.content
        ) : message.content ? (
          <span dangerouslySetInnerHTML={{ __html: renderedHtml }} />
        ) : (isConnecting || isActiveStream) ? (
          <div className="thinking-skeleton">
            <div className="thinking-skeleton-label">
              <span className="thinking-skeleton-dot" />
              Processing
            </div>
            <div className="thinking-skeleton-lines">
              <div className="thinking-skeleton-line" style={{ width: '92%' }} />
              <div className="thinking-skeleton-line" style={{ width: '78%', animationDelay: '0.15s' }} />
              <div className="thinking-skeleton-line" style={{ width: '65%', animationDelay: '0.3s' }} />
            </div>
          </div>
        ) : null}
        {isActiveStream && message.content && <span className="streaming-cursor" />}
      </div>
    </div>
  );
}

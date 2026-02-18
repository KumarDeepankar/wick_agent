import type { ChatMessage, StreamStatus } from '../types';

interface Props {
  message: ChatMessage;
  isStreaming: boolean;
  status: StreamStatus;
}

export function MessageBubble({ message, isStreaming, status }: Props) {
  const isUser = message.role === 'user';
  const isActiveStream = isStreaming && status === 'streaming';
  const isConnecting = isStreaming && status === 'connecting';

  const bubbleClass = [
    'message-bubble',
    isUser ? 'user' : 'assistant',
    isActiveStream ? 'streaming' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={bubbleClass}>
      <div className="message-role">{isUser ? 'You' : 'Assistant'}</div>
      <div className="message-content">
        {message.content || (isConnecting ? (
          <span className="message-thinking">
            Thinking
            <span className="thinking-dots">
              <span />
              <span />
              <span />
            </span>
          </span>
        ) : null)}
        {isActiveStream && <span className="streaming-cursor" />}
      </div>
    </div>
  );
}

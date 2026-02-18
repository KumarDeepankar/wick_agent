import { useState, useRef, useCallback } from 'react';
import type { ChatMessage, TraceEvent, StreamStatus, CanvasArtifact } from '../types';
import { extractExtension, extractFileName, resolveContentType, resolveLanguage, isBinaryExtension, isSlideContent } from '../utils/canvasUtils';
import { fetchFileDownload, getToken } from '../api';

interface SSEEvent {
  event: string;
  data: string;
}

function parseBlock(block: string): SSEEvent | null {
  let event = 'message';
  let data = '';

  for (const rawLine of block.split('\n')) {
    const line = rawLine.replace(/\r$/, '');
    // Skip SSE comments (ping keepalives etc.)
    if (line.startsWith(':') || line === '') continue;

    if (line.startsWith('event:')) {
      event = line.slice(6).trim();
    } else if (line.startsWith('data:')) {
      // Append with newline separator for multi-line data fields
      data += (data ? '\n' : '') + line.slice(5).trim();
    }
  }

  return data ? { event, data } : null;
}

async function* parseSSE(
  reader: ReadableStreamDefaultReader<Uint8Array>,
): AsyncGenerator<SSEEvent> {
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });

    // Normalize \r\n → \n, lone \r → \n so split always works
    buffer = buffer.replace(/\r\n/g, '\n').replace(/\r/g, '\n');

    // Events are separated by blank lines (\n\n)
    const parts = buffer.split('\n\n');
    // Last element is either empty (if buffer ended with \n\n) or a partial chunk
    buffer = parts.pop() ?? '';

    for (const part of parts) {
      const evt = parseBlock(part);
      if (evt) yield evt;
    }
  }

  // Process any remaining buffer
  if (buffer.trim()) {
    const evt = parseBlock(buffer);
    if (evt) yield evt;
  }
}

let eventCounter = 0;

export function useAgentStream() {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [traceEvents, setTraceEvents] = useState<TraceEvent[]>([]);
  const [canvasArtifacts, setCanvasArtifacts] = useState<CanvasArtifact[]>([]);
  const [status, setStatus] = useState<StreamStatus>('idle');
  const [threadId, setThreadId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const assistantIdRef = useRef<string | null>(null);
  // Track edit_file run_id → file_path so we can fetch on on_tool_end
  const pendingEditsRef = useRef<Map<string, string>>(new Map());
  // Insert a separator before next text chunk after a tool call finishes
  const needsSeparatorRef = useRef(false);
  // Batch streaming tokens via rAF for performance
  const pendingTokensRef = useRef('');
  const rafRef = useRef<number>(0);

  const stop = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setStatus((s) => (s === 'streaming' ? 'done' : s));
  }, []);

  const send = useCallback(
    async (content: string, agentId?: string) => {
      if (!content.trim()) return;

      // Add user message
      const userMsg: ChatMessage = {
        id: `user-${Date.now()}`,
        role: 'user',
        content: content.trim(),
        timestamp: Date.now(),
      };
      setMessages((prev) => [...prev, userMsg]);
      setError(null);
      setStatus('connecting');

      // Create empty assistant message for token assembly
      const assistantId = `assistant-${Date.now()}`;
      assistantIdRef.current = assistantId;
      const assistantMsg: ChatMessage = {
        id: assistantId,
        role: 'assistant',
        content: '',
        timestamp: Date.now(),
      };
      setMessages((prev) => [...prev, assistantMsg]);

      const controller = new AbortController();
      abortRef.current = controller;

      const url = agentId ? `/agents/${agentId}/stream` : '/agents/stream';

      try {
        const fetchHeaders: Record<string, string> = { 'Content-Type': 'application/json' };
        const token = getToken();
        if (token) fetchHeaders['Authorization'] = `Bearer ${token}`;

        const res = await fetch(url, {
          method: 'POST',
          headers: fetchHeaders,
          body: JSON.stringify({
            messages: [{ role: 'user', content: content.trim() }],
            thread_id: threadId ?? undefined,
            trace: true,
          }),
          signal: controller.signal,
        });

        if (!res.ok) {
          const text = await res.text();
          throw new Error(`Stream request failed (${res.status}): ${text}`);
        }

        if (!res.body) {
          throw new Error('No response body');
        }

        setStatus('streaming');
        const reader = res.body.getReader();

        for await (const sse of parseSSE(reader)) {
          if (controller.signal.aborted) break;

          let parsed: Record<string, unknown>;
          try {
            parsed = JSON.parse(sse.data);
          } catch {
            // Skip malformed JSON
            continue;
          }

          // Push every event to trace
          const traceEvt: TraceEvent = {
            id: `trace-${eventCounter++}`,
            eventType: sse.event,
            timestamp: Date.now(),
            data: parsed,
          };
          setTraceEvents((prev) => [...prev, traceEvt]);

          // Detect write_file / edit_file tool calls → canvas artifacts
          if (sse.event === 'on_tool_start') {
            const toolName = parsed.name as string;
            const input = (parsed.data as Record<string, unknown>)?.input as Record<string, unknown> | undefined;
            const rawPath = (input?.file_path ?? input?.path) as string | undefined;

            if (toolName === 'write_file' && rawPath && input?.content) {
              const filePath = rawPath;
              const content = input.content as string;
              const ext = extractExtension(filePath);
              let contentType = resolveContentType(ext);
              // Auto-detect slide decks from markdown content
              if (contentType === 'document' && isSlideContent(content)) {
                contentType = 'slides';
              }
              const artifact: CanvasArtifact = {
                id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                filePath,
                fileName: extractFileName(filePath),
                contentType,
                content: isBinaryExtension(ext) ? null : content,
                extension: ext,
                timestamp: Date.now(),
                isBinary: isBinaryExtension(ext),
                language: resolveLanguage(ext),
              };
              setCanvasArtifacts((prev) => {
                const idx = prev.findIndex((a) => a.filePath === filePath);
                if (idx >= 0) {
                  const updated = [...prev];
                  updated[idx] = artifact;
                  return updated;
                }
                return [...prev, artifact];
              });
            }

            // Track edit_file / read_file run_id → file_path for on_tool_end
            if ((toolName === 'edit_file' || toolName === 'read_file') && rawPath) {
              const runId = parsed.run_id as string;
              if (runId) {
                pendingEditsRef.current.set(runId, rawPath);
              }
            }
          }

          // Mark that a step boundary occurred so we insert a divider before next text
          if (sse.event === 'on_tool_end') {
            // Only set if assistant already has content
            setMessages((prev) => {
              const cur = prev.find((m) => m.id === assistantIdRef.current);
              if (cur && cur.content.trim()) {
                needsSeparatorRef.current = true;
              }
              return prev;
            });
          }

          // Detect edit_file completion → fetch updated file from backend
          if (sse.event === 'on_tool_end') {
            const toolName = parsed.name as string;
            const runId = parsed.run_id as string;

            if ((toolName === 'edit_file' || toolName === 'read_file') && runId && pendingEditsRef.current.has(runId)) {
              const filePath = pendingEditsRef.current.get(runId)!;
              pendingEditsRef.current.delete(runId);
              const ext = extractExtension(filePath);

              if (toolName === 'read_file') {
                // read_file output has the content directly
                const output = (parsed.data as Record<string, unknown>)?.output;
                const content = typeof output === 'string' ? output : '';
                if (content) {
                  let readContentType = resolveContentType(ext);
                  if (readContentType === 'document' && isSlideContent(content)) {
                    readContentType = 'slides';
                  }
                  const artifact: CanvasArtifact = {
                    id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                    filePath,
                    fileName: extractFileName(filePath),
                    contentType: readContentType,
                    content,
                    extension: ext,
                    timestamp: Date.now(),
                    isBinary: false,
                    language: resolveLanguage(ext),
                  };
                  setCanvasArtifacts((prev) => {
                    const idx = prev.findIndex((a) => a.filePath === filePath);
                    if (idx >= 0) {
                      const updated = [...prev];
                      updated[idx] = artifact;
                      return updated;
                    }
                    return [...prev, artifact];
                  });
                }
              } else {
                // edit_file — fetch the full updated file from backend
                fetchFileDownload(filePath).then((blob) =>
                  blob.text().then((content) => {
                    let editContentType = resolveContentType(ext);
                    if (editContentType === 'document' && isSlideContent(content)) {
                      editContentType = 'slides';
                    }
                    const artifact: CanvasArtifact = {
                      id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                      filePath,
                      fileName: extractFileName(filePath),
                      contentType: editContentType,
                      content,
                      extension: ext,
                      timestamp: Date.now(),
                      isBinary: false,
                      language: resolveLanguage(ext),
                    };
                    setCanvasArtifacts((prev) => {
                      const idx = prev.findIndex((a) => a.filePath === filePath);
                      if (idx >= 0) {
                        const updated = [...prev];
                        updated[idx] = artifact;
                        return updated;
                      }
                      return [...prev, artifact];
                    });
                  }),
                ).catch(() => {
                  // Fetch failed — file may not be accessible
                });
              }
            }

            // Detect execute tool output that looks like a document
            if (toolName === 'execute') {
              const output = (parsed.data as Record<string, unknown>)?.output;
              const outputStr = typeof output === 'string' ? output : '';
              if (outputStr.length > 200 && (outputStr.includes('# ') || outputStr.includes('| '))) {
                const artifact: CanvasArtifact = {
                  id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                  filePath: '/output/result.md',
                  fileName: 'Result',
                  contentType: 'document',
                  content: outputStr,
                  extension: '.md',
                  timestamp: Date.now(),
                  isBinary: false,
                };
                setCanvasArtifacts((prev) => [...prev, artifact]);
              }
            }
          }

          // Handle specific event types for chat assembly
          switch (sse.event) {
            case 'on_chat_model_stream': {
              // Raw framework event: data.data.chunk.content
              const chunk = (parsed.data as Record<string, unknown>)?.chunk as Record<string, unknown> | undefined;
              const token = (chunk?.content as string) ?? '';
              if (token) {
                const prefix = needsSeparatorRef.current ? '\n\n---\n\n' : '';
                needsSeparatorRef.current = false;
                // Batch tokens via rAF to avoid per-token re-renders
                pendingTokensRef.current += prefix + token;
                if (!rafRef.current) {
                  rafRef.current = requestAnimationFrame(() => {
                    const batch = pendingTokensRef.current;
                    pendingTokensRef.current = '';
                    rafRef.current = 0;
                    setMessages((prev) =>
                      prev.map((m) =>
                        m.id === assistantIdRef.current
                          ? { ...m, content: m.content + batch }
                          : m,
                      ),
                    );
                  });
                }
              }
              break;
            }

            case 'done': {
              // Flush any pending batched tokens
              if (pendingTokensRef.current) {
                const batch = pendingTokensRef.current;
                pendingTokensRef.current = '';
                if (rafRef.current) { cancelAnimationFrame(rafRef.current); rafRef.current = 0; }
                setMessages((prev) =>
                  prev.map((m) =>
                    m.id === assistantIdRef.current
                      ? { ...m, content: m.content + batch }
                      : m,
                  ),
                );
              }
              const tid = parsed.thread_id as string;
              if (tid) setThreadId(tid);
              setStatus('done');
              break;
            }

            case 'error': {
              setError(parsed.error as string);
              setStatus('error');
              break;
            }
          }
        }

        // If we finished iterating without hitting done/error
        setStatus((s) => (s === 'streaming' ? 'done' : s));
      } catch (err: unknown) {
        if (err instanceof DOMException && err.name === 'AbortError') {
          setStatus('done');
          return;
        }
        const msg = err instanceof Error ? err.message : 'Unknown error';
        setError(msg);
        setStatus('error');
      } finally {
        abortRef.current = null;
      }
    },
    [threadId],
  );

  const updateArtifactContent = useCallback((filePath: string, newContent: string) => {
    setCanvasArtifacts(prev => prev.map(a =>
      a.filePath === filePath ? { ...a, content: newContent, timestamp: Date.now() } : a
    ));
  }, []);

  const removeArtifact = useCallback((artifactId: string) => {
    setCanvasArtifacts(prev => prev.filter(a => a.id !== artifactId));
  }, []);

  const reset = useCallback(() => {
    stop();
    setMessages([]);
    setTraceEvents([]);
    setCanvasArtifacts([]);
    setThreadId(null);
    setError(null);
    setStatus('idle');
    pendingEditsRef.current.clear();
    needsSeparatorRef.current = false;
  }, [stop]);

  return {
    messages,
    traceEvents,
    canvasArtifacts,
    status,
    threadId,
    error,
    send,
    stop,
    reset,
    updateArtifactContent,
    removeArtifact,
  };
}

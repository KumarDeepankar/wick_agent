import { useState, useRef, useCallback } from 'react';
import type {
  ChatMessage,
  TraceEvent,
  StreamStatus,
  CanvasArtifact,
  Iteration,
  ToolCallInfo,
  AsyncTask,
  AsyncTaskStatus,
} from '../types';
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
  const [asyncTasks, setAsyncTasks] = useState<AsyncTask[]>([]);

  const abortRef = useRef<AbortController | null>(null);
  const assistantIdRef = useRef<string | null>(null);
  // Track edit_file run_id → file_path so we can fetch on on_tool_end
  const pendingEditsRef = useRef<Map<string, string>>(new Map());
  // Track write_file run_id → file_path so we can update artifact status on on_tool_end
  const pendingWritesRef = useRef<Map<string, string>>(new Map());
  // Track fileNames created in the current turn for turn-scoped dedup
  const turnFileNamesRef = useRef<Set<string>>(new Set());
  // Iteration tracking for ReAct loop grouping
  const iterationsRef = useRef<Iteration[]>([]);
  const currentIterRef = useRef<Iteration | null>(null);
  // Batch streaming tokens via rAF for performance
  const pendingTokensRef = useRef('');
  const rafRef = useRef<number>(0);

  // Flush iteration state into the assistant message (both iterations[] and flat content)
  const flushIterationsToMessage = useCallback(() => {
    const iters = iterationsRef.current;
    if (!iters.length) return;
    const flatContent = iters.map((it) => it.content).filter(Boolean).join('\n\n---\n\n');
    setMessages((prev) =>
      prev.map((m) =>
        m.id === assistantIdRef.current
          ? { ...m, content: flatContent, iterations: iters.map((it) => ({ ...it, toolCalls: it.toolCalls.map((tc) => ({ ...tc })) })) }
          : m,
      ),
    );
  }, []);

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
      iterationsRef.current = [];
      currentIterRef.current = null;
      pendingWritesRef.current.clear();
      turnFileNamesRef.current.clear();
      const assistantMsg: ChatMessage = {
        id: assistantId,
        role: 'assistant',
        content: '',
        timestamp: Date.now(),
        iterations: [],
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

          // on_chat_model_start → push new iteration
          // NOTE: on_chat_model_start fires BEFORE on_llm_input (Go emits
          // on_chat_model_start, then calls modelCall which emits on_llm_input).
          if (sse.event === 'on_chat_model_start') {
            // Finalize previous iteration if it exists
            if (currentIterRef.current && currentIterRef.current.status !== 'done') {
              currentIterRef.current.status = 'done';
            }
            const newIter: Iteration = {
              index: iterationsRef.current.length,
              content: '',
              toolCalls: [],
              status: 'streaming',
            };
            iterationsRef.current.push(newIter);
            currentIterRef.current = newIter;
            flushIterationsToMessage();
          }

          // on_llm_input → set trace ID on the CURRENT iteration (arrives
          // after on_chat_model_start, not before).
          if (sse.event === 'on_llm_input') {
            if (currentIterRef.current) {
              currentIterRef.current.llmInputTraceId = traceEvt.id;
              flushIterationsToMessage();
            }
          }

          // on_llm_output → the model has finished and (possibly) returned
          // tool calls. Seed the current iteration's toolCalls as 'pending'
          // BEFORE any on_tool_start arrives, so the parallel-fork group
          // renders immediately with the full set of lanes rather than
          // popping in as each goroutine fires its start event.
          if (sse.event === 'on_llm_output') {
            if (currentIterRef.current) {
              const data = parsed.data as Record<string, unknown> | undefined;
              const rawCalls = data?.tool_calls as Array<Record<string, unknown>> | undefined;
              if (rawCalls && rawCalls.length > 0) {
                for (const raw of rawCalls) {
                  const id = raw.id as string | undefined;
                  if (!id) continue;
                  if (currentIterRef.current.toolCalls.find((t) => t.id === id)) continue;
                  currentIterRef.current.toolCalls.push({
                    id,
                    name: (raw.name as string) ?? 'unknown',
                    args: (raw.args as Record<string, unknown>) ?? null,
                    output: null,
                    status: 'pending',
                  });
                }
                currentIterRef.current.status = 'tool_running';
                flushIterationsToMessage();
              }
            }
          }

          // Detect write_file / edit_file tool calls → canvas artifacts
          if (sse.event === 'on_tool_start') {
            // Track tool call in current iteration. If it was pre-seeded by
            // on_llm_output as 'pending', transition it to 'running' rather
            // than pushing a duplicate.
            if (currentIterRef.current) {
              const runId = (parsed.run_id as string) ?? `tool-${Date.now()}`;
              const inputArgs = ((parsed.data as Record<string, unknown>)?.input as Record<string, unknown>) ?? null;
              const existing = currentIterRef.current.toolCalls.find((t) => t.id === runId);
              if (existing) {
                existing.status = 'running';
                if (inputArgs && !existing.args) existing.args = inputArgs;
              } else {
                currentIterRef.current.toolCalls.push({
                  id: runId,
                  name: (parsed.name as string) ?? 'unknown',
                  args: inputArgs,
                  output: null,
                  status: 'running',
                });
              }
              currentIterRef.current.status = 'tool_running';
              flushIterationsToMessage();
            }

            const toolName = parsed.name as string;
            const input = (parsed.data as Record<string, unknown>)?.input as Record<string, unknown> | undefined;
            const rawPath = (input?.file_path ?? input?.path) as string | undefined;

            if (toolName === 'write_file' && rawPath && input?.content) {
              const filePath = rawPath;
              const content = input.content as string;
              const fileName = extractFileName(filePath);
              const ext = extractExtension(filePath);
              let contentType = resolveContentType(ext);
              // Auto-detect slide decks from markdown content
              if (contentType === 'document' && isSlideContent(content)) {
                contentType = 'slides';
              }
              const artifact: CanvasArtifact = {
                id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                filePath,
                fileName,
                contentType,
                content: isBinaryExtension(ext) ? null : content,
                extension: ext,
                timestamp: Date.now(),
                isBinary: isBinaryExtension(ext),
                language: resolveLanguage(ext),
                status: 'pending',
              };
              // Track run_id → filePath for status update on on_tool_end
              const runId = parsed.run_id as string;
              if (runId) pendingWritesRef.current.set(runId, filePath);
              setCanvasArtifacts((prev) => {
                // 1. Exact filePath match — always update
                const idx = prev.findIndex((a) => a.filePath === filePath);
                if (idx >= 0) {
                  const updated = [...prev];
                  updated[idx] = artifact;
                  return updated;
                }
                // 2. Same fileName with error status — replace across turns (retry with corrected path)
                const errIdx = prev.findIndex((a) => a.fileName === fileName && a.status === 'error');
                if (errIdx >= 0) {
                  const updated = [...prev];
                  updated[errIdx] = artifact;
                  return updated;
                }
                // 3. Same fileName with pending status — replace within current turn only
                if (turnFileNamesRef.current.has(fileName)) {
                  const pendIdx = prev.findIndex((a) => a.fileName === fileName && a.status === 'pending');
                  if (pendIdx >= 0) {
                    const updated = [...prev];
                    updated[pendIdx] = artifact;
                    return updated;
                  }
                }
                turnFileNamesRef.current.add(fileName);
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

          // Detect edit_file completion → fetch updated file from backend
          if (sse.event === 'on_tool_end') {
            // Mark matching tool call as done in current iteration
            if (currentIterRef.current) {
              const runId = parsed.run_id as string;
              const output = (parsed.data as Record<string, unknown>)?.output;
              const outputStr = typeof output === 'string' ? output : (output != null ? JSON.stringify(output) : null);
              const tc = currentIterRef.current.toolCalls.find((t) => t.id === runId);
              if (tc) {
                tc.status = 'done';
                tc.output = outputStr;
              }
              // If all tool calls finished, mark iteration as done (next model_start will create new iteration).
              // `pending` tools aren't finished — only 'done' or 'error' counts.
              if (currentIterRef.current.toolCalls.every((t) => t.status === 'done' || t.status === 'error')) {
                currentIterRef.current.status = 'done';
              }
              flushIterationsToMessage();
            }
          }

          if (sse.event === 'on_tool_end') {
            const toolName = parsed.name as string;
            const runId = parsed.run_id as string;

            // Update write_file artifact status based on tool result
            if (toolName === 'write_file' && runId && pendingWritesRef.current.has(runId)) {
              const writePath = pendingWritesRef.current.get(runId)!;
              pendingWritesRef.current.delete(runId);
              const output = (parsed.data as Record<string, unknown>)?.output;
              const outputStr = typeof output === 'string' ? output : '';
              const isError = outputStr.toLowerCase().startsWith('error');
              setCanvasArtifacts((prev) => prev.map((a) =>
                a.filePath === writePath ? { ...a, status: isError ? 'error' : 'ok' } : a
              ));
            }

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

          }

          // --- Sub-agent streaming events ---
          if (sse.event.startsWith('on_subagent_')) {
            const parentToolId = parsed.run_id as string;
            const agentName = (parsed.name as string) ?? (parsed.data as Record<string, unknown>)?.agent as string ?? '';

            // Find the parent delegate_to_agent tool call across all iterations
            let parentTc: ToolCallInfo | undefined;
            for (const iter of iterationsRef.current) {
              parentTc = iter.toolCalls.find((tc) => tc.id === parentToolId);
              if (parentTc) break;
            }

            if (parentTc) {
              // Initialize sub-agent state on first event
              if (!parentTc.subIterations) {
                parentTc.subAgentName = agentName;
                parentTc.subIterations = [];
                parentTc.subStatus = 'running';
              }

              const subIters = parentTc.subIterations!;
              const currentSubIter = () => subIters[subIters.length - 1] as Iteration | undefined;

              switch (sse.event) {
                case 'on_subagent_model_start': {
                  // Finalize previous sub-iteration
                  const prev = currentSubIter();
                  if (prev && prev.status !== 'done') prev.status = 'done';
                  subIters.push({
                    index: subIters.length,
                    content: '',
                    toolCalls: [],
                    status: 'streaming',
                  });
                  flushIterationsToMessage();
                  break;
                }

                case 'on_subagent_stream': {
                  const chunk = (parsed.data as Record<string, unknown>)?.chunk as Record<string, unknown> | undefined;
                  const token = (chunk?.content as string) ?? '';
                  if (token) {
                    if (!currentSubIter()) {
                      subIters.push({ index: 0, content: '', toolCalls: [], status: 'streaming' });
                    }
                    const iter = currentSubIter()!;
                    iter.content += token;
                    // Batch via rAF — reuse pending mechanism
                    pendingTokensRef.current += token;
                    if (!rafRef.current) {
                      rafRef.current = requestAnimationFrame(() => {
                        pendingTokensRef.current = '';
                        rafRef.current = 0;
                        flushIterationsToMessage();
                      });
                    }
                  }
                  break;
                }

                case 'on_subagent_tool_start': {
                  const data = parsed.data as Record<string, unknown>;
                  if (!currentSubIter()) {
                    subIters.push({ index: 0, content: '', toolCalls: [], status: 'tool_running' });
                  }
                  const iter = currentSubIter()!;
                  iter.status = 'tool_running';
                  iter.toolCalls.push({
                    id: (data?.sub_run_id as string) ?? `sub-tool-${Date.now()}`,
                    name: (parsed.name as string) ?? 'unknown',
                    args: (data?.input as Record<string, unknown>) ?? null,
                    output: null,
                    status: 'running',
                  });
                  flushIterationsToMessage();

                  // Canvas artifact creation for sub-agent file tools
                  const subToolName = parsed.name as string;
                  const subInput = data?.input as Record<string, unknown> | undefined;
                  const subRawPath = (subInput?.file_path ?? subInput?.path) as string | undefined;

                  if (subToolName === 'write_file' && subRawPath && subInput?.content) {
                    const filePath = subRawPath;
                    const content = subInput.content as string;
                    const fileName = extractFileName(filePath);
                    const ext = extractExtension(filePath);
                    let contentType = resolveContentType(ext);
                    if (contentType === 'document' && isSlideContent(content)) {
                      contentType = 'slides';
                    }
                    const artifact: CanvasArtifact = {
                      id: `artifact-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
                      filePath,
                      fileName,
                      contentType,
                      content: isBinaryExtension(ext) ? null : content,
                      extension: ext,
                      timestamp: Date.now(),
                      isBinary: isBinaryExtension(ext),
                      language: resolveLanguage(ext),
                      status: 'pending',
                    };
                    const subRunId = data?.sub_run_id as string;
                    if (subRunId) pendingWritesRef.current.set(subRunId, filePath);
                    setCanvasArtifacts((prev) => {
                      const idx = prev.findIndex((a) => a.filePath === filePath);
                      if (idx >= 0) {
                        const updated = [...prev];
                        updated[idx] = artifact;
                        return updated;
                      }
                      const errIdx = prev.findIndex((a) => a.fileName === fileName && a.status === 'error');
                      if (errIdx >= 0) {
                        const updated = [...prev];
                        updated[errIdx] = artifact;
                        return updated;
                      }
                      if (turnFileNamesRef.current.has(fileName)) {
                        const pendIdx = prev.findIndex((a) => a.fileName === fileName && a.status === 'pending');
                        if (pendIdx >= 0) {
                          const updated = [...prev];
                          updated[pendIdx] = artifact;
                          return updated;
                        }
                      }
                      turnFileNamesRef.current.add(fileName);
                      return [...prev, artifact];
                    });
                  }

                  if ((subToolName === 'edit_file' || subToolName === 'read_file') && subRawPath) {
                    const subRunId = data?.sub_run_id as string;
                    if (subRunId) pendingEditsRef.current.set(subRunId, subRawPath);
                  }

                  break;
                }

                case 'on_subagent_tool_end': {
                  const data = parsed.data as Record<string, unknown>;
                  const subRunId = data?.sub_run_id as string;
                  const output = data?.output;
                  const outputStr = typeof output === 'string' ? output : (output != null ? JSON.stringify(output) : null);
                  const iter = currentSubIter();
                  if (iter) {
                    const tc = iter.toolCalls.find((t) => t.id === subRunId);
                    if (tc) {
                      tc.status = 'done';
                      tc.output = outputStr;
                    }
                    if (iter.toolCalls.every((t) => t.status !== 'running')) {
                      iter.status = 'done';
                    }
                  }
                  flushIterationsToMessage();

                  // Canvas artifact updates for sub-agent file tools
                  const subEndToolName = parsed.name as string;

                  if (subEndToolName === 'write_file' && subRunId && pendingWritesRef.current.has(subRunId)) {
                    const writePath = pendingWritesRef.current.get(subRunId)!;
                    pendingWritesRef.current.delete(subRunId);
                    const writeOutput = typeof output === 'string' ? output : '';
                    const isError = writeOutput.toLowerCase().startsWith('error');
                    setCanvasArtifacts((prev) => prev.map((a) =>
                      a.filePath === writePath ? { ...a, status: isError ? 'error' : 'ok' } : a
                    ));
                  }

                  if ((subEndToolName === 'edit_file' || subEndToolName === 'read_file') && subRunId && pendingEditsRef.current.has(subRunId)) {
                    const filePath = pendingEditsRef.current.get(subRunId)!;
                    pendingEditsRef.current.delete(subRunId);
                    const ext = extractExtension(filePath);

                    if (subEndToolName === 'read_file') {
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
                      ).catch(() => {});
                    }
                  }

                  break;
                }

                case 'on_subagent_done': {
                  const last = currentSubIter();
                  if (last && last.status !== 'done') last.status = 'done';
                  parentTc.subStatus = 'done';
                  parentTc.status = 'done';
                  // Set output to the final sub-iteration content
                  if (last && last.content) {
                    parentTc.output = last.content;
                  }
                  flushIterationsToMessage();
                  break;
                }

                case 'on_subagent_error': {
                  parentTc.subStatus = 'error';
                  parentTc.status = 'error';
                  flushIterationsToMessage();
                  break;
                }
              }
            }
          }

          // --- Async sub-agent task events (start_async_task lifecycle) ---
          if (sse.event.startsWith('on_async_task_')) {
            const taskId = (parsed.task_id as string) ?? '';
            if (!taskId) {
              // Malformed event — skip rather than corrupt state.
            } else {
              const data = (parsed.data as Record<string, unknown> | undefined) ?? {};
              const now = Date.now();

              const mutate = (fn: (t: AsyncTask) => AsyncTask) =>
                setAsyncTasks((prev) => prev.map((t) => (t.taskId === taskId ? fn(t) : t)));

              switch (sse.event) {
                case 'on_async_task_started': {
                  const agentName = (data.agent as string) ?? (parsed.name as string) ?? '';
                  const taskDesc = (data.task as string) ?? '';
                  setAsyncTasks((prev) => {
                    // Idempotent — if the event is replayed don't dupe.
                    if (prev.some((t) => t.taskId === taskId)) return prev;
                    const task: AsyncTask = {
                      taskId,
                      agentName,
                      task: taskDesc,
                      status: 'running',
                      streamedContent: '',
                      toolCalls: [],
                      updates: [],
                      error: null,
                      startedAt: now,
                      updatedAt: now,
                    };
                    return [...prev, task];
                  });
                  break;
                }

                case 'on_async_task_stream': {
                  const chunk = data.chunk as Record<string, unknown> | undefined;
                  const token = (chunk?.content as string) ?? '';
                  if (token) {
                    mutate((t) => ({ ...t, streamedContent: t.streamedContent + token, updatedAt: now }));
                  }
                  break;
                }

                case 'on_async_task_tool_start': {
                  const toolName = (parsed.name as string) ?? 'unknown';
                  const input = (data.input as Record<string, unknown> | null) ?? null;
                  const runId = (data.sub_run_id as string) ?? `tc-${now}`;
                  mutate((t) => ({
                    ...t,
                    toolCalls: [
                      ...t.toolCalls,
                      { id: runId, name: toolName, input, output: null, status: 'running' },
                    ],
                    updatedAt: now,
                  }));
                  break;
                }

                case 'on_async_task_tool_end': {
                  const runId = (data.sub_run_id as string) ?? '';
                  const output = (data.output as string) ?? '';
                  mutate((t) => {
                    // Match the most recent tool call with this id (falling back to the most recent running one).
                    let idx = -1;
                    for (let i = t.toolCalls.length - 1; i >= 0; i--) {
                      const tc = t.toolCalls[i]!;
                      if (runId ? tc.id === runId : tc.status === 'running') {
                        idx = i;
                        break;
                      }
                    }
                    if (idx < 0) return t;
                    const updated = [...t.toolCalls];
                    updated[idx] = { ...updated[idx]!, output, status: 'done' };
                    return { ...t, toolCalls: updated, updatedAt: now };
                  });
                  break;
                }

                case 'on_async_task_updated': {
                  const instr = (data.instructions as string) ?? '';
                  if (instr) {
                    mutate((t) => ({ ...t, updates: [...t.updates, instr], updatedAt: now }));
                  }
                  break;
                }

                case 'on_async_task_done': {
                  const finalOutput = (data.output as string) ?? '';
                  mutate((t) => ({
                    ...t,
                    status: 'done' as AsyncTaskStatus,
                    // Prefer the final consolidated output; fall back to streamed content.
                    streamedContent: finalOutput || t.streamedContent,
                    updatedAt: now,
                  }));
                  break;
                }

                case 'on_async_task_error': {
                  const err = (data.error as string) ?? 'unknown error';
                  mutate((t) => ({ ...t, status: 'error' as AsyncTaskStatus, error: err, updatedAt: now }));
                  break;
                }

                case 'on_async_task_cancelled': {
                  mutate((t) => ({ ...t, status: 'cancelled' as AsyncTaskStatus, updatedAt: now }));
                  break;
                }
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
                // Ensure we have a current iteration (handle edge case of stream without model_start)
                if (!currentIterRef.current) {
                  const newIter: Iteration = {
                    index: iterationsRef.current.length,
                    content: '',
                    toolCalls: [],
                    status: 'streaming',
                  };
                  iterationsRef.current.push(newIter);
                  currentIterRef.current = newIter;
                }
                // Append token to current iteration content
                currentIterRef.current.content += token;

                // Batch tokens via rAF to avoid per-token re-renders
                pendingTokensRef.current += token;
                if (!rafRef.current) {
                  rafRef.current = requestAnimationFrame(() => {
                    pendingTokensRef.current = '';
                    rafRef.current = 0;
                    flushIterationsToMessage();
                  });
                }
              }
              break;
            }

            case 'done': {
              // Flush any pending batched tokens
              if (pendingTokensRef.current) {
                pendingTokensRef.current = '';
                if (rafRef.current) { cancelAnimationFrame(rafRef.current); rafRef.current = 0; }
              }
              // Mark final iteration as done
              if (currentIterRef.current && currentIterRef.current.status !== 'done') {
                currentIterRef.current.status = 'done';
              }
              flushIterationsToMessage();
              currentIterRef.current = null;
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
    setAsyncTasks([]);
    setThreadId(null);
    setError(null);
    setStatus('idle');
    pendingEditsRef.current.clear();
    pendingWritesRef.current.clear();
    turnFileNamesRef.current.clear();
    iterationsRef.current = [];
    currentIterRef.current = null;
  }, [stop]);

  const restore = useCallback((snapshot: {
    messages: ChatMessage[];
    traceEvents: TraceEvent[];
    canvasArtifacts: CanvasArtifact[];
    threadId: string | null;
  }) => {
    setMessages(snapshot.messages);
    setTraceEvents(snapshot.traceEvents);
    setCanvasArtifacts(snapshot.canvasArtifacts);
    setThreadId(snapshot.threadId);
    setError(null);
    setStatus('idle');
  }, []);

  return {
    messages,
    traceEvents,
    canvasArtifacts,
    asyncTasks,
    status,
    threadId,
    error,
    send,
    stop,
    reset,
    restore,
    updateArtifactContent,
    removeArtifact,
  };
}

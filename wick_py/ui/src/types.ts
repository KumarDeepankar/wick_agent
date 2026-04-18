export interface AgentInfo {
  agent_id: string;
  name: string | null;
  model: string;
  system_prompt: string;
  tools: string[];
  subagents: string[];
  middleware: string[];
  hooks: string[];
  backend_type: string;
  sandbox_url: string | null;
  has_interrupt_on: boolean;
  skills: string[];
  loaded_skills: string[];
  memory: string[];
  has_response_format: boolean;
  cache_enabled: boolean;
  debug: boolean;
  max_tool_output_chars: number;
  container_status: 'idle' | 'launching' | 'launched' | 'error' | null;
  container_error: string | null;
}

export interface ToolCallInfo {
  id: string;                              // run_id from SSE
  name: string;                            // tool name
  args: Record<string, unknown> | null;    // from on_tool_start data.input
  output: string | null;                   // from on_tool_end data.output
  status: 'pending' | 'running' | 'done' | 'error';
  // Sub-agent streaming state (only for delegate_to_agent tool calls)
  subAgentName?: string;
  subIterations?: Iteration[];
  subStatus?: 'running' | 'done' | 'error';
  // Async task state (only for start_async_task tool calls). The tool
  // returns a task_id synchronously, but the sub-agent runs in the
  // background — so tool.status flips to 'done' on tool_end while
  // asyncTaskStatus tracks the actual background work.
  asyncTaskId?: string;
  asyncTaskStatus?: AsyncTaskStatus;
  asyncTaskAgentName?: string;
  asyncTaskDescription?: string;
  asyncTaskStreamedContent?: string;
  asyncTaskToolCalls?: AsyncTaskToolCall[];
  asyncTaskUpdates?: string[];
  asyncTaskPolls?: AsyncTaskPoll[];
  asyncTaskFinalOutput?: string;
  asyncTaskError?: string | null;
  // Set on check/update/cancel_async_task calls whose task_id is
  // tracked inline on another tool call — the renderer hides these
  // from the main iteration flow and surfaces them as polls on the
  // parent card instead.
  foldedIntoRunId?: string;
}

export interface AsyncTaskPoll {
  at: number;                                  // timestamp (ms)
  kind: 'check' | 'update' | 'cancel';
  runId: string;                               // tool call run_id for trace
  instruction?: string;                        // for 'update' kind
}

export interface Iteration {
  index: number;
  content: string;                         // model text for this iteration
  toolCalls: ToolCallInfo[];
  status: 'thinking' | 'streaming' | 'tool_running' | 'done';
  llmInputTraceId?: string;                // trace event ID for the on_llm_input that produced this iteration
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: number;
  iterations?: Iteration[];
}

export interface TraceEvent {
  id: string;
  eventType: string;
  timestamp: number;
  data: Record<string, unknown>;
}

export type StreamStatus = 'idle' | 'connecting' | 'streaming' | 'done' | 'error';

// ── Async sub-agent tasks (started via start_async_task) ─────────────────

export type AsyncTaskStatus = 'running' | 'done' | 'error' | 'cancelled';

export interface AsyncTaskToolCall {
  id: string;
  name: string;
  input: Record<string, unknown> | null;
  output: string | null;
  status: 'running' | 'done';
}

export interface AsyncTask {
  taskId: string;
  agentName: string;
  task: string;
  status: AsyncTaskStatus;
  streamedContent: string;
  toolCalls: AsyncTaskToolCall[];
  updates: string[];       // mid-flight instructions injected via update_async_task
  error: string | null;
  startedAt: number;
  updatedAt: number;
}

export type CanvasContentType = 'code' | 'data' | 'document' | 'slides' | 'binary' | 'welcome';

export interface CanvasArtifact {
  id: string;
  filePath: string;
  fileName: string;
  contentType: CanvasContentType;
  content: string | null;
  extension: string;
  timestamp: number;
  isBinary: boolean;
  language?: string;
  status?: 'pending' | 'ok' | 'error';
}

export interface SkillInfo {
  name: string;
  description: string;
  samplePrompts: string[];
  icon: string;
}

export interface HealthResponse {
  status: string;
  agents_loaded: number;
}

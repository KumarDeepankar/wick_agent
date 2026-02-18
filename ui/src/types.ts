export interface AgentInfo {
  agent_id: string;
  name: string | null;
  model: string;
  system_prompt: string;
  tools: string[];
  subagents: string[];
  middleware: string[];
  backend_type: string;
  has_interrupt_on: boolean;
  skills: string[];
  loaded_skills: string[];
  memory: string[];
  has_response_format: boolean;
  cache_enabled: boolean;
  debug: boolean;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: number;
}

export interface TraceEvent {
  id: string;
  eventType: string;
  timestamp: number;
  data: Record<string, unknown>;
}

export type StreamStatus = 'idle' | 'connecting' | 'streaming' | 'done' | 'error';

export interface HealthResponse {
  status: string;
  agents_loaded: number;
}

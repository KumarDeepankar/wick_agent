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
  container_status: 'idle' | 'launching' | 'launched' | 'error' | null;
  container_error: string | null;
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

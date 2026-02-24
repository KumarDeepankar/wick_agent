import type { AgentInfo, HealthResponse, HookInfo, SkillInfo, ToolInfo } from './types';

// ── Token management ───────────────────────────────────────────────────
const TOKEN_KEY = 'wick-auth-token';

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

// ── Auth-aware fetch wrapper ───────────────────────────────────────────
export async function authFetch(url: string, init?: RequestInit): Promise<Response> {
  const token = getToken();
  const headers = new Headers(init?.headers);
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const res = await fetch(url, { ...init, headers, cache: 'no-store' });

  if (res.status === 401) {
    clearToken();
    window.dispatchEvent(new Event('wick-auth-expired'));
  }

  return res;
}

// ── Auth endpoints ─────────────────────────────────────────────────────
export interface AuthUser {
  username: string;
  role: string;
}

export async function login(
  username: string,
  password: string,
): Promise<{ token: string; user: AuthUser }> {
  const res = await fetch('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.detail || `Login failed: ${res.status}`);
  }
  return res.json();
}

export async function fetchMe(): Promise<AuthUser> {
  const token = getToken();
  if (!token) throw new Error('No token');
  const res = await fetch('/auth/me', {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error('Token invalid');
  return res.json();
}

// ── Agent API ──────────────────────────────────────────────────────────

export async function fetchAgents(): Promise<AgentInfo[]> {
  const res = await authFetch('/agents/');
  if (!res.ok) throw new Error(`Failed to fetch agents: ${res.status}`);
  return res.json();
}

export async function fetchSkills(): Promise<SkillInfo[]> {
  const res = await authFetch('/agents/skills/available');
  if (!res.ok) throw new Error(`Failed to fetch skills: ${res.status}`);
  const data = await res.json();
  return (data.skills ?? []).map((s: Record<string, unknown>) => ({
    name: s.name as string,
    description: s.description as string,
    samplePrompts: (s.sample_prompts as string[]) ?? [],
    icon: (s.icon as string) ?? '',
  }));
}

export async function fetchHealth(): Promise<HealthResponse> {
  const res = await fetch('/health');
  if (!res.ok) throw new Error(`Failed to fetch health: ${res.status}`);
  return res.json();
}

export async function fetchFileDownload(filePath: string, agentId?: string): Promise<Blob> {
  const params = new URLSearchParams({ path: filePath });
  if (agentId) params.set('agent_id', agentId);
  const res = await authFetch(`/agents/files/download?${params}`);
  if (!res.ok) throw new Error(`Download failed: ${res.status}`);
  return res.blob();
}

export async function saveFileContent(
  filePath: string,
  content: string,
  agentId?: string,
): Promise<{ status: string; path: string; size: number }> {
  const res = await authFetch('/agents/files/upload', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      path: filePath,
      content,
      agent_id: agentId || undefined,
    }),
  });
  if (!res.ok) throw new Error(`Upload failed: ${res.status}`);
  return res.json();
}

export async function fetchHooks(): Promise<HookInfo[]> {
  const res = await authFetch('/agents/hooks/available');
  if (!res.ok) throw new Error(`Failed to fetch hooks: ${res.status}`);
  const data = await res.json();
  return data.hooks ?? [];
}

export async function updateAgentHooks(
  agentId: string,
  payload: { add?: string[]; remove?: string[] },
): Promise<{ agent_id: string; hooks: string[] }> {
  const res = await authFetch(`/agents/${agentId}/hooks`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  if (!res.ok) throw new Error(`Failed to update hooks: ${res.status}`);
  return res.json();
}

export async function fetchTools(): Promise<ToolInfo[]> {
  const res = await authFetch('/agents/tools/available');
  if (!res.ok) throw new Error(`Failed to fetch tools: ${res.status}`);
  const data = await res.json();
  return (data.tools ?? []).map((t: any) =>
    typeof t === 'string' ? { name: t, source: 'builtin' } : { name: t.name, source: t.source ?? 'builtin' }
  );
}

export async function updateAgentTools(
  agentId: string,
  tools: string[],
): Promise<{ agent_id: string; tools: string[] }> {
  const res = await authFetch(`/agents/${agentId}/tools`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tools }),
  });
  if (!res.ok) throw new Error(`Failed to update tools: ${res.status}`);
  return res.json();
}

export async function updateAgentBackend(
  agentId: string,
  payload: { mode?: 'local' | 'remote'; sandbox_url?: string | null },
): Promise<{ agent_id: string; sandbox_url: string | null; backend_type: string; container_status: string | null; container_error: string | null }> {
  const res = await authFetch(`/agents/${agentId}/backend`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  if (!res.ok) throw new Error(`Failed to update backend: ${res.status}`);
  return res.json();
}

// ── Container file browser ──────────────────────────────────────────────

export interface FileEntry {
  name: string;
  path: string;
  type: 'file' | 'dir';
  size: number;
}

export async function listContainerFiles(
  agentId: string,
  path: string = '/workspace',
): Promise<{ path: string; entries: FileEntry[] }> {
  const params = new URLSearchParams({ path });
  const res = await authFetch(`/agents/${agentId}/files/list?${params}`);
  if (!res.ok) throw new Error(`Failed to list files: ${res.status}`);
  return res.json();
}

export async function readContainerFile(
  agentId: string,
  path: string,
): Promise<{ path: string; content: string }> {
  const params = new URLSearchParams({ path });
  const res = await authFetch(`/agents/${agentId}/files/read?${params}`);
  if (!res.ok) throw new Error(`Failed to read file: ${res.status}`);
  return res.json();
}

export async function exportSlidesAsPptx(filePath: string, agentId?: string): Promise<Blob> {
  const params = new URLSearchParams({ path: filePath });
  if (agentId) params.set('agent_id', agentId);
  const res = await authFetch(`/agents/slides/export?${params}`);
  if (!res.ok) throw new Error(`PPTX export failed: ${res.status}`);
  return res.blob();
}

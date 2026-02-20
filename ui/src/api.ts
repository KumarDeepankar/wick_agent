import type { AgentInfo, HealthResponse } from './types';

export async function fetchAgents(): Promise<AgentInfo[]> {
  const res = await fetch('/agents/');
  if (!res.ok) throw new Error(`Failed to fetch agents: ${res.status}`);
  return res.json();
}

export async function fetchHealth(): Promise<HealthResponse> {
  const res = await fetch('/health');
  if (!res.ok) throw new Error(`Failed to fetch health: ${res.status}`);
  return res.json();
}

export async function fetchFileDownload(filePath: string, agentId?: string): Promise<Blob> {
  const params = new URLSearchParams({ path: filePath });
  if (agentId) params.set('agent_id', agentId);
  const res = await fetch(`/agents/files/download?${params}`);
  if (!res.ok) throw new Error(`Download failed: ${res.status}`);
  return res.blob();
}

export async function saveFileContent(
  filePath: string,
  content: string,
  agentId?: string,
): Promise<{ status: string; path: string; size: number }> {
  const res = await fetch('/agents/files/upload', {
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

export async function exportSlidesAsPptx(filePath: string, agentId?: string): Promise<Blob> {
  const params = new URLSearchParams({ path: filePath });
  if (agentId) params.set('agent_id', agentId);
  const res = await fetch(`/agents/slides/export?${params}`);
  if (!res.ok) throw new Error(`PPTX export failed: ${res.status}`);
  return res.blob();
}

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

import type { CatalogFlags, DappSpec } from '@splendor/dapp-spec';
import { API_URL } from '../config';

/** Admin flags as stored by the API (`CatalogFlags` plus presentation-only fields). */
export type CatalogFlag = CatalogFlags & { featured?: boolean; minVersion?: string; updatedAt?: string };

export type Session = { address: string; isAdmin: boolean };

export type ProjectSummary = { id: string; name: string; updatedAt: string; shareId: string | null };
export type Project = ProjectSummary & { spec: DappSpec; createdAt: string };

export type ReleasePackage = { name: string; repoVersion: string | null; distTags: Record<string, string> };
export type ReleaseRun = { id: number; status: string; conclusion: string | null; url: string; createdAt: string; event: string; title: string };
export type ReleaseStatus = {
  configured: boolean;
  repo: string;
  packages: ReleasePackage[];
  changesets: { file: string; summary: string }[];
  runs: ReleaseRun[];
  errors: string[];
};
export type AuditEntry = { id: number; at: string; actor: string; action: string; target: string | null; detail: string | null };

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly issues?: { path: string; message: string }[],
  ) {
    super(message);
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      ...init,
      credentials: 'include',
      headers: { ...(init.body ? { 'content-type': 'application/json' } : {}), ...init.headers },
    });
  } catch {
    throw new ApiError('Playground API is not reachable', 0);
  }
  const text = await res.text();
  let body: Record<string, unknown> | null = {};
  try {
    body = text ? (JSON.parse(text) as Record<string, unknown>) : {};
  } catch {
    // A proxy or CDN error page (HTML), not the API.
    body = null;
  }
  if (!res.ok) {
    const message = body?.error ?? (res.status >= 500 ? `Playground API error (${res.status})` : res.statusText || `HTTP ${res.status}`);
    throw new ApiError(String(message), res.status, body?.issues as ApiError['issues']);
  }
  if (body === null) throw new ApiError('Playground API sent an unexpected response', res.status);
  return body as T;
}

const json = (method: string, body?: unknown): RequestInit => ({ method, body: body === undefined ? undefined : JSON.stringify(body) });

export const api = {
  health: () => request<{ ok: boolean; hosting: boolean }>('/health'),

  me: () => request<Session>('/auth/me'),
  nonce: () => request<{ nonce: string }>('/auth/nonce', json('POST', {})),
  verify: (signed: { message: string; signatureB64: string; publicKeyB64: string; address: string }) =>
    request<Session>('/auth/verify', json('POST', signed)),
  logout: () => request<{ ok: true }>('/auth/logout', json('POST', {})),

  listProjects: () => request<{ projects: ProjectSummary[] }>('/projects'),
  getProject: (id: string) => request<Project>(`/projects/${encodeURIComponent(id)}`),
  createProject: (spec: DappSpec) => request<Project>('/projects', json('POST', { spec })),
  updateProject: (id: string, spec: DappSpec) => request<Project>(`/projects/${encodeURIComponent(id)}`, json('PUT', { spec })),
  deleteProject: (id: string) => request<{ ok: true }>(`/projects/${encodeURIComponent(id)}`, json('DELETE')),
  shareProject: (id: string) => request<{ shareId: string }>(`/projects/${encodeURIComponent(id)}/share`, json('POST', {})),
  unshareProject: (id: string) => request<{ ok: true }>(`/projects/${encodeURIComponent(id)}/share`, json('DELETE')),
  getShared: (shareId: string) => request<{ name: string; spec: DappSpec }>(`/share/${encodeURIComponent(shareId)}`),

  catalogFlags: () => request<{ flags: Record<string, CatalogFlag> }>('/catalog'),
  setCatalogFlag: (id: string, flag: CatalogFlag) => request<{ flags: Record<string, CatalogFlag> }>(`/admin/catalog/${encodeURIComponent(id)}`, json('PUT', flag)),

  releases: () => request<ReleaseStatus>('/admin/releases'),
  triggerRelease: (input: { channel: 'latest' | 'next'; dryRun: boolean }) =>
    request<{ dispatched: boolean; dryRun: boolean }>('/admin/releases', json('POST', input)),
  audit: () => request<{ entries: AuditEntry[] }>('/admin/audit'),
  admins: () => request<{ admins: { address: string; source: 'env' | 'db'; addedAt: string | null }[] }>('/admin/admins'),
  addAdmin: (address: string) => request<{ ok: true }>('/admin/admins', json('POST', { address })),
  removeAdmin: (address: string) => request<{ ok: true }>(`/admin/admins/${encodeURIComponent(address)}`, json('DELETE')),
};

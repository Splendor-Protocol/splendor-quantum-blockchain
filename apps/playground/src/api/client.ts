import type { CatalogFlags, DappSpec } from '@splendor/dapp-spec';
import { API_URL } from '../config';

/** Admin flags as stored by the API (`CatalogFlags` plus presentation-only fields). */
export type CatalogFlag = CatalogFlags & { featured?: boolean; minVersion?: string; updatedAt?: string };

export type Session = { address: string; isAdmin: boolean };

export type Workspace = { id: string; name: string; createdAt: string; owner?: string; deletedAt?: string | null };
export type ProjectSummary = {
  id: string;
  name: string;
  updatedAt: string;
  shareId: string | null;
  workspaceId?: string | null;
  /** Bumped on every save; older API versions omit it. */
  version?: number;
  /** With a share link: when it stops working (null: until revoked) and how often it was opened. */
  shareExpiresAt?: string | null;
  shareViews?: number;
  /** Set for projects in the trash. */
  deletedAt?: string;
};
export type Project = ProjectSummary & { spec: DappSpec; createdAt: string };
export type ProjectList = { projects: ProjectSummary[]; nextCursor?: string | null };
export type ListOptions = { q?: string; sort?: 'updated' | 'name'; limit?: number; cursor?: string | null };
export type Revision = { version: number; savedAt: string };
export type ShareLink = { shareId: string; expiresAt?: string | null; views?: number };

function listQuery(opts: ListOptions & { workspaceId?: string } = {}): string {
  const q = new URLSearchParams();
  if (opts.workspaceId) q.set('workspaceId', opts.workspaceId);
  if (opts.q?.trim()) q.set('q', opts.q.trim().slice(0, 80));
  if (opts.sort) q.set('sort', opts.sort);
  if (opts.limit) q.set('limit', String(opts.limit));
  if (opts.cursor) q.set('cursor', opts.cursor);
  const s = q.toString();
  return s ? `?${s}` : '';
}

export type ReleasePackage = { name: string; repoVersion: string | null; distTags: Record<string, string> };
export type ReleaseRun = { id: number; status: string; conclusion: string | null; url: string; createdAt: string; event: string; title: string };
export type ReleaseStatus = {
  configured: boolean;
  repo: string;
  /** GitHub web host for run links; older APIs omit it (github.com only). */
  htmlHost?: string;
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
    /** Machine-readable code when the API sends one (`CONFLICT`, `NOT_FOUND`, …). */
    readonly code?: string,
    /** For `CONFLICT`: the project as it is now on the server. */
    readonly current?: ProjectSummary,
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
    throw new ApiError(
      String(message),
      res.status,
      body?.issues as ApiError['issues'],
      typeof body?.code === 'string' ? body.code : undefined,
      body?.current && typeof body.current === 'object' ? (body.current as ProjectSummary) : undefined,
    );
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

  listWorkspaces: () => request<{ workspaces: Workspace[] }>('/workspaces'),
  createWorkspace: (name: string) => request<Workspace>('/workspaces', json('POST', { name })),
  renameWorkspace: (id: string, name: string) => request<Workspace>(`/workspaces/${encodeURIComponent(id)}`, json('PATCH', { name })),
  /** Moves the workspace and its projects to the trash. */
  deleteWorkspace: (id: string) => request<{ ok: true }>(`/workspaces/${encodeURIComponent(id)}`, json('DELETE')),
  listWorkspaceProjects: (id: string, opts: ListOptions = {}) => request<ProjectList>(`/workspaces/${encodeURIComponent(id)}/projects${listQuery(opts)}`),

  listProjects: (workspaceId?: string, opts: ListOptions = {}) => request<ProjectList>(`/projects${listQuery({ ...opts, workspaceId })}`),

  trash: (cursor?: string | null) =>
    request<{ projects: ProjectSummary[]; workspaces: Workspace[]; nextCursor?: string | null }>(
      cursor ? `/trash?cursor=${encodeURIComponent(cursor)}` : '/trash',
    ),
  restoreProject: (id: string) => request<{ ok: true }>(`/trash/projects/${encodeURIComponent(id)}/restore`, json('POST', {})),
  purgeProject: (id: string) => request<{ ok: true }>(`/trash/projects/${encodeURIComponent(id)}`, json('DELETE')),
  restoreWorkspace: (id: string) => request<{ ok: true }>(`/trash/workspaces/${encodeURIComponent(id)}/restore`, json('POST', {})),
  purgeWorkspace: (id: string) => request<{ ok: true }>(`/trash/workspaces/${encodeURIComponent(id)}`, json('DELETE')),

  revisions: (id: string) => request<{ revisions: Revision[] }>(`/projects/${encodeURIComponent(id)}/revisions`),
  /** Saves revision `version` as the newest version, on top of `baseVersion`. */
  restoreRevision: (id: string, version: number, baseVersion: number) =>
    request<Project>(`/projects/${encodeURIComponent(id)}/revisions/${version}/restore`, json('POST', { baseVersion })),
  getProject: (id: string) => request<Project>(`/projects/${encodeURIComponent(id)}`),
  createProject: (spec: DappSpec, workspaceId?: string | null) =>
    request<Project>('/projects', json('POST', workspaceId ? { spec, workspaceId } : { spec })),
  /** With `baseVersion`, a save on top of a newer copy fails with `ApiError` code `CONFLICT`. */
  updateProject: (id: string, spec: DappSpec, baseVersion?: number) =>
    request<Project>(`/projects/${encodeURIComponent(id)}`, json('PUT', baseVersion ? { spec, baseVersion } : { spec })),
  /** `baseVersion`: the version the change started from; a newer copy on the server is a `CONFLICT`. */
  patchProject: (id: string, patch: { name?: string; workspaceId?: string | null; baseVersion?: number }) =>
    request<ProjectSummary & { createdAt: string }>(`/projects/${encodeURIComponent(id)}`, json('PATCH', patch)),
  duplicateProject: (id: string) => request<Project>(`/projects/${encodeURIComponent(id)}/duplicate`, json('POST', {})),
  /** Moves the project to the trash. */
  deleteProject: (id: string) => request<{ ok: true }>(`/projects/${encodeURIComponent(id)}`, json('DELETE')),
  shareProject: (id: string, expiresInDays?: number) =>
    request<ShareLink>(`/projects/${encodeURIComponent(id)}/share`, json('POST', expiresInDays ? { expiresInDays } : {})),
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

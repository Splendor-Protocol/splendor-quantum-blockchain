import type { DappSpec } from '@splendor/dapp-spec';
import type { Workspace } from '../api/client';

const KEY = 'sqc-playground-workspaces-v1';

type LocalStore = { workspaces: Workspace[] };

/** Thrown when the browser refuses a write (storage full or disabled), so the UI can say so. */
export class LocalStorageError extends Error {
  constructor(what: string) {
    super(`Could not save ${what} in this browser (storage is full or disabled).`);
    this.name = 'LocalStorageError';
  }
}

function read(): LocalStore {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { workspaces: [] };
    const parsed = JSON.parse(raw) as LocalStore;
    return { workspaces: Array.isArray(parsed.workspaces) ? parsed.workspaces : [] };
  } catch {
    return { workspaces: [] };
  }
}

/** True when the store was written; false when the browser refused (quota, private mode). */
function write(store: LocalStore): boolean {
  try {
    localStorage.setItem(KEY, JSON.stringify(store));
    return true;
  } catch {
    return false;
  }
}

export function listLocalWorkspaces(): Workspace[] {
  return read().workspaces;
}

/** Throws `LocalStorageError` when the workspace could not be stored. */
export function createLocalWorkspace(name: string): Workspace {
  const store = read();
  const ws: Workspace = {
    id: `local-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`,
    name: name.trim().slice(0, 80) || 'My workspace',
    createdAt: new Date().toISOString(),
  };
  store.workspaces = [ws, ...store.workspaces];
  if (!write(store)) throw new LocalStorageError('the workspace');
  return ws;
}

/** Throws `LocalStorageError` when the new name could not be stored. */
export function renameLocalWorkspace(id: string, name: string): Workspace | null {
  const store = read();
  const ws = store.workspaces.find((w) => w.id === id);
  if (!ws) return null;
  ws.name = name.trim().slice(0, 80) || ws.name;
  if (!write(store)) throw new LocalStorageError('the workspace name');
  return ws;
}

/** Throws `LocalStorageError` when the change could not be stored. */
export function deleteLocalWorkspace(id: string): boolean {
  const store = read();
  const next = store.workspaces.filter((w) => w.id !== id);
  if (next.length === store.workspaces.length) return false;
  if (!write({ workspaces: next })) throw new LocalStorageError('the workspace list');
  clearLocalProjects(id);
  return true;
}

// ---------------------------------------------------------------------------------------------
// Projects kept in this browser, keyed by workspace id. Used for local workspaces and for anyone
// who is not signed in.
// ---------------------------------------------------------------------------------------------

const PROJECTS_KEY = 'sqc-playground-workspace-projects-v1';
export const MAX_LOCAL_PROJECTS_PER_WORKSPACE = 50;

export type LocalProject = { id: string; name: string; spec: DappSpec; createdAt: string; updatedAt: string };
type ProjectStore = Record<string, LocalProject[]>;

/** Local workspaces get `local-…` ids; anything else lives in the API. */
export function isLocalWorkspaceId(id: string | null | undefined): boolean {
  return typeof id === 'string' && id.startsWith('local-');
}

function readProjects(): ProjectStore {
  try {
    const raw = localStorage.getItem(PROJECTS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {};
    const out: ProjectStore = {};
    for (const [ws, list] of Object.entries(parsed as Record<string, unknown>)) {
      if (!Array.isArray(list)) continue;
      out[ws] = list.filter(
        (p): p is LocalProject => typeof p === 'object' && p !== null && typeof (p as LocalProject).id === 'string' && typeof (p as LocalProject).spec === 'object',
      );
    }
    return out;
  } catch {
    return {};
  }
}

/** True when the store was written; false when storage is full or disabled. */
function writeProjects(store: ProjectStore): boolean {
  try {
    localStorage.setItem(PROJECTS_KEY, JSON.stringify(store));
    return true;
  } catch {
    return false;
  }
}

/** Newest first. */
export function listLocalProjects(workspaceId: string): LocalProject[] {
  return [...(readProjects()[workspaceId] ?? [])].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

export function getLocalProject(workspaceId: string, id: string): LocalProject | null {
  return (readProjects()[workspaceId] ?? []).find((p) => p.id === id) ?? null;
}

export type LocalSaveResult = {
  project: LocalProject;
  /** False when the browser refused the write (quota / disabled): nothing was persisted. */
  saved: boolean;
  /** Projects dropped to stay within the per-workspace limit (oldest first in the list). */
  trimmed: LocalProject[];
};

/**
 * Create (no `id`) or update a project in a workspace, and report what happened: whether the
 * write stuck and which older projects were trimmed to stay within the limit.
 */
export function saveLocalProjectWithInfo(workspaceId: string, spec: DappSpec, id?: string | null): LocalSaveResult {
  const store = readProjects();
  const list = store[workspaceId] ?? [];
  const now = new Date().toISOString();
  const existing = id ? list.find((p) => p.id === id) : undefined;
  const project: LocalProject = existing
    ? { ...existing, name: spec.name, spec, updatedAt: now }
    : { id: `lp-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, name: spec.name, spec, createdAt: now, updatedAt: now };
  const ordered = [project, ...list.filter((p) => p.id !== project.id)];
  store[workspaceId] = ordered.slice(0, MAX_LOCAL_PROJECTS_PER_WORKSPACE);
  const trimmed = ordered.slice(MAX_LOCAL_PROJECTS_PER_WORKSPACE);
  const saved = writeProjects(store);
  return { project, saved, trimmed: saved ? trimmed : [] };
}

/** Create (no `id`) or update a project in a workspace. Returns the saved project. */
export function saveLocalProject(workspaceId: string, spec: DappSpec, id?: string | null): LocalProject {
  return saveLocalProjectWithInfo(workspaceId, spec, id).project;
}

/** False when the project does not exist, or when the change could not be stored. */
export function deleteLocalProject(workspaceId: string, id: string): boolean {
  const store = readProjects();
  const list = store[workspaceId] ?? [];
  const next = list.filter((p) => p.id !== id);
  if (next.length === list.length) return false;
  store[workspaceId] = next;
  return writeProjects(store);
}

/** Drop a workspace's local projects (when the workspace itself is deleted). */
export function clearLocalProjects(workspaceId: string): boolean {
  const store = readProjects();
  if (!(workspaceId in store)) return true;
  delete store[workspaceId];
  return writeProjects(store);
}

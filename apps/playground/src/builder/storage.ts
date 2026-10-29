import { isContainer, migrateSpec, type DappSpec } from '@splendor/dapp-spec';

const KEY = 'splendor-playground:draft:v1';
/** A draft that was replaced before it was saved anywhere else (template, New, Import, Remix). */
const STASH_KEY = 'splendor-playground:draft-stash:v1';

export type Draft = {
  spec: DappSpec;
  projectId: string | null;
  savedAt: string;
  /** Cloud version the draft is based on, when it belongs to an account project. */
  version?: number;
  /** Edits that the account copy does not have yet. */
  dirty?: boolean;
  /**
   * False while the draft is exactly what was opened (a template, a project, a stash) and nobody
   * changed it. Such a draft is never stashed, so it cannot push real work out of the one-slot
   * stash. Missing (older drafts) counts as edited.
   */
  edited?: boolean;
};

/** Enough structure for the editor to open it; value problems are shown as issues, not dropped. */
function isEditable(spec: DappSpec): boolean {
  if (typeof spec.name !== 'string' || !Array.isArray(spec.pages) || spec.pages.length === 0) return false;
  if (typeof spec.theme !== 'object' || spec.theme === null) return false;
  return spec.pages.every(
    (p) => typeof p === 'object' && p !== null && typeof p.path === 'string' && typeof p.title === 'string' && !!p.root && isContainer(p.root),
  );
}

function read(key: string): Draft | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { spec?: unknown; projectId?: unknown; savedAt?: unknown; version?: unknown; dirty?: unknown; edited?: unknown };
    const spec = migrateSpec(parsed.spec);
    if (!isEditable(spec)) return null;
    return {
      spec,
      projectId: typeof parsed.projectId === 'string' ? parsed.projectId : null,
      savedAt: typeof parsed.savedAt === 'string' ? parsed.savedAt : new Date(0).toISOString(),
      ...(typeof parsed.version === 'number' && Number.isSafeInteger(parsed.version) && parsed.version > 0 ? { version: parsed.version } : {}),
      ...(parsed.dirty === true ? { dirty: true } : {}),
      ...(parsed.edited === false ? { edited: false } : {}),
    };
  } catch {
    return null;
  }
}

/** False when storage is full or disabled; the editor keeps working in memory and says so. */
function write(key: string, draft: Omit<Draft, 'savedAt'>): boolean {
  try {
    localStorage.setItem(key, JSON.stringify({ ...draft, savedAt: new Date().toISOString() }));
    return true;
  } catch {
    return false;
  }
}

function remove(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    /* ignore */
  }
}

/**
 * Last draft from this browser, migrated to the current spec version. A draft with invalid values
 * (a half-typed address, a duplicate id) still opens so the user can fix it; only drafts the editor
 * cannot open at all are dropped.
 */
export function loadDraft(): Draft | null {
  return read(KEY);
}

/** True when the draft was written; false when the browser refused (storage full or disabled). */
export function saveDraft(draft: Omit<Draft, 'savedAt'>): boolean {
  return write(KEY, draft);
}

export function clearDraft(): void {
  remove(KEY);
}

/**
 * Before something replaces the current draft: keep it aside when it exists nowhere else (no
 * account project), or when it has edits its account project does not have yet. An untouched
 * draft is not stashed. Returns true when a draft was stashed.
 */
export function stashDraft(): boolean {
  const draft = read(KEY);
  if (!draft || draft.edited === false || (draft.projectId && !draft.dirty)) return false;
  return write(STASH_KEY, {
    spec: draft.spec,
    projectId: draft.projectId,
    ...(draft.projectId && draft.version ? { version: draft.version } : {}),
    ...(draft.projectId ? { dirty: true } : {}),
  });
}

export function peekStashedDraft(): Draft | null {
  return read(STASH_KEY);
}

/** Returns the stashed draft and forgets it. */
export function takeStashedDraft(): Draft | null {
  const draft = read(STASH_KEY);
  remove(STASH_KEY);
  return draft;
}

export function discardStashedDraft(): void {
  remove(STASH_KEY);
}

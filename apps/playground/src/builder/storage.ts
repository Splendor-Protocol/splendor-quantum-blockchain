import { isContainer, migrateSpec, type DappSpec } from '@splendor/dapp-spec';

const KEY = 'splendor-playground:draft:v1';

export type Draft = { spec: DappSpec; projectId: string | null; savedAt: string };

/** Enough structure for the editor to open it; value problems are shown as issues, not dropped. */
function isEditable(spec: DappSpec): boolean {
  if (typeof spec.name !== 'string' || !Array.isArray(spec.pages) || spec.pages.length === 0) return false;
  if (typeof spec.theme !== 'object' || spec.theme === null) return false;
  return spec.pages.every(
    (p) => typeof p === 'object' && p !== null && typeof p.path === 'string' && typeof p.title === 'string' && !!p.root && isContainer(p.root),
  );
}

/**
 * Last draft from this browser, migrated to the current spec version. A draft with invalid values
 * (a half-typed address, a duplicate id) still opens so the user can fix it; only drafts the editor
 * cannot open at all are dropped.
 */
export function loadDraft(): Draft | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { spec?: unknown; projectId?: unknown; savedAt?: unknown };
    const spec = migrateSpec(parsed.spec);
    if (!isEditable(spec)) return null;
    return {
      spec,
      projectId: typeof parsed.projectId === 'string' ? parsed.projectId : null,
      savedAt: typeof parsed.savedAt === 'string' ? parsed.savedAt : new Date(0).toISOString(),
    };
  } catch {
    return null;
  }
}

export function saveDraft(draft: Omit<Draft, 'savedAt'>): void {
  try {
    localStorage.setItem(KEY, JSON.stringify({ ...draft, savedAt: new Date().toISOString() }));
  } catch {
    /* storage full or disabled: the editor keeps working in memory */
  }
}

export function clearDraft(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

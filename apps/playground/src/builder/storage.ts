import { migrateSpec, validateSpec, type DappSpec } from '@splendor/dapp-spec';

const KEY = 'splendor-playground:draft:v1';

export type Draft = { spec: DappSpec; projectId: string | null; savedAt: string };

/** Last draft from this browser, migrated to the current spec version. Invalid drafts are dropped. */
export function loadDraft(): Draft | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { spec?: unknown; projectId?: unknown; savedAt?: unknown };
    const spec = migrateSpec(parsed.spec);
    // Shape check only: a draft may legitimately have unfilled required props.
    const r = validateSpec(spec);
    if (!r.ok && r.issues.some((i) => !i.message.includes('required'))) return null;
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

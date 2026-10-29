import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, type ProjectSummary, type Workspace } from '../api/client';
import { useConfirm } from '../components/ConfirmDialog';

const TRASH_DAYS = 30;

function daysLeft(deletedAt: string | null | undefined): number | null {
  if (!deletedAt) return null;
  const left = Math.ceil((Date.parse(deletedAt) + TRASH_DAYS * 24 * 3600 * 1000 - Date.now()) / (24 * 3600 * 1000));
  return Math.max(0, left);
}

/** Account projects and workspaces deleted in the last 30 days: restore them, or delete for good. */
export function Trash({ reloadKey, onRestored }: { reloadKey: number; onRestored: () => void }) {
  const [trash, setTrash] = useState<{ projects: ProjectSummary[]; workspaces: Workspace[]; nextCursor?: string | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [confirmDialog, confirm] = useConfirm();

  const loadMore = async () => {
    if (!trash?.nextCursor) return;
    try {
      const more = await api.trash(trash.nextCursor);
      setTrash((t) => (t ? { projects: [...t.projects, ...more.projects], workspaces: t.workspaces, nextCursor: more.nextCursor ?? null } : t));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  /** Refreshes the list; it never clears an error, so a failed restore or purge stays on screen. */
  const reload = useCallback(async () => {
    try {
      setTrash(await api.trash());
    } catch (e) {
      // An older API without a trash: show nothing rather than an error.
      if (e instanceof ApiError && e.status === 404) setTrash({ projects: [], workspaces: [] });
      else setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    setError(null);
    void reload();
  }, [reload, reloadKey]);

  const act = async (fn: () => Promise<unknown>, restored: boolean) => {
    setError(null);
    let ok = true;
    try {
      await fn();
    } catch (e) {
      ok = false;
      setError(e instanceof Error ? e.message : String(e));
    }
    await reload();
    if (ok && restored) onRestored();
  };

  const purge = async (kind: 'project' | 'workspace', name: string, fn: () => Promise<unknown>) => {
    const ok = await confirm({
      title: `Delete "${name}" for good?`,
      message: kind === 'workspace' ? 'The workspace and the projects deleted with it cannot be restored afterwards.' : 'It cannot be restored afterwards.',
      confirmLabel: 'Delete for good',
      danger: true,
    });
    if (ok) await act(fn, false);
  };

  const count = (trash?.projects.length ?? 0) + (trash?.workspaces.length ?? 0);
  if (!count && !error) return null;

  return (
    <section aria-labelledby="trash-title" data-testid="trash">
      {confirmDialog}
      <div className="pg-row">
        <h2 id="trash-title" style={{ margin: 0 }}>
          Trash ({count}
          {trash?.nextCursor ? '+' : ''})
        </h2>
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? 'Hide' : 'Show'}
        </button>
      </div>
      <p className="pg-muted">Deleted account projects and workspaces stay here for {TRASH_DAYS} days.</p>
      {error ? (
        <p className="pg-error" role="alert">
          {error}
        </p>
      ) : null}
      {open && trash ? (
        <ul className="pg-list">
          {trash.workspaces.map((w) => (
            <li key={`w-${w.id}`} className="pg-row" data-testid={`trash-workspace-${w.id}`}>
              <span>
                Workspace <strong>{w.name}</strong> <small className="pg-muted">{daysLeft(w.deletedAt)} days left</small>
              </span>
              <span className="pg-spacer" />
              <button type="button" className="pg-btn pg-btn-sm" onClick={() => void act(() => api.restoreWorkspace(w.id), true)}>
                Restore
              </button>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void purge('workspace', w.name, () => api.purgeWorkspace(w.id))}>
                Delete for good
              </button>
            </li>
          ))}
          {trash.projects.map((p) => (
            <li key={`p-${p.id}`} className="pg-row" data-testid={`trash-project-${p.id}`}>
              <span>
                <strong>{p.name}</strong> <small className="pg-muted">{daysLeft(p.deletedAt)} days left</small>
              </span>
              <span className="pg-spacer" />
              <button type="button" className="pg-btn pg-btn-sm" aria-label={`Restore ${p.name}`} onClick={() => void act(() => api.restoreProject(p.id), true)}>
                Restore
              </button>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void purge('project', p.name, () => api.purgeProject(p.id))}>
                Delete for good
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      {open && trash?.nextCursor ? (
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void loadMore()}>
          Load more
        </button>
      ) : null}
    </section>
  );
}

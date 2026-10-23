import { useEffect, useState } from 'react';
import { migrateSpec, type CatalogEntry, type DappSpec } from '@splendor/dapp-spec';
import { api, ApiError, type ProjectSummary } from '../api/client';
import { SignInPanel } from '../auth/SignInPanel';
import { useSession } from '../auth/session';
import { validateForPreview } from '../preview/validatePreview';

/**
 * `unsaved`: the account copy is behind because the draft has issues the API would reject.
 * `conflict`: the account has a newer copy than the one these edits started from.
 */
export type CloudState = { status: 'local' | 'saving' | 'saved' | 'unsaved' | 'conflict' | 'error'; message?: string };

/** Short label for the studio top bar. */
export function cloudLabel(cloud: CloudState): string {
  switch (cloud.status) {
    case 'local':
      return 'In this browser';
    case 'saving':
      return 'Syncing…';
    case 'saved':
      return 'Synced';
    case 'unsaved':
      return 'Not synced';
    case 'conflict':
      return 'Conflict';
    case 'error':
      return 'Sync failed';
  }
}

const shareUrlFor = (shareId: string) => `${window.location.origin}/s/${shareId}`;

/** Parse pasted spec JSON the same way a shared project is checked; returns an error message on failure. */
export function parseSpecJson(text: string, catalog: readonly CatalogEntry[]): { spec: DappSpec } | { error: string } {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return { error: 'That is not valid JSON.' };
  }
  try {
    const r = validateForPreview(migrateSpec(raw), catalog);
    if (!r.spec) return { error: `This spec has problems: ${r.issues.slice(0, 3).map((i) => `${i.path || 'spec'} ${i.message}`).join('; ')}` };
    return { spec: r.spec };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}

function downloadJson(spec: DappSpec) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(spec, null, 2)], { type: 'application/json' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `${spec.name.replace(/[^\w.-]+/g, '-') || 'dapp'}.json`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

export function ProjectPanel({
  spec,
  catalog,
  projectId,
  cloud,
  valid,
  onSaveNew,
  onOpen,
  onImport,
  onDeleted,
  onNew,
  onOpenNewer,
  onKeepMine,
}: {
  spec: DappSpec;
  catalog: readonly CatalogEntry[];
  projectId: string | null;
  cloud: CloudState;
  valid: boolean;
  onSaveNew: () => Promise<void>;
  onOpen: (id: string) => Promise<void>;
  onImport: (spec: DappSpec) => void;
  /** The project was deleted from the account; the editor keeps the local draft. */
  onDeleted: (id: string) => void;
  onNew: () => void;
  /** Conflict: reload the account copy, dropping these edits. */
  onOpenNewer?: () => Promise<void>;
  /** Conflict: save these edits over the account copy. */
  onKeepMine?: () => Promise<void>;
}) {
  const { session } = useSession();
  const [projects, setProjects] = useState<ProjectSummary[] | null>(null);
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [importText, setImportText] = useState('');
  const [importError, setImportError] = useState<string | null>(null);

  const reload = () =>
    api
      .listProjects()
      .then((r) => setProjects(r.projects))
      .catch(() => setProjects([]));

  useEffect(() => {
    if (!session) {
      setProjects(null);
      return;
    }
    void reload();
  }, [session, projectId]);

  // An already shared project shows its link instead of asking to share again.
  useEffect(() => {
    const current = projects?.find((p) => p.id === projectId);
    setShareUrl(current?.shareId ? shareUrlFor(current.shareId) : null);
  }, [projects, projectId]);

  const guard = async (fn: () => Promise<void>) => {
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  };

  const share = () =>
    guard(async () => {
      if (!projectId) return;
      const { shareId } = await api.shareProject(projectId);
      setShareUrl(shareUrlFor(shareId));
      void reload();
    });

  const unshare = () =>
    guard(async () => {
      if (!projectId || !window.confirm('Stop sharing? The current link stops working for everyone.')) return;
      await api.unshareProject(projectId);
      setShareUrl(null);
      void reload();
    });

  const remove = (p: ProjectSummary) =>
    guard(async () => {
      if (!window.confirm(`Delete "${p.name}" from your account? This cannot be undone.`)) return;
      await api.deleteProject(p.id);
      if (p.id === projectId) onDeleted(p.id);
      void reload();
    });

  const importSpec = () => {
    const r = parseSpecJson(importText, catalog);
    if ('error' in r) {
      setImportError(r.error);
      return;
    }
    setImportError(null);
    setImportText('');
    onImport(r.spec);
  };

  return (
    <div className="pg-stack">
      <p
        className={cloud.status === 'error' || cloud.status === 'unsaved' || cloud.status === 'conflict' ? 'pg-error' : 'pg-muted'}
        style={{ margin: 0 }}
        data-testid="save-status"
        role="status"
      >
        {cloud.status === 'local' && 'Saved in this browser.'}
        {cloud.status === 'saving' && 'Saving to your account…'}
        {cloud.status === 'saved' && 'Saved to your account.'}
        {cloud.status === 'unsaved' && 'Latest changes are only in this browser. Fix the listed issues to save them to your account.'}
        {cloud.status === 'conflict' && 'A newer copy was saved in another tab or device. Choose which one to keep.'}
        {cloud.status === 'error' && `Cloud save failed: ${cloud.message ?? 'unknown error'}`}
      </p>
      {cloud.status === 'conflict' && onOpenNewer && onKeepMine ? (
        <div className="pg-row">
          <button type="button" className="pg-btn pg-btn-sm" onClick={() => void onOpenNewer()}>
            Open the newer copy
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void onKeepMine()}>
            Keep my version
          </button>
        </div>
      ) : null}
      <SignInPanel />
      {session ? (
        <>
          {!projectId ? (
            <button type="button" className="pg-btn" disabled={!valid} onClick={() => void onSaveNew()}>
              Save to my account
            </button>
          ) : shareUrl ? (
            <div className="pg-field">
              <label htmlFor="share-url">Share link (read-only, anyone can remix)</label>
              <input id="share-url" readOnly value={shareUrl} onFocus={(e) => e.currentTarget.select()} />
              <button type="button" className="pg-btn pg-btn-danger pg-btn-sm" onClick={() => void unshare()} data-testid="unshare">
                Stop sharing
              </button>
            </div>
          ) : (
            <button type="button" className="pg-btn pg-btn-ghost" onClick={() => void share()}>
              Create share link
            </button>
          )}
          {!valid && !projectId ? <small className="pg-muted">Fix the listed issues to save to your account.</small> : null}
          {error ? (
            <p className="pg-error" role="alert">
              {error}
            </p>
          ) : null}
          {projects?.length ? (
            <div>
              <p className="pg-list-title">My projects</p>
              <ul className="pg-list" data-testid="my-projects">
                {projects.map((p) => (
                  <li key={p.id} style={{ display: 'flex' }}>
                    <button type="button" className={p.id === projectId ? 'is-active' : ''} onClick={() => void onOpen(p.id)}>
                      {p.name}
                      {p.shareId ? <span className="pg-badge">shared</span> : null}
                      <span className="pg-spacer" />
                      <small className="pg-muted">{new Date(p.updatedAt).toLocaleDateString()}</small>
                    </button>
                    <button type="button" className="pg-icon-btn" style={{ width: 'auto' }} aria-label={`Delete project ${p.name}`} onClick={() => void remove(p)}>
                      ✕
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </>
      ) : null}
      <button type="button" className="pg-btn pg-btn-ghost" onClick={onNew}>
        New project…
      </button>
      <details>
        <summary>Spec JSON</summary>
        <div className="pg-stack">
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => downloadJson(spec)}>
            Download spec.json
          </button>
          <pre className="pg-output">{JSON.stringify(spec, null, 2)}</pre>
          <div className="pg-field">
            <label htmlFor="import-spec">Import a spec (replaces this draft)</label>
            <textarea id="import-spec" rows={5} className="pg-mono" value={importText} onChange={(e) => setImportText(e.target.value)} placeholder='{ "version": 1, … }' />
          </div>
          <button type="button" className="pg-btn pg-btn-sm" disabled={!importText.trim()} onClick={importSpec} data-testid="import-spec">
            Import
          </button>
          {importError ? (
            <p className="pg-error" role="alert">
              {importError}
            </p>
          ) : null}
        </div>
      </details>
    </div>
  );
}

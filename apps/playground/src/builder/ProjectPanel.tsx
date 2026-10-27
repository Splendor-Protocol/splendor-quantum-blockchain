import { useEffect, useState } from 'react';
import { migrateSpec, type CatalogEntry, type DappSpec } from '@splendor/dapp-spec';
import { api, ApiError, type ProjectSummary, type Revision } from '../api/client';
import { SignInPanel } from '../auth/SignInPanel';
import { useSession } from '../auth/session';
import { CopyButton } from '../components/CodeBlock';
import { useConfirm } from '../components/ConfirmDialog';
import { validateForPreview } from '../preview/validatePreview';

/**
 * `unsaved`: the account copy is behind because the draft has issues the API would reject.
 * `conflict`: the account has a newer copy than the one these edits started from.
 * `storage`: the browser refused to keep the draft (storage full or disabled) and no account copy has it.
 */
export type CloudState = { status: 'local' | 'saving' | 'saved' | 'unsaved' | 'conflict' | 'error' | 'storage'; message?: string };

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
    case 'storage':
      return 'Not saved';
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
  saving = false,
  valid,
  onSaveNew,
  onOpen,
  onImport,
  onDeleted,
  onNew,
  onOpenNewer,
  onKeepMine,
  onRestoreRevision,
}: {
  spec: DappSpec;
  catalog: readonly CatalogEntry[];
  projectId: string | null;
  cloud: CloudState;
  /** A save to the account is in flight. */
  saving?: boolean;
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
  /** Save an older version of the open project as its newest one, and open it. */
  onRestoreRevision?: (version: number) => Promise<void>;
}) {
  const { session } = useSession();
  const [confirmDialog, confirm] = useConfirm();
  const [projects, setProjects] = useState<ProjectSummary[] | null>(null);
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [importText, setImportText] = useState('');
  const [importError, setImportError] = useState<string | null>(null);
  const [projectQuery, setProjectQuery] = useState('');
  const [expiryDays, setExpiryDays] = useState<'' | '7' | '30'>('');
  const [revisions, setRevisions] = useState<Revision[] | null>(null);
  const current = projects?.find((p) => p.id === projectId);
  const shownProjects = (projects ?? []).filter((p) => !projectQuery.trim() || p.name.toLowerCase().includes(projectQuery.trim().toLowerCase()));

  const loadRevisions = () => {
    if (!projectId) return;
    void api
      .revisions(projectId)
      .then((r) => setRevisions(r.revisions))
      .catch(() => setRevisions([]));
  };
  useEffect(() => setRevisions(null), [projectId]);

  const restoreRevision = (version: number) =>
    guard(async () => {
      if (!onRestoreRevision) return;
      const ok = await confirm({
        title: `Restore version ${version}?`,
        message: 'It becomes the newest version. What is saved now stays in the history, so this can be undone the same way.',
        confirmLabel: 'Restore this version',
      });
      if (ok) await onRestoreRevision(version);
    });

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
      const { shareId } = await api.shareProject(projectId, expiryDays ? Number(expiryDays) : undefined);
      setShareUrl(shareUrlFor(shareId));
      void reload();
    });

  const unshare = () =>
    guard(async () => {
      if (!projectId) return;
      const ok = await confirm({ title: 'Stop sharing?', message: 'The current link stops working for everyone.', confirmLabel: 'Stop sharing', danger: true });
      if (!ok) return;
      await api.unshareProject(projectId);
      setShareUrl(null);
      void reload();
    });

  const remove = (p: ProjectSummary) =>
    guard(async () => {
      const ok = await confirm({
        title: `Delete "${p.name}"?`,
        message: 'It moves to the trash on the home page, where it can be restored for 30 days.',
        confirmLabel: 'Delete project',
        danger: true,
      });
      if (!ok) return;
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
      {confirmDialog}
      <p
        className={cloud.status === 'error' || cloud.status === 'unsaved' || cloud.status === 'conflict' || cloud.status === 'storage' ? 'pg-error' : 'pg-muted'}
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
        {cloud.status === 'storage' &&
          'This browser refused to save the draft (storage full or disabled). Download the spec or save it to your account before you leave.'}
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
            <button type="button" className="pg-btn" disabled={!valid || saving} onClick={() => void onSaveNew()}>
              Save to my account
            </button>
          ) : shareUrl ? (
            <div className="pg-field">
              <label htmlFor="share-url">Share link (read-only, anyone can remix)</label>
              <input id="share-url" readOnly value={shareUrl} onFocus={(e) => e.currentTarget.select()} />
              <small className="pg-muted" data-testid="share-details">
                {current?.shareExpiresAt ? `Expires ${new Date(current.shareExpiresAt).toLocaleDateString()}` : 'Works until you stop sharing'}
                {current?.shareViews ? ` \u00B7 opened ${current.shareViews} time${current.shareViews === 1 ? '' : 's'}` : ''}
              </small>
              <div className="pg-row">
                <CopyButton text={shareUrl} label="Copy link" testId="share-copy" />
                <button type="button" className="pg-btn pg-btn-danger pg-btn-sm" onClick={() => void unshare()} data-testid="unshare">
                  Stop sharing
                </button>
              </div>
            </div>
          ) : (
            <div className="pg-row">
              <button type="button" className="pg-btn pg-btn-ghost" onClick={() => void share()}>
                Create share link
              </button>
              <label className="pg-muted" htmlFor="share-expiry">
                Link works
              </label>
              <select id="share-expiry" value={expiryDays} onChange={(e) => setExpiryDays(e.target.value as '' | '7' | '30')}>
                <option value="">until I stop sharing</option>
                <option value="7">for 7 days</option>
                <option value="30">for 30 days</option>
              </select>
            </div>
          )}
          {projectId && onRestoreRevision ? (
            <details
              data-testid="version-history"
              onToggle={(e) => {
                if (e.currentTarget.open && revisions === null) loadRevisions();
              }}
            >
              <summary>Version history</summary>
              {revisions === null ? (
                <p className="pg-muted" role="status">
                  Loading versions…
                </p>
              ) : revisions.length ? (
                <ul className="pg-list">
                  {revisions.map((r, i) => (
                    <li key={r.version} className="pg-row">
                      <span>
                        Version {r.version} <small className="pg-muted">{new Date(r.savedAt).toLocaleString()}</small>
                      </span>
                      <span className="pg-spacer" />
                      {i === 0 ? (
                        <small className="pg-muted">current</small>
                      ) : (
                        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Restore version ${r.version}`} onClick={() => void restoreRevision(r.version)}>
                          Restore
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="pg-muted">No saved versions yet.</p>
              )}
            </details>
          ) : null}
          {!valid && !projectId ? <small className="pg-muted">Fix the listed issues to save to your account.</small> : null}
          {error ? (
            <p className="pg-error" role="alert">
              {error}
            </p>
          ) : null}
          {projects?.length ? (
            <div>
              <p className="pg-list-title">My projects</p>
              {projects.length > 5 ? (
                <input
                  type="search"
                  aria-label="Search my projects"
                  placeholder="Search my projects"
                  value={projectQuery}
                  maxLength={80}
                  onChange={(e) => setProjectQuery(e.target.value)}
                  style={{ marginBottom: '0.4rem' }}
                />
              ) : null}
              {!shownProjects.length ? <p className="pg-muted">No projects match "{projectQuery.trim()}".</p> : null}
              <ul className="pg-list" data-testid="my-projects">
                {shownProjects.map((p) => (
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

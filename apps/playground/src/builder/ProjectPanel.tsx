import { useEffect, useState } from 'react';
import type { DappSpec } from '@splendor/dapp-spec';
import { api, ApiError, type ProjectSummary } from '../api/client';
import { SignInPanel } from '../auth/SignInPanel';
import { useSession } from '../auth/session';

export type CloudState = { status: 'local' | 'saving' | 'saved' | 'error'; message?: string };

export function ProjectPanel({
  spec,
  projectId,
  cloud,
  valid,
  onSaveNew,
  onOpen,
  onNew,
}: {
  spec: DappSpec;
  projectId: string | null;
  cloud: CloudState;
  valid: boolean;
  onSaveNew: () => Promise<void>;
  onOpen: (id: string) => Promise<void>;
  onNew: () => void;
}) {
  const { session } = useSession();
  const [projects, setProjects] = useState<ProjectSummary[] | null>(null);
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!session) {
      setProjects(null);
      return;
    }
    api
      .listProjects()
      .then((r) => setProjects(r.projects))
      .catch(() => setProjects([]));
  }, [session, projectId]);

  const share = async () => {
    if (!projectId) return;
    setError(null);
    try {
      const { shareId } = await api.shareProject(projectId);
      setShareUrl(`${window.location.origin}/s/${shareId}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  };

  return (
    <div className="pg-stack">
      <p className="pg-muted" style={{ margin: 0 }} data-testid="save-status">
        {cloud.status === 'local' && 'Saved in this browser.'}
        {cloud.status === 'saving' && 'Saving to your account…'}
        {cloud.status === 'saved' && 'Saved to your account.'}
        {cloud.status === 'error' && `Cloud save failed: ${cloud.message ?? 'unknown error'}`}
      </p>
      <SignInPanel />
      {session ? (
        <>
          {!projectId ? (
            <button type="button" className="pg-btn" disabled={!valid} onClick={() => void onSaveNew()}>
              Save to my account
            </button>
          ) : (
            <button type="button" className="pg-btn pg-btn-ghost" onClick={() => void share()}>
              Create share link
            </button>
          )}
          {!valid && !projectId ? <small className="pg-muted">Fix the listed issues to save to your account.</small> : null}
          {shareUrl ? (
            <div className="pg-field">
              <label htmlFor="share-url">Share link (read-only, anyone can remix)</label>
              <input id="share-url" readOnly value={shareUrl} onFocus={(e) => e.currentTarget.select()} />
            </div>
          ) : null}
          {error ? <p className="pg-error">{error}</p> : null}
          {projects?.length ? (
            <div>
              <p className="pg-list-title">My projects</p>
              <ul className="pg-list">
                {projects.map((p) => (
                  <li key={p.id}>
                    <button type="button" className={p.id === projectId ? 'is-active' : ''} onClick={() => void onOpen(p.id)}>
                      {p.name}
                      <span className="pg-spacer" />
                      <small className="pg-muted">{new Date(p.updatedAt).toLocaleDateString()}</small>
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
        <pre className="pg-output">{JSON.stringify(spec, null, 2)}</pre>
      </details>
    </div>
  );
}

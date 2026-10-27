import { useMemo, useState } from 'react';
import { migrateSpec } from '@splendor/dapp-spec';
import { api, ApiError, type Workspace } from '../api/client';
import { deleteLocalProject, isLocalWorkspaceId, listAllLocalProjects } from '../workspaces/local';

type Row = ReturnType<typeof listAllLocalProjects>[number];

const rowKey = (r: Row) => `${r.workspaceId}/${r.project.id}`;

/**
 * Signed in, with projects still kept only in this browser: the user picks which ones to copy into
 * the account. A browser copy is removed only after the account has it. Nothing moves on its own.
 */
export function LocalUpload({ workspaces, onUploaded }: { workspaces: Workspace[]; onUploaded: () => void }) {
  const [version, setVersion] = useState(0);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const rows = useMemo(() => listAllLocalProjects(), [version]);
  const remote = workspaces.filter((w) => !isLocalWorkspaceId(w.id));
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [target, setTarget] = useState<string>('');
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<{ saved: number; failed: string[] } | null>(null);

  if (!rows.length && !report) return null;

  const upload = async () => {
    setBusy(true);
    let saved = 0;
    const failed: string[] = [];
    for (const r of rows.filter((x) => picked.has(rowKey(x)))) {
      // Work filed under an account workspace id (older builds, signed out) goes back to that workspace.
      const own = remote.some((w) => w.id === r.workspaceId) ? r.workspaceId : null;
      const workspaceId = own ?? (target || null);
      try {
        await api.createProject(migrateSpec(r.project.spec), workspaceId);
        deleteLocalProject(r.workspaceId, r.project.id);
        saved += 1;
      } catch (e) {
        const why =
          e instanceof ApiError && e.status === 422
            ? 'it has setup issues the account rejects; open it and fix them first'
            : e instanceof Error
              ? e.message
              : String(e);
        failed.push(`${r.project.name} (${why})`);
      }
    }
    setBusy(false);
    setPicked(new Set());
    setReport({ saved, failed });
    setVersion((v) => v + 1);
    if (saved) onUploaded();
  };

  return (
    <section aria-labelledby="local-upload-title" data-testid="local-upload">
      <h2 id="local-upload-title">Projects only in this browser</h2>
      {rows.length ? (
        <p className="pg-muted">
          {rows.length === 1 ? '1 project is' : `${rows.length} projects are`} kept only in this browser. Save the ones you want to your
          account so they sync and can be shared.{' '}
          {!open ? (
            <button type="button" className="pg-link-btn" data-testid="local-upload-review" onClick={() => setOpen(true)}>
              Review
            </button>
          ) : null}
        </p>
      ) : null}
      {report ? (
        <p className={report.failed.length ? 'pg-error' : 'pg-muted'} role="status" data-testid="local-upload-report">
          {report.saved ? `Saved ${report.saved} to your account.` : ''}
          {report.failed.length ? ` Not saved: ${report.failed.join('; ')}.` : ''}
        </p>
      ) : null}
      {open && rows.length ? (
        <div className="pg-stack">
          <ul className="pg-list">
            {rows.map((r) => (
              <li key={rowKey(r)}>
                <label className="pg-row">
                  <input
                    type="checkbox"
                    checked={picked.has(rowKey(r))}
                    onChange={(e) => {
                      const next = new Set(picked);
                      if (e.target.checked) next.add(rowKey(r));
                      else next.delete(rowKey(r));
                      setPicked(next);
                    }}
                  />
                  <span>{r.project.name}</span>
                  <small className="pg-muted">{new Date(r.project.updatedAt).toLocaleString()}</small>
                </label>
              </li>
            ))}
          </ul>
          {remote.length ? (
            <div className="pg-field">
              <label htmlFor="local-upload-target">File them in</label>
              <select id="local-upload-target" value={target} onChange={(e) => setTarget(e.target.value)}>
                <option value="">No workspace</option>
                {remote.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </select>
            </div>
          ) : null}
          <div className="pg-row">
            <button type="button" className="pg-btn pg-btn-sm" disabled={busy || picked.size === 0} data-testid="local-upload-save" onClick={() => void upload()}>
              {busy ? 'Saving…' : picked.size ? `Save ${picked.size} to my account` : 'Save to my account'}
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={busy} onClick={() => setPicked(new Set(rows.map(rowKey)))}>
              Select all
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={busy} onClick={() => setOpen(false)}>
              Close
            </button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

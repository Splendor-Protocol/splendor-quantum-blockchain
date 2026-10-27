import { useCallback, useEffect, useState } from 'react';
import { NavLink, Navigate, Route, Routes } from 'react-router-dom';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { BLOCK_MANIFESTS, TEMPLATES } from '@splendor/dapp-spec';
import { RECIPES } from '../registry/recipes';
import { api, ApiError, type AuditEntry, type CatalogFlag, type ReleaseStatus } from '../api/client';
import { SignInPanel } from '../auth/SignInPanel';
import { useConfirm } from '../components/ConfirmDialog';
import { useSession } from '../auth/session';
import { recipeFlagId, templateFlagId, useRegistry } from '../registry';

/**
 * Run links come from the GitHub API through ours. Only https links to github.com, or to an
 * Actions run on a GitHub Enterprise host, become clickable.
 */
export function isGitHubRunUrl(url: string): boolean {
  try {
    const u = new URL(url);
    return u.protocol === 'https:' && (u.hostname === 'github.com' || /\/actions\/runs\/\d+$/.test(u.pathname));
  } catch {
    return false;
  }
}

function useLoad<T>(fn: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await fn());
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [fn]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { data, error, loading, reload };
}

function FlagRow({ id, title, kind, status, flag }: { id: string; title: string; kind: string; status?: string; flag: CatalogFlag }) {
  const { setFlags } = useRegistry();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const update = async (patch: Partial<CatalogFlag>) => {
    setSaving(true);
    setError(null);
    try {
      const merged = { ...flag, ...patch };
      const next: CatalogFlag = {};
      if (merged.hidden) next.hidden = true;
      if (merged.featured) next.featured = true;
      if (merged.status) next.status = merged.status;
      if (merged.minVersion) next.minVersion = merged.minVersion;
      setFlags((await api.setCatalogFlag(id, next)).flags);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };
  return (
    <tr data-testid={`flag-${id}`}>
      <td>
        <strong>{title}</strong>
        <div className="pg-muted pg-mono" style={{ fontSize: '0.8rem' }}>
          {id}
        </div>
        {error ? <div className="pg-error">{error}</div> : null}
      </td>
      <td>{kind}</td>
      <td>
        <input type="checkbox" aria-label={`Show ${title}`} checked={!flag.hidden} disabled={saving} onChange={(e) => void update({ hidden: !e.target.checked })} />
      </td>
      <td>
        <input type="checkbox" aria-label={`Feature ${title}`} checked={!!flag.featured} disabled={saving} onChange={(e) => void update({ featured: e.target.checked })} />
      </td>
      <td>
        {status !== undefined ? (
          <select aria-label={`Status of ${title}`} value={flag.status ?? ''} disabled={saving} onChange={(e) => void update({ status: (e.target.value || undefined) as CatalogFlag['status'] })}>
            <option value="">Default ({status})</option>
            <option value="stable">stable</option>
            <option value="beta">beta</option>
            <option value="experimental">experimental</option>
          </select>
        ) : (
          <span className="pg-muted">—</span>
        )}
      </td>
      <td>
        {kind === 'widget' || kind === 'part' ? (
          <input
            aria-label={`Minimum connect-react version for ${title}`}
            placeholder="e.g. 3.1.0"
            defaultValue={flag.minVersion ?? ''}
            disabled={saving}
            style={{ width: '7rem' }}
            onBlur={(e) => {
              if ((flag.minVersion ?? '') !== e.target.value.trim()) void update({ minVersion: e.target.value.trim() || undefined });
            }}
          />
        ) : null}
      </td>
    </tr>
  );
}

/** Status as shipped (before admin overrides). */
function baseStatus(id: string): string | undefined {
  return [...WIDGET_MANIFESTS, ...BLOCK_MANIFESTS].find((m) => m.id === id)?.status;
}

function CatalogAdmin() {
  const { all, flags } = useRegistry();
  return (
    <div className="pg-stack">
      <p className="pg-muted">
        Hidden widgets cannot be added in the builder (existing projects keep rendering). Status overrides change the badge users see. A
        minimum version marks widgets that need a newer connect-react than the one this playground bundles.
      </p>
      <table className="pg-table">
        <thead>
          <tr>
            <th>Widget / block</th>
            <th>Kind</th>
            <th>Visible</th>
            <th>Featured</th>
            <th>Status</th>
            <th>Min version</th>
          </tr>
        </thead>
        <tbody>
          {all.map((e) => (
            <FlagRow key={e.id} id={e.id} title={e.title} kind={e.kind} status={baseStatus(e.id) ?? e.status} flag={flags[e.id] ?? {}} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TemplatesAdmin() {
  const { flags } = useRegistry();
  const all = useRegistryTemplatesAndRecipes();
  return (
    <div className="pg-stack">
      <p className="pg-muted">Templates and SDK recipes ship with the playground; here you choose which ones users see.</p>
      <table className="pg-table">
        <thead>
          <tr>
            <th>Item</th>
            <th>Kind</th>
            <th>Visible</th>
            <th>Featured</th>
            <th />
            <th />
          </tr>
        </thead>
        <tbody>
          {all.map((x) => (
            <FlagRow key={x.flagId} id={x.flagId} title={x.title} kind={x.kind} flag={flags[x.flagId] ?? {}} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Every template and recipe, including hidden ones. */
function useRegistryTemplatesAndRecipes() {
  return [
    ...TEMPLATES.map((t) => ({ flagId: templateFlagId(t.id), title: t.title, kind: 'template' })),
    ...RECIPES.map((r) => ({ flagId: recipeFlagId(r.id), title: r.title, kind: `recipe (${r.module})` })),
  ];
}

function ReleasesAdmin() {
  const load = useCallback(() => api.releases(), []);
  const { data, error, loading, reload } = useLoad<ReleaseStatus>(load);
  const [channel, setChannel] = useState<'latest' | 'next'>('next');
  const [dryRun, setDryRun] = useState(true);
  const [result, setResult] = useState<string | null>(null);
  const [triggerError, setTriggerError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmDialog, confirm] = useConfirm();

  const trigger = async () => {
    if (
      !dryRun &&
      channel === 'latest' &&
      !(await confirm({
        title: 'Publish to npm under "latest"?',
        message: 'Every user installing the packages will get this release.',
        confirmLabel: 'Publish to latest',
        danger: true,
      }))
    ) {
      return;
    }
    setBusy(true);
    setTriggerError(null);
    setResult(null);
    try {
      const r = await api.triggerRelease({ channel, dryRun });
      setResult(r.dryRun ? 'Dry run started: the workflow builds and packs without publishing.' : `Release workflow started on the "${channel}" channel.`);
      window.setTimeout(() => void reload(), 4000);
    } catch (e) {
      setTriggerError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="pg-stack">
      <div className="pg-row">
        <p className="pg-muted" style={{ margin: 0 }}>
          Releases run in GitHub Actions (<code>release-packages.yml</code>) with changesets and npm provenance. This panel only reads status
          and dispatches the workflow — it never holds an npm token.
        </p>
        <span className="pg-spacer" />
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void reload()} disabled={loading}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {error ? <p className="pg-error">{error}</p> : null}
      {data && !data.configured ? (
        <p className="pg-issues">GitHub is not configured on the API (set <code>GITHUB_TOKEN</code> and <code>GITHUB_REPO</code>). npm versions are still shown.</p>
      ) : null}
      {data?.errors.length ? (
        <ul className="pg-error">
          {data.errors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      ) : null}

      {data ? (
        <>
          <h3>Packages</h3>
          <table className="pg-table" data-testid="release-packages">
            <thead>
              <tr>
                <th>Package</th>
                <th>In repo (main)</th>
                <th>npm latest</th>
                <th>npm next</th>
              </tr>
            </thead>
            <tbody>
              {data.packages.map((p) => (
                <tr key={p.name}>
                  <td className="pg-mono">{p.name}</td>
                  <td>{p.repoVersion ?? '—'}</td>
                  <td>{p.distTags.latest ?? 'not published'}</td>
                  <td>{p.distTags.next ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <h3>Pending changesets ({data.changesets.length})</h3>
          {data.changesets.length ? (
            <ul>
              {data.changesets.map((c) => (
                <li key={c.file}>
                  <code>{c.file}</code> — {c.summary}
                </li>
              ))}
            </ul>
          ) : (
            <p className="pg-muted">None. Contributors add one with <code>npm run changeset</code> in their PR.</p>
          )}

          <h3>Recent release runs</h3>
          {data.runs.length ? (
            <table className="pg-table">
              <thead>
                <tr>
                  <th>Run</th>
                  <th>Trigger</th>
                  <th>Status</th>
                  <th>Started</th>
                </tr>
              </thead>
              <tbody>
                {data.runs.map((r) => (
                  <tr key={r.id}>
                    <td>
                      {isGitHubRunUrl(r.url) ? (
                        <a href={r.url} target="_blank" rel="noreferrer noopener">
                          {r.title || `#${r.id}`}
                        </a>
                      ) : (
                        <span>{r.title || `#${r.id}`}</span>
                      )}
                    </td>
                    <td>{r.event}</td>
                    <td className={r.conclusion === 'success' ? 'pg-ok' : r.conclusion === 'failure' ? 'pg-error' : ''}>{r.conclusion ?? r.status}</td>
                    <td>{new Date(r.createdAt).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className="pg-muted">No runs yet.</p>
          )}
        </>
      ) : null}

      <h3>Start a release</h3>
      {confirmDialog}
      <div className="pg-panel pg-form" style={{ maxWidth: 520 }}>
        <div className="pg-field">
          <label htmlFor="release-channel">Channel</label>
          <select id="release-channel" value={channel} onChange={(e) => setChannel(e.target.value as 'latest' | 'next')}>
            <option value="next">next — snapshot pre-release (npm i @splendor/connect-react@next)</option>
            <option value="latest">latest — publish the versions on main</option>
          </select>
        </div>
        <div className="pg-field pg-field-inline">
          <input id="release-dry" type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
          <label htmlFor="release-dry">Dry run (build, test and pack only)</label>
        </div>
        <button type="button" className={`pg-btn${!dryRun && channel === 'latest' ? ' pg-btn-danger' : ''}`} disabled={busy || !data?.configured} data-testid="trigger-release" onClick={() => void trigger()}>
          {busy ? 'Starting…' : dryRun ? 'Start dry run' : `Publish to ${channel}`}
        </button>
        {result ? <p className="pg-ok" data-testid="release-result">{result}</p> : null}
        {triggerError ? <p className="pg-error">{triggerError}</p> : null}
        <small className="pg-muted">
          "latest" publishes whatever versions the merged "Version packages" PR set; it does nothing if those versions are already on npm.
        </small>
      </div>
    </div>
  );
}

function AuditAdmin() {
  const load = useCallback(() => api.audit(), []);
  const { data, error, loading } = useLoad<{ entries: AuditEntry[] }>(load);
  if (error) return <p className="pg-error">{error}</p>;
  if (!data) return <p className="pg-muted" role="status">{loading ? 'Loading the audit log…' : 'No audit entries.'}</p>;
  if (!data.entries.length) return <p className="pg-muted" data-testid="audit-empty">No audit entries yet.</p>;
  return (
    <table className="pg-table" data-testid="audit-log">
      <thead>
        <tr>
          <th>When</th>
          <th>Who</th>
          <th>Action</th>
          <th>Target</th>
          <th>Detail</th>
        </tr>
      </thead>
      <tbody>
        {data?.entries.map((e) => (
          <tr key={e.id}>
            <td>{new Date(e.at).toLocaleString()}</td>
            <td className="pg-mono pg-truncate" title={e.actor}>
              {e.actor}
            </td>
            <td>{e.action}</td>
            <td className="pg-mono">{e.target ?? ''}</td>
            <td className="pg-mono" style={{ fontSize: '0.8rem' }}>
              {e.detail ?? ''}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function AdminsAdmin() {
  const load = useCallback(() => api.admins(), []);
  const { data, error, reload } = useLoad(load);
  const [address, setAddress] = useState('');
  const [msg, setMsg] = useState<string | null>(null);
  const [confirmDialog, confirm] = useConfirm();
  const remove = async (address: string) => {
    const ok = await confirm({ title: 'Remove this admin?', message: <code className="pg-mono">{address}</code>, confirmLabel: 'Remove admin', danger: true });
    if (!ok) return;
    setMsg(null);
    try {
      await api.removeAdmin(address);
      await reload();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : String(e));
    }
  };
  return (
    <div className="pg-stack">
      {confirmDialog}
      {error ? <p className="pg-error">{error}</p> : null}
      <table className="pg-table">
        <thead>
          <tr>
            <th>Address</th>
            <th>Source</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {data?.admins.map((a) => (
            <tr key={a.address}>
              <td className="pg-mono">{a.address}</td>
              <td>{a.source === 'env' ? 'ADMIN_ADDRESSES (env)' : `added ${a.addedAt ? new Date(a.addedAt).toLocaleDateString() : ''}`}</td>
              <td>
                {a.source === 'db' ? (
                  <button type="button" className="pg-btn pg-btn-danger pg-btn-sm" aria-label={`Remove admin ${a.address}`} onClick={() => void remove(a.address)}>
                    Remove
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <form
        className="pg-row"
        onSubmit={(e) => {
          e.preventDefault();
          setMsg(null);
          void api
            .addAdmin(address.trim())
            .then(() => {
              setAddress('');
              return reload();
            })
            .catch((err) => setMsg(err instanceof ApiError ? err.message : String(err)));
        }}
      >
        <input className="pg-mono" style={{ flex: 1 }} placeholder="SQ:… address" value={address} onChange={(e) => setAddress(e.target.value)} aria-label="New admin address" />
        <button type="submit" className="pg-btn">
          Add admin
        </button>
      </form>
      {msg ? <p className="pg-error">{msg}</p> : null}
    </div>
  );
}

export function AdminPage() {
  const { session, ready } = useSession();
  if (!ready) return <p className="pg-pad pg-muted">Checking session…</p>;
  if (!session?.isAdmin) {
    return (
      <div className="pg-page">
        <h1>Admin</h1>
        <p className="pg-muted">Sign in with an admin wallet. Admins are listed in the API's ADMIN_ADDRESSES or added by another admin.</p>
        <div className="pg-panel" style={{ maxWidth: 520 }}>
          <SignInPanel statement="Sign in to the Splendor dApp Playground admin" />
        </div>
        {session && !session.isAdmin ? <p className="pg-error">This wallet is not an admin.</p> : null}
      </div>
    );
  }
  return (
    <div className="pg-page">
      <h1>Admin</h1>
      <nav className="pg-tabs-inline">
        <NavLink to="/admin/catalog">Catalog</NavLink>
        <NavLink to="/admin/templates">Templates & recipes</NavLink>
        <NavLink to="/admin/releases">Releases</NavLink>
        <NavLink to="/admin/audit">Audit log</NavLink>
        <NavLink to="/admin/admins">Admins</NavLink>
      </nav>
      <Routes>
        <Route index element={<Navigate to="catalog" replace />} />
        <Route path="catalog" element={<CatalogAdmin />} />
        <Route path="templates" element={<TemplatesAdmin />} />
        <Route path="releases" element={<ReleasesAdmin />} />
        <Route path="audit" element={<AuditAdmin />} />
        <Route path="admins" element={<AdminsAdmin />} />
      </Routes>
    </div>
  );
}

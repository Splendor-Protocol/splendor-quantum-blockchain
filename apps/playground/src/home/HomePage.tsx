import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { api, ApiError, type ProjectList, type ProjectSummary, type Workspace } from '../api/client';
import { useSession } from '../auth/session';
import { useConfirm } from '../components/ConfirmDialog';
import { useRegistry } from '../registry';
import {
  createLocalWorkspace,
  deleteLocalProject,
  deleteLocalWorkspace,
  duplicateLocalProject,
  isLocalWorkspaceId,
  listLocalProjects,
  listLocalWorkspaces,
  renameLocalProject,
  renameLocalWorkspace,
} from '../workspaces/local';
import { KitCards } from './KitCards';
import { LocalUpload } from './LocalUpload';
import { Trash } from './Trash';
import { NewProjectWizard, wizardQuery } from './NewProjectWizard';

const PROJECT_PAGE = 24;

/** Cloud or browser-local project row for the workspace grid. */
type ProjectCard = Pick<ProjectSummary, 'id' | 'name' | 'updatedAt' | 'version'> & { local: boolean };

function errorText(e: unknown): string {
  return e instanceof Error && e.message ? e.message : 'unknown error';
}

export function HomePage() {
  const { templates, catalog, recipes } = useRegistry();
  const { session, ready: sessionReady } = useSession();
  const [confirmDialog, confirm] = useConfirm();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const workspaceId = params.get('workspace');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [newWs, setNewWs] = useState('');
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [renamingProject, setRenamingProject] = useState<{ id: string; name: string } | null>(null);
  const [wizard, setWizard] = useState<{ template: string | null } | null>(null);
  const [projects, setProjects] = useState<ProjectCard[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [projectsError, setProjectsError] = useState<string | null>(null);
  /** Failures of remote calls or local writes (announced to assistive tech). */
  const [error, setError] = useState<string | null>(null);
  /** Non-error news, such as a workspace that was created in this browser instead of the account. */
  const [notice, setNotice] = useState<string | null>(null);

  /** Latest request wins: a slow earlier response must not overwrite a newer list. */
  const workspacesReq = useRef(0);

  const reloadWorkspaces = useCallback(async () => {
    const req = ++workspacesReq.current;
    // Until /auth/me answers, a signed-in user would see only browser workspaces (or "none yet").
    if (!sessionReady) return;
    if (session) {
      try {
        // Account workspaces first, then the ones kept in this browser.
        const remote = (await api.listWorkspaces()).workspaces;
        if (req !== workspacesReq.current) return;
        setWorkspaces([...remote, ...listLocalWorkspaces()]);
        setLoaded(true);
        setError((prev) => (prev?.startsWith('Could not load your account workspaces') ? null : prev));
        return;
      } catch (e) {
        if (req !== workspacesReq.current) return;
        setError(`Could not load your account workspaces (${errorText(e)}). Showing the workspaces stored in this browser instead.`);
      }
    }
    setWorkspaces(listLocalWorkspaces());
    setLoaded(true);
  }, [session, sessionReady]);

  useEffect(() => {
    void reloadWorkspaces();
    return () => {
      workspacesReq.current += 1;
    };
  }, [reloadWorkspaces]);

  const selected = workspaceId ? (workspaces.find((w) => w.id === workspaceId) ?? null) : null;

  const selectWorkspace = (id: string | null) => {
    const next = new URLSearchParams(params);
    if (id) next.set('workspace', id);
    else next.delete('workspace');
    setParams(next);
  };

  // Projects of the selected workspace: the API for account workspaces, localStorage otherwise.
  /** Each call supersedes earlier ones; a response that arrives after a newer call (or after the workspace changed) is dropped. */
  const projectsReq = useRef(0);
  const [projectQuery, setProjectQuery] = useState('');
  const [projectSort, setProjectSort] = useState<'updated' | 'name'>('updated');
  /** The search box, applied a moment after typing stops. */
  const [appliedQuery, setAppliedQuery] = useState('');
  useEffect(() => {
    const t = window.setTimeout(() => setAppliedQuery(projectQuery.trim()), 250);
    return () => window.clearTimeout(t);
  }, [projectQuery]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [trashKey, setTrashKey] = useState(0);

  const reloadProjects = useCallback(async (more?: string) => {
    const req = ++projectsReq.current;
    const stale = () => req !== projectsReq.current;
    if (!more) setNextCursor(null);
    if (!workspaceId) {
      setProjects([]);
      setProjectsLoading(false);
      return;
    }
    setProjectsError(null);
    if (isLocalWorkspaceId(workspaceId)) {
      const q = appliedQuery.toLowerCase();
      const list = listLocalProjects(workspaceId)
        .filter((p) => !q || p.name.toLowerCase().includes(q))
        .sort((a, b) => (projectSort === 'name' ? a.name.localeCompare(b.name) : b.updatedAt.localeCompare(a.updatedAt)));
      setProjects(list.map((p) => ({ id: p.id, name: p.name, updatedAt: p.updatedAt, local: true })));
      setProjectsLoading(false);
      return;
    }
    // An account workspace: its projects live in the API only.
    if (!session) {
      setProjects([]);
      setProjectsLoading(!sessionReady);
      if (sessionReady) setProjectsError('Sign in to see the projects in this workspace.');
      return;
    }
    setProjectsLoading(true);
    const opts = { q: appliedQuery, sort: projectSort, limit: PROJECT_PAGE, cursor: more ?? null };
    try {
      let page: ProjectList;
      try {
        page = await api.listWorkspaceProjects(workspaceId, opts);
      } catch (e) {
        // Older API without the nested route: filter on the flat one.
        if (!(e instanceof ApiError) || e.status !== 404) throw e;
        page = await api.listProjects(workspaceId, opts);
      }
      if (stale()) return;
      const cards = page.projects.map((p): ProjectCard => ({ id: p.id, name: p.name, updatedAt: p.updatedAt, version: p.version, local: false }));
      setProjects((prev) => (more ? [...prev, ...cards] : cards));
      setNextCursor(page.nextCursor ?? null);
    } catch (e) {
      if (stale()) return;
      if (!more) setProjects([]);
      setProjectsError(e instanceof ApiError ? e.message : 'Could not load projects.');
    } finally {
      if (!stale()) setProjectsLoading(false);
    }
  }, [workspaceId, session, sessionReady, appliedQuery, projectSort]);

  useEffect(() => {
    void reloadProjects();
    return () => {
      projectsReq.current += 1;
    };
  }, [reloadProjects]);

  const addWorkspace = async () => {
    const name = newWs.trim() || 'My workspace';
    setError(null);
    setNotice(null);
    if (session) {
      try {
        await api.createWorkspace(name);
        setNewWs('');
        await reloadWorkspaces();
        return;
      } catch (e) {
        setNotice(`Could not create "${name}" in your account (${errorText(e)}). It was created in this browser instead, so it will not sync.`);
      }
    }
    try {
      createLocalWorkspace(name);
    } catch (e) {
      setNotice(null);
      setError(errorText(e));
      return;
    }
    setNewWs('');
    await reloadWorkspaces();
  };

  const saveRename = async (w: Workspace) => {
    const name = (renaming?.id === w.id ? renaming.name : '').trim().slice(0, 80);
    setRenaming(null);
    if (!name || name === w.name) return;
    setError(null);
    setNotice(null);
    if (!isLocalWorkspaceId(w.id) && session) {
      try {
        await api.renameWorkspace(w.id, name);
      } catch (e) {
        setError(`Could not rename workspace "${w.name}": ${errorText(e)}.`);
        return;
      }
    } else {
      try {
        if (!renameLocalWorkspace(w.id, name)) {
          setError(`Workspace "${w.name}" was not found in this browser.`);
          return;
        }
      } catch (e) {
        setError(errorText(e));
        return;
      }
    }
    await reloadWorkspaces();
  };

  const removeWorkspace = async (w: Workspace) => {
    const local = isLocalWorkspaceId(w.id);
    const ok = await confirm({
      title: `Delete workspace "${w.name}"?`,
      message: local
        ? 'The workspace and all of its projects are removed from this browser. This cannot be undone.'
        : 'The workspace and all of its projects move to the trash below, where they can be restored for 30 days.',
      confirmLabel: 'Delete workspace',
      danger: true,
    });
    if (!ok) return;
    setError(null);
    setNotice(null);
    if (!local && session) {
      try {
        await api.deleteWorkspace(w.id);
      } catch (e) {
        setError(`Could not delete workspace "${w.name}": ${errorText(e)}.`);
        return;
      }
    } else {
      try {
        deleteLocalWorkspace(w.id);
      } catch (e) {
        setError(errorText(e));
        return;
      }
    }
    if (workspaceId === w.id) selectWorkspace(null);
    setTrashKey((k) => k + 1);
    await reloadWorkspaces();
  };

  const removeProject = async (p: ProjectCard) => {
    const ok = await confirm({
      title: `Delete project "${p.name}"?`,
      message: p.local ? 'It is removed from this browser. This cannot be undone.' : 'It moves to the trash below, where it can be restored for 30 days.',
      confirmLabel: 'Delete project',
      danger: true,
    });
    if (!ok) return;
    setError(null);
    setNotice(null);
    if (p.local) {
      if (workspaceId && !deleteLocalProject(workspaceId, p.id)) {
        setError(`Could not delete project "${p.name}" from this browser.`);
        return;
      }
    } else {
      try {
        await api.deleteProject(p.id);
      } catch (e) {
        setError(`Could not delete project "${p.name}": ${errorText(e)}.`);
        return;
      }
      setTrashKey((k) => k + 1);
    }
    await reloadProjects();
  };

  const saveProjectRename = async (p: ProjectCard) => {
    const name = (renamingProject?.id === p.id ? renamingProject.name : '').trim().slice(0, 80);
    setRenamingProject(null);
    if (!name || name === p.name) return;
    setError(null);
    setNotice(null);
    try {
      if (p.local) {
        if (!workspaceId || !renameLocalProject(workspaceId, p.id, name)) {
          setError(`Project "${p.name}" was not found in this browser.`);
          return;
        }
      } else {
        await api.patchProject(p.id, { name, ...(p.version ? { baseVersion: p.version } : {}) });
      }
    } catch (e) {
      setError(
        e instanceof ApiError && e.code === 'CONFLICT'
          ? `"${p.name}" was saved somewhere else since this list loaded. The list is refreshed; rename it again.`
          : `Could not rename project "${p.name}": ${errorText(e)}.`,
      );
      await reloadProjects();
      return;
    }
    await reloadProjects();
  };

  const duplicateProject = async (p: ProjectCard) => {
    setError(null);
    setNotice(null);
    try {
      if (p.local) {
        const r = workspaceId ? duplicateLocalProject(workspaceId, p.id) : null;
        if (!r) {
          setError(`Project "${p.name}" was not found in this browser.`);
          return;
        }
        if (r.trimmed.length) {
          setNotice(`Removed ${r.trimmed.length} oldest project${r.trimmed.length === 1 ? '' : 's'} to stay within the browser limit: ${r.trimmed.map((t) => t.name).join(', ')}.`);
        }
      } else {
        await api.duplicateProject(p.id);
      }
    } catch (e) {
      setError(`Could not duplicate project "${p.name}": ${errorText(e)}.`);
      return;
    }
    await reloadProjects();
  };

  /** Template cards start straight in the studio; the wizard adds theme + name. */
  const buildHref = (template: string) => `/build?template=${encodeURIComponent(template)}${workspaceId ? `&workspace=${encodeURIComponent(workspaceId)}` : ''}`;
  const studioHref = workspaceId ? `/build?workspace=${encodeURIComponent(workspaceId)}` : '/build';

  return (
    <div className="pg-page">
      {confirmDialog}
      <section className="pg-hero">
        <h1>Splendor dApp Studio</h1>
        <p>
          Drag pieces onto a live page, bind contracts, pick a theme, then export a Vite or Next.js project that runs with{' '}
          <code>npm install &amp;&amp; npm run dev</code>.
        </p>
        <div className="pg-row">
          <Link className="pg-btn" to={studioHref}>
            Open the studio
          </Link>
          <button type="button" className="pg-btn pg-btn-ghost" data-testid="new-project" onClick={() => setWizard({ template: null })}>
            New project
          </button>
          <Link className="pg-btn pg-btn-ghost" to="/widgets">
            Browse widgets
          </Link>
          <Link className="pg-btn pg-btn-ghost" to="/learn">
            Learn the SDK
          </Link>
        </div>
      </section>

      <section aria-labelledby="home-kits-title" data-testid="home-kits">
        <h2 id="home-kits-title">Start with a ready-made app</h2>
        <p className="pg-muted">A landing page and a dashboard, wired to your token or contract. Point it at your own address, or try the demo first.</p>
        <KitCards kits={templates.filter((t) => t.kit)} demoHref={buildHref} onPick={(id) => setWizard({ template: id })} />
      </section>

      <section>
        <h2>Workspaces</h2>
        <p className="pg-muted">Group projects. Signed-in users sync to the API; everyone else keeps them in this browser.</p>
        <div className="pg-row" style={{ marginBottom: '0.75rem' }}>
          <input aria-label="New workspace name" placeholder="Workspace name" value={newWs} onChange={(e) => setNewWs(e.target.value)} />
          <button type="button" className="pg-btn pg-btn-sm" onClick={() => void addWorkspace()}>
            New workspace
          </button>
        </div>
        {error ? (
          <p className="pg-error" role="alert" data-testid="home-error">
            {error}
          </p>
        ) : null}
        {notice ? (
          <p className="pg-muted" role="status" data-testid="home-notice">
            {notice}
          </p>
        ) : null}
        <div className="pg-cards">
          {workspaces.map((w) => (
            <div key={w.id} className={`pg-card pg-ws-card${w.id === workspaceId ? ' is-selected' : ''}`} data-testid={`workspace-card-${w.id}`}>
              {renaming?.id === w.id ? (
                <form
                  className="pg-row"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void saveRename(w);
                  }}
                >
                  <input
                    aria-label={`New name for workspace ${w.name}`}
                    data-testid={`workspace-rename-input-${w.id}`}
                    maxLength={80}
                    autoFocus
                    value={renaming.name}
                    onChange={(e) => setRenaming({ id: w.id, name: e.target.value })}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') setRenaming(null);
                    }}
                  />
                  <button type="submit" className="pg-btn pg-btn-sm" data-testid={`workspace-rename-save-${w.id}`}>
                    Save
                  </button>
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setRenaming(null)}>
                    Cancel
                  </button>
                </form>
              ) : (
                <h3>{w.name}</h3>
              )}
              <p className="pg-muted">
                Created {new Date(w.createdAt).toLocaleDateString()}
                {isLocalWorkspaceId(w.id) ? ' \u00B7 this browser' : ''}
              </p>
              <div className="pg-row" style={{ marginTop: '0.75rem' }}>
                <button
                  type="button"
                  className="pg-btn pg-btn-sm"
                  aria-pressed={w.id === workspaceId}
                  aria-label={`${w.id === workspaceId ? 'Close' : 'Open'} workspace ${w.name}`}
                  data-testid={`workspace-open-${w.id}`}
                  onClick={() => selectWorkspace(w.id === workspaceId ? null : w.id)}
                >
                  {w.id === workspaceId ? 'Close' : 'Open'}
                </button>
                <Link className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Open studio in workspace ${w.name}`} to={`/build?workspace=${encodeURIComponent(w.id)}`}>
                  Studio
                </Link>
                <button
                  type="button"
                  className="pg-btn pg-btn-ghost pg-btn-sm"
                  aria-label={`Rename workspace ${w.name}`}
                  data-testid={`workspace-rename-${w.id}`}
                  onClick={() => setRenaming({ id: w.id, name: w.name })}
                >
                  Rename
                </button>
                <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Delete workspace ${w.name}`} onClick={() => void removeWorkspace(w)}>
                  Delete
                </button>
              </div>
            </div>
          ))}
          {!loaded ? (
            <p className="pg-muted" role="status">
              Loading workspaces{'\u2026'}
            </p>
          ) : !workspaces.length ? (
            <p className="pg-muted">No workspaces yet {'\u2014'} create one or start building.</p>
          ) : null}
        </div>
      </section>

      {session ? <LocalUpload workspaces={workspaces} onUploaded={() => void reloadProjects()} /> : null}

      {workspaceId ? (
        <section data-testid="workspace-projects" aria-label="Workspace projects">
          <div className="pg-row">
            <h2 style={{ margin: 0 }}>{selected ? `Projects in ${selected.name}` : 'Projects'}</h2>
            <span className="pg-spacer" />
            <button type="button" className="pg-btn pg-btn-sm" onClick={() => setWizard({ template: null })}>
              New project here
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => selectWorkspace(null)}>
              All workspaces
            </button>
          </div>
          <div className="pg-row" style={{ margin: '0.75rem 0' }}>
            <input
              type="search"
              aria-label="Search projects"
              placeholder="Search projects"
              data-testid="project-search"
              value={projectQuery}
              maxLength={80}
              onChange={(e) => setProjectQuery(e.target.value)}
            />
            <label className="pg-muted" htmlFor="project-sort">
              Sort
            </label>
            <select id="project-sort" value={projectSort} onChange={(e) => setProjectSort(e.target.value as 'updated' | 'name')}>
              <option value="updated">Last edited</option>
              <option value="name">Name</option>
            </select>
          </div>
          {loaded && !selected ? (
            <p className="pg-error" role="alert">
              That workspace was not found. It may belong to another account or browser.
            </p>
          ) : null}
          {projectsError ? (
            <p className="pg-error" role="alert">
              {projectsError}
            </p>
          ) : null}
          {projectsLoading ? <p className="pg-muted">Loading projects{'\u2026'}</p> : null}
          <div className="pg-cards">
            {projects.map((p) => (
              <div key={p.id} className="pg-card" data-testid={`project-card-${p.id}`}>
                {renamingProject?.id === p.id ? (
                  <form
                    className="pg-row"
                    onSubmit={(e) => {
                      e.preventDefault();
                      void saveProjectRename(p);
                    }}
                  >
                    <input
                      aria-label={`New name for project ${p.name}`}
                      maxLength={80}
                      autoFocus
                      value={renamingProject.name}
                      onChange={(e) => setRenamingProject({ id: p.id, name: e.target.value })}
                      onKeyDown={(e) => {
                        if (e.key === 'Escape') setRenamingProject(null);
                      }}
                    />
                    <button type="submit" className="pg-btn pg-btn-sm">
                      Save
                    </button>
                    <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setRenamingProject(null)}>
                      Cancel
                    </button>
                  </form>
                ) : (
                  <h3>{p.name}</h3>
                )}
                <p className="pg-muted">
                  Updated {new Date(p.updatedAt).toLocaleString()}
                  {p.local ? ' \u00B7 this browser' : ''}
                </p>
                <div className="pg-row" style={{ marginTop: '0.75rem' }}>
                  <Link className="pg-btn pg-btn-sm" aria-label={`Open project ${p.name}`} to={`/build?project=${encodeURIComponent(p.id)}&workspace=${encodeURIComponent(workspaceId)}`}>
                    Open
                  </Link>
                  <button
                    type="button"
                    className="pg-btn pg-btn-ghost pg-btn-sm"
                    aria-label={`Rename project ${p.name}`}
                    onClick={() => setRenamingProject({ id: p.id, name: p.name })}
                  >
                    Rename
                  </button>
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Duplicate project ${p.name}`} onClick={() => void duplicateProject(p)}>
                    Duplicate
                  </button>
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Delete project ${p.name}`} onClick={() => void removeProject(p)}>
                    Delete
                  </button>
                </div>
              </div>
            ))}
            {!projects.length && !projectsLoading && !projectsError ? (
              <p className="pg-muted" data-testid="workspace-projects-empty">
                {appliedQuery
                  ? `No projects match "${appliedQuery}".`
                  : 'No projects in this workspace yet. Start one from a template below, or use New project.'}
              </p>
            ) : null}
          </div>
          {nextCursor ? (
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={projectsLoading} onClick={() => void reloadProjects(nextCursor)}>
              Load more
            </button>
          ) : null}
        </section>
      ) : null}

      {session ? (
        <Trash
          reloadKey={trashKey}
          onRestored={() => {
            void reloadWorkspaces();
            void reloadProjects();
          }}
        />
      ) : null}

      <section className="pg-cards">
        <Link to="/learn" className="pg-card">
          <h2>Learn</h2>
          <p>Guides and {recipes.length} runnable SDK recipes against testnet or devnet.</p>
        </Link>
        <Link to="/widgets" className="pg-card">
          <h2>Widgets</h2>
          <p>{catalog.length} widgets and blocks {'\u2014'} preview, settings, React + embed code.</p>
        </Link>
        <Link to={studioHref} className="pg-card">
          <h2>Studio</h2>
          <p>Pages, drag-and-drop pieces, themes, contracts, mock or live preview, export.</p>
        </Link>
      </section>

      <section>
        <h2>Start from a template</h2>
        <div className="pg-cards">
          {templates.filter((t) => !t.kit).map((t) => (
            <div key={t.id} style={{ display: 'grid', gap: '0.35rem', alignContent: 'start' }}>
              <Link to={buildHref(t.id)} className="pg-card" data-testid={`template-${t.id}`}>
                <h3>{t.title}</h3>
                <p>{t.description}</p>
              </Link>
              <button type="button" className="pg-link-btn" data-testid={`customize-${t.id}`} onClick={() => setWizard({ template: t.id })}>
                Choose theme and name first
              </button>
            </div>
          ))}
        </div>
      </section>

      {wizard ? (
        <NewProjectWizard
          templates={templates}
          workspaceName={selected?.name ?? null}
          initialTemplate={wizard.template}
          onCancel={() => setWizard(null)}
          onCreate={(result) => {
            setWizard(null);
            navigate(`/build?${wizardQuery(result, workspaceId)}`);
          }}
        />
      ) : null}
    </div>
  );
}

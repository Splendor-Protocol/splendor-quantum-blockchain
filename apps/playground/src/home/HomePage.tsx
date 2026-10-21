import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { api, ApiError, type ProjectSummary, type Workspace } from '../api/client';
import { useSession } from '../auth/session';
import { useRegistry } from '../registry';
import {
  createLocalWorkspace,
  deleteLocalProject,
  deleteLocalWorkspace,
  isLocalWorkspaceId,
  listLocalProjects,
  listLocalWorkspaces,
} from '../workspaces/local';
import { NewProjectWizard, wizardQuery } from './NewProjectWizard';

/** Cloud or browser-local project row for the workspace grid. */
type ProjectCard = Pick<ProjectSummary, 'id' | 'name' | 'updatedAt'> & { local: boolean };

export function HomePage() {
  const { templates, catalog, recipes } = useRegistry();
  const { session } = useSession();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const workspaceId = params.get('workspace');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [newWs, setNewWs] = useState('');
  const [wizard, setWizard] = useState<{ template: string | null } | null>(null);
  const [projects, setProjects] = useState<ProjectCard[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [projectsError, setProjectsError] = useState<string | null>(null);

  const reloadWorkspaces = useCallback(async () => {
    if (session) {
      try {
        // Account workspaces first, then the ones kept in this browser.
        const remote = (await api.listWorkspaces()).workspaces;
        setWorkspaces([...remote, ...listLocalWorkspaces()]);
        setLoaded(true);
        return;
      } catch (e) {
        if (!(e instanceof ApiError)) throw e;
      }
    }
    setWorkspaces(listLocalWorkspaces());
    setLoaded(true);
  }, [session]);

  useEffect(() => {
    void reloadWorkspaces();
  }, [reloadWorkspaces]);

  const selected = workspaceId ? (workspaces.find((w) => w.id === workspaceId) ?? null) : null;

  const selectWorkspace = (id: string | null) => {
    const next = new URLSearchParams(params);
    if (id) next.set('workspace', id);
    else next.delete('workspace');
    setParams(next);
  };

  // Projects of the selected workspace: the API for account workspaces, localStorage otherwise.
  const reloadProjects = useCallback(async () => {
    if (!workspaceId) {
      setProjects([]);
      return;
    }
    setProjectsError(null);
    if (isLocalWorkspaceId(workspaceId) || !session) {
      setProjects(listLocalProjects(workspaceId).map((p) => ({ id: p.id, name: p.name, updatedAt: p.updatedAt, local: true })));
      return;
    }
    setProjectsLoading(true);
    try {
      let list: ProjectSummary[];
      try {
        list = (await api.listWorkspaceProjects(workspaceId)).projects;
      } catch (e) {
        // Older API without the nested route: filter on the flat one.
        if (!(e instanceof ApiError) || e.status !== 404) throw e;
        list = (await api.listProjects(workspaceId)).projects;
      }
      setProjects(list.map((p) => ({ id: p.id, name: p.name, updatedAt: p.updatedAt, local: false })));
    } catch (e) {
      setProjects([]);
      setProjectsError(e instanceof ApiError ? e.message : 'Could not load projects.');
    } finally {
      setProjectsLoading(false);
    }
  }, [workspaceId, session]);

  useEffect(() => {
    void reloadProjects();
  }, [reloadProjects]);

  const addWorkspace = async () => {
    const name = newWs.trim() || 'My workspace';
    if (session) {
      try {
        await api.createWorkspace(name);
        setNewWs('');
        await reloadWorkspaces();
        return;
      } catch {
        /* fall through to local */
      }
    }
    createLocalWorkspace(name);
    setNewWs('');
    await reloadWorkspaces();
  };

  const removeWorkspace = async (id: string) => {
    if (!isLocalWorkspaceId(id) && session) {
      try {
        await api.deleteWorkspace(id);
      } catch {
        return;
      }
    } else {
      deleteLocalWorkspace(id);
    }
    if (workspaceId === id) selectWorkspace(null);
    await reloadWorkspaces();
  };

  const removeProject = async (p: ProjectCard) => {
    if (p.local) {
      if (workspaceId) deleteLocalProject(workspaceId, p.id);
    } else {
      try {
        await api.deleteProject(p.id);
      } catch {
        return;
      }
    }
    await reloadProjects();
  };

  /** Template cards start straight in the studio; the wizard adds theme + name. */
  const buildHref = (template: string) => `/build?template=${encodeURIComponent(template)}${workspaceId ? `&workspace=${encodeURIComponent(workspaceId)}` : ''}`;
  const studioHref = workspaceId ? `/build?workspace=${encodeURIComponent(workspaceId)}` : '/build';

  return (
    <div className="pg-page">
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

      <section>
        <h2>Workspaces</h2>
        <p className="pg-muted">Group projects. Signed-in users sync to the API; everyone else keeps them in this browser.</p>
        <div className="pg-row" style={{ marginBottom: '0.75rem' }}>
          <input aria-label="New workspace name" placeholder="Workspace name" value={newWs} onChange={(e) => setNewWs(e.target.value)} />
          <button type="button" className="pg-btn pg-btn-sm" onClick={() => void addWorkspace()}>
            New workspace
          </button>
        </div>
        <div className="pg-cards">
          {workspaces.map((w) => (
            <div key={w.id} className={`pg-card pg-ws-card${w.id === workspaceId ? ' is-selected' : ''}`} data-testid={`workspace-card-${w.id}`}>
              <h3>{w.name}</h3>
              <p className="pg-muted">
                Created {new Date(w.createdAt).toLocaleDateString()}
                {isLocalWorkspaceId(w.id) ? ' \u00B7 this browser' : ''}
              </p>
              <div className="pg-row" style={{ marginTop: '0.75rem' }}>
                <button
                  type="button"
                  className="pg-btn pg-btn-sm"
                  aria-pressed={w.id === workspaceId}
                  data-testid={`workspace-open-${w.id}`}
                  onClick={() => selectWorkspace(w.id === workspaceId ? null : w.id)}
                >
                  {w.id === workspaceId ? 'Close' : 'Open'}
                </button>
                <Link className="pg-btn pg-btn-ghost pg-btn-sm" to={`/build?workspace=${encodeURIComponent(w.id)}`}>
                  Studio
                </Link>
                <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void removeWorkspace(w.id)}>
                  Delete
                </button>
              </div>
            </div>
          ))}
          {!workspaces.length ? <p className="pg-muted">No workspaces yet {'\u2014'} create one or start building.</p> : null}
        </div>
      </section>

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
                <h3>{p.name}</h3>
                <p className="pg-muted">
                  Updated {new Date(p.updatedAt).toLocaleString()}
                  {p.local ? ' \u00B7 this browser' : ''}
                </p>
                <div className="pg-row" style={{ marginTop: '0.75rem' }}>
                  <Link className="pg-btn pg-btn-sm" to={`/build?project=${encodeURIComponent(p.id)}&workspace=${encodeURIComponent(workspaceId)}`}>
                    Open
                  </Link>
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" aria-label={`Delete project ${p.name}`} onClick={() => void removeProject(p)}>
                    Delete
                  </button>
                </div>
              </div>
            ))}
            {!projects.length && !projectsLoading && !projectsError ? (
              <p className="pg-muted" data-testid="workspace-projects-empty">
                No projects in this workspace yet. Start one from a template below, or use New project.
              </p>
            ) : null}
          </div>
        </section>
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
          {templates.map((t) => (
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

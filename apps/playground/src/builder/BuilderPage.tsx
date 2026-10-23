import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  defaultProps,
  emptySpec,
  instantiateTemplate,
  migrateSpec,
  validateSpec,
  type DappSpec,
} from '@splendor/dapp-spec';
import { api, ApiError } from '../api/client';
import { useSession } from '../auth/session';
import type { MockWalletState } from '../preview/mock';
import { validateForPreview } from '../preview/validatePreview';
import { useRegistry } from '../registry';
import { type CloudState } from './ProjectPanel';
import { Studio } from './Studio';
import { getLocalProject, isLocalWorkspaceId, saveLocalProjectWithInfo } from '../workspaces/local';
import { decodeAnswers, specFromWizard } from './newProject';
import { KitCards } from '../home/KitCards';
import { NewProjectWizard } from '../home/NewProjectWizard';
import { builderReducer, initBuilder } from './state';
import { clearDraft, loadDraft, saveDraft } from './storage';

type EditorTab = 'block' | 'theme' | 'export' | 'project';

function TemplatePicker({ onPick }: { onPick: (spec: DappSpec) => void }) {
  const { templates } = useRegistry();
  /** Kit id the setup wizard is open on. */
  const [kitWizard, setKitWizard] = useState<string | null>(null);
  return (
    <div className="pg-page">
      <section>
        <h1>What do you want to make?</h1>
        <p className="pg-muted">Choose a ready-made dApp and change it, or start blank and drop pieces onto the page.</p>
      </section>
      <section aria-label="Ready-made apps">
        <KitCards kits={templates.filter((t) => t.kit)} testIdPrefix="pick-kit" onPick={setKitWizard} />
      </section>
      {kitWizard ? (
        <NewProjectWizard
          templates={templates}
          initialTemplate={kitWizard}
          onCancel={() => setKitWizard(null)}
          onCreate={(r) => {
            setKitWizard(null);
            onPick(specFromWizard({ template: r.template, preset: r.preset, name: r.name, answers: r.answers }) ?? emptySpec());
          }}
        />
      ) : null}
      <div className="pg-cards">
        {templates.filter((t) => !t.kit).map((t) => (
          <button key={t.id} type="button" className="pg-card" style={{ textAlign: 'left', font: 'inherit', cursor: 'pointer' }} data-testid={`pick-${t.id}`} onClick={() => onPick(instantiateTemplate(t))}>
            <h3>{t.title}</h3>
            <p>{t.description}</p>
            {t.setup.length ? <p style={{ marginTop: '0.5rem' }}>You will set: {t.setup.map((s) => s.label).join(', ')}</p> : null}
          </button>
        ))}
        <button type="button" className="pg-card" style={{ textAlign: 'left', font: 'inherit', cursor: 'pointer' }} data-testid="pick-blank" onClick={() => onPick(emptySpec())}>
          <h3>Blank page</h3>
          <p>A heading, then an empty page. Drag pieces on and watch them appear.</p>
        </button>
      </div>
    </div>
  );
}

/** Query params that only seed a new project; they are removed once consumed. `workspace` stays. */
const SEED_PARAMS = ['template', 'preset', 'name', 'answers', 'project'] as const;

type Start = { spec: DappSpec; projectId: string | null; key: number; synced?: boolean; localProjectId?: string | null };

export function BuilderPage() {
  const [params, setParams] = useSearchParams();
  const workspaceId = params.get('workspace');
  const projectParam = params.get('project');
  const [loadingProject, setLoadingProject] = useState(projectParam != null);
  /** `synced`: the spec was just loaded from the account, so it matches the saved copy. */
  const [start, setStart] = useState<Start | null>(() => {
    if (projectParam) return null;
    const t = params.get('template');
    if (t) {
      // The home wizard passes template + preset + name; a bare ?template= keeps the template as is.
      const spec = specFromWizard({ template: t, preset: params.get('preset'), name: params.get('name'), answers: decodeAnswers(params.get('answers')) });
      if (spec) return { spec, projectId: null, key: Date.now() };
    }
    const draft = loadDraft();
    if (draft) return { spec: draft.spec, projectId: draft.projectId, key: Date.now() };
    return null;
  });

  // ?project=<id> from the home page: a cloud project, or `lp-…` from this browser's workspace store.
  useEffect(() => {
    if (!projectParam) return;
    let live = true;
    void (async () => {
      try {
        if (projectParam.startsWith('lp-')) {
          const local = workspaceId ? getLocalProject(workspaceId, projectParam) : null;
          if (local && live) setStart({ spec: migrateSpec(local.spec), projectId: null, localProjectId: local.id, key: Date.now() });
        } else {
          const p = await api.getProject(projectParam);
          if (live) setStart({ spec: migrateSpec(p.spec), projectId: p.id, key: Date.now(), synced: true });
        }
      } catch {
        // Missing or unreadable project: fall through to the draft / picker.
        const draft = loadDraft();
        if (draft && live) setStart({ spec: draft.spec, projectId: draft.projectId, key: Date.now() });
      } finally {
        if (live) setLoadingProject(false);
      }
    })();
    return () => {
      live = false;
    };
    // Once per project id; `workspaceId` only selects the local store.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectParam]);

  useEffect(() => {
    if (loadingProject || !SEED_PARAMS.some((k) => params.has(k))) return;
    const next = new URLSearchParams(params);
    for (const k of SEED_PARAMS) next.delete(k);
    setParams(next, { replace: true });
  }, [params, setParams, loadingProject]);

  if (loadingProject) return <p className="pg-muted pg-page">Opening project…</p>;

  if (!start) {
    return <TemplatePicker onPick={(spec) => setStart({ spec, projectId: null, key: Date.now() })} />;
  }

  return (
    <Editor
      key={start.key}
      initial={start.spec}
      initialProjectId={start.projectId}
      initialLocalProjectId={start.localProjectId ?? null}
      initialSynced={start.synced === true}
      onOpen={(spec, projectId) => setStart({ spec, projectId, key: Date.now(), synced: projectId != null })}
      onNew={() => {
        clearDraft();
        setStart(null);
      }}
    />
  );
}

function Editor({
  initial,
  initialProjectId,
  initialLocalProjectId,
  initialSynced,
  onOpen,
  onNew,
}: {
  initial: DappSpec;
  initialProjectId: string | null;
  initialLocalProjectId: string | null;
  initialSynced: boolean;
  onOpen: (spec: DappSpec, projectId: string | null) => void;
  onNew: () => void;
}) {
  const { all, catalog, compatibilityNote } = useRegistry();
  const { session } = useSession();
  const [params, setParams] = useSearchParams();
  const [state, dispatch] = useReducer(builderReducer, initial, initBuilder);
  // `initBuilder` normalizes `initial` into a new object: "unchanged" means still this one.
  const openedSpec = useRef(state.spec);
  const [projectId, setProjectId] = useState<string | null>(initialProjectId);
  const [mode, setMode] = useState<'mock' | 'live'>('mock');
  const [walletState, setWalletState] = useState<MockWalletState>('connected');
  const [tab, setTab] = useState<EditorTab>('block');
  const [calls, setCalls] = useState<{ method: string; args: string }[]>([]);
  const [blockQuery, setBlockQuery] = useState('');
  const [dropIndex, setDropIndex] = useState<number | null>(null);
  const dragRef = useRef<{ kind: 'widget' | 'block'; id: string } | null>(null);
  const skipClick = useRef(false);
  const [cloud, setCloud] = useState<CloudState>({ status: initialSynced ? 'saved' : 'local' });
  const mainnetLiveConfirmed = useRef(false);
  const [newPageTitle, setNewPageTitle] = useState<string | null>(null);
  const { spec, pagePath, selectedBlockId } = state;

  const validation = useMemo(() => validateSpec(spec, { catalog: all }), [spec, all]);
  const issues = validation.ok ? [] : validation.issues;
  // Live mode renders like the mock preview: empty setup fields show the block's own prompt.
  const renderable = useMemo(() => validateForPreview(spec, all).spec, [spec, all]);

  /** Live mode signs real transactions; mainnet needs an explicit yes however you get there. */
  const confirmMainnetLive = () => {
    if (mainnetLiveConfirmed.current) return true;
    const ok = window.confirm('Live mode on mainnet signs real transactions that spend real SPLD. Check every address in this dApp first. Continue?');
    if (ok) mainnetLiveConfirmed.current = true;
    return ok;
  };

  // Undo, redo, an import or an opened project can also move a live preview onto mainnet.
  useEffect(() => {
    if (mode === 'live' && spec.network === 'mainnet' && !confirmMainnetLive()) setMode('mock');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, spec.network]);

  const goToIssue = (target: { pagePath: string; blockId: string; prop: string | null; entryId: string | null }) => {
    dispatch({ type: 'selectPage', path: target.pagePath });
    dispatch({ type: 'selectBlock', id: target.blockId });
    setTab('block');
    if (target.prop && target.entryId) {
      const id = `prop-${target.entryId}-${target.prop}`;
      window.setTimeout(() => document.getElementById(id)?.focus(), 0);
    }
  };
  const page = spec.pages.find((p) => p.path === pagePath) ?? spec.pages[0]!;
  const selected = (page.blocks ?? []).find((b) => b.id === selectedBlockId) ?? null;
  const selectedEntry = selected ? all.find((e) => e.id === selected.widget) : undefined;

  // ?add=<widget> from the gallery
  useEffect(() => {
    const add = params.get('add');
    if (!add) return;
    const entry = catalog.find((e) => e.id === add && e.placeable);
    if (entry) {
      dispatch({ type: 'addBlock', widget: entry.id, props: defaultProps(entry) });
      setTab('block');
    }
    params.delete('add');
    setParams(params, { replace: true });
  }, [params, setParams, catalog]);

  useEffect(() => saveDraft({ spec, projectId }), [spec, projectId]);

  // Cloud autosave (debounced) once the project lives in the user's account.
  const lastSaved = useRef<DappSpec | null>(initialSynced ? openedSpec.current : null);
  useEffect(() => {
    if (!session || !projectId || lastSaved.current === spec) return;
    if (!validation.ok) {
      setCloud({ status: 'unsaved' });
      return;
    }
    const t = window.setTimeout(() => {
      setCloud({ status: 'saving' });
      api
        .updateProject(projectId, spec)
        .then(() => {
          lastSaved.current = spec;
          setCloud({ status: 'saved' });
        })
        .catch((e) => setCloud({ status: 'error', message: e instanceof ApiError ? e.message : String(e) }));
    }, 1500);
    return () => window.clearTimeout(t);
  }, [spec, session, projectId, validation.ok]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
      if (!(e.ctrlKey || e.metaKey)) return;
      if (e.key.toLowerCase() === 'z') {
        e.preventDefault();
        dispatch({ type: e.shiftKey ? 'redo' : 'undo' });
      } else if (e.key.toLowerCase() === 'y') {
        e.preventDefault();
        dispatch({ type: 'redo' });
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const pageIndex = spec.pages.indexOf(page);
  const blockErrors = useMemo(() => {
    if (!selected) return {};
    const bi = (page.blocks ?? []).indexOf(selected);
    const prefix = `pages[${pageIndex}].blocks[${bi}].props.`;
    return Object.fromEntries(issues.filter((i) => i.path.startsWith(prefix)).map((i) => [i.path.slice(prefix.length), i.message]));
  }, [issues, selected, page, pageIndex]);

  // ?workspace=<id>: new projects are filed under it. Local workspaces (and signed-out use) keep them in this browser.
  const workspaceId = params.get('workspace');
  const remoteWorkspaceId = workspaceId && !isLocalWorkspaceId(workspaceId) ? workspaceId : null;
  const localWorkspaceId = workspaceId && (isLocalWorkspaceId(workspaceId) || !session) ? workspaceId : null;
  const localProjectId = useRef<string | null>(initialLocalProjectId);
  const [localNotice, setLocalNotice] = useState<string | null>(null);
  useEffect(() => {
    // Nothing to save until the user changes something.
    if (!localWorkspaceId || spec === openedSpec.current) return;
    const t = window.setTimeout(() => {
      const r = saveLocalProjectWithInfo(localWorkspaceId, spec, localProjectId.current);
      localProjectId.current = r.project.id;
      if (!r.saved) setLocalNotice('This browser refused to save the project (storage full or disabled). Export it or sign in so you do not lose it.');
      else if (r.trimmed.length) {
        setLocalNotice(`Removed ${r.trimmed.length} oldest project${r.trimmed.length === 1 ? '' : 's'} from this workspace to stay within the browser limit: ${r.trimmed.map((p) => p.name).join(', ')}.`);
      }
    }, 800);
    return () => window.clearTimeout(t);
  }, [spec, localWorkspaceId]);

  const saveNew = useCallback(async () => {
    setCloud({ status: 'saving' });
    try {
      const p = await api.createProject(spec, remoteWorkspaceId);
      lastSaved.current = spec;
      setProjectId(p.id);
      setCloud({ status: 'saved' });
    } catch (e) {
      setCloud({ status: 'error', message: e instanceof ApiError ? e.message : String(e) });
    }
  }, [spec, remoteWorkspaceId]);

  const openProject = useCallback(
    async (id: string) => {
      const p = await api.getProject(id);
      onOpen(migrateSpec(p.spec), p.id);
    },
    [onOpen],
  );

  return (
    <>
    {localNotice ? (
      <div className="pg-local-notice" role="alert">
        <span>{localNotice}</span>
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setLocalNotice(null)}>
          Dismiss
        </button>
      </div>
    ) : null}
    <Studio
      spec={spec}
      pagePath={pagePath}
      selectedBlockId={selectedBlockId}
      dispatch={dispatch}
      catalog={catalog}
      all={all}
      compatibilityNote={compatibilityNote}
      mode={mode}
      setMode={setMode}
      walletState={walletState}
      setWalletState={setWalletState}
      confirmMainnetLive={confirmMainnetLive}
      tab={tab}
      setTab={setTab}
      calls={calls}
      setCalls={setCalls}
      issues={issues}
      blockErrors={blockErrors}
      goToIssue={goToIssue}
      newPageTitle={newPageTitle}
      setNewPageTitle={setNewPageTitle}
      blockQuery={blockQuery}
      setBlockQuery={setBlockQuery}
      dropIndex={dropIndex}
      setDropIndex={setDropIndex}
      dragRef={dragRef}
      skipClick={skipClick}
      validationOk={validation.ok}
      renderable={renderable}
      projectId={projectId}
      cloud={cloud}
      signedIn={session != null}
      canUndo={state.past.length > 0}
      canRedo={state.future.length > 0}
      onSaveNew={saveNew}
      onOpenProject={openProject}
      onImport={(imported) => onOpen(imported, null)}
      onDeleted={() => setProjectId(null)}
      onNew={onNew}
    />
    </>
  );
}

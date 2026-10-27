import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  defaultProps,
  emptySpec,
  findNode,
  instantiateTemplate,
  migrateSpec,
  validateSpec,
  type DappSpec,
  type LayoutNode,
} from '@splendor/dapp-spec';
import { api, ApiError } from '../api/client';
import { useSession } from '../auth/session';
import type { MockWalletState } from '../preview/mock';
import { validateForPreview } from '../preview/validatePreview';
import { useConfirm } from '../components/ConfirmDialog';
import { useRegistry } from '../registry';
import { type CloudState } from './ProjectPanel';
import { Studio } from './Studio';
import { getLocalProject, isLocalWorkspaceId, saveLocalProjectWithInfo } from '../workspaces/local';
import { decodeAnswers, specFromWizard } from './newProject';
import { KitCards } from '../home/KitCards';
import { NewProjectWizard } from '../home/NewProjectWizard';
import { builderReducer, initBuilder } from './state';
import { clearDraft, discardStashedDraft, loadDraft, peekStashedDraft, saveDraft, stashDraft, takeStashedDraft, type Draft } from './storage';

type EditorTab = 'block' | 'theme' | 'export' | 'project';

/** Ctrl/Cmd+C on a selected block or container; kept for this tab so it can be pasted into another project. */
let copiedNode: LayoutNode | null = null;

/** Edits are written to the browser draft at most this often (and on leave / unmount). */
const DRAFT_DEBOUNCE_MS = 800;
/** Waits between cloud autosave retries after a network or server error. */
const SAVE_RETRY_DELAYS = [2_000, 5_000, 15_000] as const;

function errorText(e: unknown): string {
  return e instanceof ApiError || e instanceof Error ? e.message : String(e);
}

/** Offers the draft that a template, New, Import or Remix replaced before it was saved anywhere. */
function StashNotice({ onRestore }: { onRestore: (draft: Draft) => void }) {
  const [stash, setStash] = useState(() => peekStashedDraft());
  if (!stash) return null;
  return (
    <div className="pg-local-notice" role="status" data-testid="stash-notice">
      <span>
        {stash.projectId
          ? `Unsaved edits to “${stash.spec.name}” were replaced before they reached your account.`
          : `Your previous draft “${stash.spec.name}” was replaced. It was not saved anywhere else.`}
      </span>
      <button
        type="button"
        className="pg-btn pg-btn-sm"
        data-testid="stash-restore"
        onClick={() => {
          const d = takeStashedDraft();
          setStash(null);
          if (d) onRestore(d);
        }}
      >
        Restore it
      </button>
      <button
        type="button"
        className="pg-btn pg-btn-ghost pg-btn-sm"
        onClick={() => {
          discardStashedDraft();
          setStash(null);
        }}
      >
        Dismiss
      </button>
    </div>
  );
}

function TemplatePicker({ onPick, onRestore }: { onPick: (spec: DappSpec) => void; onRestore: (draft: Draft) => void }) {
  const { templates } = useRegistry();
  /** Kit id the setup wizard is open on. */
  const [kitWizard, setKitWizard] = useState<string | null>(null);
  return (
    <div className="pg-page">
      <StashNotice onRestore={onRestore} />
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

/** Query params that only seed a new project; they are removed once consumed. `workspace` and `project` stay. */
const SEED_PARAMS = ['template', 'preset', 'name', 'answers'] as const;

type Start = {
  spec: DappSpec;
  projectId: string | null;
  key: number;
  /** The spec was just loaded from the account, so it matches the saved copy. */
  synced?: boolean;
  localProjectId?: string | null;
  /** Cloud version the spec is based on. */
  version?: number;
  /** The spec has edits the account copy does not have yet. */
  dirty?: boolean;
};

export function BuilderPage() {
  const [params, setParams] = useSearchParams();
  const workspaceId = params.get('workspace');
  const projectParam = params.get('project');
  const [loadingProject, setLoadingProject] = useState(projectParam != null);
  /** `?project=` could not be opened; shown above the picker or the editor until dismissed. */
  const [loadError, setLoadError] = useState<string | null>(null);
  /** Project the editor has open (cloud id or `lp-…`), so writing it to the URL does not reload it. */
  const editorProject = useRef<string | null>(null);
  const [start, setStart] = useState<Start | null>(() => {
    if (projectParam) return null;
    const t = params.get('template');
    if (t) {
      // The home wizard passes template + preset + name; a bare ?template= keeps the template as is.
      const spec = specFromWizard({ template: t, preset: params.get('preset'), name: params.get('name'), answers: decodeAnswers(params.get('answers')) });
      if (spec) {
        stashDraft();
        return { spec, projectId: null, key: Date.now() };
      }
    }
    const draft = loadDraft();
    if (draft) return { spec: draft.spec, projectId: draft.projectId, version: draft.version, dirty: draft.dirty, key: Date.now() };
    return null;
  });

  // ?project=<id>: a cloud project, or `lp-…` from this browser's workspace store.
  useEffect(() => {
    if (!projectParam || editorProject.current === projectParam) return;
    let live = true;
    setLoadingProject(true);
    setLoadError(null);
    void (async () => {
      try {
        if (projectParam.startsWith('lp-')) {
          const local = workspaceId ? getLocalProject(workspaceId, projectParam) : null;
          if (!live) return;
          if (local) setStart({ spec: migrateSpec(local.spec), projectId: null, localProjectId: local.id, key: Date.now() });
          else {
            setStart(null);
            setLoadError('That project is not in this browser. Browser projects only exist where they were made.');
          }
        } else {
          const p = await api.getProject(projectParam);
          // Unsaved edits to this same version (a reload before autosave ran) win over the account copy.
          const draft = loadDraft();
          const resume = draft && draft.projectId === p.id && draft.dirty && draft.version != null && draft.version === p.version;
          if (live) {
            setStart(
              resume
                ? { spec: draft.spec, projectId: p.id, version: p.version, dirty: true, key: Date.now() }
                : { spec: migrateSpec(p.spec), projectId: p.id, version: p.version, key: Date.now(), synced: true },
            );
          }
        }
      } catch (e) {
        if (!live) return;
        // This browser's copy of the same project still opens (the API is down, say); any other
        // draft is unrelated to the link and is not opened in its place.
        const draft = loadDraft();
        if (draft && draft.projectId === projectParam) {
          setStart({ spec: draft.spec, projectId: draft.projectId, version: draft.version, dirty: draft.dirty, key: Date.now() });
          setLoadError(`Could not load the account copy (${errorText(e)}). Showing the copy saved in this browser.`);
        } else {
          setStart(null);
          const status = e instanceof ApiError ? e.status : -1;
          setLoadError(
            status === 404
              ? 'That project was not found. It may have been deleted, or it belongs to another account.'
              : status === 401
                ? 'Sign in to open that project. Use Share in the studio, or the sign-in in the header.'
                : `Could not open that project: ${errorText(e)}.`,
          );
        }
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

  const onProjectIdChange = useCallback((id: string | null) => {
    editorProject.current = id;
  }, []);

  if (loadingProject) return <p className="pg-muted pg-page">Opening project…</p>;

  const restore = (d: Draft) => {
    setLoadError(null);
    setStart({ spec: d.spec, projectId: d.projectId, version: d.version, dirty: d.dirty, key: Date.now() });
  };
  const loadNotice = loadError ? (
    <div className="pg-local-notice" role="alert" data-testid="project-load-error">
      <span>{loadError}</span>
      <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setLoadError(null)}>
        Dismiss
      </button>
    </div>
  ) : null;

  if (!start) {
    return (
      <>
        {loadNotice}
        <TemplatePicker
          onPick={(spec) => {
            setLoadError(null);
            setStart({ spec, projectId: null, key: Date.now() });
          }}
          onRestore={restore}
        />
      </>
    );
  }

  return (
    <>
    {loadNotice}
    <Editor
      key={start.key}
      initial={start.spec}
      initialProjectId={start.projectId}
      initialLocalProjectId={start.localProjectId ?? null}
      initialSynced={start.synced === true}
      initialVersion={start.version}
      initialDirty={start.dirty === true}
      onProjectIdChange={onProjectIdChange}
      onOpen={(spec, projectId, version) => {
        setLoadError(null);
        setStart({ spec, projectId, version, key: Date.now(), synced: projectId != null });
      }}
      onRestore={restore}
      onImport={(spec) => {
        stashDraft();
        setLoadError(null);
        setStart({ spec, projectId: null, key: Date.now() });
      }}
      onNew={() => {
        stashDraft();
        clearDraft();
        setLoadError(null);
        editorProject.current = null;
        setStart(null);
      }}
    />
    </>
  );
}

function Editor({
  initial,
  initialProjectId,
  initialLocalProjectId,
  initialSynced,
  initialVersion,
  initialDirty,
  onProjectIdChange,
  onOpen,
  onRestore,
  onImport: onImportSpec,
  onNew: onNewProject,
}: {
  initial: DappSpec;
  initialProjectId: string | null;
  initialLocalProjectId: string | null;
  initialSynced: boolean;
  initialVersion: number | undefined;
  initialDirty: boolean;
  onProjectIdChange: (id: string | null) => void;
  onOpen: (spec: DappSpec, projectId: string | null, version?: number) => void;
  onRestore: (draft: Draft) => void;
  onImport: (spec: DappSpec) => void;
  onNew: () => void;
}) {
  const { all, catalog, compatibilityNote } = useRegistry();
  const { session, ready: sessionReady } = useSession();
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

  /**
   * Live mode signs real transactions; mainnet needs an explicit yes however you get there. The
   * yes lasts for this editor only: opening, importing or remixing another spec mounts a new one.
   */
  const [confirmDialog, confirm] = useConfirm();
  const mainnetAsk = useRef<Promise<boolean> | null>(null);
  const confirmMainnetLive = useCallback((): Promise<boolean> => {
    if (mainnetLiveConfirmed.current) return Promise.resolve(true);
    mainnetAsk.current ??= confirm({
      title: 'Use a real wallet on mainnet?',
      message: 'Live mode on mainnet signs real transactions that spend real SPLD. Check every address in this dApp first.',
      confirmLabel: 'Use mainnet',
      danger: true,
    }).then((ok) => {
      mainnetAsk.current = null;
      if (ok) mainnetLiveConfirmed.current = true;
      return ok;
    });
    return mainnetAsk.current;
  }, [confirm]);

  // Undo, redo, an import or an opened project can also move a live preview onto mainnet.
  useEffect(() => {
    if (mode !== 'live' || spec.network !== 'mainnet') return;
    void confirmMainnetLive().then((ok) => {
      if (!ok) setMode('mock');
    });
  }, [mode, spec.network, confirmMainnetLive]);

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

  // Cloud saves send the version they started from, so a newer copy from another tab or device is
  // reported as a conflict instead of being overwritten.
  const versionRef = useRef<number | undefined>(initialVersion);
  const lastSaved = useRef<DappSpec | null>(initialSynced ? openedSpec.current : null);
  /** Set while the account has a newer copy than the one these edits started from. */
  const conflictRef = useRef<{ version?: number } | null>(null);
  const specRef = useRef(spec);
  specRef.current = spec;
  const projectIdRef = useRef(projectId);
  projectIdRef.current = projectId;
  /** Edits the account copy does not have: nothing counts until the user changes something. */
  const hasPendingEdits = (s: DappSpec) => lastSaved.current !== s && (initialDirty || s !== openedSpec.current);

  /** The browser refused the last draft write: the edits on screen exist only in memory. */
  const [draftWriteFailed, setDraftWriteFailed] = useState(false);
  const draftWriteFailedRef = useRef(false);
  /** An edit is waiting for the debounced draft write. */
  const draftPending = useRef(false);
  const persistDraft = useCallback(() => {
    draftPending.current = false;
    const s = specRef.current;
    const id = projectIdRef.current;
    const ok = saveDraft({ spec: s, projectId: id, ...(id && versionRef.current ? { version: versionRef.current } : {}), dirty: id != null && hasPendingEdits(s) });
    draftWriteFailedRef.current = !ok;
    setDraftWriteFailed(!ok);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const firstDraftWrite = useRef(true);
  useEffect(() => {
    // What opened is written at once, so a reload reopens it; edits after that are batched.
    if (firstDraftWrite.current) {
      firstDraftWrite.current = false;
      persistDraft();
      return;
    }
    draftPending.current = true;
    const t = window.setTimeout(persistDraft, DRAFT_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [spec, projectId, persistDraft]);
  const flushDraft = useCallback(() => {
    if (draftPending.current) persistDraft();
  }, [persistDraft]);
  useEffect(() => {
    // A pending write must not be lost to a closed tab, or to switching projects.
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      flushDraft();
      if (!draftWriteFailedRef.current) return;
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('pagehide', flushDraft);
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => {
      window.removeEventListener('pagehide', flushDraft);
      window.removeEventListener('beforeunload', onBeforeUnload);
      flushDraft();
    };
  }, [flushDraft]);
  // New and Import read the draft to stash it, so it must be current first.
  const onNew = useCallback(() => {
    flushDraft();
    onNewProject();
  }, [flushDraft, onNewProject]);
  const onImport = useCallback(
    (s: DappSpec) => {
      flushDraft();
      onImportSpec(s);
    },
    [flushDraft, onImportSpec],
  );

  /** Saves run one at a time, each with the version the previous one returned. */
  const saveQueue = useRef<Promise<void>>(Promise.resolve());
  const retryCount = useRef(0);
  const retryTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(retryTimer.current), []);
  const saveToCloudRef = useRef<(s: DappSpec) => Promise<void>>(() => Promise.resolve());
  const saveToCloud = useCallback(
    (s: DappSpec) => {
      const run = async () => {
        const id = projectIdRef.current;
        if (!id || lastSaved.current === s || conflictRef.current) return;
        window.clearTimeout(retryTimer.current);
        setCloud({ status: 'saving' });
        try {
          const p = await api.updateProject(id, s, versionRef.current);
          if (p.version) versionRef.current = p.version;
          lastSaved.current = s;
          retryCount.current = 0;
          setCloud({ status: 'saved' });
        } catch (e) {
          if (e instanceof ApiError && e.code === 'CONFLICT') {
            conflictRef.current = { version: e.current?.version };
            setCloud({ status: 'conflict', message: e.message });
          } else {
            // Network and server errors are retried; a rejected spec (4xx) waits for the next edit.
            const transient = !(e instanceof ApiError) || e.status === 0 || e.status === 429 || e.status >= 500;
            const delay = transient ? SAVE_RETRY_DELAYS[retryCount.current] : undefined;
            if (delay !== undefined) {
              retryCount.current += 1;
              retryTimer.current = window.setTimeout(() => {
                if (projectIdRef.current === id) void saveToCloudRef.current(specRef.current);
              }, delay);
            }
            setCloud({ status: 'error', message: `${errorText(e)}${delay !== undefined ? ' Retrying…' : ''}` });
          }
        }
        persistDraft();
      };
      saveQueue.current = saveQueue.current.then(run);
      return saveQueue.current;
    },
    [persistDraft],
  );
  saveToCloudRef.current = saveToCloud;

  // Cloud autosave (debounced) once the project lives in the user's account.
  useEffect(() => {
    if (!session || !projectId || !hasPendingEdits(spec) || conflictRef.current) return;
    if (!validation.ok) {
      setCloud({ status: 'unsaved' });
      return;
    }
    retryCount.current = 0;
    const t = window.setTimeout(() => void saveToCloud(spec), 1500);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spec, session, projectId, validation.ok, saveToCloud]);

  /** Conflict: save these edits on top of the newer copy. */
  const keepMine = useCallback(async () => {
    const c = conflictRef.current;
    conflictRef.current = null;
    if (c?.version) versionRef.current = c.version;
    lastSaved.current = null;
    await saveToCloud(specRef.current);
  }, [saveToCloud]);

  const selectionRef = useRef({ spec, selectedBlockId });
  selectionRef.current = { spec, selectedBlockId };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target && (/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.isContentEditable)) return;
      if (!(e.ctrlKey || e.metaKey)) return;
      const key = e.key.toLowerCase();
      if (key === 'z') {
        e.preventDefault();
        dispatch({ type: e.shiftKey ? 'redo' : 'undo' });
      } else if (key === 'y') {
        e.preventDefault();
        dispatch({ type: 'redo' });
      } else if (key === 'c') {
        // Selected text on the page is copied as text, as usual.
        if (window.getSelection()?.toString()) return;
        const { spec: s, selectedBlockId: id } = selectionRef.current;
        const node = id ? s.pages.map((p) => findNode(p.root, id)).find((n) => n != null) : null;
        if (!node || s.pages.some((p) => p.root === node)) return;
        e.preventDefault();
        copiedNode = node;
      } else if (key === 'v' && copiedNode) {
        e.preventDefault();
        dispatch({ type: 'pasteNode', node: copiedNode, afterId: selectionRef.current.selectedBlockId });
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

  // ?workspace=<id>: new projects are filed under it. `local-…` workspaces keep them in this browser;
  // an account workspace id is never used as a browser key, signed in or not.
  const workspaceId = params.get('workspace');
  const remoteWorkspaceId = workspaceId && !isLocalWorkspaceId(workspaceId) ? workspaceId : null;
  const localWorkspaceId = isLocalWorkspaceId(workspaceId) ? workspaceId : null;
  const needsSignIn = sessionReady && !session && remoteWorkspaceId != null;
  const localProjectId = useRef<string | null>(initialLocalProjectId);
  const [localProject, setLocalProject] = useState<string | null>(initialLocalProjectId);
  const [localNotice, setLocalNotice] = useState<string | null>(null);
  useEffect(() => {
    // Nothing to save until the user changes something.
    if (!localWorkspaceId || spec === openedSpec.current) return;
    const t = window.setTimeout(() => {
      const r = saveLocalProjectWithInfo(localWorkspaceId, spec, localProjectId.current);
      localProjectId.current = r.project.id;
      if (r.saved) setLocalProject(r.project.id);
      if (!r.saved) setLocalNotice('This browser refused to save the project (storage full or disabled). Export it or sign in so you do not lose it.');
      else if (r.trimmed.length) {
        setLocalNotice(`Removed ${r.trimmed.length} oldest project${r.trimmed.length === 1 ? '' : 's'} from this workspace to stay within the browser limit: ${r.trimmed.map((p) => p.name).join(', ')}.`);
      }
    }, 800);
    return () => window.clearTimeout(t);
  }, [spec, localWorkspaceId]);

  // The open project lives in the URL, so a reload or a second tab opens the same one.
  const urlProject = projectId ?? (localWorkspaceId ? localProject : null);
  useEffect(() => {
    onProjectIdChange(urlProject);
    if ((params.get('project') ?? null) === urlProject) return;
    const next = new URLSearchParams(params);
    if (urlProject) next.set('project', urlProject);
    else next.delete('project');
    setParams(next, { replace: true });
  }, [urlProject, params, setParams, onProjectIdChange]);

  /** One create at a time: a second click must not make a second project. */
  const creating = useRef(false);
  const saveNew = useCallback(async () => {
    if (creating.current || projectIdRef.current) return;
    creating.current = true;
    setCloud({ status: 'saving' });
    try {
      const p = await api.createProject(spec, remoteWorkspaceId);
      lastSaved.current = spec;
      versionRef.current = p.version;
      setProjectId(p.id);
      setCloud({ status: 'saved' });
    } catch (e) {
      setCloud({ status: 'error', message: errorText(e) });
    } finally {
      creating.current = false;
    }
  }, [spec, remoteWorkspaceId]);

  const openProject = useCallback(
    async (id: string) => {
      const p = await api.getProject(id);
      onOpen(migrateSpec(p.spec), p.id, p.version);
    },
    [onOpen],
  );

  /** Saves an older version as the newest one; unsaved edits here are saved first, so they stay in the history. */
  const restoreRevision = useCallback(
    async (version: number) => {
      const id = projectIdRef.current;
      if (!id) return;
      if (hasPendingEdits(specRef.current) && !conflictRef.current && validation.ok) await saveToCloud(specRef.current);
      if (conflictRef.current) throw new Error('Resolve the save conflict first.');
      const p = await api.restoreRevision(id, version, versionRef.current ?? 1);
      onOpen(migrateSpec(p.spec), p.id, p.version);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [onOpen, saveToCloud, validation.ok],
  );

  const openNewer = useCallback(async () => {
    if (!projectId) return;
    try {
      await openProject(projectId);
    } catch (e) {
      setCloud({ status: 'conflict', message: e instanceof ApiError ? e.message : String(e) });
    }
  }, [projectId, openProject]);

  return (
    <>
    {confirmDialog}
    <StashNotice onRestore={onRestore} />
    {needsSignIn ? (
      <div className="pg-local-notice" role="status" data-testid="workspace-signin">
        <span>This link opens a workspace in an account. Sign in to file projects in it; until then this draft stays in this browser only.</span>
        <button type="button" className="pg-btn pg-btn-sm" onClick={() => setTab('project')}>
          Sign in
        </button>
      </div>
    ) : null}
    {cloud.status === 'conflict' && session && projectId ? (
      <div className="pg-local-notice" role="alert" data-testid="save-conflict">
        <span>A newer copy of this project was saved in another tab or device. Your edits here are kept in this browser until you choose.</span>
        <button type="button" className="pg-btn pg-btn-sm" data-testid="conflict-open-newer" onClick={() => void openNewer()}>
          Open the newer copy
        </button>
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="conflict-keep-mine" onClick={() => void keepMine()}>
          Keep my version
        </button>
      </div>
    ) : null}
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
      draftSaveFailed={draftWriteFailed}
      signedIn={session != null}
      canUndo={state.past.length > 0}
      canRedo={state.future.length > 0}
      onSaveNew={saveNew}
      onOpenProject={openProject}
      onImport={onImport}
      onDeleted={() => setProjectId(null)}
      onNew={onNew}
      onOpenNewer={openNewer}
      onKeepMine={keepMine}
      onRestoreRevision={restoreRevision}
    />
    </>
  );
}

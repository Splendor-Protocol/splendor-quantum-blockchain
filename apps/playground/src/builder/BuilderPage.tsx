import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { WrongNetworkBanner, useNetwork } from '@splendor/connect-react';
import {
  defaultProps,
  emptySpec,
  getTemplate,
  instantiateTemplate,
  validateSpec,
  type DappSpec,
  type NetworkId,
} from '@splendor/dapp-spec';
import { api, ApiError } from '../api/client';
import { useSession } from '../auth/session';
import { PropsForm } from '../components/PropsForm';
import { Tabs, type TabItem } from '../components/Tabs';
import { ThemeEditor } from '../components/ThemeEditor';
import { PLAYGROUND_NETWORKS } from '../config';
import { StatusBadge } from '../gallery/GalleryPage';
import type { MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import { SpecRenderer } from '../preview/SpecRenderer';
import { validateForPreview } from '../preview/validatePreview';
import { useRegistry } from '../registry';
import { ExportPanel } from './ExportPanel';
import { describeIssue } from './issues';
import { ProjectPanel, type CloudState } from './ProjectPanel';
import { MAX_BLOCKS_PER_PAGE, MAX_PAGES, builderReducer, initBuilder } from './state';
import { clearDraft, loadDraft, saveDraft } from './storage';

type EditorTab = 'block' | 'theme' | 'export' | 'project';
const EDITOR_TABS: readonly TabItem<EditorTab>[] = [
  { id: 'block', label: 'Block', testId: 'tab-block' },
  { id: 'theme', label: 'Theme', testId: 'tab-theme' },
  { id: 'export', label: 'Export', testId: 'tab-export' },
  { id: 'project', label: 'Save & share', testId: 'tab-project' },
];

function TemplatePicker({ onPick }: { onPick: (spec: DappSpec) => void }) {
  const { templates } = useRegistry();
  return (
    <div className="pg-page">
      <section>
        <h1>Start a new dApp</h1>
        <p className="pg-muted">Pick a template — every one is a working scenario you can change — or start blank.</p>
      </section>
      <div className="pg-cards">
        {templates.map((t) => (
          <button key={t.id} type="button" className="pg-card" style={{ textAlign: 'left', font: 'inherit', cursor: 'pointer' }} data-testid={`pick-${t.id}`} onClick={() => onPick(instantiateTemplate(t))}>
            <h3>{t.title}</h3>
            <p>{t.description}</p>
            {t.setup.length ? <p style={{ marginTop: '0.5rem' }}>You will set: {t.setup.map((s) => s.label).join(', ')}</p> : null}
          </button>
        ))}
        <button type="button" className="pg-card" style={{ textAlign: 'left', font: 'inherit', cursor: 'pointer' }} data-testid="pick-blank" onClick={() => onPick(emptySpec())}>
          <h3>Blank</h3>
          <p>One page with a heading. Add blocks from the catalog.</p>
        </button>
      </div>
    </div>
  );
}

export function BuilderPage() {
  const [params, setParams] = useSearchParams();
  /** `synced`: the spec was just loaded from the account, so it matches the saved copy. */
  const [start, setStart] = useState<{ spec: DappSpec; projectId: string | null; key: number; synced?: boolean } | null>(() => {
    const t = params.get('template');
    const tpl = t ? getTemplate(t) : undefined;
    if (tpl) return { spec: instantiateTemplate(tpl), projectId: null, key: Date.now() };
    const draft = loadDraft();
    if (draft) return { spec: draft.spec, projectId: draft.projectId, key: Date.now() };
    return null;
  });

  useEffect(() => {
    if (params.has('template')) {
      params.delete('template');
      setParams(params, { replace: true });
    }
  }, [params, setParams]);

  if (!start) {
    return <TemplatePicker onPick={(spec) => setStart({ spec, projectId: null, key: Date.now() })} />;
  }
  return (
    <Editor
      key={start.key}
      initial={start.spec}
      initialProjectId={start.projectId}
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
  initialSynced,
  onOpen,
  onNew,
}: {
  initial: DappSpec;
  initialProjectId: string | null;
  initialSynced: boolean;
  onOpen: (spec: DappSpec, projectId: string | null) => void;
  onNew: () => void;
}) {
  const { all, catalog, compatibilityNote } = useRegistry();
  const { session } = useSession();
  const [params, setParams] = useSearchParams();
  const [state, dispatch] = useReducer(builderReducer, initial, initBuilder);
  const [projectId, setProjectId] = useState<string | null>(initialProjectId);
  const [mode, setMode] = useState<'mock' | 'live'>('mock');
  const [walletState, setWalletState] = useState<MockWalletState>('connected');
  const [tab, setTab] = useState<EditorTab>('block');
  const [calls, setCalls] = useState<{ method: string; args: string }[]>([]);
  const [cloud, setCloud] = useState<CloudState>({ status: initialSynced ? 'saved' : 'local' });
  const mainnetLiveConfirmed = useRef(false);
  const [newPageTitle, setNewPageTitle] = useState<string | null>(null);
  const { networkId } = useNetwork();
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
  const selected = page.blocks.find((b) => b.id === selectedBlockId) ?? null;
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
  const lastSaved = useRef<DappSpec | null>(initialSynced ? initial : null);
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
    const bi = page.blocks.indexOf(selected);
    const prefix = `pages[${pageIndex}].blocks[${bi}].props.`;
    return Object.fromEntries(issues.filter((i) => i.path.startsWith(prefix)).map((i) => [i.path.slice(prefix.length), i.message]));
  }, [issues, selected, page, pageIndex]);

  const saveNew = useCallback(async () => {
    setCloud({ status: 'saving' });
    try {
      const p = await api.createProject(spec);
      lastSaved.current = spec;
      setProjectId(p.id);
      setCloud({ status: 'saved' });
    } catch (e) {
      setCloud({ status: 'error', message: e instanceof ApiError ? e.message : String(e) });
    }
  }, [spec]);

  const openProject = useCallback(
    async (id: string) => {
      const p = await api.getProject(id);
      onOpen(p.spec, p.id);
    },
    [onOpen],
  );

  const categories = [...new Set(catalog.filter((e) => e.placeable).map((e) => e.category))];

  return (
    <div className="pg-workspace">
      <aside className="pg-sidebar pg-panel">
        <div className="pg-form">
          <div className="pg-field">
            <label htmlFor="dapp-name">dApp name</label>
            <input id="dapp-name" value={spec.name} maxLength={80} onChange={(e) => dispatch({ type: 'setName', name: e.target.value })} />
          </div>
          <div className="pg-field">
            <label htmlFor="dapp-network">Network</label>
            <select
              id="dapp-network"
              value={spec.network}
              onChange={(e) => {
                const network = e.target.value as NetworkId;
                if (network === 'mainnet' && mode === 'live' && !confirmMainnetLive()) return;
                dispatch({ type: 'setNetwork', network });
              }}
            >
              {PLAYGROUND_NETWORKS.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div>
          <p className="pg-list-title">Pages</p>
          <ul className="pg-blocks">
            {spec.pages.map((p, i) => (
              <li key={p.path} className={p.path === page.path ? 'is-active' : ''}>
                <button type="button" aria-current={p.path === page.path ? 'true' : undefined} onClick={() => dispatch({ type: 'selectPage', path: p.path })}>
                  {p.title} <small className="pg-muted">{p.path}</small>
                </button>
                {spec.pages.length > 1 ? (
                  <>
                    <button
                      type="button"
                      className="pg-icon-btn"
                      aria-label={`Move page ${p.title} up`}
                      title="Move up"
                      disabled={i === 0}
                      onClick={() => dispatch({ type: 'movePage', path: p.path, delta: -1 })}
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      className="pg-icon-btn"
                      aria-label={`Move page ${p.title} down`}
                      title="Move down"
                      disabled={i === spec.pages.length - 1}
                      onClick={() => dispatch({ type: 'movePage', path: p.path, delta: 1 })}
                    >
                      ↓
                    </button>
                  </>
                ) : null}
                {p.path !== '/' ? (
                  <button
                    type="button"
                    className="pg-icon-btn"
                    aria-label={`Delete page ${p.title}`}
                    title="Delete page (Ctrl+Z to undo)"
                    onClick={() => dispatch({ type: 'removePage', path: p.path })}
                  >
                    ✕
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
          {newPageTitle == null ? (
            <button
              type="button"
              className="pg-btn pg-btn-ghost pg-btn-sm"
              style={{ marginTop: '0.4rem' }}
              disabled={spec.pages.length >= MAX_PAGES}
              onClick={() => setNewPageTitle('')}
            >
              + Add page
            </button>
          ) : (
            <form
              className="pg-row"
              style={{ marginTop: '0.4rem' }}
              onSubmit={(e) => {
                e.preventDefault();
                if (newPageTitle.trim()) dispatch({ type: 'addPage', title: newPageTitle.trim() });
                setNewPageTitle(null);
              }}
            >
              <input
                aria-label="New page title"
                autoFocus
                maxLength={60}
                placeholder="Page title"
                value={newPageTitle}
                style={{ flex: 1 }}
                onChange={(e) => setNewPageTitle(e.target.value)}
                onKeyDown={(e) => e.key === 'Escape' && setNewPageTitle(null)}
              />
              <button type="submit" className="pg-btn pg-btn-sm" disabled={!newPageTitle.trim()}>
                Add
              </button>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setNewPageTitle(null)}>
                Cancel
              </button>
            </form>
          )}
          <div className="pg-field" style={{ marginTop: '0.5rem' }}>
            <label htmlFor="page-title">Page title</label>
            <input id="page-title" value={page.title} maxLength={60} onChange={(e) => dispatch({ type: 'renamePage', path: page.path, title: e.target.value })} />
          </div>
        </div>

        <div>
          <p className="pg-list-title">Blocks on “{page.title}”</p>
          <ul className="pg-blocks" data-testid="block-list">
            {page.blocks.map((b, i) => {
              const entry = all.find((e) => e.id === b.widget);
              return (
                <li key={b.id} className={b.id === selectedBlockId ? 'is-active' : ''}>
                  <button
                    type="button"
                    aria-current={b.id === selectedBlockId ? 'true' : undefined}
                    onClick={() => {
                      dispatch({ type: 'selectBlock', id: b.id });
                      setTab('block');
                    }}
                  >
                    {entry?.title ?? b.widget}
                  </button>
                  <button
                    type="button"
                    className="pg-icon-btn"
                    aria-label={`Move ${entry?.title ?? b.widget} up`}
                    title="Move up"
                    disabled={i === 0}
                    onClick={() => dispatch({ type: 'moveBlock', id: b.id, delta: -1 })}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className="pg-icon-btn"
                    aria-label={`Move ${entry?.title ?? b.widget} down`}
                    title="Move down"
                    disabled={i === page.blocks.length - 1}
                    onClick={() => dispatch({ type: 'moveBlock', id: b.id, delta: 1 })}
                  >
                    ↓
                  </button>
                  <button
                    type="button"
                    className="pg-icon-btn"
                    aria-label={`Duplicate ${entry?.title ?? b.widget}`}
                    title="Duplicate"
                    disabled={page.blocks.length >= MAX_BLOCKS_PER_PAGE}
                    onClick={() => {
                      dispatch({ type: 'duplicateBlock', id: b.id });
                      setTab('block');
                    }}
                  >
                    ⧉
                  </button>
                  <button
                    type="button"
                    className="pg-icon-btn"
                    aria-label={`Remove ${entry?.title ?? b.widget}`}
                    title="Remove (Ctrl+Z to undo)"
                    onClick={() => dispatch({ type: 'removeBlock', id: b.id })}
                  >
                    ✕
                  </button>
                </li>
              );
            })}
          </ul>
        </div>

        <div>
          <p className="pg-list-title">Add a block</p>
          {categories.map((c) => (
            <ul key={c} className="pg-list">
              <li className="pg-muted" style={{ fontSize: '0.8rem', padding: '0.3rem 0.6rem 0' }}>
                {c}
              </li>
              {catalog
                .filter((e) => e.placeable && e.category === c)
                .map((e) => (
                  <li key={e.id}>
                    <button
                      type="button"
                      title={e.description}
                      data-testid={`add-${e.id}`}
                      disabled={page.blocks.length >= MAX_BLOCKS_PER_PAGE}
                      onClick={() => {
                        dispatch({ type: 'addBlock', widget: e.id, props: defaultProps(e) });
                        setTab('block');
                      }}
                    >
                      + {e.title} <StatusBadge status={e.status} />
                    </button>
                  </li>
                ))}
            </ul>
          ))}
        </div>
      </aside>

      <section className="pg-stack">
        <div className="pg-preview-toolbar">
          <div className="pg-segmented" role="group" aria-label="Preview mode">
            <button type="button" aria-pressed={mode === 'mock'} onClick={() => setMode('mock')}>
              Mock wallet
            </button>
            <button
              type="button"
              aria-pressed={mode === 'live'}
              onClick={() => {
                if (spec.network === 'mainnet' && !confirmMainnetLive()) return;
                setMode('live');
              }}
            >
              Live
            </button>
          </div>
          {mode === 'mock' ? (
            <div className="pg-segmented" role="group" aria-label="Mock wallet state">
              {(['connected', 'disconnected'] as const).map((s) => (
                <button key={s} type="button" aria-pressed={walletState === s} onClick={() => setWalletState(s)}>
                  {s === 'connected' ? 'Connected' : 'No wallet'}
                </button>
              ))}
            </div>
          ) : null}
          <span className="pg-spacer" />
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!state.past.length} onClick={() => dispatch({ type: 'undo' })} aria-label="Undo" title="Undo (Ctrl+Z)">
            ↶ Undo
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!state.future.length} onClick={() => dispatch({ type: 'redo' })} aria-label="Redo" title="Redo (Ctrl+Y or Ctrl+Shift+Z)">
            ↷ Redo
          </button>
        </div>

        {issues.length ? (
          <div className="pg-issues" data-testid="spec-issues">
            <strong>{issues.length === 1 ? '1 thing to fix' : `${issues.length} things to fix`}</strong>
            <ul>
              {issues.slice(0, 10).map((i) => {
                const d = describeIssue(spec, all, i);
                return (
                  <li key={`${i.path}:${i.message}`}>
                    {d.target ? (
                      <button type="button" className="pg-link-btn" onClick={() => goToIssue(d.target!)} title={i.path}>
                        {d.where}
                      </button>
                    ) : (
                      <strong title={i.path}>{d.where}</strong>
                    )}{' '}
                    {d.message}
                  </li>
                );
              })}
            </ul>
            {issues.length > 10 ? <p className="pg-muted" style={{ margin: '0.4rem 0 0' }}>…and {issues.length - 10} more. Fix these first.</p> : null}
          </div>
        ) : null}

        {mode === 'mock' ? (
          <>
            <PreviewFrame
              spec={spec}
              pagePath={page.path}
              network={spec.network}
              walletState={walletState}
              selectedBlockId={selectedBlockId}
              onNavigate={(path) => dispatch({ type: 'selectPage', path })}
              onSelectBlock={(id) => {
                dispatch({ type: 'selectBlock', id });
                setTab('block');
              }}
              onCalls={setCalls}
              height="calc(100vh - 14rem)"
            />
            <details>
              <summary className="pg-muted">Mock wallet activity ({calls.length})</summary>
              <div className="pg-output" data-testid="mock-calls">
                {calls.length ? calls.map((c) => `${c.method} ${c.args}`).join('\n') : 'No wallet calls yet. Try sending a transfer in the preview.'}
              </div>
            </details>
            <p className="pg-muted" style={{ margin: 0 }}>
              Alt+click a block in the preview to select it. Nothing is signed or sent in mock mode.
            </p>
          </>
        ) : (
          <div className="pg-stack">
            <p className="pg-muted" style={{ margin: 0 }}>
              Live mode uses the wallet connected at the top right on the selected network. Transactions are real
              {spec.network === 'mainnet' ? ' and spend real SPLD' : ''}.
            </p>
            <WrongNetworkBanner expectedNetworkId={spec.network} />
            {networkId !== spec.network ? (
              // Widgets sign on the playground's network, so they stay off until it matches the dApp's.
              <p className="pg-error" data-testid="live-network-mismatch">
                This dApp is set to {spec.network}. Switch the playground to it (above) to run the live preview.
              </p>
            ) : renderable ? (
              <SpecRenderer spec={renderable} catalog={all} pagePath={page.path} onNavigate={(path) => dispatch({ type: 'selectPage', path })} />
            ) : (
              <p className="pg-error">Fix the issues above to run the live preview.</p>
            )}
          </div>
        )}
      </section>

      <aside className="pg-sidebar pg-panel">
        <Tabs idPrefix="builder" label="Editor" tabs={EDITOR_TABS} value={tab} onChange={setTab}>
        {tab === 'block' ? (
          selected && selectedEntry ? (
            <div className="pg-stack">
              <div className="pg-row">
                <h3 style={{ margin: 0 }}>{selectedEntry.title}</h3>
                <StatusBadge status={selectedEntry.status} />
              </div>
              <p className="pg-muted" style={{ margin: 0 }}>
                {selectedEntry.description}
              </p>
              {compatibilityNote(selectedEntry) ? <p className="pg-error">{compatibilityNote(selectedEntry)}</p> : null}
              <PropsForm
                entry={selectedEntry}
                values={selected.props}
                errors={blockErrors}
                onChange={(name, value) => dispatch({ type: 'setProp', id: selected.id, name, value })}
              />
            </div>
          ) : (
            <p className="pg-muted">Select a block on the left (or Alt+click it in the preview) to edit its settings.</p>
          )
        ) : null}
        {tab === 'theme' ? <ThemeEditor value={spec.theme} onChange={(theme) => dispatch({ type: 'setTheme', theme })} /> : null}
        {tab === 'export' ? <ExportPanel spec={spec} catalog={all} valid={validation.ok} /> : null}
        {tab === 'project' ? (
          <ProjectPanel
            spec={spec}
            catalog={all}
            projectId={projectId}
            cloud={session && projectId ? cloud : { status: 'local' }}
            valid={validation.ok}
            onSaveNew={saveNew}
            onOpen={openProject}
            onImport={(imported) => onOpen(imported, null)}
            onDeleted={() => setProjectId(null)}
            onNew={onNew}
          />
        ) : null}
        </Tabs>
      </aside>
    </div>
  );
}

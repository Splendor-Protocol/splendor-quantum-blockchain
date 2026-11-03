import { useCallback, useEffect, useMemo, useRef, useState, type Dispatch, type DragEvent, type ReactNode, type RefObject } from 'react';
import { Link } from 'react-router-dom';
import { ConnectKit, WrongNetworkBanner, getContacts, useAccount, useNetwork, useNetworkSwitch } from '@splendor/connect-react';
import { MOCK_ADDRESS } from '@splendor/connect-react/testing';
import type { AddressSuggestion } from '../components/PropsForm';
import {
  blocksFromTree,
  defaultProps,
  findNode,
  type CatalogEntry,
  type DappSpec,
  type NetworkId,
  type NodeStyle,
  type PageAccess,
  type PropValue,
} from '@splendor/dapp-spec';
import { ModalDialog } from '../components/ModalDialog';
import { Tabs, type TabItem } from '../components/Tabs';
import { useStableCallback } from '../components/useStableCallback';
import { PLAYGROUND_NETWORKS } from '../config';
import type { MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import { SpecRenderer } from '../preview/SpecRenderer';
import { PreviewApp } from '../studio/PreviewApp';
import {
  CanvasOverlay,
  CONTRACT_FN_MIME,
  ContractsPanel,
  createCanvasStore,
  DeviceSwitch,
  FlowsPanel,
  Inspector,
  isInNav,
  LayersPanel,
  Onboarding,
  PagesPanel,
  Palette,
  StudioShell,
  StudioTopBar,
  ThemePanel,
  NARROW_STUDIO,
  useMediaQuery,
  type DeviceSize,
  type CanvasDrop,
  type InspectorTab,
  type StudioDrawer,
  type StudioRail,
} from '../studio';
import { parseContractDrop } from '../studio/abi';
import { CommandPalette, ShortcutSheet, modKey, type Command } from '../studio/CommandPalette';
import { ExportPanel } from './ExportPanel';
import { describeIssue, setupItems, type IssueTarget } from './issues';
import { ProjectPanel, cloudLabel, type CloudState } from './ProjectPanel';
import { MAX_BLOCKS_PER_PAGE, insertBlocker, insertionPoint, type BuilderAction } from './state';

type EditorTab = 'block' | 'theme' | 'export' | 'project';

const OVERLAY_TABS: readonly TabItem<EditorTab>[] = [
  { id: 'export', label: 'Export' },
  { id: 'project', label: 'Save & share' },
];

type DragPayload = { kind: 'widget' | 'block'; id: string };

/** Width the Desktop canvas renders at when the studio has less room (then scaled to fit). */
const DESKTOP_CANVAS_WIDTH = 1024;
const CANVAS_HEIGHT = 'max(420px, calc(100dvh - 320px))';

/** "More" in the narrow top bar: a small popover that closes on Escape or a click outside. */
function MoreMenu({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        setOpen(false);
        root.current?.querySelector<HTMLButtonElement>('.pg-more-toggle')?.focus();
      }
    };
    document.addEventListener('pointerdown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);
  return (
    <div className="pg-more" ref={root}>
      <button
        type="button"
        className="pg-btn pg-btn-ghost pg-btn-sm pg-more-toggle"
        aria-expanded={open}
        aria-controls="pg-more-panel"
        data-testid="topbar-more"
        onClick={() => setOpen((v) => !v)}
      >
        More
      </button>
      <div id="pg-more-panel" className="pg-more-panel" hidden={!open}>
        {children}
      </div>
    </div>
  );
}

const NO_CONTRACTS: NonNullable<DappSpec['contracts']> = [];

function pageBlocks(page: DappSpec['pages'][number]) {
  return page.blocks ?? blocksFromTree(page.root);
}

export function Studio({
  spec,
  pagePath,
  selectedBlockId,
  dispatch,
  catalog,
  all,
  compatibilityNote,
  mode,
  setMode,
  walletState,
  setWalletState,
  confirmMainnetLive,
  tab,
  setTab,
  calls,
  setCalls,
  issues,
  blockErrors,
  goToIssue: goToIssueField,
  newPageTitle,
  setNewPageTitle,
  blockQuery,
  setBlockQuery,
  setDropIndex,
  dragRef,
  skipClick,
  validationOk,
  renderable,
  projectId,
  cloud,
  draftSaveFailed = false,
  signedIn,
  canUndo,
  canRedo,
  onSaveNew,
  onOpenProject,
  onImport,
  onDeleted,
  onNew,
  onOpenNewer,
  onKeepMine,
  onRestoreRevision,
}: {
  spec: DappSpec;
  pagePath: string;
  selectedBlockId: string | null;
  dispatch: Dispatch<BuilderAction>;
  catalog: CatalogEntry[];
  all: CatalogEntry[];
  compatibilityNote: (entry: CatalogEntry) => string | null;
  mode: 'mock' | 'live';
  setMode: (mode: 'mock' | 'live') => void;
  walletState: MockWalletState;
  setWalletState: (state: MockWalletState) => void;
  confirmMainnetLive: () => Promise<boolean>;
  tab: EditorTab;
  setTab: (tab: EditorTab) => void;
  calls: { method: string; args: string }[];
  setCalls: (calls: { method: string; args: string }[]) => void;
  issues: { path: string; message: string }[];
  blockErrors: Record<string, string>;
  goToIssue: (target: IssueTarget) => void;
  newPageTitle: string | null;
  setNewPageTitle: (title: string | null) => void;
  blockQuery: string;
  setBlockQuery: (query: string) => void;
  dropIndex: number | null;
  setDropIndex: (index: number | null) => void;
  dragRef: RefObject<DragPayload | null>;
  skipClick: RefObject<boolean>;
  validationOk: boolean;
  renderable: DappSpec | null;
  projectId: string | null;
  cloud: CloudState;
  /** The browser refused the last draft write. */
  draftSaveFailed?: boolean;
  signedIn: boolean;
  canUndo: boolean;
  canRedo: boolean;
  onSaveNew: () => Promise<void>;
  onOpenProject: (id: string) => Promise<void>;
  onImport: (spec: DappSpec) => void;
  onDeleted: () => void;
  onNew: () => void;
  onOpenNewer?: () => Promise<void>;
  onKeepMine?: () => Promise<void>;
  onRestoreRevision?: (version: number) => Promise<void>;
}) {
  const { networkId } = useNetwork();
  const { switchNetwork, switching } = useNetworkSwitch();
  const [rail, setRail] = useState<StudioRail>('components');
  /** Narrow screens: which panel is open over the canvas. */
  const [drawer, setDrawer] = useState<StudioDrawer>(null);
  const [device, setDevice] = useState<DeviceSize>('desktop');
  const narrow = useMediaQuery(NARROW_STUDIO);
  // A laptop's canvas is narrower than the app's 720px breakpoint once the panels are open, which
  // would show "Desktop" with the phone layout. Render it at a desktop width and zoom it to fit.
  const canvasFrame = useRef<HTMLDivElement>(null);
  const [canvasWidth, setCanvasWidth] = useState(0);
  useEffect(() => {
    const el = canvasFrame.current;
    if (!el || typeof ResizeObserver !== 'function') return;
    const ro = new ResizeObserver(([entry]) => setCanvasWidth(Math.floor(entry?.contentRect.width ?? 0)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const fit = device === 'desktop' && !narrow && canvasWidth >= 480 && canvasWidth < DESKTOP_CANVAS_WIDTH ? canvasWidth / DESKTOP_CANVAS_WIDTH : 1;
  /** Page the full-screen Preview app starts on, or null when it is closed. */
  const [playFrom, setPlayFrom] = useState<string | null>(null);
  const page = spec.pages.find((p) => p.path === pagePath) ?? spec.pages[0]!;
  const blocks = pageBlocks(page);
  // The selection can be a leaf block or a container (picked on the canvas or in the breadcrumb).
  const selectedNode = selectedBlockId ? findNode(page.root, selectedBlockId) : null;
  const selected = selectedNode?.kind === 'block' ? selectedNode.block : null;
  const selectedContainer = selectedNode && selectedNode.kind !== 'block' ? selectedNode : null;
  const selectedEntry = selected ? all.find((e) => e.id === selected.widget) : undefined;
  const selectedIndex = selected ? blocks.indexOf(selected) : -1;
  const full = blocks.length >= MAX_BLOCKS_PER_PAGE;
  const overlayOpen = tab === 'export' || tab === 'project';
  // Before the first save only a failed or running "Save to my account" is about the account.
  const accountCloud: CloudState =
    signedIn && (projectId || cloud.status === 'error' || cloud.status === 'saving') ? cloud : { status: 'local' };
  // An account copy that is in sync is enough; otherwise a refused draft write means nothing is kept.
  const shownCloud: CloudState = draftSaveFailed && accountCloud.status !== 'saved' && accountCloud.status !== 'saving' ? { status: 'storage' } : accountCloud;
  const [inspectorTab, setInspectorTab] = useState<InspectorTab>('props');
  /** The field to focus lives on the Content tab (in the inspector drawer on a narrow screen). */
  const goToIssue: typeof goToIssueField = (target) => {
    setInspectorTab(target.style ? 'style' : 'props');
    setDrawer('inspector');
    goToIssueField(target);
  };
  /** Pieces in this dApp whose manifest lists networks that do not include the chosen one. */
  const unsupported = useMemo(() => {
    const names = new Set<string>();
    for (const p of spec.pages) {
      for (const b of p.blocks ?? blocksFromTree(p.root)) {
        const entry = all.find((e) => e.id === b.widget);
        const nets = entry?.requires?.networks;
        if (entry && nets?.length && !nets.includes(spec.network)) names.add(entry.title);
      }
    }
    return [...names];
  }, [spec, all]);
  /** Show every issue, not just the first ten. */
  const [allIssues, setAllIssues] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  /** Bumped by "Replay the studio tour". */
  const [tourKey, setTourKey] = useState(0);
  // Ctrl/Cmd+K opens the command palette; `?` lists the shortcuts. Both also arrive from the canvas.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      const typing = !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName ?? '') || t.isContentEditable);
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((v) => !v);
        return;
      }
      if (e.key === '?' && !typing && !e.ctrlKey && !e.metaKey && !document.querySelector('[aria-modal="true"]:not([data-drawer])')) {
        e.preventDefault();
        setShortcutsOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  /** Shows a rail panel; on a narrow screen that means opening its drawer. */
  const openRail = useStableCallback((next: StudioRail) => {
    setRail(next);
    setDrawer('side');
  });

  // "Finish setup": unbound contracts and tokens first, then what the validator reports.
  const setup = useMemo(() => setupItems(spec, all), [spec, all]);
  const shownIssues = useMemo(() => {
    const unbound = new Set(setup.flatMap((s) => (s.kind === 'component' ? [s.target.blockId] : [])));
    return issues
      .map((issue) => ({ issue, d: describeIssue(spec, all, issue) }))
      // A component that still needs a contract is already listed above: skip its "needs a value" twin.
      .filter(({ issue, d }) => !(issue.message === 'required' && d.target && unbound.has(d.target.blockId)));
  }, [issues, setup, spec, all]);
  /** What the list shows: issues already covered by a setup item are not counted twice. */
  const setupLeft = setup.length + shownIssues.length;
  /** From the Export dialog: close it and go where the first setup item is fixed. */
  const fixFirstSetupItem = () => {
    setTab('block');
    const first = setup[0];
    if (first?.kind === 'contracts') openRail('contracts');
    else if (first) goToIssue(first.target);
    else {
      const target = shownIssues.find(({ d }) => d.target)?.d.target;
      if (target) goToIssue(target);
    }
  };

  // In-canvas editing: the preview frame reports where nodes and insertion points are, and what is
  // under the pointer. Only the canvas overlay reads that, so it does not re-render the studio.
  const canvasStore = useMemo(createCanvasStore, []);
  useEffect(() => {
    canvasStore.setHover(null);
  }, [canvasStore, mode, pagePath]);

  const labelFor = (id: string, kind: string): string => {
    const node = findNode(page.root, id);
    if (node?.kind === 'block') {
      const widget = node.block.widget;
      return all.find((e) => e.id === widget)?.title ?? widget;
    }
    return kind === 'block' ? 'Component' : kind.charAt(0).toUpperCase() + kind.slice(1);
  };

  /** Only drags the studio starts on purpose (palette, layers, contract function, node handle). */
  const acceptsDrag = useCallback(
    (dt: DataTransfer | null) => dragRef.current !== null || Boolean(dt?.types.includes(CONTRACT_FN_MIME)),
    [dragRef],
  );

  const dropIntoTree = useStableCallback(({ parentId, index, moveId, contractFn }: { parentId: string; index: number; moveId?: string | null; contractFn?: string }) => {
    const drag = dragRef.current;
    dragRef.current = null;
    setDropIndex(null);
    if (moveId) {
      dispatch({ type: 'moveNode', id: moveId, parentId, index });
      return;
    }
    if (drag?.kind === 'block') {
      dispatch({ type: 'moveNode', id: drag.id, parentId, index });
      return;
    }
    if (full) return;
    if (drag?.kind === 'widget') {
      const entry = catalog.find((e) => e.id === drag.id && e.placeable);
      if (!entry) return;
      dispatch({ type: 'insertIntoTree', parentId, index, widget: entry.id, props: defaultProps(entry) });
      setTab('block');
      return;
    }
    if (!contractFn) return;
    const drop = parseContractDrop(contractFn, spec.contracts ?? []);
    if (!drop) return;
    const { contract, address, fn } = drop;
    const widget = fn.kind === 'write' ? 'contract-write' : 'contract-read';
    const entry = all.find((e) => e.id === widget);
    // Components that have a contract picker follow the contract; older ones get its address.
    const picker = Boolean(contract && entry?.props.some((p) => p.name === 'contractId' && p.format === 'contract'));
    const props: Record<string, PropValue> = {
      ...(entry ? defaultProps(entry) : {}),
      ...(picker && contract ? { contractId: contract.id } : { contract: address }),
      functionSig: fn.signature,
      title: `${contract?.name ?? 'Contract'}: ${fn.name}`,
      ...(fn.kind === 'write' ? { buttonLabel: fn.name } : {}),
    };
    dispatch({ type: 'insertIntoTree', parentId, index, widget, props });
    setTab('block');
  });

  const onOverlayDrop = (d: CanvasDrop) => dropIntoTree(d);

  const startDrag = useStableCallback((event: DragEvent, payload: DragPayload) => {
    skipClick.current = true;
    dragRef.current = payload;
    event.dataTransfer.setData('text/plain', payload.id);
    event.dataTransfer.effectAllowed = payload.kind === 'widget' ? 'copy' : 'move';
  });

  const endDrag = useStableCallback(() => {
    dragRef.current = null;
    setDropIndex(null);
    window.setTimeout(() => {
      skipClick.current = false;
    }, 50);
  });

  // Stable handlers for the memoized palette, layers and inspector.
  const selectNode = useStableCallback((id: string) => {
    dispatch({ type: 'selectBlock', id });
    setTab('block');
  });
  const duplicateNode = useStableCallback((id: string) => dispatch({ type: 'duplicateNode', id }));
  const removeNode = useStableCallback((id: string) => dispatch({ type: 'removeNodeFromTree', id }));
  const renameNode = useStableCallback((id: string, label: string) => dispatch({ type: 'renameNode', id, label }));
  const moveNode = useStableCallback((move: { id: string; parentId: string; index: number }) => dispatch({ type: 'moveNode', ...move }));
  // "Fill with…" on address fields: the wallet this preview uses, saved contacts, the spec's contracts.
  const { address: liveAddress } = useAccount();
  const addressSuggestions = useMemo<AddressSuggestion[]>(() => {
    const out: AddressSuggestion[] = [];
    if (mode === 'mock') out.push({ label: 'Test wallet (preview)', address: MOCK_ADDRESS });
    else if (liveAddress) out.push({ label: 'My wallet', address: liveAddress });
    for (const c of getContacts(spec.network)) out.push({ label: `Contact: ${c.label}`, address: c.address });
    for (const c of spec.contracts ?? []) if (/^SQ:[0-9a-fA-F]{64}$/.test(c.address)) out.push({ label: `Contract: ${c.name}`, address: c.address });
    return out.filter((s, i) => out.findIndex((o) => o.address === s.address) === i);
  }, [mode, liveAddress, spec.network, spec.contracts]);

  /** Why the last add did not happen (page full, nested too deep); cleared on the next success. */
  const [addNotice, setAddNotice] = useState<string | null>(null);
  const addFromPalette = useStableCallback((widget: string, props: Record<string, PropValue>) => {
    // A container selected: the piece goes inside it. A component selected: right after it, in the
    // same box. Nothing selected: at the end of the page.
    const at = insertionPoint(page.root, selectedBlockId);
    const blocker = at ? insertBlocker(page, at.parentId) : null;
    if (blocker) {
      setAddNotice(blocker);
      return;
    }
    setAddNotice(null);
    if (at) dispatch({ type: 'insertIntoTree', parentId: at.parentId, index: at.index, widget, props });
    else dispatch({ type: 'addBlock', widget, props });
    setTab('block');
    // On a narrow screen, show the page the piece landed on.
    setDrawer(null);
  });
  const commands = useMemo<Command[]>(() => {
    const out: Command[] = [
      { id: 'export', label: 'Export or deploy…', group: 'Project', keywords: 'download zip stackblitz vercel netlify github', run: () => setTab('export') },
      { id: 'share', label: 'Save & share…', group: 'Project', keywords: 'link account', run: () => setTab('project') },
      { id: 'preview', label: 'Preview the app', group: 'Project', keywords: 'play run', run: () => setPlayFrom(page.path) },
      { id: 'undo', label: 'Undo', group: 'Edit', run: () => dispatch({ type: 'undo' }) },
      { id: 'redo', label: 'Redo', group: 'Edit', run: () => dispatch({ type: 'redo' }) },
      { id: 'shortcuts', label: 'Keyboard shortcuts', group: 'Help', keywords: 'keys hotkeys', run: () => setShortcutsOpen(true) },
      { id: 'tour', label: 'Replay the studio tour', group: 'Help', keywords: 'onboarding help guide', run: () => setTourKey((k) => k + 1) },
      { id: 'new-page', label: 'Add a page', group: 'Page', run: () => {
        openRail('pages');
        setNewPageTitle('');
      } },
    ];
    for (const [rail, label] of [
      ['components', 'Components'],
      ['pages', 'Pages'],
      ['theme', 'Theme'],
      ['contracts', 'Contracts'],
      ['flows', 'Flows'],
    ] as const) {
      out.push({ id: `panel-${rail}`, label: `Open ${label}`, group: 'Panel', run: () => openRail(rail) });
    }
    for (const p of spec.pages) {
      out.push({ id: `page-${p.path}`, label: `Go to ${p.title} (${p.path})`, group: 'Page', run: () => dispatch({ type: 'selectPage', path: p.path }) });
    }
    for (const e of catalog) {
      if (!e.placeable || e.hidden) continue;
      out.push({
        id: `add-${e.id}`,
        label: `Add ${e.title}`,
        group: 'Add',
        keywords: `component ${e.category} ${e.id} ${e.importName}`,
        run: () => addFromPalette(e.id, defaultProps(e)),
      });
    }
    return out;
  }, [spec.pages, catalog, page.path, dispatch, openRail, setNewPageTitle, setTab, addFromPalette]);

  /** Network, preview mode, test wallet and device: inline on wide screens, in "More" on narrow ones. */
  const previewControls = (
    <>
      <label className="pg-studio-net" htmlFor="dapp-network">
        Network
      </label>
      <select
        id="dapp-network"
        aria-label="dApp network"
        value={spec.network}
        onChange={(e) => {
          const network = e.target.value as NetworkId;
          void (async () => {
            if (network === 'mainnet' && mode === 'live' && !(await confirmMainnetLive())) return;
            dispatch({ type: 'setNetwork', network });
          })();
        }}
      >
        {PLAYGROUND_NETWORKS.map((n) => (
          <option key={n.id} value={n.id}>
            {n.label}
          </option>
        ))}
      </select>
      <div className="pg-segmented" role="group" aria-label="Preview mode">
        <button type="button" aria-pressed={mode === 'mock'} onClick={() => setMode('mock')}>
          Test wallet
        </button>
        <button
          type="button"
          aria-pressed={mode === 'live'}
          onClick={() => {
            void (async () => {
              if (spec.network === 'mainnet' && !(await confirmMainnetLive())) return;
              setMode('live');
            })();
          }}
        >
          Real wallet
        </button>
      </div>
      {mode === 'mock' ? (
        <div className="pg-segmented" role="group" aria-label="Test wallet state">
          {(['connected', 'disconnected'] as const).map((s) => (
            <button key={s} type="button" aria-pressed={walletState === s} onClick={() => setWalletState(s)}>
              {s === 'connected' ? 'Connected' : 'No wallet'}
            </button>
          ))}
        </div>
      ) : null}
      <DeviceSwitch value={device} onChange={setDevice} />
      {narrow ? (
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="tab-project" onClick={() => setTab('project')}>
          Save &amp; share
        </button>
      ) : null}
    </>
  );

  const inspectorHandlers = {
    onPropChange: useStableCallback((name: string, value: PropValue | undefined) => {
      if (selected) dispatch({ type: 'setProp', id: selected.id, name, value });
    }),
    onStyleChange: useStableCallback((style: NodeStyle) => {
      if (selectedNode && selectedBlockId) dispatch({ type: 'setBlockStyle', id: selectedBlockId, style });
    }),
    onMoveBlock: useStableCallback((delta: -1 | 1) => {
      if (selected) dispatch({ type: 'moveBlock', id: selected.id, delta });
    }),
    onRenamePage: useStableCallback((title: string) => dispatch({ type: 'renamePage', path: page.path, title })),
    onOpenContracts: useStableCallback(() => openRail('contracts')),
    onPageAccess: useStableCallback((access: PageAccess) => dispatch({ type: 'setPageAccess', path: page.path, access })),
    onNavVisible: useStableCallback((visible: boolean) => dispatch({ type: 'setNavVisible', path: page.path, visible })),
    onMovePage: useStableCallback((delta: -1 | 1) => dispatch({ type: 'movePage', path: page.path, delta })),
  };

  const sidebar =
    rail === 'pages' ? (
      <PagesPanel spec={spec} pagePath={page.path} dispatch={dispatch} newPageTitle={newPageTitle} setNewPageTitle={setNewPageTitle} />
    ) : rail === 'components' ? (
      <Palette
        catalog={catalog}
        query={blockQuery}
        onQueryChange={setBlockQuery}
        full={full}
        insertTarget={
          selectedContainer
            ? `inside the selected ${labelFor(selectedContainer.id, selectedContainer.kind).toLowerCase()}`
            : selected
              ? `after the selected ${labelFor(selected.id, 'block')}`
              : null
        }
        onAdd={addFromPalette}
        notice={addNotice}
        startDrag={startDrag}
        endDrag={endDrag}
        skipClick={skipClick}
      />
    ) : rail === 'theme' ? (
      <ThemePanel spec={spec} dispatch={dispatch} />
    ) : rail === 'contracts' ? (
      <ContractsPanel
        spec={spec}
        dispatch={dispatch}
        mode={mode}
        onInsertContractBlock={(widget, props) => {
          dispatch({ type: 'addBlock', widget, props: props as Record<string, PropValue> });
          setTab('block');
          setRail('components');
          setDrawer(null);
        }}
      />
    ) : (
      <FlowsPanel
        spec={spec}
        onPlay={(path) => setPlayFrom(path)}
        onSelectPage={(path) => dispatch({ type: 'selectPage', path })}
        onSelectBlock={(pagePath, blockId) => {
          dispatch({ type: 'selectPage', path: pagePath });
          dispatch({ type: 'selectBlock', id: blockId });
          setTab('block');
        }}
      />
    );

  const canvas = (
    <>
      <div className="pg-page-row">
        <div className="pg-chips" aria-label="Pages">
          {spec.pages.map((p) => (
            <span key={p.path} className="pg-chip-wrap">
              <button
                type="button"
                className="pg-chip"
                aria-current={p.path === page.path ? 'true' : undefined}
                aria-label={`${p.title} ${p.path}`}
                onClick={() => dispatch({ type: 'selectPage', path: p.path })}
              >
                {p.title}
              </button>
            </span>
          ))}
        </div>
      </div>

      {setupLeft ? (
        <div className="pg-setup" data-testid="spec-issues" role="region" aria-label="Finish setup">
          <div className="pg-setup-head">
            <strong>Finish setup</strong>
            <span className="pg-muted" data-testid="setup-count">{setupLeft === 1 ? '1 thing left' : `${setupLeft} things left`}</span>
          </div>
          <ul>
            {setup.map((item) => (
              <li key={item.key}>
                {item.message}{' '}
                {item.kind === 'contracts' ? (
                  <button type="button" className="pg-link-btn" onClick={() => openRail('contracts')}>
                    Open Contracts
                  </button>
                ) : (
                  <button type="button" className="pg-link-btn" onClick={() => goToIssue(item.target)}>
                    Choose
                  </button>
                )}
              </li>
            ))}
            {(allIssues ? shownIssues : shownIssues.slice(0, 10)).map(({ issue: i, d }) => (
              <li key={`${i.path}:${i.message}`}>
                {d.target ? (
                  <button type="button" className="pg-link-btn" onClick={() => goToIssue(d.target!)} title={i.path}>
                    {d.where}
                  </button>
                ) : d.panel ? (
                  <button type="button" className="pg-link-btn" onClick={() => openRail(d.panel!)} title={i.path}>
                    {d.where}
                  </button>
                ) : (
                  <strong title={i.path}>{d.where}</strong>
                )}{' '}
                {d.message}
              </li>
            ))}
          </ul>
          {shownIssues.length > 10 ? (
            <button type="button" className="pg-link-btn" data-testid="issues-toggle" onClick={() => setAllIssues((v) => !v)}>
              {allIssues ? 'Show fewer' : `Show all ${shownIssues.length} issues`}
            </button>
          ) : null}
        </div>
      ) : null}

      <div ref={canvasFrame} className={`pg-stage-canvas pg-device-frame pg-device-${device}`}>
        {mode === 'mock' && fit < 1 ? (
          <span className="pg-canvas-zoom" data-testid="canvas-zoom" title={`Desktop is shown ${DESKTOP_CANVAS_WIDTH}px wide, scaled to fit`}>
            {Math.round(fit * 100)}%
          </span>
        ) : null}
        {mode === 'mock' ? (
          // A transform (not CSS zoom): hit-testing inside a zoomed iframe is unreliable.
          <div className="pg-canvas-fit" style={fit < 1 ? { height: CANVAS_HEIGHT } : undefined}>
          <div
            className="pg-canvas-stack"
            style={fit < 1 ? { width: DESKTOP_CANVAS_WIDTH, transform: `scale(${fit})`, transformOrigin: '0 0' } : undefined}
          >
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
                // On a phone or tablet the inspector is a drawer: a tap on the page opens it.
                if (narrow) setDrawer('inspector');
              }}
              onCalls={setCalls}
              onRects={canvasStore.setRects}
              onHover={canvasStore.setHover}
              onDrop={dropIntoTree}
              height={fit < 1 ? `calc(${CANVAS_HEIGHT} / ${fit.toFixed(4)})` : CANVAS_HEIGHT}
            />
            <CanvasOverlay
              store={canvasStore}
              scale={fit}
              selectedId={selectedBlockId}
              labelFor={labelFor}
              acceptsDrag={acceptsDrag}
              full={full}
              onSelect={selectNode}
              onDuplicate={duplicateNode}
              onRemove={removeNode}
              onDrop={onOverlayDrop}
            />
          </div>
          </div>
        ) : (
          <div className="pg-pad pg-stack">
            <p className="pg-muted" style={{ margin: 0 }}>
              Real wallet mode uses the wallet in the header. Transactions are real
              {spec.network === 'mainnet' ? ' and spend real SPLD' : ''}.
            </p>
            {unsupported.length ? (
              <p className="pg-local-notice" role="status" data-testid="live-unsupported">
                Not available on {spec.network}: {unsupported.join(', ')}. These pieces may show errors with a real wallet.
              </p>
            ) : null}
            {networkId !== spec.network ? (
              // One message: the playground's connection, not the dApp, is on the other network.
              <p className="pg-error" data-testid="live-network-mismatch">
                This dApp runs on {spec.network}, but the playground is connected to {networkId}.{' '}
                <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={switching} onClick={() => void switchNetwork(spec.network)}>
                  Switch to {spec.network}
                </button>
              </p>
            ) : renderable ? (
              <>
                {/* The wallet itself can still sit on another chain (extension): the SDK banner says so. */}
                <WrongNetworkBanner expectedNetworkId={spec.network} />
                <SpecRenderer
                  spec={renderable}
                  catalog={all}
                  pagePath={page.path}
                  onNavigate={(path) => dispatch({ type: 'selectPage', path })}
                  externalLinksInNewTab
                  headerExtra={<ConnectKit showNetwork={false} />}
                />
              </>
            ) : (
              <p className="pg-error">Finish the setup above to run the real wallet preview.</p>
            )}
          </div>
        )}
      </div>

      {mode === 'mock' ? (
        <details className="pg-activity">
          <summary>Test wallet activity ({calls.length})</summary>
          <div className="pg-output" data-testid="mock-calls">
            {calls.length ? calls.map((c) => `${c.method} ${c.args}`).join('\n') : 'No wallet calls yet. Try a button in the page above.'}
          </div>
        </details>
      ) : null}
    </>
  );

  return (
    <StudioShell
      rail={rail}
      onRailChange={setRail}
      drawer={drawer}
      onDrawerChange={setDrawer}
      topBar={
        <StudioTopBar>
          <Link to="/" className="pg-btn pg-btn-ghost pg-btn-sm pg-back-link" aria-label="Back to my projects" title="Back to my projects">
            ← <span className="pg-hide-narrow">Projects</span>
          </Link>
          <input
            id="dapp-name"
            className="pg-studio-name"
            aria-label="dApp name"
            value={spec.name}
            maxLength={80}
            onChange={(e) => dispatch({ type: 'setName', name: e.target.value })}
          />
          {narrow ? null : <span className="pg-spacer" />}
          {narrow ? <MoreMenu>{previewControls}</MoreMenu> : previewControls}
          {narrow ? <span className="pg-spacer" /> : null}
          <button
            type="button"
            className="pg-btn pg-btn-sm"
            disabled={!renderable}
            title={renderable ? 'Click through your whole app like a visitor' : 'Finish setup first'}
            onClick={() => setPlayFrom('/')}
          >
            Preview app
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!canUndo} onClick={() => dispatch({ type: 'undo' })} aria-label="Undo" title={`Undo (${modKey()}+Z)`}>
            Undo
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!canRedo} onClick={() => dispatch({ type: 'redo' })} aria-label="Redo" title={`Redo (${modKey()}+Shift+Z)`}>
            Redo
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="tab-export" onClick={() => setTab('export')}>
            Export
          </button>
          {narrow ? null : (
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="tab-project" onClick={() => setTab('project')}>
              Share
            </button>
          )}
          <button
            type="button"
            className="pg-save-chip"
            data-status={shownCloud.status}
            data-testid="save-chip"
            title={shownCloud.message ?? 'Account sync'}
            aria-label={`Account sync: ${cloudLabel(shownCloud)}`}
            onClick={() => setTab('project')}
          >
            {cloudLabel(shownCloud)}
          </button>
        </StudioTopBar>
      }
      sidebar={sidebar}
      canvas={canvas}
      inspector={
        <Inspector
          page={page}
          selectedNode={selectedNode}
          selectedEntry={selectedEntry}
          selectedIndex={selectedIndex}
          blockCount={blocks.length}
          blockErrors={blockErrors}
          compatibilityNote={compatibilityNote}
          pages={spec.pages}
          canDuplicate={!full}
          onPropChange={inspectorHandlers.onPropChange}
          onStyleChange={inspectorHandlers.onStyleChange}
          onDuplicateNode={duplicateNode}
          onRemoveNode={removeNode}
          onMoveBlock={inspectorHandlers.onMoveBlock}
          onRenamePage={inspectorHandlers.onRenamePage}
          contracts={spec.contracts ?? NO_CONTRACTS}
          onOpenContracts={inspectorHandlers.onOpenContracts}
          pageIndex={spec.pages.findIndex((p) => p.path === page.path)}
          pageCount={spec.pages.length}
          navVisible={isInNav(spec, page.path)}
          onPageAccess={inspectorHandlers.onPageAccess}
          onNavVisible={inspectorHandlers.onNavVisible}
          onMovePage={inspectorHandlers.onMovePage}
          tab={inspectorTab}
          onTabChange={setInspectorTab}
          addressSuggestions={addressSuggestions}
          onRenameNode={renameNode}
        />
      }
      dock={
        <LayersPanel
          page={page}
          catalog={all}
          selectedId={selectedBlockId}
          full={full}
          onSelect={selectNode}
          onDuplicate={duplicateNode}
          onRemove={removeNode}
          onMove={moveNode}
          onDrop={dropIntoTree}
          acceptsDrag={acceptsDrag}
          startDrag={startDrag}
          endDrag={endDrag}
          onRename={renameNode}
        />
      }
      overlay={
        <>
          {paletteOpen ? <CommandPalette commands={commands} onClose={() => setPaletteOpen(false)} /> : null}
          {shortcutsOpen ? <ShortcutSheet onClose={() => setShortcutsOpen(false)} /> : null}
          <Onboarding onRailChange={openRail} replayKey={tourKey} />
          {playFrom && renderable ? (
            <PreviewApp
              spec={renderable}
              catalog={all}
              startPath={playFrom}
              network={spec.network}
              walletState={walletState}
              onWalletStateChange={setWalletState}
              onClose={() => setPlayFrom(null)}
            />
          ) : null}
          {overlayOpen ? (
          <ModalDialog label={tab === 'export' ? 'Export' : 'Save and share'} onClose={() => setTab('block')}>
            <div className="pg-studio-overlay-panel">
              <Tabs idPrefix="builder" label="Editor" tabs={OVERLAY_TABS} value={tab} onChange={setTab}>
                {tab === 'export' ? (
                  <ExportPanel
                    spec={spec}
                    catalog={all}
                    valid={validationOk}
                    setupLeft={[...setup.map((s) => s.message), ...shownIssues.map(({ d }) => `${d.where} ${d.message}`)]}
                    onFixFirst={fixFirstSetupItem}
                  />
                ) : null}
                {tab === 'project' ? (
                  <ProjectPanel
                    spec={spec}
                    catalog={all}
                    projectId={projectId}
                    cloud={shownCloud}
                    saving={cloud.status === 'saving'}
                    valid={validationOk}
                    onSaveNew={onSaveNew}
                    onOpen={onOpenProject}
                    onImport={onImport}
                    onDeleted={onDeleted}
                    onNew={onNew}
                    onOpenNewer={onOpenNewer}
                    onKeepMine={onKeepMine}
                    onRestoreRevision={onRestoreRevision}
                  />
                ) : null}
              </Tabs>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm pg-studio-overlay-close" onClick={() => setTab('block')}>
                Close
              </button>
            </div>
          </ModalDialog>
          ) : null}
        </>
      }
    />
  );
}

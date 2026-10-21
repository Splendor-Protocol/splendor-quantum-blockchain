import { useCallback, useEffect, useState, type Dispatch, type DragEvent, type RefObject } from 'react';
import { WrongNetworkBanner, useNetwork } from '@splendor/connect-react';
import { blocksFromTree, defaultProps, findNode, type CatalogEntry, type DappSpec, type NetworkId, type PropValue } from '@splendor/dapp-spec';
import { Tabs, type TabItem } from '../components/Tabs';
import { PLAYGROUND_NETWORKS } from '../config';
import type { MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import type { PreviewRects } from '../preview/protocol';
import { SpecRenderer } from '../preview/SpecRenderer';
import {
  CanvasOverlay,
  CONTRACT_FN_MIME,
  ContractsPanel,
  DeviceSwitch,
  FlowsPanel,
  Inspector,
  Onboarding,
  PagesPanel,
  Palette,
  StudioShell,
  StudioTopBar,
  ThemePanel,
  type DeviceSize,
  type CanvasDrop,
  type StudioRail,
} from '../studio';
import { ExportPanel } from './ExportPanel';
import { describeIssue } from './issues';
import { ProjectPanel, type CloudState } from './ProjectPanel';
import { MAX_BLOCKS_PER_PAGE, type BuilderAction } from './state';

type EditorTab = 'block' | 'theme' | 'export' | 'project';

const OVERLAY_TABS: readonly TabItem<EditorTab>[] = [
  { id: 'export', label: 'Export' },
  { id: 'project', label: 'Save & share' },
];

type DragPayload = { kind: 'widget' | 'block'; id: string };

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
  goToIssue,
  newPageTitle,
  setNewPageTitle,
  blockQuery,
  setBlockQuery,
  dropIndex,
  setDropIndex,
  dragRef,
  skipClick,
  validationOk,
  renderable,
  projectId,
  cloud,
  signedIn,
  canUndo,
  canRedo,
  onSaveNew,
  onOpenProject,
  onImport,
  onDeleted,
  onNew,
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
  confirmMainnetLive: () => boolean;
  tab: EditorTab;
  setTab: (tab: EditorTab) => void;
  calls: { method: string; args: string }[];
  setCalls: (calls: { method: string; args: string }[]) => void;
  issues: { path: string; message: string }[];
  blockErrors: Record<string, string>;
  goToIssue: (target: { pagePath: string; blockId: string; prop: string | null; entryId: string | null }) => void;
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
  signedIn: boolean;
  canUndo: boolean;
  canRedo: boolean;
  onSaveNew: () => Promise<void>;
  onOpenProject: (id: string) => Promise<void>;
  onImport: (spec: DappSpec) => void;
  onDeleted: () => void;
  onNew: () => void;
}) {
  const { networkId } = useNetwork();
  const [rail, setRail] = useState<StudioRail>('components');
  const [device, setDevice] = useState<DeviceSize>('desktop');
  const page = spec.pages.find((p) => p.path === pagePath) ?? spec.pages[0]!;
  const blocks = pageBlocks(page);
  const selected = blocks.find((b) => b.id === selectedBlockId) ?? null;
  const selectedEntry = selected ? all.find((e) => e.id === selected.widget) : undefined;
  const selectedIndex = selected ? blocks.indexOf(selected) : -1;
  const full = blocks.length >= MAX_BLOCKS_PER_PAGE;
  const overlayOpen = tab === 'export' || tab === 'project';

  // In-canvas editing: the preview frame reports where nodes and insertion points are.
  const [rects, setRects] = useState<PreviewRects | null>(null);
  const [hoverId, setHoverId] = useState<string | null>(null);
  useEffect(() => {
    setHoverId(null);
  }, [mode, pagePath]);

  const labelFor = (id: string, kind: string): string => {
    const node = findNode(page.root, id);
    if (node?.kind === 'block') {
      const widget = node.block.widget;
      return all.find((e) => e.id === widget)?.title ?? widget;
    }
    return kind === 'block' ? 'Block' : kind.charAt(0).toUpperCase() + kind.slice(1);
  };

  /** Only drags the studio starts on purpose (palette, block list, contract function, node handle). */
  const acceptsDrag = useCallback(
    (dt: DataTransfer | null) => dragRef.current !== null || Boolean(dt?.types.includes(CONTRACT_FN_MIME)),
    [dragRef],
  );

  const dropIntoTree = ({ parentId, index, moveId, contractFn }: { parentId: string; index: number; moveId?: string | null; contractFn?: string }) => {
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
    try {
      const parsed = JSON.parse(contractFn) as { contractId?: unknown; address?: unknown; fn?: { name?: unknown; signature?: unknown; kind?: unknown } };
      const fn = parsed.fn;
      if (!fn || typeof fn.name !== 'string' || typeof fn.signature !== 'string') return;
      const widget = fn.kind === 'write' ? 'contract-write' : 'contract-read';
      const entry = all.find((e) => e.id === widget);
      const contract = spec.contracts?.find((c) => c.id === parsed.contractId);
      const props: Record<string, PropValue> = {
        ...(entry ? defaultProps(entry) : {}),
        contract: typeof parsed.address === 'string' ? parsed.address : (contract?.address ?? ''),
        functionSig: fn.signature,
        title: `${contract?.name ?? String(parsed.contractId ?? 'Contract')}: ${fn.name}`,
        ...(fn.kind === 'write' ? { buttonLabel: fn.name } : {}),
      };
      dispatch({ type: 'insertIntoTree', parentId, index, widget, props });
      setTab('block');
    } catch {
      // Not our payload.
    }
  };

  const onOverlayDrop = (d: CanvasDrop) => dropIntoTree(d);

  const dropAt = (index: number) => {
    const drag = dragRef.current;
    dragRef.current = null;
    setDropIndex(null);
    if (!drag) return;
    if (drag.kind === 'widget') {
      const entry = catalog.find((e) => e.id === drag.id && e.placeable);
      if (!entry || full) return;
      if (index >= blocks.length) dispatch({ type: 'addBlock', widget: entry.id, props: defaultProps(entry) });
      else dispatch({ type: 'insertBlock', widget: entry.id, props: defaultProps(entry), index });
      setTab('block');
      setRail('components');
      return;
    }
    dispatch({ type: 'reorderBlock', id: drag.id, index });
  };

  const startDrag = (event: DragEvent, payload: DragPayload) => {
    skipClick.current = true;
    dragRef.current = payload;
    event.dataTransfer.setData('text/plain', payload.id);
    event.dataTransfer.effectAllowed = payload.kind === 'widget' ? 'copy' : 'move';
  };

  const endDrag = () => {
    dragRef.current = null;
    setDropIndex(null);
    window.setTimeout(() => {
      skipClick.current = false;
    }, 50);
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
        onAdd={(widget, props) => {
          dispatch({ type: 'addBlock', widget, props });
          setTab('block');
        }}
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
        }}
      />
    ) : (
      <FlowsPanel
        spec={spec}
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

      <div
        className="pg-rail"
        onDragOver={(e) => {
          e.preventDefault();
          if (dropIndex == null) setDropIndex(blocks.length);
        }}
        onDrop={(e) => {
          e.preventDefault();
          dropAt(dropIndex ?? blocks.length);
        }}
      >
        <ul className="pg-blocks pg-rail-list" data-testid="block-list">
          {blocks.map((b, i) => {
            const entry = all.find((e) => e.id === b.widget);
            const title = entry?.title ?? b.widget;
            return (
              <li
                key={b.id}
                className={`${b.id === selectedBlockId ? 'is-active' : ''}${dropIndex === i ? ' is-drop' : ''}`}
                draggable
                onDragStart={(event) => startDrag(event, { kind: 'block', id: b.id })}
                onDragEnd={endDrag}
                onDragOver={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  setDropIndex(i);
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  dropAt(i);
                }}
              >
                <button
                  type="button"
                  aria-current={b.id === selectedBlockId ? 'true' : undefined}
                  onClick={() => {
                    dispatch({ type: 'selectBlock', id: b.id });
                    setTab('block');
                  }}
                >
                  {title}
                </button>
                <button
                  type="button"
                  className="pg-icon-btn"
                  aria-label={`Duplicate ${title}`}
                  title="Duplicate"
                  disabled={full}
                  onClick={() => {
                    dispatch({ type: 'duplicateBlock', id: b.id });
                    setTab('block');
                  }}
                >
                  ⧉
                </button>
                <button type="button" className="pg-icon-btn" aria-label={`Remove ${title}`} title="Remove" onClick={() => dispatch({ type: 'removeBlock', id: b.id })}>
                  ×
                </button>
              </li>
            );
          })}
        </ul>
        <div
          className={`pg-dropcap${dropIndex === blocks.length ? ' is-over' : ''}`}
          onDragOver={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setDropIndex(blocks.length);
          }}
          onDrop={(e) => {
            e.preventDefault();
            e.stopPropagation();
            dropAt(blocks.length);
          }}
        >
          Drop here
        </div>
      </div>
      <p className="pg-stage-hint">This row is the page. Drag to rearrange. Everything under it is the live dApp.</p>

      <div className={`pg-stage-canvas pg-device-frame pg-device-${device}`}>
        {mode === 'mock' ? (
          <div className="pg-canvas-stack">
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
              onRects={setRects}
              onHover={setHoverId}
              onDrop={dropIntoTree}
              height="calc(100vh - 320px)"
            />
            <CanvasOverlay
              rects={rects}
              selectedId={selectedBlockId}
              hoverId={hoverId}
              labelFor={labelFor}
              acceptsDrag={acceptsDrag}
              full={full}
              onSelect={(id) => {
                dispatch({ type: 'selectBlock', id });
                setTab('block');
              }}
              onDuplicate={(id) => dispatch({ type: 'duplicateNode', id })}
              onRemove={(id) => dispatch({ type: 'removeNodeFromTree', id })}
              onDrop={onOverlayDrop}
            />
          </div>
        ) : (
          <div className="pg-pad pg-stack">
            <p className="pg-muted" style={{ margin: 0 }}>
              Live mode uses the wallet in the header. Transactions are real
              {spec.network === 'mainnet' ? ' and spend real SPLD' : ''}.
            </p>
            <WrongNetworkBanner expectedNetworkId={spec.network} />
            {networkId !== spec.network ? (
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
      </div>

      {mode === 'mock' ? (
        <details className="pg-activity">
          <summary>Mock wallet activity ({calls.length})</summary>
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
      topBar={
        <StudioTopBar>
          <input
            id="dapp-name"
            className="pg-studio-name"
            aria-label="dApp name"
            value={spec.name}
            maxLength={80}
            onChange={(e) => dispatch({ type: 'setName', name: e.target.value })}
          />
          <label className="pg-studio-net" htmlFor="dapp-network">
            dApp
          </label>
          <select
            id="dapp-network"
            aria-label="dApp network"
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
          <span className="pg-spacer" />
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
          <DeviceSwitch value={device} onChange={setDevice} />
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!canUndo} onClick={() => dispatch({ type: 'undo' })} aria-label="Undo" title="Undo (Ctrl+Z)">
            Undo
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={!canRedo} onClick={() => dispatch({ type: 'redo' })} aria-label="Redo" title="Redo (Ctrl+Y)">
            Redo
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="tab-export" onClick={() => setTab('export')}>
            Export
          </button>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="tab-project" onClick={() => setTab('project')}>
            Share
          </button>
          <div className="pg-sr-only" aria-hidden={false}>
            <button type="button" data-testid="tab-block" tabIndex={-1} onClick={() => setTab('block')}>
              Block
            </button>
            <button type="button" data-testid="tab-theme" tabIndex={-1} onClick={() => { setTab('theme'); setRail('theme'); }}>
              Theme
            </button>
          </div>
        </StudioTopBar>
      }
      sidebar={sidebar}
      canvas={canvas}
      inspector={
        <Inspector
          page={page}
          selected={selected}
          selectedEntry={selectedEntry}
          selectedIndex={selectedIndex}
          blockCount={blocks.length}
          blockErrors={blockErrors}
          compatibilityNote={compatibilityNote}
          onPropChange={(name, value) => selected && dispatch({ type: 'setProp', id: selected.id, name, value })}
          onStyleChange={(style) => selected && dispatch({ type: 'setBlockStyle', id: selected.id, style })}
          onMoveBlock={(delta) => selected && dispatch({ type: 'moveBlock', id: selected.id, delta })}
          onRenamePage={(title) => dispatch({ type: 'renamePage', path: page.path, title })}
        />
      }
      overlay={
        <>
          <Onboarding onRailChange={setRail} />
          {overlayOpen ? (
          <div className="pg-studio-overlay" role="dialog" aria-modal="true" aria-label={tab === 'export' ? 'Export' : 'Save and share'}>
            <div className="pg-studio-overlay-panel">
              <Tabs idPrefix="builder" label="Editor" tabs={OVERLAY_TABS} value={tab} onChange={setTab}>
                {tab === 'export' ? <ExportPanel spec={spec} catalog={all} valid={validationOk} /> : null}
                {tab === 'project' ? (
                  <ProjectPanel
                    spec={spec}
                    catalog={all}
                    projectId={projectId}
                    cloud={signedIn && projectId ? cloud : { status: 'local' }}
                    valid={validationOk}
                    onSaveNew={onSaveNew}
                    onOpen={onOpenProject}
                    onImport={onImport}
                    onDeleted={onDeleted}
                    onNew={onNew}
                  />
                ) : null}
              </Tabs>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm pg-studio-overlay-close" onClick={() => setTab('block')}>
                Close
              </button>
            </div>
          </div>
          ) : null}
        </>
      }
    />
  );
}

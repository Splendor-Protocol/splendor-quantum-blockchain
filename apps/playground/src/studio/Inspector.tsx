import { useState } from 'react';
import type { CatalogEntry, ContainerNode, ContractRef, DappPage, LayoutNode, NodeStyle, PageAccess, PropValue } from '@splendor/dapp-spec';
import { PropsForm } from '../components/PropsForm';
import { Tabs, type TabItem } from '../components/Tabs';
import { StatusBadge } from '../gallery/GalleryPage';
import { ACTION_KEYS, isExternalHref, pagePathOf } from './FlowsPanel';
import { PageSettings, PageTitleInput } from './PagesPanel';

export type InspectorTab = 'props' | 'style' | 'actions';

const TABS: readonly TabItem<InspectorTab>[] = [
  { id: 'props', label: 'Content' },
  { id: 'style', label: 'Style' },
  { id: 'actions', label: 'Actions' },
];

const STYLE_FIELDS: { key: keyof NodeStyle; label: string; placeholder?: string }[] = [
  { key: 'width', label: 'Width', placeholder: '100%' },
  { key: 'maxWidth', label: 'Max width', placeholder: '720px' },
  { key: 'gap', label: 'Gap', placeholder: '1rem' },
  { key: 'padding', label: 'Padding', placeholder: '1rem' },
  { key: 'background', label: 'Background', placeholder: 'transparent' },
];

const KIND_LABEL: Record<string, string> = { section: 'Section', row: 'Row', column: 'Column', card: 'Card', stack: 'Stack' };

/** Width, spacing, alignment, background and hide-on-device: shared by components and containers. */
function StyleForm({ style, onStyleChange }: { style: NodeStyle; onStyleChange: (style: NodeStyle) => void }) {
  return (
    <div className="pg-form">
      {STYLE_FIELDS.map(({ key, label, placeholder }) => (
        <div key={key} className="pg-field">
          <label htmlFor={`style-${key}`}>{label}</label>
          <input
            id={`style-${key}`}
            value={style[key] ?? ''}
            placeholder={placeholder}
            onChange={(e) => {
              const next: Record<string, string | undefined> = { ...style };
              if (e.target.value.trim()) next[key] = e.target.value;
              else delete next[key];
              onStyleChange(next as NodeStyle);
            }}
          />
        </div>
      ))}
      <div className="pg-field">
        <label htmlFor="style-align">Align</label>
        <select
          id="style-align"
          value={style.align ?? ''}
          onChange={(e) => {
            const next = { ...style };
            if (e.target.value) next.align = e.target.value as NodeStyle['align'];
            else delete next.align;
            onStyleChange(next);
          }}
        >
          <option value="">Default</option>
          <option value="start">Start</option>
          <option value="center">Center</option>
          <option value="end">End</option>
          <option value="stretch">Stretch</option>
          <option value="between">Space between</option>
        </select>
      </div>
      <div className="pg-field">
        <label htmlFor="style-hide">Hide on</label>
        <select
          id="style-hide"
          value={style.hideOn ?? ''}
          onChange={(e) => {
            const next = { ...style };
            if (e.target.value === 'mobile' || e.target.value === 'desktop') next.hideOn = e.target.value;
            else delete next.hideOn;
            onStyleChange(next);
          }}
        >
          <option value="">Always show</option>
          <option value="mobile">Mobile</option>
          <option value="desktop">Desktop</option>
        </select>
      </div>
    </div>
  );
}

const EXTERNAL = '__external';

/** A link prop: pick one of the dApp's pages, or switch to a free-text URL. */
function ActionField({
  name,
  label,
  value,
  pages,
  onChange,
}: {
  name: string;
  label: string;
  value: PropValue | undefined;
  pages: readonly Pick<DappPage, 'path' | 'title'>[];
  onChange: (value: string | undefined) => void;
}) {
  const current = typeof value === 'string' ? value : value === undefined ? '' : String(value);
  const asPath = current.trim() && !isExternalHref(current) ? pagePathOf(current) : null;
  const matched = asPath && pages.some((p) => p.path === asPath) ? asPath : null;
  const [external, setExternal] = useState(false);
  const mode = external ? EXTERNAL : (matched ?? (current ? EXTERNAL : ''));
  const missing = mode === EXTERNAL && asPath && !matched ? asPath : null;

  return (
    <div className="pg-field">
      <label htmlFor={`action-${name}-target`}>{label}</label>
      <select
        id={`action-${name}-target`}
        value={mode}
        onChange={(e) => {
          const next = e.target.value;
          if (next === EXTERNAL) {
            setExternal(true);
            return;
          }
          setExternal(false);
          onChange(next || undefined);
        }}
      >
        <option value="">No link</option>
        {pages.map((p) => (
          <option key={p.path} value={p.path}>
            {p.title} ({p.path})
          </option>
        ))}
        <option value={EXTERNAL}>External URL</option>
      </select>
      {mode === EXTERNAL ? (
        <>
          <input
            id={`action-${name}`}
            aria-label={`${label} URL`}
            aria-describedby={missing ? `action-${name}-missing` : undefined}
            aria-invalid={missing ? true : undefined}
            value={current}
            placeholder="https://…"
            onChange={(e) => onChange(e.target.value || undefined)}
          />
          {missing ? (
            <small id={`action-${name}-missing`} className="pg-error" role="alert">
              No page at {missing}. Pick a page above or fix the path.
            </small>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

export function Inspector({
  page,
  selectedNode,
  selectedEntry,
  selectedIndex,
  blockCount,
  blockErrors,
  compatibilityNote,
  pages,
  canDuplicate = true,
  onPropChange,
  onStyleChange,
  onMoveBlock,
  onRenamePage,
  onDuplicateNode,
  onRemoveNode,
  contracts,
  onOpenContracts,
  pageIndex = -1,
  pageCount = 0,
  navVisible = true,
  onPageAccess,
  onNavVisible,
  onMovePage,
  tab: tabProp,
  onTabChange,
}: {
  page: DappPage;
  /** The selected block or container (null: nothing selected). */
  selectedNode: LayoutNode | null;
  selectedEntry: CatalogEntry | undefined;
  selectedIndex: number;
  blockCount: number;
  blockErrors: Record<string, string>;
  compatibilityNote: (entry: CatalogEntry) => string | null;
  /** Pages a link can point to. */
  pages: readonly Pick<DappPage, 'path' | 'title'>[];
  /** False when the page is full and nothing more can be added. */
  canDuplicate?: boolean;
  onPropChange: (name: string, value: PropValue | undefined) => void;
  onStyleChange: (style: NodeStyle) => void;
  onMoveBlock: (delta: -1 | 1) => void;
  onRenamePage: (title: string) => void;
  onDuplicateNode?: (id: string) => void;
  onRemoveNode?: (id: string) => void;
  /** The dApp's contracts: with them, contract and function props are pickers. */
  contracts?: readonly ContractRef[];
  /** "+ Add contract" in a contract picker: the studio opens its Contracts panel. */
  onOpenContracts?: () => void;
  /** Position of the current page among all pages, for the page settings shown when nothing is selected. */
  pageIndex?: number;
  pageCount?: number;
  /** Whether the current page is listed in the dApp's navigation. */
  navVisible?: boolean;
  onPageAccess?: (access: PageAccess) => void;
  onNavVisible?: (visible: boolean) => void;
  onMovePage?: (delta: -1 | 1) => void;
  /** Controlled tab, so the studio can switch to Content before focusing a field. */
  tab?: InspectorTab;
  onTabChange?: (tab: InspectorTab) => void;
}) {
  const [ownTab, setOwnTab] = useState<InspectorTab>('props');
  const tab = tabProp ?? ownTab;
  const setTab = (next: InspectorTab) => {
    setOwnTab(next);
    onTabChange?.(next);
  };

  if (selectedNode && selectedNode.kind !== 'block') {
    const container: ContainerNode = selectedNode;
    const isRoot = page.root.kind !== 'block' && page.root.id === container.id;
    return (
      <div className="pg-stack" data-testid="inspector-container">
        <div className="pg-row">
          <h3 style={{ margin: 0 }}>{KIND_LABEL[container.kind] ?? container.kind}</h3>
        </div>
        <p className="pg-muted" style={{ margin: 0 }}>
          {isRoot ? 'This is the whole page.' : 'A layout box that holds other components.'} Style it here.
        </p>
        {!isRoot ? (
          <div className="pg-row">
            <button
              type="button"
              className="pg-btn pg-btn-ghost pg-btn-sm"
              disabled={!canDuplicate}
              data-testid="inspector-duplicate"
              onClick={() => onDuplicateNode?.(container.id)}
            >
              Duplicate
            </button>
            <button type="button" className="pg-btn pg-btn-danger pg-btn-sm" data-testid="inspector-delete" onClick={() => onRemoveNode?.(container.id)}>
              Delete
            </button>
          </div>
        ) : null}
        <StyleForm style={container.style ?? {}} onStyleChange={onStyleChange} />
      </div>
    );
  }

  const selected = selectedNode?.kind === 'block' ? selectedNode.block : null;

  if (!selected || !selectedEntry) {
    return (
      <div className="pg-stack">
        <p className="pg-muted" style={{ margin: 0 }}>
          Click a component on the page to edit it.
        </p>
        <div className="pg-field">
          <label htmlFor="page-title">Page title</label>
          <PageTitleInput id="page-title" title={page.title} onRename={onRenamePage} />
        </div>
        {onPageAccess && onNavVisible && onMovePage ? (
          <PageSettings
            page={page}
            index={pageIndex}
            count={pageCount}
            inNav={navVisible}
            idPrefix="inspector-page"
            onAccess={onPageAccess}
            onNavVisible={onNavVisible}
            onMove={onMovePage}
          />
        ) : null}
      </div>
    );
  }

  const actionProps = selectedEntry.props.filter((p) => (ACTION_KEYS as readonly string[]).includes(p.name));

  return (
    <Tabs idPrefix="inspector" label="Inspector" tabs={TABS} value={tab} onChange={setTab}>
      {tab === 'props' ? (
        <div className="pg-stack">
          <div className="pg-row">
            <h3 style={{ margin: 0 }}>{selectedEntry.title}</h3>
            <StatusBadge status={selectedEntry.status} />
          </div>
          <p className="pg-muted" style={{ margin: 0 }}>
            {selectedEntry.description}
          </p>
          <div className="pg-row">
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={selectedIndex <= 0} onClick={() => onMoveBlock(-1)}>
              Earlier
            </button>
            <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" disabled={selectedIndex < 0 || selectedIndex >= blockCount - 1} onClick={() => onMoveBlock(1)}>
              Later
            </button>
          </div>
          {compatibilityNote(selectedEntry) ? <p className="pg-error">{compatibilityNote(selectedEntry)}</p> : null}
          <PropsForm entry={selectedEntry} values={selected.props} errors={blockErrors} onChange={onPropChange} contracts={contracts} pages={pages} onOpenContracts={onOpenContracts} />
        </div>
      ) : null}
      {tab === 'style' ? <StyleForm style={selected.style ?? {}} onStyleChange={onStyleChange} /> : null}
      {tab === 'actions' ? (
        actionProps.length ? (
          <div className="pg-form">
            {actionProps.map((p) => (
              <ActionField key={`${selected.id}:${p.name}`} name={p.name} label={p.label} value={selected.props[p.name]} pages={pages} onChange={(v) => onPropChange(p.name, v)} />
            ))}
          </div>
        ) : (
          <p className="pg-muted">This component has no navigation actions.</p>
        )
      ) : null}
    </Tabs>
  );
}

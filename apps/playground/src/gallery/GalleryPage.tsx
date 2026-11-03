import { useMemo, useState } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import {
  DAPP_SPEC_VERSION,
  defaultProps,
  validateSpec,
  wrapBlocks,
  type CatalogEntry,
  type DappSpec,
  type PropValue,
} from '@splendor/dapp-spec';
import { CodeBlock } from '../components/CodeBlock';
import { EmptyState } from '../components/Notice';
import { PieceArt } from '../studio/PieceArt';
import { PropsForm } from '../components/PropsForm';
import { Tabs, type TabItem } from '../components/Tabs';
import { ThemeEditor, type ThemeValue } from '../components/ThemeEditor';
import { DEFAULT_NETWORK, PACKAGE_VERSIONS, wasmPinsFromEnv } from '../config';
import { DEMO_RECIPIENT, type MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import { useRegistry } from '../registry';
import { groupMeta, groupOrder } from '../registry/groups';
import { PartPreview, hasPartPreview } from './PartPreview';
import { componentExport, downloadText, embedSnippet, fullPageSource, reactUsage } from './codeExport';

export function StatusBadge({ status }: { status: CatalogEntry['status'] }) {
  if (status === 'stable') return null;
  return <span className={`pg-badge pg-badge-${status}`}>{status}</span>;
}

const WASM_PINS = wasmPinsFromEnv();

type CodeView = 'react' | 'embed' | 'page';
const CODE_VIEWS: readonly { id: CodeView; label: string }[] = [
  { id: 'react', label: 'React' },
  { id: 'embed', label: 'Embed' },
  { id: 'page', label: 'Full page' },
];

/**
 * Defaults plus demo values for required fields, so a widget previews right away instead of
 * waiting for an address. "Add to my dApp" still starts from the plain defaults.
 */
export function galleryProps(entry: CatalogEntry): Record<string, PropValue> {
  const props: Record<string, PropValue> = defaultProps(entry);
  for (const p of entry.props) {
    if (!p.required || (props[p.name] != null && props[p.name] !== '')) continue;
    if (p.format === 'address') props[p.name] = DEMO_RECIPIENT;
    else if (p.type === 'string') props[p.name] = p.placeholder?.split(/\s/)[0] || 'demo';
  }
  return props;
}

/** Placeholder shown in copied code instead of the preview's demo address. */
export const YOUR_ADDRESS = 'SQ:YOUR_ADDRESS';

/** Props for the Code tab: demo values only make the preview work and must never be copied into an app. */
export function snippetProps(props: Record<string, PropValue>): Record<string, PropValue> {
  return Object.fromEntries(Object.entries(props).map(([k, v]) => [k, v === DEMO_RECIPIENT ? YOUR_ADDRESS : v]));
}

type GalleryTab = 'props' | 'theme' | 'code';
const GALLERY_TABS: readonly TabItem<GalleryTab>[] = [
  { id: 'props', label: 'Settings' },
  { id: 'theme', label: 'Theme' },
  { id: 'code', label: 'Code' },
];

function GalleryDetail({ entry }: { entry: CatalogEntry }) {
  const { compatibilityNote } = useRegistry();
  const navigate = useNavigate();
  const [props, setProps] = useState<Record<string, PropValue>>(() => galleryProps(entry));
  const [theme, setTheme] = useState<ThemeValue>({ base: 'splendor', overrides: {} });
  const [walletState, setWalletState] = useState<MockWalletState>('connected');
  const [tab, setTab] = useState<GalleryTab>('props');
  const [codeView, setCodeView] = useState<CodeView>('react');
  /** Props for copied code: demo values never leave the preview. */
  const shown = snippetProps(props);
  const embed = embedSnippet(entry, shown, DEFAULT_NETWORK, PACKAGE_VERSIONS.connectReact, WASM_PINS);

  /** One .tsx file plus a README; browsers ask once before saving a second file. */
  const downloadComponent = () => {
    const out = componentExport(entry, shown, DEFAULT_NETWORK);
    downloadText(out.fileName, out.source, 'text/typescript');
    window.setTimeout(() => downloadText(out.readmeName, out.readme, 'text/markdown'), 250);
  };

  const spec: DappSpec = useMemo(
    () => {
      const block = { id: 'item', widget: entry.id, props };
      return {
        version: DAPP_SPEC_VERSION,
        name: entry.title,
        network: DEFAULT_NETWORK,
        theme,
        pages: [{ path: '/', title: entry.title, root: wrapBlocks([block]), blocks: [block] }],
      };
    },
    [entry, props, theme],
  );
  const validation = validateSpec(spec, { catalog: [entry] });
  const errors = Object.fromEntries(
    validation.ok ? [] : validation.issues.map((i) => [i.path.split('.').pop() ?? '', i.message]),
  );
  const note = compatibilityNote(entry);

  return (
    <div className="pg-gallery-main">
      <section className="pg-stack pg-gallery-stage">
        <div className="pg-row">
          <h1 style={{ margin: 0 }}>{entry.title}</h1>
          <StatusBadge status={entry.status} />
          {note ? <span className="pg-badge pg-badge-beta">{note}</span> : null}
          <span className="pg-spacer" />
          <div className="pg-segmented" role="group" aria-label="Mock wallet">
            {(['connected', 'disconnected'] as const).map((s) => (
              <button key={s} type="button" aria-pressed={walletState === s} onClick={() => setWalletState(s)}>
                {s === 'connected' ? 'Wallet connected' : 'No wallet'}
              </button>
            ))}
          </div>
        </div>
        <p className="pg-muted" style={{ margin: 0 }}>
          {entry.description}
        </p>
        {entry.placeable ? (
          <PreviewFrame spec={spec} pagePath="/" network={DEFAULT_NETWORK} walletState={walletState} height="max(420px, calc(100dvh - 280px))" title={`${entry.title} preview`} />
        ) : (
          <div className="pg-panel pg-stack">
            <p style={{ margin: 0 }}>
              <code>{entry.importName}</code> needs data from a hook (for example <code>useEstimateFee</code>), so it is used inside your own
              components rather than placed on a page. See the docs below and the Tip jar block for an example.
            </p>
            {hasPartPreview(entry.id) ? <PartPreview id={entry.id} theme={theme} /> : null}
          </div>
        )}
        <p className="pg-muted" style={{ margin: 0 }}>
          Preview uses a mock wallet: signatures succeed instantly and nothing is sent. Use the builder's Live mode to try a real wallet.
        </p>
      </section>
      <aside className="pg-sidebar pg-panel">
        <Tabs idPrefix="gallery" label="Widget details" tabs={GALLERY_TABS} value={tab} onChange={setTab}>
        {tab === 'props' ? (
          <PropsForm
            entry={entry}
            values={props}
            errors={errors}
            onChange={(name, value) =>
              setProps((p) => {
                const next = { ...p };
                if (value === undefined) delete next[name];
                else next[name] = value;
                return next;
              })
            }
          />
        ) : null}
        {tab === 'theme' ? <ThemeEditor value={theme} onChange={setTheme} /> : null}
        {tab === 'code' ? (
          <div className="pg-stack">
            <div className="pg-segmented" role="group" aria-label="Code format">
              {CODE_VIEWS.map((v) => (
                <button key={v.id} type="button" aria-pressed={codeView === v.id} onClick={() => setCodeView(v.id)}>
                  {v.label}
                </button>
              ))}
            </div>
            {codeView === 'react' ? (
              <>
                <CodeBlock code={reactUsage(entry, shown, theme, DEFAULT_NETWORK)} />
                {entry.kind === 'block' ? (
                  <p className="pg-muted">
                    Blocks are small source files: use <strong>Download as component</strong> below, or export a project from the builder.
                  </p>
                ) : null}
                <CodeBlock code="npm install @splendor/connect-react @splendor/sqc-sdk" />
              </>
            ) : null}
            {codeView === 'embed' ? (
              embed ? (
                <>
                  <CodeBlock code={embed} />
                  <p className="pg-muted">Plain HTML, no React app needed. The script bundles React and mounts every <code>data-sqc-widget</code> element.</p>
                </>
              ) : (
                <p className="pg-muted">
                  The embed script mounts connect-react widgets only (transfer, account card, token panel, deploy token, sign-in, pending
                  transactions). Use the React or Full page code for <code>{entry.importName}</code>.
                </p>
              )
            ) : null}
            {codeView === 'page' ? <CodeBlock code={fullPageSource(entry, shown, theme, DEFAULT_NETWORK, WASM_PINS, PACKAGE_VERSIONS.connectReact)} /> : null}
            <button type="button" className="pg-btn pg-btn-ghost" onClick={downloadComponent}>
              Download as component
            </button>
          </div>
        ) : null}
        </Tabs>
        {entry.placeable ? (
          <button type="button" className="pg-btn" onClick={() => navigate(`/build?add=${encodeURIComponent(entry.id)}`)}>
            Add to my dApp
          </button>
        ) : null}
        {entry.docs ? (
          <Link to="/learn/guide/widgets" className="pg-muted">
            Read the widgets and hooks guide
          </Link>
        ) : null}
      </aside>
    </div>
  );
}

/** Matches title, id, description and category label, so "nft" or "send" both find something. */
export function galleryMatches(entry: CatalogEntry, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [entry.title, entry.id, entry.description, groupMeta(entry.category).label].some((s) => s?.toLowerCase().includes(q));
}

/** /widgets: every widget as a card, with search and category filters. */
function GalleryOverview({ entries }: { entries: readonly CatalogEntry[] }) {
  const { isFeatured } = useRegistry();
  const [query, setQuery] = useState('');
  const [category, setCategory] = useState<string | null>(null);
  const categories = groupOrder([...new Set(entries.map((e) => e.category))]);
  const shown = entries
    .filter((e) => (category ? e.category === category : true) && galleryMatches(e, query))
    .sort((a, b) => Number(isFeatured(b.id)) - Number(isFeatured(a.id)));
  return (
    <div className="pg-page pg-gallery-overview">
      <header className="pg-stack">
        <h1 style={{ margin: 0 }}>Widget gallery</h1>
        <p className="pg-muted" style={{ margin: 0 }}>
          Every piece you can put on a page, live with a test wallet. Open one to change its settings and copy the code.
        </p>
      </header>
      <div className="pg-gallery-filters">
        <input
          type="search"
          className="pg-gallery-search"
          placeholder="Search widgets"
          aria-label="Search widgets"
          data-testid="gallery-search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <div className="pg-chips pg-gallery-cats" role="group" aria-label="Category">
          <button type="button" className="pg-chip" aria-pressed={category === null} onClick={() => setCategory(null)}>
            All
          </button>
          {categories.map((c) => (
            <button key={c} type="button" className="pg-chip" aria-pressed={category === c} onClick={() => setCategory(category === c ? null : c)}>
              {groupMeta(c).label}
            </button>
          ))}
        </div>
      </div>
      <p className="pg-muted" role="status" style={{ margin: 0 }}>
        {shown.length === entries.length ? `${entries.length} widgets` : `${shown.length} of ${entries.length} widgets`}
      </p>
      {shown.length ? (
        <div className="pg-template-grid">
          {shown.map((e) => (
            <Link key={e.id} to={`/widgets/${e.id}`} className="pg-template-card pg-template-link pg-gallery-card" data-testid={`gallery-${e.id}`}>
              <span className="pg-template-thumb">
                <PieceArt id={e.id} category={e.category} tone={groupMeta(e.category).tone} />
              </span>
              <span className="pg-row" style={{ gap: '0.4rem' }}>
                <strong>{e.title}</strong>
                <StatusBadge status={e.status} />
              </span>
              <span className="pg-muted">{e.description}</span>
            </Link>
          ))}
        </div>
      ) : (
        <EmptyState
          title="No widget matches"
          testId="gallery-empty"
          action={
            <button
              type="button"
              className="pg-btn pg-btn-ghost pg-btn-sm"
              onClick={() => {
                setQuery('');
                setCategory(null);
              }}
            >
              Clear filters
            </button>
          }
        >
          Try a shorter word, or show every category.
        </EmptyState>
      )}
    </div>
  );
}

export function GalleryPage() {
  const { id } = useParams();
  const { all, catalog, isFeatured } = useRegistry();
  const visible = all.filter((e) => catalog.includes(e) || !e.placeable);
  if (!id) return <GalleryOverview entries={visible} />;
  const entry = visible.find((e) => e.id === id);
  const categories = [...new Set(visible.map((e) => e.category))];
  return (
    <div className="pg-gallery">
      <div className="pg-toy-rail" aria-label="Widgets">
        <Link to="/widgets" className="pg-toy-chip pg-gallery-all" data-testid="gallery-all">
          ← All widgets
        </Link>
        {groupOrder(categories).map((c) => (
          <div key={c} className="pg-toy-group">
            <span className="pg-toy-group-label">{groupMeta(c).label}</span>
            {visible
              .filter((e) => e.category === c)
              .sort((a, b) => Number(isFeatured(b.id)) - Number(isFeatured(a.id)))
              .map((e) => (
                <NavLink key={e.id} to={`/widgets/${e.id}`} className={() => `pg-toy-chip${e === entry ? ' is-active' : ''}`} data-testid={`gallery-${e.id}`}>
                  <span className="pg-toy-mark" data-tone={groupMeta(c).tone} aria-hidden="true">
                    {e.title.slice(0, 1)}
                  </span>
                  {e.title}
                  <StatusBadge status={e.status} />
                </NavLink>
              ))}
          </div>
        ))}
      </div>
      {entry ? (
        <GalleryDetail key={entry.id} entry={entry} />
      ) : (
        <div className="pg-page">
          <EmptyState
            title="Widget not found"
            testId="gallery-not-found"
            action={
              <Link className="pg-btn pg-btn-sm" to="/widgets">
                See all widgets
              </Link>
            }
          >
            There is no widget called “{id}”. It may have been renamed.
          </EmptyState>
        </div>
      )}
    </div>
  );
}

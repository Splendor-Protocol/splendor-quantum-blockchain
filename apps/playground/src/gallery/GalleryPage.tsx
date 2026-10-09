import { useMemo, useState } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import {
  DAPP_SPEC_VERSION,
  blockSnippet,
  defaultProps,
  validateSpec,
  type CatalogEntry,
  type DappSpec,
  type PropValue,
} from '@splendor/dapp-spec';
import { CodeBlock } from '../components/CodeBlock';
import { PropsForm } from '../components/PropsForm';
import { ThemeEditor, type ThemeValue } from '../components/ThemeEditor';
import { DEFAULT_NETWORK } from '../config';
import type { MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import { useRegistry } from '../registry';

export function StatusBadge({ status }: { status: CatalogEntry['status'] }) {
  if (status === 'stable') return null;
  return <span className={`pg-badge pg-badge-${status}`}>{status}</span>;
}

function providerSnippet(theme: ThemeValue): string {
  const themeProp = Object.keys(theme.overrides).length
    ? `theme={${JSON.stringify(theme.overrides)}}`
    : `theme="${theme.base}"`;
  return `import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';

<SplendorConnectProvider network="testnet" connectors="auto" ${themeProp}>
  <ConnectKit />
  {/* widgets go here */}
</SplendorConnectProvider>`;
}

function GalleryDetail({ entry }: { entry: CatalogEntry }) {
  const { compatibilityNote } = useRegistry();
  const navigate = useNavigate();
  const [props, setProps] = useState<Record<string, PropValue>>(() => defaultProps(entry));
  const [theme, setTheme] = useState<ThemeValue>({ base: 'splendor', overrides: {} });
  const [walletState, setWalletState] = useState<MockWalletState>('connected');
  const [tab, setTab] = useState<'props' | 'theme' | 'code'>('props');

  const spec: DappSpec = useMemo(
    () => ({
      version: DAPP_SPEC_VERSION,
      name: entry.title,
      network: DEFAULT_NETWORK,
      theme,
      pages: [{ path: '/', title: entry.title, blocks: [{ id: 'item', widget: entry.id, props }] }],
    }),
    [entry, props, theme],
  );
  const validation = validateSpec(spec, { catalog: [entry] });
  const errors = Object.fromEntries(
    validation.ok ? [] : validation.issues.map((i) => [i.path.split('.').pop() ?? '', i.message]),
  );
  const note = compatibilityNote(entry);

  return (
    <>
      <section className="pg-stack">
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
          <PreviewFrame spec={spec} pagePath="/" network={DEFAULT_NETWORK} walletState={walletState} height={520} title={`${entry.title} preview`} />
        ) : (
          <div className="pg-panel">
            <p>
              <code>{entry.importName}</code> needs data from a hook (for example <code>useEstimateFee</code>), so it is used inside your own
              components rather than placed on a page. See the docs below and the Tip jar block for an example.
            </p>
          </div>
        )}
        <p className="pg-muted" style={{ margin: 0 }}>
          Preview uses a mock wallet: signatures succeed instantly and nothing is sent. Use the builder's Live mode to try a real wallet.
        </p>
      </section>
      <aside className="pg-sidebar pg-panel">
        <div className="pg-tabs-inline" role="tablist">
          {(['props', 'theme', 'code'] as const).map((t) => (
            <button key={t} type="button" role="tab" aria-selected={tab === t} onClick={() => setTab(t)}>
              {t === 'props' ? 'Settings' : t === 'theme' ? 'Theme' : 'Code'}
            </button>
          ))}
        </div>
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
            <CodeBlock code={blockSnippet({ id: 'item', widget: entry.id, props }, entry)} />
            {entry.kind === 'block' ? (
              <p className="pg-muted">Blocks are small source files. Export a project from the builder to get this file, or copy it from <code>@splendor/dapp-spec/blocks</code>.</p>
            ) : null}
            <CodeBlock code={providerSnippet(theme)} />
            <CodeBlock code={`npm install @splendor/connect-react @splendor/sqc-sdk`} />
          </div>
        ) : null}
        {entry.placeable ? (
          <button type="button" className="pg-btn" onClick={() => navigate(`/build?add=${encodeURIComponent(entry.id)}`)}>
            Add to my dApp
          </button>
        ) : null}
        {entry.docs ? (
          <Link to="/learn/guide/widgets" className="pg-muted">
            Read the guide ({entry.docs.split('#')[1] ?? 'widgets'})
          </Link>
        ) : null}
      </aside>
    </>
  );
}

export function GalleryPage() {
  const { id } = useParams();
  const { all, catalog, isFeatured } = useRegistry();
  const visible = all.filter((e) => catalog.includes(e) || !e.placeable);
  const entry = visible.find((e) => e.id === id) ?? visible[0];
  const categories = [...new Set(visible.map((e) => e.category))];
  return (
    <div className="pg-workspace">
      <aside className="pg-sidebar pg-panel">
        {categories.map((c) => (
          <div key={c}>
            <p className="pg-list-title">{c}</p>
            <ul className="pg-list">
              {visible
                .filter((e) => e.category === c)
                .sort((a, b) => Number(isFeatured(b.id)) - Number(isFeatured(a.id)))
                .map((e) => (
                  <li key={e.id}>
                    <NavLink to={`/widgets/${e.id}`} className={() => (e === entry ? 'is-active' : '')} data-testid={`gallery-${e.id}`}>
                      {e.title}
                      <StatusBadge status={e.status} />
                      {isFeatured(e.id) ? <span className="pg-badge pg-badge-featured">featured</span> : null}
                    </NavLink>
                  </li>
                ))}
            </ul>
          </div>
        ))}
      </aside>
      {entry ? <GalleryDetail key={entry.id} entry={entry} /> : <p className="pg-muted">No widgets available.</p>}
    </div>
  );
}

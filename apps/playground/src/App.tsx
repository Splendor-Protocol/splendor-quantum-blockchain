import { lazy, Suspense, useEffect, useRef, useState, type RefObject } from 'react';
import { Skeleton } from './components/Notice';
import { BrowserRouter, Link, NavLink, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { ConnectKit, SplendorConnectProvider, TxToastHost, connectErrorMessage } from '@splendor/connect-react';
import { AppToasts, pushToast } from './components/Toasts';
import { AccountMenu } from './auth/AccountMenu';
import { SessionProvider, useSession } from './auth/session';
import { AppErrorBoundary } from './components/AppErrorBoundary';
import { NetworkSelect } from './components/NetworkSelect';
import { DEFAULT_NETWORK } from './config';
import { playgroundNetworks } from './networks';
import { RegistryProvider } from './registry';
import { HomePage } from './home/HomePage';

const LearnPage = lazy(() => import('./learn/LearnPage').then((m) => ({ default: m.LearnPage })));
const GalleryPage = lazy(() => import('./gallery/GalleryPage').then((m) => ({ default: m.GalleryPage })));
const BuilderPage = lazy(() => import('./builder/BuilderPage').then((m) => ({ default: m.BuilderPage })));
const AdminPage = lazy(() => import('./admin/AdminPage').then((m) => ({ default: m.AdminPage })));
const SharedPage = lazy(() => import('./builder/SharedPage').then((m) => ({ default: m.SharedPage })));

const CUSTOM_NETWORKS = playgroundNetworks();

function wasmPins() {
  const wasmSha256 = import.meta.env.VITE_SQC_WASM_SHA256?.trim();
  const execSha256 = import.meta.env.VITE_SQC_EXEC_SHA256?.trim();
  if (!import.meta.env.DEV && (!wasmSha256 || !execSha256)) {
    throw new Error('VITE_SQC_WASM_SHA256 and VITE_SQC_EXEC_SHA256 are required for production builds.');
  }
  return wasmSha256 && execSha256 ? { wasmSha256, execSha256 } : { allowUnpinnedWasm: true as const };
}

/** Live wallet, session and registry for every page except the preview iframe. */
function Shell() {
  return (
    <SplendorConnectProvider
      network={DEFAULT_NETWORK}
      connectors="auto"
      theme="splendor"
      autoReconnect
      persistNetwork
      customNetworks={CUSTOM_NETWORKS}
      // Network switches and reconnects that fail would otherwise be silent.
      onError={(err) => pushToast({ kind: 'error', title: 'Wallet', message: connectErrorMessage(err) })}
      {...wasmPins()}
    >
      <TxToastHost>
        <SessionProvider>
          <RegistryProvider>
            <Layout />
          </RegistryProvider>
        </SessionProvider>
      </TxToastHost>
      <AppToasts />
    </SplendorConnectProvider>
  );
}

const TITLES: [RegExp, string][] = [
  [/^\/learn/, 'Learn'],
  [/^\/widgets/, 'Widgets'],
  [/^\/build/, 'Build'],
  [/^\/s\//, 'Shared project'],
  [/^\/admin/, 'Admin'],
];

export const PRODUCT_NAME = 'Splendor dApp Studio';
const KNOWN_PATH = /^\/($|learn(\/|$)|widgets(\/|$)|build\/?$|s\/[^/]+\/?$|admin(\/|$))/;

/** Tab title per area, so history, bookmarks and screen readers can tell pages apart. */
function useDocumentTitle(pathname: string) {
  useEffect(() => {
    if (!KNOWN_PATH.test(pathname)) {
      document.title = `Page not found · ${PRODUCT_NAME}`;
      return;
    }
    const area = TITLES.find(([re]) => re.test(pathname))?.[1];
    document.title = area ? `${area} · ${PRODUCT_NAME}` : PRODUCT_NAME;
  }, [pathname]);
}

function NotFound() {
  return (
    <div className="pg-page">
      <h1>Page not found</h1>
      <p className="pg-muted">This address does not match any page of the studio.</p>
      <div className="pg-row">
        <Link className="pg-btn" to="/">
          Home
        </Link>
        <Link className="pg-btn pg-btn-ghost" to="/build">
          Open the studio
        </Link>
      </div>
    </div>
  );
}

/** Keeps `--pg-header-h` equal to the header's real height, so sticky panels never hide under it. */
function useHeaderHeight(ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver !== 'function') return;
    const ro = new ResizeObserver(() => document.documentElement.style.setProperty('--pg-header-h', `${el.offsetHeight}px`));
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
}

function Footer() {
  return (
    <footer className="pg-footer">
      <div className="pg-footer-inner">
        <div>
          <strong>{PRODUCT_NAME}</strong>
          <p className="pg-muted">Build, test and ship post-quantum dApps on Splendor Quantum Chain.</p>
        </div>
        <nav aria-label="Footer">
          <ul>
            <li>
              <Link to="/learn">Docs and recipes</Link>
            </li>
            <li>
              <Link to="/widgets">Widget gallery</Link>
            </li>
            <li>
              <a href="https://github.com/Splendor-Protocol/splendor-quantum-blockchain" target="_blank" rel="noopener noreferrer">
                GitHub
              </a>
            </li>
            <li>
              <a href="https://www.npmjs.com/package/@splendor/connect-react" target="_blank" rel="noopener noreferrer">
                npm
              </a>
            </li>
            <li>
              <a href="https://github.com/Splendor-Protocol/splendor-quantum-blockchain/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">
                License
              </a>
            </li>
            <li>
              <a href="https://github.com/Splendor-Protocol/splendor-quantum-blockchain/security/policy" target="_blank" rel="noopener noreferrer">
                Security
              </a>
            </li>
          </ul>
        </nav>
      </div>
    </footer>
  );
}

function Layout() {
  const { session } = useSession();
  const { pathname, key: locationKey } = useLocation();
  useDocumentTitle(pathname);
  const onBuild = /^\/build(\/|$)/.test(pathname);
  const header = useRef<HTMLElement>(null);
  useHeaderHeight(header);
  /** Small screens: the nav and account controls live behind a menu button. */
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => setMenuOpen(false), [pathname]);
  return (
    <div className="pg-app">
      <a className="pg-skip-link" href="#main">
        Skip to content
      </a>
      <header className="pg-header" ref={header}>
        <NavLink to="/" className="pg-logo">
          Splendor <span>dApp Studio</span>
        </NavLink>
        <button
          type="button"
          className="pg-menu-toggle pg-btn pg-btn-ghost pg-btn-sm"
          aria-expanded={menuOpen}
          aria-controls="pg-header-menu"
          onClick={() => setMenuOpen((v) => !v)}
        >
          {menuOpen ? 'Close' : 'Menu'}
        </button>
        <div
          id="pg-header-menu"
          className="pg-header-menu"
          data-open={menuOpen ? 'true' : 'false'}
          onKeyDown={(e) => {
            if (e.key === 'Escape' && menuOpen) setMenuOpen(false);
          }}
        >
          <nav className="pg-tabs" aria-label="Studio">
            <NavLink to="/learn">Learn</NavLink>
            <NavLink to="/widgets">Widgets</NavLink>
            <NavLink to="/build">Build</NavLink>
            {session?.isAdmin ? <NavLink to="/admin">Admin</NavLink> : null}
          </nav>
          <div className="pg-header-right">
            {/* The studio has its own network choice for the dApp: one picker on /build, not two. */}
            {onBuild ? null : <NetworkSelect />}
            <AccountMenu />
            <ConnectKit showNetwork={false} />
          </div>
        </div>
      </header>
      <main id="main" tabIndex={-1} className="pg-main">
        {/* Every navigation (also a new ?query on the same path) gets a fresh boundary. */}
        <AppErrorBoundary resetKey={locationKey}>
          <Suspense fallback={<Skeleton lines={4} label="Loading…" />}>
            <Outlet />
          </Suspense>
        </AppErrorBoundary>
      </main>
      {onBuild ? null : <Footer />}
    </div>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Shell />}>
          <Route index element={<HomePage />} />
          <Route path="learn/*" element={<LearnPage />} />
          <Route path="widgets/:id?" element={<GalleryPage />} />
          <Route path="build" element={<BuilderPage />} />
          <Route path="s/:shareId" element={<SharedPage />} />
          <Route path="admin/*" element={<AdminPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

import { lazy, Suspense, useEffect } from 'react';
import { BrowserRouter, Link, NavLink, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { ConnectKit, SplendorConnectProvider, TxToastHost } from '@splendor/connect-react';
import { SessionProvider, useSession } from './auth/session';
import { AppErrorBoundary } from './components/AppErrorBoundary';
import { NetworkSelect } from './components/NetworkSelect';
import { DEFAULT_NETWORK } from './config';
import { playgroundNetworks } from './networks';
import { PreviewRuntime } from './preview/PreviewRuntime';
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
      {...wasmPins()}
    >
      <TxToastHost>
        <SessionProvider>
          <RegistryProvider>
            <Layout />
          </RegistryProvider>
        </SessionProvider>
      </TxToastHost>
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

/** Tab title per area, so history, bookmarks and screen readers can tell pages apart. */
function useDocumentTitle(pathname: string) {
  useEffect(() => {
    const area = TITLES.find(([re]) => re.test(pathname))?.[1];
    document.title = area ? `${area} · Splendor dApp Playground` : 'Splendor dApp Playground';
  }, [pathname]);
}

function NotFound() {
  return (
    <div className="pg-page">
      <h1>Page not found</h1>
      <p className="pg-muted">This address does not match any playground page.</p>
      <div className="pg-row">
        <Link className="pg-btn" to="/">
          Home
        </Link>
        <Link className="pg-btn pg-btn-ghost" to="/build">
          Open the builder
        </Link>
      </div>
    </div>
  );
}

function Layout() {
  const { session } = useSession();
  const { pathname } = useLocation();
  useDocumentTitle(pathname);
  return (
    <div className="pg-app">
      <a className="pg-skip-link" href="#main">
        Skip to content
      </a>
      <header className="pg-header">
        <NavLink to="/" className="pg-logo">
          Splendor <span>dApp Playground</span>
        </NavLink>
        <nav className="pg-tabs" aria-label="Playground">
          <NavLink to="/learn">Learn</NavLink>
          <NavLink to="/widgets">Widgets</NavLink>
          <NavLink to="/build">Build</NavLink>
          {session?.isAdmin ? <NavLink to="/admin">Admin</NavLink> : null}
        </nav>
        <div className="pg-header-right">
          <NetworkSelect />
          <ConnectKit showNetwork={false} />
        </div>
      </header>
      <main id="main" tabIndex={-1} className="pg-main">
        <AppErrorBoundary resetKey={pathname}>
          <Suspense fallback={<p className="pg-muted pg-pad" role="status">Loading…</p>}>
            <Outlet />
          </Suspense>
        </AppErrorBoundary>
      </main>
    </div>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/preview" element={<PreviewRuntime />} />
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

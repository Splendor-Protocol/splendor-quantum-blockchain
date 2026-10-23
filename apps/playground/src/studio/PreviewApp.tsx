import { useEffect, useMemo, useState } from 'react';
import type { CatalogEntry, DappSpec, NetworkId } from '@splendor/dapp-spec';
import { ModalDialog } from '../components/ModalDialog';
import type { MockWalletState } from '../preview/mock';
import { PreviewFrame } from '../preview/PreviewFrame';
import { validateForPreview } from '../preview/validatePreview';
import './preview-app.css';

export type PreviewAppProps = {
  spec: DappSpec;
  /** Used to check the spec can run before it is handed to the frame. */
  catalog: readonly CatalogEntry[];
  /** Page to open on, and to return to on "Restart flow". Defaults to the home (first) page. */
  startPath?: string;
  network: NetworkId;
  /** Initial state of the test wallet. */
  walletState: MockWalletState;
  /** Called when the test wallet is switched inside the preview. */
  onWalletStateChange?: (state: MockWalletState) => void;
  /** "Back to editor" and Escape. */
  onClose: () => void;
};

type Device = 'desktop' | 'tablet' | 'mobile';
const DEVICES: { id: Device; label: string; width: string }[] = [
  { id: 'desktop', label: 'Desktop', width: '100%' },
  { id: 'tablet', label: 'Tablet', width: '820px' },
  { id: 'mobile', label: 'Mobile', width: '390px' },
];

const MAX_TRAIL = 12;

/**
 * Full-screen "Preview app": the dApp as a visitor sees it. Page gates are enforced, links move
 * between pages, and the test wallet can be switched between connected and not connected.
 */
export function PreviewApp({ spec, catalog, startPath, network, walletState, onWalletStateChange, onClose }: PreviewAppProps) {
  const home = spec.pages[0]?.path ?? '/';
  const first = startPath && spec.pages.some((p) => p.path === startPath) ? startPath : home;
  const [path, setPath] = useState(first);
  const [trail, setTrail] = useState<string[]>([first]);
  const [device, setDevice] = useState<Device>('desktop');
  const [wallet, setWallet] = useState<MockWalletState>(walletState);
  const [resetKey, setResetKey] = useState(0);
  /** What the test wallet has been asked to do since the app (re)started. */
  const [calls, setCalls] = useState<{ method: string; args: string }[]>([]);

  // Follow the editor if the test wallet is switched while this is open.
  useEffect(() => setWallet(walletState), [walletState]);

  const page = spec.pages.find((p) => p.path === path) ?? spec.pages[0];
  const current = page?.path ?? path;
  const title = (p: string) => spec.pages.find((x) => x.path === p)?.title ?? p;
  const appName = spec.nav?.brand?.trim() || spec.name;

  const go = (next: string) => {
    setPath(next);
    setTrail((t) => (t[t.length - 1] === next ? t : [...t, next].slice(-MAX_TRAIL)));
  };
  const goToCrumb = (index: number) => {
    const target = trail[index];
    if (target === undefined) return;
    setPath(target);
    setTrail((t) => t.slice(0, index + 1));
  };
  const restart = () => {
    setPath(first);
    setTrail([first]);
    setResetKey((k) => k + 1);
  };
  const switchWallet = (next: MockWalletState) => {
    setWallet(next);
    onWalletStateChange?.(next);
  };

  const ready = useMemo(() => validateForPreview(spec, catalog), [spec, catalog]);
  const access = page?.access ?? 'public';
  const hint =
    access === 'wallet' && wallet === 'disconnected'
      ? 'This page needs a connected wallet. Switch the test wallet to Connected to open it.'
      : access === 'signin' && wallet === 'disconnected'
        ? 'This page needs a connected wallet and a sign-in. Switch the test wallet to Connected, then sign in.'
        : access === 'signin'
          ? 'This page asks visitors to sign in. The test wallet signs for free.'
          : null;
  const width = DEVICES.find((d) => d.id === device)!.width;

  return (
    <ModalDialog label="Preview app" onClose={onClose} className="pg-preview-app" testId="preview-app">
      <header className="pg-preview-app-bar">
        <strong className="pg-preview-app-name" data-testid="preview-app-name">
          {appName}
        </strong>

        <nav className="pg-preview-app-crumbs" aria-label="Visited pages">
          <ol>
            {trail.map((p, i) => {
              const last = i === trail.length - 1;
              return (
                <li key={`${p}-${i}`}>
                  <button type="button" className="pg-link-btn" aria-current={last ? 'page' : undefined} data-testid="preview-crumb" onClick={() => goToCrumb(i)} disabled={last}>
                    {title(p)}
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>

        <span className="pg-spacer" />

        <label className="pg-preview-app-field">
          <span className="pg-muted">Page</span>
          <select aria-label="Page" data-testid="preview-page-select" value={current} onChange={(e) => go(e.target.value)}>
            {spec.pages.map((p) => (
              <option key={p.path} value={p.path}>
                {p.title} ({p.path})
              </option>
            ))}
          </select>
        </label>

        <div className="pg-segmented" role="group" aria-label="Device">
          {DEVICES.map((d) => (
            <button key={d.id} type="button" aria-pressed={device === d.id} data-testid={`preview-device-${d.id}`} onClick={() => setDevice(d.id)}>
              {d.label}
            </button>
          ))}
        </div>

        <div className="pg-segmented" role="group" aria-label="Test wallet">
          <button type="button" aria-pressed={wallet === 'connected'} data-testid="preview-wallet-connected" onClick={() => switchWallet('connected')}>
            Connected
          </button>
          <button type="button" aria-pressed={wallet === 'disconnected'} data-testid="preview-wallet-disconnected" onClick={() => switchWallet('disconnected')}>
            No wallet
          </button>
        </div>

        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="preview-restart" onClick={restart}>
          Restart flow
        </button>
        <button type="button" className="pg-btn pg-btn-sm" data-testid="preview-close" autoFocus onClick={onClose}>
          Back to editor
        </button>
      </header>

      {hint ? (
        <p className="pg-preview-app-hint" role="status" data-testid="preview-hint">
          {hint}
        </p>
      ) : null}

      <div className="pg-preview-app-stage">
        {ready.spec ? (
          <div className="pg-preview-app-device" style={{ width }} data-device={device}>
            <PreviewFrame
              mode="play"
              resetKey={resetKey}
              spec={spec}
              pagePath={current}
              network={network}
              walletState={wallet}
              onNavigate={go}
              onCalls={setCalls}
              title="App preview"
              height="100%"
            />
          </div>
        ) : (
          <div className="pg-preview-app-blocked" role="alert" data-testid="preview-blocked">
            <strong>This app cannot run yet. Fix these in the editor first:</strong>
            <ul>
              {ready.issues.slice(0, 8).map((i) => (
                <li key={`${i.path}:${i.message}`}>
                  <code>{i.path || 'spec'}</code> {i.message}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <details className="pg-preview-app-calls">
        <summary data-testid="preview-calls-summary">Test wallet activity ({calls.length})</summary>
        <div className="pg-output" data-testid="preview-calls" role="log" aria-live="polite">
          {calls.length ? (
            <ul>
              {calls.map((c, i) => (
                <li key={`${c.method}-${i}`}>
                  <code>{c.method}</code> {c.args}
                </li>
              ))}
            </ul>
          ) : (
            <p className="pg-muted">Nothing yet. Use the app and its calls to the test wallet show up here.</p>
          )}
        </div>
      </details>
    </ModalDialog>
  );
}

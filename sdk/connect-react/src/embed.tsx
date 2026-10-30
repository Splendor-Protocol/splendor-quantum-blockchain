/**
 * Minimal embed bootstrap: mount a single widget into a host page without a React app.
 *
 * Built as an IIFE (`dist/embed.js`, global `SplendorEmbed`) by `vite.embed.config.ts`:
 *   <link rel="stylesheet" href=".../style.css" />
 *   <div data-sqc-widget="transfer" data-network="testnet" data-props='{"defaultAmount":"0.01"}'></div>
 *   <script src=".../embed.js"></script>
 *
 * Widgets are mounted on DOMContentLoaded; call `SplendorEmbed.mountEmbed(root)` for content added later.
 */
import type { ComponentType } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import {
  AccountCard,
  ActivityFeed,
  AddressBook,
  BridgeClaimWidget,
  DeployTokenWidget,
  NftPanel,
  PendingTxList,
  QrCode,
  ReceiveWidget,
  SignInButton,
  TokenBalances,
  TokenPanel,
  TransferWidget,
  ValidatorList,
} from './widgets/index.js';
import { SplendorConnectProvider } from './react/provider.js';
import { TxToastHost } from './ui/index.js';

type AnyProps = Record<string, unknown>;
type EmbedNetwork = 'testnet' | 'devnet' | 'mainnet' | 'local';

const NETWORKS: readonly EmbedNetwork[] = ['testnet', 'devnet', 'mainnet', 'local'];
const SHA256_RE = /^[0-9a-f]{64}$/i;

const WIDGETS: Record<string, ComponentType<AnyProps>> = {
  transfer: TransferWidget as ComponentType<AnyProps>,
  'account-card': AccountCard as ComponentType<AnyProps>,
  'token-panel': TokenPanel as ComponentType<AnyProps>,
  'deploy-token': DeployTokenWidget as ComponentType<AnyProps>,
  'sign-in': SignInButton as ComponentType<AnyProps>,
  'pending-tx': PendingTxList as ComponentType<AnyProps>,
  'nft-panel': NftPanel as ComponentType<AnyProps>,
  'bridge-claim': BridgeClaimWidget as ComponentType<AnyProps>,
  'validator-list': ValidatorList as ComponentType<AnyProps>,
  'activity-feed': ActivityFeed as ComponentType<AnyProps>,
  receive: ReceiveWidget as ComponentType<AnyProps>,
  'address-book': AddressBook as ComponentType<AnyProps>,
  'token-balances': TokenBalances as ComponentType<AnyProps>,
  'qr-code': QrCode as ComponentType<AnyProps>,
};

const roots = new WeakMap<Element, Root>();

function parseProps(el: Element): AnyProps {
  const raw = el.getAttribute('data-props');
  if (!raw) return {};
  try {
    const v = JSON.parse(raw) as unknown;
    return typeof v === 'object' && v && !Array.isArray(v) ? (v as AnyProps) : {};
  } catch {
    return {};
  }
}

function resolveWidget(name: string): ComponentType<AnyProps> | null {
  return Object.prototype.hasOwnProperty.call(WIDGETS, name) ? (WIDGETS[name] ?? null) : null;
}

/** Widgets render their own `data-sqc-widget` root, so skip anything inside an already mounted host. */
function insideMountedHost(el: Element): boolean {
  for (let p = el.parentElement; p; p = p.parentElement) if (roots.has(p)) return true;
  return false;
}

function mountOne(el: Element): void {
  const name = el.getAttribute('data-sqc-widget');
  if (!name || insideMountedHost(el)) return;
  const C = resolveWidget(name);
  if (!C) {
    el.textContent = `Unknown Splendor widget "${name}"`;
    return;
  }
  const attr = el.getAttribute('data-network') as EmbedNetwork | null;
  const network: EmbedNetwork = attr && NETWORKS.includes(attr) ? attr : 'testnet';
  const props = parseProps(el);
  let root = roots.get(el);
  if (!root) {
    root = createRoot(el);
    roots.set(el, root);
  }
  // Pin the signer with data-wasm-sha256 + data-exec-sha256. Unpinned is tolerated on test networks;
  // mainnet (real funds) needs both digests or an explicit data-allow-unpinned-wasm="true".
  const wasmSha256 = el.getAttribute('data-wasm-sha256')?.trim();
  const execSha256 = el.getAttribute('data-exec-sha256')?.trim();
  const pinned = !!(wasmSha256 && execSha256 && SHA256_RE.test(wasmSha256) && SHA256_RE.test(execSha256));
  if (!pinned && network === 'mainnet' && el.getAttribute('data-allow-unpinned-wasm') !== 'true') {
    root.render(
      <p role="alert" style={{ font: '14px system-ui, sans-serif', color: '#b42318' }}>
        Splendor widget not loaded: mainnet embeds must pin the signer with data-wasm-sha256 and data-exec-sha256.
      </p>,
    );
    return;
  }
  if (!pinned) console.warn(`[splendor-embed] "${name}" on ${network} uses an unpinned signer; add data-wasm-sha256 and data-exec-sha256.`);
  const integrity = pinned ? { wasmSha256: wasmSha256!, execSha256: execSha256! } : { allowUnpinnedWasm: true };
  root.render(
    <SplendorConnectProvider network={network} connectors="auto" theme="splendor" {...integrity}>
      <TxToastHost>
        <C {...props} />
      </TxToastHost>
    </SplendorConnectProvider>,
  );
}

/** Mount every `[data-sqc-widget]` element under `root` (default: the whole document). */
export function mountEmbed(root: ParentNode = document): void {
  root.querySelectorAll('[data-sqc-widget]').forEach(mountOne);
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => mountEmbed());
  else mountEmbed();
}

export default mountEmbed;

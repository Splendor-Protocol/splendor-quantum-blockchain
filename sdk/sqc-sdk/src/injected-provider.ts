/**
 * SQC injected provider — `window.splendor` (NOT Ethereum EIP-1193 / eth_*).
 *
 * Methods use the `splendor_*` prefix. Prefer an already-injected extension
 * provider; in-page install only when absent.
 *
 * Security:
 * - Never accepts seed/privateKey/evmSignature via injected connect (use ConnectKit UI).
 * - Rejects raw signMessage (domain separation required).
 * - Sensitive methods require onApprove (default deny when omitted).
 * - switchNetwork requires onSwitchNetwork (no fake local-only switch).
 * - Does not overwrite a non-in-page (extension) inject.
 */
import type { Connection } from './core/connection.js';
import type { SplendorPQBridge } from './splendor-wallet.js';
import type { UnsignedTx, SignedTx } from './types.js';
import { BridgeError } from './errors.js';
import { getNetwork, resolveNetwork, type NetworkId } from './networks.js';

export type SplendorProviderRequestArgs = {
  method: string;
  params?: unknown;
};

export type SplendorProviderEventMap = {
  connect: { address: string; evmAddress?: string };
  disconnect: Record<string, never>;
  accountsChanged: { address: string; evmAddress?: string };
  chainChanged: { chainId: number; networkId: string };
  lock: Record<string, never>;
  error: { code: string; message: string };
};

type Handler = (...args: unknown[]) => void;

export type SplendorInjectedProvider = {
  isSplendor: true;
  /** true when installed by @splendor/sqc-sdk (vs browser extension). */
  isInPage?: boolean;
  request: (args: SplendorProviderRequestArgs) => Promise<unknown>;
  on: (event: string, handler: Handler) => void;
  removeListener: (event: string, handler: Handler) => void;
  once: (event: string, handler: Handler) => void;
};

export type InstallSplendorProviderOptions = {
  wallet: SplendorPQBridge;
  connection?: Connection;
  networkId?: string;
  /** Required for splendor_switchNetwork — rebuilds RPC/session via host. */
  onSwitchNetwork?: (networkId: string) => Promise<void>;
  /**
   * Approval gate for sensitive methods (sign/send/disconnect/switchNetwork).
   * Required for those methods — omitting it fails closed (USER_REJECTED).
   * Return false or throw to reject. Hosts SHOULD show a confirmation UI.
   */
  onApprove?: (args: { method: string; params?: unknown }) => boolean | Promise<boolean>;
  /**
   * @deprecated Ignored — extension/non-in-page providers are never overwritten.
   */
  force?: boolean;
};

declare global {
  interface Window {
    splendor?: SplendorInjectedProvider;
  }
}

function asRecord(params: unknown): Record<string, unknown> {
  if (params && typeof params === 'object' && !Array.isArray(params)) {
    return params as Record<string, unknown>;
  }
  if (Array.isArray(params) && params[0] && typeof params[0] === 'object') {
    return params[0] as Record<string, unknown>;
  }
  return {};
}

function parseChainIdNumber(chainId: string): number {
  const n = Number.parseInt(String(chainId).replace(/\D/g, ''), 10);
  return Number.isFinite(n) ? n : 0;
}

const SENSITIVE = new Set([
  'splendor_signMessage',
  'splendor_signTransaction',
  'splendor_sendTransaction',
  'splendor_disconnect',
  'splendor_switchNetwork',
]);

/**
 * Create an in-page SQC provider bound to a Wallet (+ optional Connection).
 */
export function createSplendorProvider(
  options: InstallSplendorProviderOptions,
): SplendorInjectedProvider {
  const { wallet } = options;
  let networkId = options.networkId ?? wallet.networkId ?? 'local';
  const listeners = new Map<string, Set<Handler>>();
  const unsubs: Array<() => void> = [];

  const emit = (event: string, ...args: unknown[]) => {
    const set = listeners.get(event);
    if (!set) return;
    for (const h of set) {
      try {
        h(...args);
      } catch {
        /* ignore */
      }
    }
  };

  unsubs.push(
    wallet.on('connect', (p) => emit('connect', p)),
    wallet.on('disconnect', (p) => emit('disconnect', p)),
    wallet.on('accountsChanged', (p) => emit('accountsChanged', p)),
    wallet.on('chainChanged', (p) => {
      networkId = p.networkId;
      emit('chainChanged', p);
    }),
    wallet.on('lock', (p) => emit('lock', p)),
    wallet.on('error', (p) => emit('error', p)),
  );

  const approve = async (method: string, params?: unknown) => {
    if (!SENSITIVE.has(method)) return;
    if (!options.onApprove) {
      throw BridgeError.userRejected(
        `Request rejected: ${method} (no onApprove — silent inject signing disabled)`,
      );
    }
    const ok = await options.onApprove({ method, params });
    if (!ok) throw BridgeError.userRejected(`Request rejected: ${method}`);
  };

  const provider: SplendorInjectedProvider = {
    isSplendor: true,
    isInPage: true,
    on(event, handler) {
      let set = listeners.get(event);
      if (!set) {
        set = new Set();
        listeners.set(event, set);
      }
      set.add(handler);
    },
    removeListener(event, handler) {
      listeners.get(event)?.delete(handler);
    },
    once(event, handler) {
      const wrap: Handler = (...args) => {
        provider.removeListener(event, wrap);
        handler(...args);
      };
      provider.on(event, wrap);
    },
    async request({ method, params }) {
      const p = asRecord(params);
      await approve(method, params);
      switch (method) {
        case 'splendor_accounts': {
          return wallet.getAddresses();
        }
        case 'splendor_chainId': {
          const net = getNetwork(networkId);
          const chainIdStr = net?.chainId ?? wallet.connection.chainId;
          return {
            chainId: chainIdStr,
            chainIdNumber: parseChainIdNumber(chainIdStr),
            networkId,
          };
        }
        case 'splendor_connect': {
          if (wallet.account) return wallet.getAddresses();
          const mode = (p.mode as string) || 'metamask';
          // Never accept key material via injected API (XSS / silent theft).
          if (
            mode === 'seed' ||
            mode === 'evmKey' ||
            mode === 'evmSignature' ||
            p.seed != null ||
            p.privateKey != null ||
            p.signature != null
          ) {
            throw BridgeError.userRejected(
              'splendor_connect refuses seed/privateKey/evmSignature — unlock via ConnectKit UI',
            );
          }
          if (mode === 'metamask') {
            await wallet.connect({ mode: 'metamask' });
          } else {
            throw BridgeError.rpc(
              `splendor_connect supports metamask only via inject (got ${String(mode)})`,
            );
          }
          return wallet.getAddresses();
        }
        case 'splendor_disconnect': {
          wallet.disconnect();
          return null;
        }
        case 'splendor_signMessage': {
          const message =
            typeof p.message === 'string'
              ? p.message
              : typeof params === 'string'
                ? params
                : Array.isArray(params) && typeof params[0] === 'string'
                  ? params[0]
                  : null;
          if (message == null) {
            throw BridgeError.rpc('splendor_signMessage requires params.message');
          }
          if (p.raw === true) {
            throw BridgeError.userRejected(
              'splendor_signMessage rejects raw:true — domain separation required',
            );
          }
          return wallet.signMessage(message);
        }
        case 'splendor_signTransaction': {
          const tx = (p.tx ?? p) as UnsignedTx;
          if (!tx || typeof tx !== 'object' || !('type' in tx)) {
            throw BridgeError.rpc('splendor_signTransaction requires params.tx with type');
          }
          return wallet.signTransaction(tx);
        }
        case 'splendor_sendTransaction': {
          const tx = (p.tx ?? p) as UnsignedTx | SignedTx;
          if (!tx || typeof tx !== 'object') {
            throw BridgeError.rpc('splendor_sendTransaction requires params.tx');
          }
          return wallet.sendTransaction(tx, {
            waitForConfirmation: p.waitForConfirmation === true,
            confirmTimeoutMs:
              typeof p.confirmTimeoutMs === 'number' ? p.confirmTimeoutMs : undefined,
          });
        }
        case 'splendor_switchNetwork': {
          const id = String(p.networkId ?? p.id ?? '');
          if (!id) throw BridgeError.rpc('splendor_switchNetwork requires params.networkId');
          if (!options.onSwitchNetwork) {
            throw BridgeError.rpc(
              'splendor_switchNetwork requires onSwitchNetwork callback (refuses desynced local-only switch)',
            );
          }
          try {
            resolveNetwork(id as NetworkId, { allowPlaceholder: true });
          } catch (e) {
            if (!getNetwork(id)) throw e;
          }
          await options.onSwitchNetwork(id);
          networkId = id;
          return provider.request({ method: 'splendor_chainId' });
        }
        default:
          throw BridgeError.rpc(
            `Unsupported method "${method}" — SQC provider uses splendor_* methods (not eth_*)`,
          );
      }
    },
  };

  (provider as SplendorInjectedProvider & { _dispose?: () => void })._dispose = () => {
    for (const u of unsubs) u();
    unsubs.length = 0;
    listeners.clear();
  };

  return provider;
}

/**
 * Install `window.splendor` bound to the given wallet session.
 * Never overwrites an existing non-in-page (extension) provider.
 */
export function installSplendorProvider(
  options: InstallSplendorProviderOptions,
): SplendorInjectedProvider | null {
  if (typeof window === 'undefined') return null;

  const existing = window.splendor;
  if (existing?.isSplendor && !existing.isInPage) {
    // Extension owns the inject — do not replace (force ignored).
    return existing;
  }
  if (existing?.isSplendor && existing.isInPage) {
    // Replace prior in-page install with fresh wallet binding.
    uninstallSplendorProvider();
  }

  const provider = createSplendorProvider(options);
  window.splendor = provider;
  return provider;
}

/** Remove an in-page provider previously installed by this SDK. */
export function uninstallSplendorProvider(): void {
  if (typeof window === 'undefined') return;
  const p = window.splendor as (SplendorInjectedProvider & { _dispose?: () => void }) | undefined;
  if (p?.isInPage) {
    p._dispose?.();
    if (window.splendor === p) delete window.splendor;
  }
}

export function getSplendorProvider(): SplendorInjectedProvider | undefined {
  if (typeof window === 'undefined') return undefined;
  return window.splendor;
}

/**
 * SQC injected provider — `window.splendor` (NOT Ethereum EIP-1193 / eth_*).
 *
 * Methods use the `splendor_*` prefix. Prefer an already-injected extension
 * provider; in-page install only when absent (or force: true).
 */
import type { Connection } from './core/connection.js';
import type { SplendorPQBridge } from './splendor-wallet.js';
import type { UnsignedTx, SignedTx, WalletConnectOpts } from './types.js';
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
  /**
   * Network id for splendor_chainId / switchNetwork responses.
   * Defaults to wallet opts networkId or connection-derived lookup.
   */
  networkId?: string;
  /**
   * Optional soft-switch callback (ConnectKit). When omitted,
   * splendor_switchNetwork only validates and emits chainChanged locally.
   */
  onSwitchNetwork?: (networkId: string) => Promise<void>;
  /**
   * When true, replace an existing window.splendor (dangerous — prefer false).
   * Default false: keep extension inject if present.
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

  // Bridge wallet events → provider events
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
          const mode = (p.mode as WalletConnectOpts['mode']) || 'seed';
          if (mode === 'seed') {
            if (typeof p.seed !== 'string') {
              throw BridgeError.rpc('splendor_connect seed mode requires params.seed');
            }
            await wallet.connect({ mode: 'seed', seed: p.seed });
          } else if (mode === 'metamask') {
            await wallet.connect({ mode: 'metamask' });
          } else if (mode === 'evmKey') {
            if (typeof p.privateKey !== 'string') {
              throw BridgeError.rpc('splendor_connect evmKey mode requires params.privateKey');
            }
            await wallet.connect({ mode: 'evmKey', privateKey: p.privateKey });
          } else if (mode === 'evmSignature') {
            if (typeof p.signature !== 'string') {
              throw BridgeError.rpc('splendor_connect evmSignature mode requires params.signature');
            }
            await wallet.connect({
              mode: 'evmSignature',
              signature: p.signature,
              evmAddress: typeof p.evmAddress === 'string' ? p.evmAddress : undefined,
            });
          } else {
            throw BridgeError.rpc(`splendor_connect unknown mode: ${String(mode)}`);
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
          return wallet.signMessage(message, {
            raw: p.raw === true,
          });
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
          // Validate resolvable when possible
          try {
            resolveNetwork(id as NetworkId, { allowPlaceholder: true });
          } catch (e) {
            if (!getNetwork(id)) throw e;
          }
          if (options.onSwitchNetwork) {
            await options.onSwitchNetwork(id);
          } else {
            networkId = id;
            const net = getNetwork(id);
            wallet.notifyChainChanged({
              chainId: net ? parseChainIdNumber(net.chainId) : 0,
              networkId: id,
            });
          }
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

  // Attach dispose for tests (not part of public surface)
  (provider as SplendorInjectedProvider & { _dispose?: () => void })._dispose = () => {
    for (const u of unsubs) u();
    unsubs.length = 0;
    listeners.clear();
  };

  return provider;
}

/**
 * Install `window.splendor` bound to the given wallet session.
 * Skips install if an extension (or other) provider is already present unless `force`.
 */
export function installSplendorProvider(
  options: InstallSplendorProviderOptions,
): SplendorInjectedProvider | null {
  if (typeof window === 'undefined') return null;

  const existing = window.splendor;
  if (existing?.isSplendor && !options.force) {
    // Prefer extension / prior inject — do not double-bind.
    return existing;
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

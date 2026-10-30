/**
 * Subpath: @splendor/connect-react/testing
 *
 * Test doubles for apps built on connect-react. No WASM, no RPC, no test-runner
 * dependency: mocks record calls in plain arrays you can assert on.
 *
 *   const ctrl = createMockConnectController({ status: 'connected', balance: '12.5' });
 *   render(<MockSplendorProvider controller={ctrl}><TransferWidget /></MockSplendorProvider>);
 *   expect(ctrl.wallet.calls[0].method).toBe('sendTransfer');
 */
import { useMemo, useSyncExternalStore, type ReactNode } from 'react';
import type { DeriveInfo, NetworkConfig, NetworkId } from '@splendor/sqc-sdk';
import { getNetwork } from '@splendor/sqc-sdk/networks';
import type { Connector } from './connectors/types.js';
import type { ConnectController } from './core/controller.js';
import type { ConnectAccount, ConnectControllerState, ConnectionStatus } from './core/types.js';
import {
  BrandContext,
  ControllerContext,
  HostModalsContext,
  NetworkSwitchContext,
  type ConnectBrandLabels,
} from './react/context.js';
import { resolveThemeAttr, themeOverridesToStyle, type ThemeProp } from './theme/themes.js';

export const MOCK_ADDRESS = 'SQ:' + 'ab'.repeat(32);
const MOCK_HASH = 'cd'.repeat(32);

export type MockCall = { method: string; args: unknown[] };

export type MockWalletOptions = {
  address?: string;
  /** Make a method reject with this error. */
  fail?: Partial<Record<string, Error>>;
  /** Hash returned by sends (64 hex), or a function returning a fresh one per send. */
  hash?: string | (() => string);
  /** When false, sends return no confirmation, so they land in the pending list. Default true. */
  confirm?: boolean;
  /** Answers `connection.getTxReceipt`. Default: `{ status: 'success' }`. */
  receipt?: (hash: string) => unknown;
  /** Called after each successful send (`sendTransfer`, `sendModuleCall`, …) with its result. */
  onSend?: (call: MockCall, result: { status: string; hash: string; signedTx: { nonce: number } }) => void;
  /** Answers `connection.moduleCall` (token reads etc.). */
  moduleCall?: (envelope: { module: string; action: string; params: Record<string, unknown> }) => unknown;
  /** Answers `connection.call` (read-only contract calls). Default: empty return data. */
  call?: (req: { to: string; data: string; from?: string }) => unknown;
  /** Answers `connection.estimateGas`. Default: fee 1000 atto, floor. */
  estimate?: { fee: string; fee_is_floor?: boolean; gas?: number; reverted?: boolean; revert_reason?: string };
  /** Raw (atto) balance for `connection.getBalance`. */
  balanceRaw?: string | (() => string);
  /** Human SPLD balance for `connection.getBalance`. */
  balanceSPLD?: string | (() => string);
  /** Chain head reported by `connection.getStatus`. Default 1. */
  height?: number | (() => number);
  /** Answers `connection.getLogs` with the logs matching `filter`. Default: none. */
  logs?: (filter: { address?: string; from_block?: number; to_block?: number; topics?: string[] }) => unknown[];
};

const value = (v: string | (() => string) | undefined, fallback: string): string =>
  typeof v === 'function' ? v() : (v ?? fallback);

export type MockWallet = {
  calls: MockCall[];
  connection: Record<string, (...args: never[]) => Promise<unknown>>;
  [method: string]: unknown;
};

function infoFor(address: string): DeriveInfo {
  return {
    address,
    public_key_b64: 'bW9jaw==',
    public_key_hex: '6d6f636b',
    postQuantumSecure: true,
    keyOrigin: 'native',
  } as DeriveInfo;
}

/** A wallet whose signing methods resolve immediately and are recorded in `calls`. */
export function mockWallet(opts: MockWalletOptions = {}): MockWallet {
  const calls: MockCall[] = [];
  const address = opts.address ?? MOCK_ADDRESS;
  const method =
    <T,>(name: string, result: (...args: unknown[]) => T) =>
    async (...args: unknown[]): Promise<T> => {
      calls.push({ method: name, args });
      const err = opts.fail?.[name];
      if (err) throw err;
      return result(...args);
    };
  // Each send uses the next account nonce, like the chain, so predicted deploy addresses differ.
  let nonce = 0;
  const sender =
    <T extends object>(name: string, extra: () => T = () => ({}) as T) =>
    async (...args: unknown[]) => {
      const call = { method: name, args };
      calls.push(call);
      const err = opts.fail?.[name];
      if (err) throw err;
      const hash = typeof opts.hash === 'function' ? opts.hash() : (opts.hash ?? MOCK_HASH);
      const res = {
        status: 'ok',
        hash,
        signedTx: { nonce: nonce++ },
        ...(opts.confirm === false ? {} : { confirmation: { ok: true } }),
        ...extra(),
      };
      opts.onSend?.(call, res);
      return res;
    };
  const balance = () => ({ balance: value(opts.balanceRaw, '0'), balance_spld: value(opts.balanceSPLD, '0') });
  const connection = {
    getBalance: method('connection.getBalance', balance),
    getNonce: method('connection.getNonce', () => ({ nonce })),
    getTxReceipt: method('connection.getTxReceipt', (h) => (opts.receipt ? opts.receipt(String(h)) : { status: 'success' })),
    estimateGas: method('connection.estimateGas', () => ({
      gas: 0,
      fee_is_floor: true,
      ...(opts.estimate ?? { fee: '1000' }),
    })),
    moduleCall: method('connection.moduleCall', (env) =>
      opts.moduleCall ? opts.moduleCall(env as never) : {},
    ),
    call: method('connection.call', (req) => (opts.call ? opts.call(req as never) : { return_data: '0x' })),
    getStatus: method('connection.getStatus', () => ({
      height: typeof opts.height === 'function' ? opts.height() : (opts.height ?? 1),
    })),
    getLogs: method('connection.getLogs', (filter) => {
      const f = (filter ?? {}) as { from_block?: number; to_block?: number };
      const logs = opts.logs ? opts.logs(filter as never) : [];
      return { logs, count: logs.length, from_block: f.from_block ?? 0, to_block: f.to_block ?? 0, truncated: false };
    }),
  };
  return {
    calls,
    connection,
    getAddress: () => address,
    getNonce: method('getNonce', () => ({ address, nonce })),
    getLatestBlock: method('getLatestBlock', () => ({ height: 1 })),
    sendTransfer: sender('sendTransfer'),
    callContract: sender('callContract'),
    deployContract: sender('deployContract', () => ({ address: 'SQ:' + '77'.repeat(32) })),
    sendModuleCall: sender('sendModuleCall'),
    // x402: one settled payment per call, reported to onSend like any other send.
    payX402: async (...args: unknown[]) => {
      const call = { method: 'payX402', args };
      calls.push(call);
      const err = opts.fail?.payX402;
      if (err) throw err;
      const o = (args[0] ?? {}) as { to?: string; amount?: string; maxAmount?: string };
      if (o.maxAmount != null && o.amount != null && BigInt(o.amount) > BigInt(o.maxAmount)) {
        throw new Error(`x402 amount ${o.amount} exceeds maxAmount ${o.maxAmount}`);
      }
      const hash = typeof opts.hash === 'function' ? opts.hash() : (opts.hash ?? MOCK_HASH);
      opts.onSend?.(call, { status: 'ok', hash, signedTx: { nonce: nonce++ } });
      return {
        payload: { version: 1, from: address, to: o.to, amount: o.amount, nonce: hash.slice(0, 32), mock: true },
        verify: { valid: true },
        settle: { success: true, status: 'confirmed', tx_hash: hash },
      };
    },
    signMessage: method('signMessage', (message) => ({
      message,
      signatureB64: 'c2ln',
      publicKeyB64: 'bW9jaw==',
      address,
    })),
    getDeriveInfo: () => infoFor(address),
    getConnectionState: () => 'connected',
    notifyChainChanged: () => undefined,
    disconnect: () => undefined,
  };
}

/** A connector that connects instantly to `address` (or fails with `fail`). */
export function mockConnector(opts: { id?: string; name?: string; address?: string; fail?: Error } = {}): Connector {
  const address = opts.address ?? MOCK_ADDRESS;
  return {
    id: opts.id ?? 'mock',
    name: opts.name ?? 'Mock wallet',
    keyOrigin: 'native',
    postQuantumSecure: true,
    async ready() {
      return true;
    },
    async connect() {
      if (opts.fail) throw opts.fail;
      return infoFor(address);
    },
    async disconnect() {
      /* nothing to release */
    },
  } as unknown as Connector;
}

export type MockConnectControllerOptions = {
  status?: ConnectionStatus;
  account?: Partial<ConnectAccount> & { address?: string };
  /** Human SPLD balance reported by the provider, or a function read on every fetch. */
  balance?: string | (() => string);
  network?: NetworkId | NetworkConfig;
  connectors?: Connector[];
  wallet?: MockWallet;
  walletChainId?: string | null;
};

export type MockConnectController = ConnectController & {
  wallet: MockWallet;
  /** Merge a state patch and notify subscribers. */
  setState(patch: Partial<ConnectControllerState>): void;
};

function resolveNet(n: NetworkId | NetworkConfig | undefined): NetworkConfig {
  if (n && typeof n === 'object') return n;
  const net = getNetwork(String(n ?? 'local'));
  if (!net) throw new Error(`@splendor/connect-react/testing: unknown network ${String(n)}`);
  return net;
}

/** In-memory controller: drives every hook and widget without WASM or RPC. */
export function createMockConnectController(opts: MockConnectControllerOptions = {}): MockConnectController {
  const status = opts.status ?? 'connected';
  const address = opts.account?.address ?? MOCK_ADDRESS;
  const wallet = opts.wallet ?? mockWallet({ address, balanceSPLD: opts.balance });
  const connected = status === 'connected' || status === 'locked';
  const connectors = opts.connectors ?? [mockConnector({ address })];
  let state: ConnectControllerState = {
    status,
    connectingStep: null,
    account: connected ? ({ address, info: infoFor(address), ...opts.account } as ConnectAccount) : null,
    error: null,
    activeConnectorId: connected ? (connectors[0]?.id ?? 'mock') : null,
    modal: 'closed',
    connectors,
    network: resolveNet(opts.network),
    walletChainId: opts.walletChainId ?? null,
    signRequest: null,
  };
  const listeners = new Set<() => void>();
  const setState = (patch: Partial<ConnectControllerState>) => {
    state = { ...state, ...patch };
    for (const l of listeners) l();
  };
  const provider = {
    getBalance: async () => ({ balance: '0', balance_spld: value(opts.balance, '0') }),
  };
  let epoch = 0;
  const ctrl: MockConnectController = {
    wallet,
    setState,
    client: {} as ConnectController['client'],
    isStub: false,
    getState: () => state,
    subscribe(l) {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    openModal: (view = 'connect') => setState({ modal: view }),
    closeModal: () => setState({ modal: 'closed' }),
    clearError: () => setState({ error: null }),
    async connect(connectorId) {
      const c = state.connectors.find((x) => x.id === connectorId);
      if (!c) throw new Error(`Unknown connector ${connectorId}`);
      setState({ status: 'connecting' });
      try {
        const info = await c.connect({} as never);
        epoch += 1;
        setState({
          status: 'connected',
          account: { address: info.address, info },
          activeConnectorId: connectorId,
          modal: 'closed',
        });
        return info;
      } catch (e) {
        setState({ status: 'error', error: e instanceof Error ? e : new Error(String(e)) });
        throw e;
      }
    },
    async disconnect() {
      epoch += 1;
      setState({ status: 'disconnected', account: null, activeConnectorId: null, modal: 'closed' });
    },
    async softDisconnect() {
      await ctrl.disconnect();
    },
    async tryAutoReconnect() {
      return false;
    },
    getWallet: () => (state.status === 'connected' ? wallet : { ...wallet, getConnectionState: () => 'disconnected' }) as never,
    getProvider: () => provider as never,
    getNetwork: () => state.network!,
    setSessionWallet: () => undefined,
    async rebindNetwork(id) {
      setState({ network: resolveNet(id) });
    },
    getWalletEpoch: () => epoch,
    resolveSignRequest: () => setState({ signRequest: null }),
    attachSignApprovalUi: () => () => undefined,
    setSignApprover: () => undefined,
    destroy: () => listeners.clear(),
  };
  return ctrl;
}

export type MockSplendorProviderProps = {
  children: ReactNode;
  controller?: MockConnectController;
  /** Shortcut for `createMockConnectController(options)` when no controller is passed. */
  options?: MockConnectControllerOptions;
  labels?: ConnectBrandLabels;
  allowPlaceholderNetworks?: boolean;
  /** Same as `SplendorConnectProvider`'s `theme`. Default `splendor`. */
  theme?: ThemeProp;
};

/** Drop-in replacement for `SplendorConnectProvider` in unit tests and Storybook. */
export function MockSplendorProvider({
  children,
  controller,
  options,
  labels,
  allowPlaceholderNetworks = true,
  theme = 'splendor',
}: MockSplendorProviderProps) {
  const ctrl = useMemo(() => controller ?? createMockConnectController(options), [controller, options]);
  const network = useSyncExternalStore(
    (l) => ctrl.subscribe(l),
    () => ctrl.getState().network,
    () => ctrl.getState().network,
  );
  const networkCtx = useMemo(
    () => ({
      switchNetwork: (id: NetworkId) => ctrl.rebindNetwork(id),
      allowPlaceholderNetworks,
      networkId: (network?.id ?? 'local') as NetworkId,
      switching: false,
    }),
    [ctrl, network, allowPlaceholderNetworks],
  );
  return (
    <ControllerContext.Provider value={ctrl}>
      <BrandContext.Provider value={labels ?? {}}>
        <HostModalsContext.Provider value={false}>
          <NetworkSwitchContext.Provider value={networkCtx}>
            <div data-sqc-theme={resolveThemeAttr(theme)} style={themeOverridesToStyle(theme)} className="sqc-root">
              {children}
            </div>
          </NetworkSwitchContext.Provider>
        </HostModalsContext.Provider>
      </BrandContext.Provider>
    </ControllerContext.Provider>
  );
}

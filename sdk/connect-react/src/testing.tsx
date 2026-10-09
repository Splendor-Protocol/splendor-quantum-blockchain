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

export const MOCK_ADDRESS = 'SQ:' + 'ab'.repeat(32);
const MOCK_HASH = 'cd'.repeat(32);

export type MockCall = { method: string; args: unknown[] };

export type MockWalletOptions = {
  address?: string;
  /** Make a method reject with this error. */
  fail?: Partial<Record<string, Error>>;
  /** Hash returned by sends (64 hex). */
  hash?: string;
  /** Answers `connection.moduleCall` (token reads etc.). */
  moduleCall?: (envelope: { module: string; action: string; params: Record<string, unknown> }) => unknown;
  /** Answers `connection.estimateGas`. Default: fee 1000 atto, floor. */
  estimate?: { fee: string; fee_is_floor?: boolean; gas?: number; reverted?: boolean; revert_reason?: string };
  /** Raw (atto) balance for `connection.getBalance`. */
  balanceRaw?: string;
  /** Human SPLD balance for `connection.getBalance`. */
  balanceSPLD?: string;
};

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
  const hash = opts.hash ?? MOCK_HASH;
  const address = opts.address ?? MOCK_ADDRESS;
  const method =
    <T,>(name: string, result: (...args: unknown[]) => T) =>
    async (...args: unknown[]): Promise<T> => {
      calls.push({ method: name, args });
      const err = opts.fail?.[name];
      if (err) throw err;
      return result(...args);
    };
  const sent = () => ({ status: 'ok', hash, confirmation: { ok: true } });
  const balance = () => ({ balance: opts.balanceRaw ?? '0', balance_spld: opts.balanceSPLD ?? '0' });
  const connection = {
    getBalance: method('connection.getBalance', balance),
    getNonce: method('connection.getNonce', () => ({ nonce: 0 })),
    getTxReceipt: method('connection.getTxReceipt', () => ({ status: 'success' })),
    estimateGas: method('connection.estimateGas', () => ({
      gas: 0,
      fee_is_floor: true,
      ...(opts.estimate ?? { fee: '1000' }),
    })),
    moduleCall: method('connection.moduleCall', (env) =>
      opts.moduleCall ? opts.moduleCall(env as never) : {},
    ),
    call: method('connection.call', () => ({ return_data: '0x' })),
  };
  return {
    calls,
    connection,
    getAddress: () => address,
    getNonce: method('getNonce', () => ({ address, nonce: 0 })),
    getLatestBlock: method('getLatestBlock', () => ({ height: 1 })),
    sendTransfer: method('sendTransfer', sent),
    callContract: method('callContract', sent),
    deployContract: method('deployContract', () => ({ ...sent(), address: 'SQ:' + '77'.repeat(32) })),
    sendModuleCall: method('sendModuleCall', sent),
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
  /** Human SPLD balance reported by the provider. */
  balance?: string;
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
    getBalance: async () => ({ balance: '0', balance_spld: opts.balance ?? '0' }),
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
};

/** Drop-in replacement for `SplendorConnectProvider` in unit tests and Storybook. */
export function MockSplendorProvider({
  children,
  controller,
  options,
  labels,
  allowPlaceholderNetworks = true,
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
            <div data-sqc-theme="splendor" className="sqc-root">
              {children}
            </div>
          </NetworkSwitchContext.Provider>
        </HostModalsContext.Provider>
      </BrandContext.Provider>
    </ControllerContext.Provider>
  );
}

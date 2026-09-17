/**
 * Beginner-friendly module registry — typed clients for first-party Programs.
 *
 * Unbound (advanced): `modules.qrc20(connection, addr).transfer(wallet, to, amt)`
 * Bound (beginner):   `modules.bind(connection, wallet).qrc20(addr).transfer(to, amt)`
 *
 * Automatic native clients: qrc20, qrc1155, bridge, contract.
 * Routed on-chain modules without a dedicated Program client (use sendModuleCall):
 * verify, validators, qrc721. Authority: vm.KnownModuleActions / DispatchModuleCall.
 *
 * DEX and exchange leftover clients are unsupported application adapters.
 * They are not routed by the node and are not registered until
 * registerUnsupportedApplicationAdapters() is called.
 *
 * Addons: `registerModule({ id, create })` then use `createModuleClient` / `bindRegisteredModules`.
 */
import type { Connection } from './core/connection.js';
import {
  registerModule,
  defineModule,
  bindRegisteredModules,
  listModules,
  unregisterModule,
} from './core/module-registry.js';
import { getToken } from './tokens/qrc20.js';
import type { QRC20 } from './tokens/qrc20.js';
import { getCollection } from './tokens/qrc1155.js';
import type { QRC1155 } from './tokens/qrc1155.js';
import { getExchange } from './defi/exchange.js';
import type { Exchange } from './defi/exchange.js';
import { getPool } from './defi/dex.js';
import type { DEX } from './defi/dex.js';
import { getBridge } from './bridge/module.js';
import type { BridgeModule } from './bridge/module.js';
import { getContract } from './contracts/contract.js';
import type { Contract } from './contracts/contract.js';
import type { BoundClient, WalletLike } from './bound.js';

/** Matches vm.KnownModuleActions / DispatchModuleCall. */
export const ROUTED_NATIVE_MODULE_IDS = [
  'qrc20',
  'qrc1155',
  'bridge',
  'verify',
  'validators',
  'qrc721',
] as const;

export const UNSUPPORTED_APPLICATION_ADAPTER_IDS = ['dex', 'exchange'] as const;

export const UNSUPPORTED_ADAPTER_MESSAGE =
  'DEX and exchange are unsupported application adapters, not routed native modules. ' +
  'The node router only dispatches qrc20, qrc1155, bridge, verify, validators, and qrc721. ' +
  'Call registerUnsupportedApplicationAdapters() to opt in, or import clients from @splendor/sqc-sdk/defi.';

let builtInsRegistered = false;
let unsupportedAdaptersRegistered = false;

/** Idempotent registration of routed first-party Programs (not DEX/exchange). */
export function ensureBuiltInModulesRegistered(): void {
  if (builtInsRegistered) return;
  registerModule(defineModule({ id: 'qrc20', create: getToken, label: 'QRC-20' }));
  registerModule(defineModule({ id: 'qrc1155', create: getCollection, label: 'QRC-1155' }));
  registerModule(defineModule({ id: 'bridge', create: getBridge, label: 'Bridge' }));
  registerModule(defineModule({ id: 'contract', create: getContract, label: 'Contract' }));
  builtInsRegistered = true;
}

/**
 * Opt-in leftover DEX/exchange clients. These are not native chain modules;
 * DispatchModuleCall rejects them as unknown.
 */
export function registerUnsupportedApplicationAdapters(): void {
  if (unsupportedAdaptersRegistered) return;
  registerModule(
    defineModule({
      id: 'exchange',
      create: getExchange,
      label: 'Exchange (unsupported adapter)',
    }),
  );
  registerModule(
    defineModule({
      id: 'dex',
      create: getPool,
      label: 'DEX (unsupported adapter)',
    }),
  );
  unsupportedAdaptersRegistered = true;
}

export function unsupportedApplicationAdaptersEnabled(): boolean {
  return unsupportedAdaptersRegistered;
}

export function assertUnsupportedApplicationAdaptersEnabled(): void {
  if (!unsupportedAdaptersRegistered) {
    throw new Error(UNSUPPORTED_ADAPTER_MESSAGE);
  }
}

/** Test helper — drop adapter registration without clearing built-ins. */
export function _resetUnsupportedAdaptersForTests(): void {
  unsupportedAdaptersRegistered = false;
  unregisterModule('dex');
  unregisterModule('exchange');
}

ensureBuiltInModulesRegistered();

export const modules = {
  qrc20: (connection: Connection, address: string): QRC20 => getToken(connection, address),
  qrc1155: (connection: Connection, address: string): QRC1155 =>
    getCollection(connection, address),
  exchange: (connection: Connection, address: string): Exchange => {
    assertUnsupportedApplicationAdaptersEnabled();
    return getExchange(connection, address);
  },
  dex: (connection: Connection, address: string): DEX => {
    assertUnsupportedApplicationAdaptersEnabled();
    return getPool(connection, address);
  },
  bridge: (connection: Connection, address: string): BridgeModule =>
    getBridge(connection, address),
  contract: (connection: Connection, address: string): Contract =>
    getContract(connection, address),

  /**
   * Connection-bound (and optionally wallet-bound) factories — address-only lookup.
   * Built-ins stay typed; registered addons are available via `registered`.
   */
  bind(connection: Connection, wallet?: WalletLike | null): BoundModules {
    const maybeSign = <T extends { withSigner(w: WalletLike): BoundClient<T> }>(
      client: T,
    ): T | BoundClient<T> => (wallet ? client.withSigner(wallet) : client);

    return {
      qrc20: (address: string) => maybeSign(getToken(connection, address)),
      qrc1155: (address: string) => maybeSign(getCollection(connection, address)),
      exchange: (address: string) => {
        assertUnsupportedApplicationAdaptersEnabled();
        return maybeSign(getExchange(connection, address));
      },
      dex: (address: string) => {
        assertUnsupportedApplicationAdaptersEnabled();
        return maybeSign(getPool(connection, address));
      },
      bridge: (address: string) => maybeSign(getBridge(connection, address)),
      contract: (address: string) => maybeSign(getContract(connection, address)),
      /** All registered modules (built-ins + addons). */
      registered: bindRegisteredModules(connection, wallet),
      list: () => listModules().map((m) => m.id),
    };
  },
};

export type Modules = typeof modules;

export type BoundModules = {
  qrc20: (address: string) => QRC20 | BoundClient<QRC20>;
  qrc1155: (address: string) => QRC1155 | BoundClient<QRC1155>;
  exchange: (address: string) => Exchange | BoundClient<Exchange>;
  dex: (address: string) => DEX | BoundClient<DEX>;
  bridge: (address: string) => BridgeModule | BoundClient<BridgeModule>;
  contract: (address: string) => Contract | BoundClient<Contract>;
  registered: Record<string, (address: string) => unknown>;
  list: () => string[];
};

/** Session-bound modules always include a signer. */
export type SignedBoundModules = {
  qrc20: (address: string) => BoundClient<QRC20>;
  qrc1155: (address: string) => BoundClient<QRC1155>;
  exchange: (address: string) => BoundClient<Exchange>;
  dex: (address: string) => BoundClient<DEX>;
  bridge: (address: string) => BoundClient<BridgeModule>;
  contract: (address: string) => BoundClient<Contract>;
  registered: Record<string, (address: string) => unknown>;
  list: () => string[];
};

export function bindModules(
  connection: Connection,
  wallet: WalletLike,
): SignedBoundModules {
  return {
    qrc20: (address) => getToken(connection, address).withSigner(wallet),
    qrc1155: (address) => getCollection(connection, address).withSigner(wallet),
    exchange: (address) => {
      assertUnsupportedApplicationAdaptersEnabled();
      return getExchange(connection, address).withSigner(wallet);
    },
    dex: (address) => {
      assertUnsupportedApplicationAdaptersEnabled();
      return getPool(connection, address).withSigner(wallet);
    },
    bridge: (address) => getBridge(connection, address).withSigner(wallet),
    contract: (address) => getContract(connection, address).withSigner(wallet),
    registered: bindRegisteredModules(connection, wallet),
    list: () => listModules().map((m) => m.id),
  };
}

export {
  registerModule,
  defineModule,
  getModule,
  listModules,
  createModuleClient,
  bindRegisteredModules,
} from './core/module-registry.js';

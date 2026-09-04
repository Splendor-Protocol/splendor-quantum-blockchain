/**
 * Beginner-friendly module registry — typed clients for every native router module.
 *
 * Unbound (advanced): `modules.qrc20(connection, addr).transfer(wallet, to, amt)`
 * Bound (beginner):   `modules.bind(connection, wallet).qrc20(addr).transfer(to, amt)`
 *
 * Addons: `registerModule({ id, create })` then use `createModuleClient` / `bindRegisteredModules`.
 */
import type { Connection } from './core/connection.js';
import {
  registerModule,
  defineModule,
  bindRegisteredModules,
  listModules,
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

let builtInsRegistered = false;

/** Idempotent registration of first-party Programs. */
export function ensureBuiltInModulesRegistered(): void {
  if (builtInsRegistered) return;
  registerModule(defineModule({ id: 'qrc20', create: getToken, label: 'QRC-20' }));
  registerModule(defineModule({ id: 'qrc1155', create: getCollection, label: 'QRC-1155' }));
  registerModule(defineModule({ id: 'exchange', create: getExchange, label: 'Exchange' }));
  registerModule(defineModule({ id: 'dex', create: getPool, label: 'DEX' }));
  registerModule(defineModule({ id: 'bridge', create: getBridge, label: 'Bridge' }));
  registerModule(defineModule({ id: 'contract', create: getContract, label: 'Contract' }));
  builtInsRegistered = true;
}

ensureBuiltInModulesRegistered();

export const modules = {
  qrc20: (connection: Connection, address: string): QRC20 => getToken(connection, address),
  qrc1155: (connection: Connection, address: string): QRC1155 => getCollection(connection, address),
  exchange: (connection: Connection, address: string): Exchange => getExchange(connection, address),
  dex: (connection: Connection, address: string): DEX => getPool(connection, address),
  bridge: (connection: Connection, address: string): BridgeModule => getBridge(connection, address),
  contract: (connection: Connection, address: string): Contract => getContract(connection, address),

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
      exchange: (address: string) => maybeSign(getExchange(connection, address)),
      dex: (address: string) => maybeSign(getPool(connection, address)),
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
    exchange: (address) => getExchange(connection, address).withSigner(wallet),
    dex: (address) => getPool(connection, address).withSigner(wallet),
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

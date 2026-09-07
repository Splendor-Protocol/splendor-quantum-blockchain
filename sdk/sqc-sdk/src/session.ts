/**
 * createSession — thin DX helper for common dApp flows (less glue than raw connect()).
 *
 * Connect once → pick a module by address → call methods (wallet already bound).
 */
import { connect, type ConnectOptions, type ConnectResult } from './wallet/connect.js';
import { getToken, deployQRC20, type QRC20, type QRC20DeployParams } from './tokens/qrc20.js';
import { getCollection, deployQRC1155, type QRC1155, type QRC1155DeployParams } from './tokens/qrc1155.js';
import { getContract, deployBytecode, type Contract } from './contracts/contract.js';
import { getPool, deployPool, type DEX } from './defi/dex.js';
import { getExchange, deployExchange, type Exchange, type ExchangeDeployParams } from './defi/exchange.js';
import { getBridge, deployBridge, type BridgeModule, type BridgeDeployParams } from './bridge/module.js';
import { modules, bindModules, type SignedBoundModules } from './modules.js';
import type { BoundClient } from './bound.js';
import type { DeployOpts } from './types.js';

export type Session = ConnectResult & {
  /** Connected SQ: address (throws if not unlocked). */
  address: () => string;
  /** Native balance for the connected wallet. */
  getBalance: () => Promise<{
    address: string;
    balance: string;
    balance_spld: string;
    unit: string;
  }>;
  /** Send native SPLD (waits for confirmation by default). */
  sendSPLD: (
    to: string,
    amountSPLD: string,
    fee?: string,
  ) => ReturnType<ConnectResult['wallet']['sendTransfer']>;

  /** Wallet-bound QRC-20 (writes omit wallet arg). */
  token: (addr: string) => BoundClient<QRC20>;
  /** Alias of token(). */
  qrc20: (addr: string) => BoundClient<QRC20>;
  /** Wallet-bound QRC-1155. */
  collection: (addr: string) => BoundClient<QRC1155>;
  qrc1155: (addr: string) => BoundClient<QRC1155>;
  /** Wallet-bound DEX pool. */
  pool: (addr: string) => BoundClient<DEX>;
  dex: (addr: string) => BoundClient<DEX>;
  /** Wallet-bound Exchange. */
  exchange: (addr: string) => BoundClient<Exchange>;
  /** Wallet-bound Bridge module. */
  bridge: (addr: string) => BoundClient<BridgeModule>;
  /** Wallet-bound bytecode contract. */
  contract: (addr: string) => BoundClient<Contract>;

  /** Address-only module factories with signer bound. */
  modules: SignedBoundModules;
  /** Unbound global registry (advanced — still needs connection + wallet). */
  modulesUnbound: typeof modules;

  deployToken: (
    params: QRC20DeployParams,
    opts?: { fee?: string; waitForConfirmation?: boolean },
  ) => Promise<{ address: string; token: BoundClient<QRC20>; hash?: string }>;
  deployCollection: (
    params: QRC1155DeployParams,
    opts?: { fee?: string; waitForConfirmation?: boolean },
  ) => Promise<{ address: string; collection: BoundClient<QRC1155>; hash?: string }>;
  deployPool: (
    token0: string,
    token1: string,
    opts?: { fee?: string; waitForConfirmation?: boolean },
  ) => Promise<{ address: string; pool: BoundClient<DEX>; hash?: string }>;
  deployExchange: (
    params: ExchangeDeployParams,
    opts?: { fee?: string; waitForConfirmation?: boolean; height?: number },
  ) => Promise<{
    address: string;
    exchange: BoundClient<Exchange>;
    hash?: string;
    height: number;
    /** False means `address` is the next-height prediction, not the receipt's. */
    confirmed: boolean;
  }>;
  deployBridge: (
    params: BridgeDeployParams,
    opts?: { fee?: string; waitForConfirmation?: boolean; height?: number },
  ) => Promise<{
    address: string;
    bridge: BoundClient<BridgeModule>;
    splvAddress: string;
    hash?: string;
    /** False means `address` is the next-height prediction, not the receipt's. */
    confirmed: boolean;
  }>;
  deployBytecode: (
    bytecode: Uint8Array | string,
    opts?: Omit<DeployOpts, 'bytecode'>,
  ) => Promise<{ address: string; contract: BoundClient<Contract>; hash?: string }>;
};

/**
 * One-touch session with bound helpers for balance, SPLD, and every native module.
 *
 * ```ts
 * const s = await createSession({ network: 'local', mode: 'seed', seed });
 * await s.sendSPLD(to, '0.01');
 * await s.token(tokenAddr).transfer(to, '100');
 * await s.modules.dex(pool).swap({ tokenIn: 0, amountIn: '100', minAmountOut: '1' });
 * ```
 */
export async function createSession(opts: ConnectOptions = {}): Promise<Session> {
  const s = await connect(opts);
  const bound = (): SignedBoundModules => bindModules(s.connection, s.wallet);

  return {
    ...s,
    address: () => s.wallet.getAddress(),
    getBalance: () => s.connection.getBalance(s.wallet.getAddress()),
    sendSPLD: (to, amountSPLD, fee) =>
      s.wallet.sendTransfer({
        to,
        amountSPLD,
        fee,
        waitForConfirmation: true,
      }),

    token: (addr) => getToken(s.connection, addr).withSigner(s.wallet),
    qrc20: (addr) => getToken(s.connection, addr).withSigner(s.wallet),
    collection: (addr) => getCollection(s.connection, addr).withSigner(s.wallet),
    qrc1155: (addr) => getCollection(s.connection, addr).withSigner(s.wallet),
    pool: (addr) => getPool(s.connection, addr).withSigner(s.wallet),
    dex: (addr) => getPool(s.connection, addr).withSigner(s.wallet),
    exchange: (addr) => getExchange(s.connection, addr).withSigner(s.wallet),
    bridge: (addr) => getBridge(s.connection, addr).withSigner(s.wallet),
    contract: (addr) => getContract(s.connection, addr).withSigner(s.wallet),

    get modules() {
      return bound();
    },
    modulesUnbound: modules,

    async deployToken(params, deployOpts) {
      const { address, result } = await deployQRC20(s.wallet, params, deployOpts);
      return {
        address,
        token: getToken(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployCollection(params, deployOpts) {
      const { address, result } = await deployQRC1155(s.wallet, params, deployOpts);
      return {
        address,
        collection: getCollection(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployPool(token0, token1, deployOpts) {
      const { address, result } = await deployPool(s.wallet, token0, token1, deployOpts);
      return {
        address,
        pool: getPool(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployExchange(params, deployOpts) {
      const { address, height, confirmed, result } = await deployExchange(
        s.wallet,
        params,
        deployOpts,
      );
      return {
        address,
        height,
        confirmed,
        exchange: getExchange(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployBridge(params, deployOpts) {
      const { address, splvAddress, confirmed, result } = await deployBridge(
        s.wallet,
        params,
        deployOpts,
      );
      return {
        address,
        splvAddress,
        confirmed,
        bridge: getBridge(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployBytecode(bytecode, deployOpts) {
      const { address, result } = await deployBytecode(s.wallet, bytecode, deployOpts);
      return {
        address,
        contract: getContract(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
  };
}

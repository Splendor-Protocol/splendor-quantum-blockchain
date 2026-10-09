/**
 * createSession — thin DX helper for common dApp flows (less glue than raw connect()).
 *
 * Connect once → pick a module by address → call methods (wallet already bound).
 * Supported: QRC-20, QRC-1155, QRC-721, bridge, bytecode contract. DEX/exchange are not on
 * Session — import `@splendor/sqc-sdk/defi` for unsupported leftover adapters.
 */
import { connect, type ConnectOptions, type ConnectResult } from './wallet/connect.js';
import { getToken, deployQRC20, type QRC20, type QRC20DeployParams } from './tokens/qrc20.js';
import { getCollection, deployQRC1155, type QRC1155, type QRC1155DeployParams } from './tokens/qrc1155.js';
import { getNftCollection, deployQRC721, type QRC721, type QRC721DeployParams } from './tokens/qrc721.js';
import { getContract, deployBytecode, type Contract } from './contracts/contract.js';
import { getBridge, deployBridge, type BridgeModule, type BridgeDeployParams } from './bridge/module.js';
import { modules, bindModules, type SignedBoundModules } from './modules.js';
import type { BoundClient } from './bound.js';
import type { DeployOpts, WriteOpts } from './types.js';

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
  /** Wallet-bound QRC-721 collection. */
  nft: (addr: string) => BoundClient<QRC721>;
  qrc721: (addr: string) => BoundClient<QRC721>;
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
    opts?: WriteOpts,
  ) => Promise<{ address: string; token: BoundClient<QRC20>; hash?: string }>;
  deployCollection: (
    params: QRC1155DeployParams,
    opts?: WriteOpts,
  ) => Promise<{ address: string; collection: BoundClient<QRC1155>; hash?: string }>;
  deployNftCollection: (
    params: QRC721DeployParams,
    opts?: WriteOpts,
  ) => Promise<{ address: string; nft: BoundClient<QRC721>; hash?: string }>;
  deployBridge: (
    params: BridgeDeployParams,
    opts?: WriteOpts & { height?: number },
  ) => Promise<{
    address: string;
    bridge: BoundClient<BridgeModule>;
    splvAddress: string;
    hash?: string;
  }>;
  deployBytecode: (
    bytecode: Uint8Array | string,
    opts?: Omit<DeployOpts, 'bytecode'>,
  ) => Promise<{ address: string; contract: BoundClient<Contract>; hash?: string }>;
};

/**
 * One-touch session with bound helpers for balance, SPLD, and routed native modules.
 *
 * ```ts
 * const s = await createSession({ network: 'local', mode: 'seed', seed });
 * await s.sendSPLD(to, '0.01');
 * await s.token(tokenAddr).transfer(to, '100');
 * await s.bridge(bridgeAddr).claim({ ... });
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
    nft: (addr) => getNftCollection(s.connection, addr).withSigner(s.wallet),
    qrc721: (addr) => getNftCollection(s.connection, addr).withSigner(s.wallet),
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
    async deployNftCollection(params, deployOpts) {
      const { address, result } = await deployQRC721(s.wallet, params, deployOpts);
      return {
        address,
        nft: getNftCollection(s.connection, address).withSigner(s.wallet),
        hash: result.hash,
      };
    },
    async deployBridge(params, deployOpts) {
      const { address, splvAddress, result } = await deployBridge(s.wallet, params, deployOpts);
      return {
        address,
        splvAddress,
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

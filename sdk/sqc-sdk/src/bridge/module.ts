/**
 * On-chain legacy→quantum Bridge module client (`bridge_v1`).
 * Not to be confused with `@splendor/pq-bridge` (deprecated SDK shim).
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import { requireCapability, signerAccountNonce, signedTxNonce, writeOpts } from '../core/signer.js';
import { bridgeAddress, bridgeSplvAddress } from '../addresses/index.js';
import { assertAddress } from '../address.js';
import { BridgeError } from '../errors.js';
import type { WriteOpts } from '../types.js';

export type BridgeDeployParams = {
  evmChainId: number;
  evmContract: string;
  splvName: string;
  splvSymbol: string;
  splvMaxSupply: string;
  maxClaimAmount: string;
  threshold: number;
};

export type BridgeClaimParams = {
  id: number;
  asset: 'spld' | 'splv';
  amount: string;
  recipient: string;
  evmTx: string;
  attestations: Array<{ pubkey: string; signature: string }>;
};

function blockHeight(block: unknown): number {
  if (!block || typeof block !== 'object') return 0;
  const b = block as Record<string, unknown>;
  if (typeof b.height === 'number') return b.height;
  const header = b.header as Record<string, unknown> | undefined;
  if (header && typeof header.height === 'number') return header.height;
  return 0;
}

function safeInt(n: unknown, label: string, min = 0): number {
  if (typeof n !== 'number' || !Number.isSafeInteger(n) || n < min) {
    throw BridgeError.invalidParams(`${label} must be an integer >= ${min}`);
  }
  return n;
}

function uintString(v: unknown, label: string): string {
  const s = String(v ?? '').trim();
  if (!/^(0|[1-9]\d*)$/.test(s)) throw BridgeError.invalidAmount(`${label}: expected a non-negative integer string`);
  return s;
}

const BRIDGE_WRITES = new Set([
  'claim',
  'setOperator',
  'setThreshold',
  'pause',
  'unpause',
  'transferOwnership',
]);

export class BridgeModule extends Program {
  protected moduleName(): string {
    return 'bridge';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return BRIDGE_WRITES;
  }

  protected instanceParamKey(): string {
    return 'bridge';
  }

  static predictAddress(deployer: string, nonce: number, height: number): string {
    return bridgeAddress(deployer, nonce, height);
  }

  static splvAddress(bridge: string): string {
    return bridgeSplvAddress(bridge);
  }

  async info(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('bridge_info', {});
  }

  async claimed(id: number): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('claim_status', { id: safeInt(id, 'id') });
  }

  async claim(wallet: Signer, params: BridgeClaimParams, opts?: WriteOpts) {
    if (params.asset !== 'spld' && params.asset !== 'splv') {
      throw BridgeError.invalidParams("asset must be 'spld' or 'splv'");
    }
    if (!Array.isArray(params.attestations) || params.attestations.length === 0) {
      throw BridgeError.invalidParams('attestations are required');
    }
    return this.write(wallet, 'claim', {
      id: safeInt(params.id, 'id'),
      asset: params.asset,
      amount: uintString(params.amount, 'amount'),
      recipient: assertAddress(params.recipient),
      evm_tx: params.evmTx,
      attestations: params.attestations,
    }, opts);
  }

  async setOperator(wallet: Signer, operator: string, active: boolean, opts?: WriteOpts) {
    return this.write(wallet, 'set_operator', { operator: assertAddress(operator), active: active === true }, opts);
  }

  async setThreshold(wallet: Signer, threshold: number, opts?: WriteOpts) {
    return this.write(wallet, 'set_threshold', { threshold: safeInt(threshold, 'threshold', 1) }, opts);
  }

  async pause(wallet: Signer, opts?: WriteOpts) {
    return this.write(wallet, 'pause', {}, opts);
  }

  async unpause(wallet: Signer, opts?: WriteOpts) {
    return this.write(wallet, 'unpause', {}, opts);
  }

  async transferOwnership(wallet: Signer, newOwner: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer_ownership', { new_owner: assertAddress(newOwner) }, opts);
  }
}

export async function deployBridge(
  wallet: Signer,
  params: BridgeDeployParams,
  opts?: WriteOpts & { height?: number },
): Promise<{
  address: string;
  splvAddress: string;
  height: number;
  result: Awaited<ReturnType<Signer['sendModuleCall']>>;
}> {
  const nonce = await signerAccountNonce(wallet);
  let height = opts?.height;
  if (height == null) {
    const getLatestBlock = requireCapability(wallet, 'getLatestBlock');
    const latest = await getLatestBlock.call(wallet);
    height = blockHeight(latest) + 1;
  }
  const result = await wallet.sendModuleCall({
    module: 'bridge',
    action: 'deploy',
    params: {
      evm_chain_id: safeInt(params.evmChainId, 'evmChainId', 1),
      evm_contract: params.evmContract,
      splv_name: params.splvName,
      splv_symbol: params.splvSymbol,
      splv_max_supply: uintString(params.splvMaxSupply, 'splvMaxSupply'),
      max_claim_amount: uintString(params.maxClaimAmount, 'maxClaimAmount'),
      threshold: safeInt(params.threshold, 'threshold', 1),
    },
    ...writeOpts(opts),
  });
  const predicted = BridgeModule.predictAddress(wallet.getAddress(), signedTxNonce(result) ?? nonce, height);
  return {
    address: predicted,
    splvAddress: BridgeModule.splvAddress(predicted),
    height,
    result,
  };
}

export function getBridge(connection: Connection, address: string): BridgeModule {
  return new BridgeModule(connection, address);
}

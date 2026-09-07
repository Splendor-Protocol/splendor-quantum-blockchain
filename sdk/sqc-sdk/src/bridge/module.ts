/**
 * On-chain legacy→quantum Bridge module client (`bridge_v1`).
 * Not to be confused with `@splendor/pq-bridge` (deprecated SDK shim).
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import {
  confirmedBlockHeight,
  confirmedContractAddress,
  requireCapability,
  signerAccountNonce,
} from '../core/signer.js';
import { bridgeAddress, bridgeSplvAddress } from '../addresses/index.js';

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
    return this.read<Record<string, unknown>>('claim_status', { id });
  }

  async claim(wallet: Signer, params: BridgeClaimParams) {
    return this.write(wallet, 'claim', {
      id: params.id,
      asset: params.asset,
      amount: params.amount,
      recipient: params.recipient,
      evm_tx: params.evmTx,
      attestations: params.attestations,
    });
  }

  async setOperator(wallet: Signer, operator: string, active: boolean) {
    return this.write(wallet, 'set_operator', { operator, active });
  }

  async setThreshold(wallet: Signer, threshold: number) {
    return this.write(wallet, 'set_threshold', { threshold });
  }

  async pause(wallet: Signer) {
    return this.write(wallet, 'pause', {});
  }

  async unpause(wallet: Signer) {
    return this.write(wallet, 'unpause', {});
  }

  async transferOwnership(wallet: Signer, newOwner: string) {
    return this.write(wallet, 'transfer_ownership', { new_owner: newOwner });
  }
}

/**
 * Deploy a bridge instance (and its SPLV token).
 *
 * Both addresses depend on the height the deploy executes at. Before sending
 * they can only be predicted (next height); once confirmed, the receipt says
 * where the bridge really landed, and `address` / `splvAddress` are derived
 * from that. `confirmed` tells the two apart.
 */
export async function deployBridge(
  wallet: Signer,
  params: BridgeDeployParams,
  opts?: { fee?: string; waitForConfirmation?: boolean; height?: number },
): Promise<{
  /** The deployed bridge: from the receipt when confirmed, else predicted. */
  address: string;
  /** Its SPLV token, derived from `address`. */
  splvAddress: string;
  /** The next-height prediction made before sending. */
  predictedAddress: string;
  /** True when `address` came from the confirmed receipt. */
  confirmed: boolean;
  /** The height the deploy executed at (from the receipt), else the assumed one. */
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
  const predicted = BridgeModule.predictAddress(wallet.getAddress(), nonce, height);
  const result = await wallet.sendModuleCall({
    module: 'bridge',
    action: 'deploy',
    params: {
      evm_chain_id: params.evmChainId,
      evm_contract: params.evmContract,
      splv_name: params.splvName,
      splv_symbol: params.splvSymbol,
      splv_max_supply: params.splvMaxSupply,
      max_claim_amount: params.maxClaimAmount,
      threshold: params.threshold,
    },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  const confirmedAddress = confirmedContractAddress(result);
  const address = confirmedAddress ?? predicted;
  return {
    address,
    splvAddress: BridgeModule.splvAddress(address),
    predictedAddress: predicted,
    confirmed: confirmedAddress != null,
    height: confirmedBlockHeight(result) ?? height,
    result,
  };
}

export function getBridge(connection: Connection, address: string): BridgeModule {
  return new BridgeModule(connection, address);
}

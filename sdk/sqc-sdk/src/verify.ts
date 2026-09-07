/**
 * SQVL verify module client (`verify_v1`): dual-signed instructions, operator
 * attestations, settlement. Chain side: vm/verify.go, docs/modules/verify.md.
 *
 * Signing lives on the Wallet (`signVerifyInstruction`, `signVerifyAttestation`);
 * this Program only builds and submits the module calls and reads the proof.
 * The `/verify/*` HTTP doors are on `SqRpc` (verifyCheck / verifyProof /
 * verifyAttest / verifySettle) for integrators that speak one endpoint per step.
 */
import { Program } from './core/program.js';
import type { Connection } from './core/connection.js';
import type { Signer } from './core/signer.js';
import {
  confirmedBlockHeight,
  confirmedContractAddress,
  requireCapability,
  signerAccountNonce,
} from './core/signer.js';
import { verifyAddress } from './addresses/index.js';
import type { VerifyInstruction } from './types.js';

export type VerifyDeployParams = {
  /** 1 ≤ threshold ≤ 1 + operators.length (the owner is an attestor too). */
  threshold: number;
  operators?: string[];
};

/** The `attestation` object `verify.attest` takes — what `signVerifyAttestation` returns. */
export type VerifyAttestationInput = {
  pubkey: string;
  slh_pubkey: string;
  sig_mldsa: string;
  sig_slh: string;
};

function blockHeight(block: unknown): number {
  if (!block || typeof block !== 'object') return 0;
  const b = block as Record<string, unknown>;
  if (typeof b.height === 'number') return b.height;
  const header = b.header as Record<string, unknown> | undefined;
  if (header && typeof header.height === 'number') return header.height;
  return 0;
}

function instructionId(id: string): string {
  const hex = String(id).replace(/^0x/i, '').toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(hex)) {
    throw new Error(`instruction_id must be 64 hex characters, got ${JSON.stringify(id)}`);
  }
  return hex;
}

const VERIFY_WRITES = new Set([
  'register',
  'attest',
  'finalize',
  'spend',
  'setOperator',
  'setThreshold',
  'pause',
  'unpause',
]);

export class VerifyModule extends Program {
  protected moduleName(): string {
    return 'verify';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return VERIFY_WRITES;
  }

  protected instanceParamKey(): string {
    return 'verify';
  }

  static predictAddress(deployer: string, nonce: number, height: number): string {
    return verifyAddress(deployer, nonce, height);
  }

  /** Owner, operators, threshold, paused. */
  async instance(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('instance', {});
  }

  /** The on-chain record of one instruction: attestors, finalized, spent, … */
  async proof(id: string): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('proof', { instruction_id: instructionId(id) });
  }

  /** Register a dual-signed instruction (caller: its signer or an attestor). */
  async register(wallet: Signer, instruction: VerifyInstruction) {
    return this.write(wallet, 'register', {
      instruction: { ...instruction, instruction_id: instructionId(instruction.instruction_id) },
    });
  }

  /** Relay an operator's attestation (anyone may submit it). */
  async attest(wallet: Signer, id: string, attestation: VerifyAttestationInput) {
    return this.write(wallet, 'attest', {
      instruction_id: instructionId(id),
      attestation: {
        pubkey: attestation.pubkey,
        slh_pubkey: attestation.slh_pubkey,
        sig_mldsa: attestation.sig_mldsa,
        sig_slh: attestation.sig_slh,
      },
    });
  }

  async finalize(wallet: Signer, id: string) {
    return this.write(wallet, 'finalize', { instruction_id: instructionId(id) });
  }

  /** Settle once. For a value move the wallet must be the instruction's `from`. */
  async spend(wallet: Signer, id: string) {
    return this.write(wallet, 'spend', { instruction_id: instructionId(id) });
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
}

/**
 * Deploy a verify instance. Like the bridge, the address depends on the
 * height the deploy executes at: predicted before sending, read from the
 * receipt once confirmed (`confirmed` tells the two apart).
 */
export async function deployVerify(
  wallet: Signer,
  params: VerifyDeployParams,
  opts?: { fee?: string; waitForConfirmation?: boolean; height?: number },
): Promise<{
  address: string;
  predictedAddress: string;
  confirmed: boolean;
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
  const predicted = VerifyModule.predictAddress(wallet.getAddress(), nonce, height);
  const result = await wallet.sendModuleCall({
    module: 'verify',
    action: 'deploy',
    params: { threshold: params.threshold, operators: params.operators ?? [] },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  const confirmedAddress = confirmedContractAddress(result);
  const address = confirmedAddress ?? predicted;
  return {
    address,
    predictedAddress: predicted,
    confirmed: confirmedAddress != null,
    height: confirmedBlockHeight(result) ?? height,
    result,
  };
}

export function getVerify(connection: Connection, address: string): VerifyModule {
  return new VerifyModule(connection, address);
}

/**
 * Signer — Program/Contract write surface (+ optional domain-signing capabilities).
 * WASM stays inside Wallet; Programs depend on this interface, not SplendorPQBridge.
 */
import type {
  ModuleCallOpts,
  CallOpts,
  DeployOpts,
  PermitParams,
  SignedPermit,
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
  BridgeClaimSignParams,
  SignedBridgeClaim,
  VerifyInstructionInput,
  SignedVerifyInstruction,
  VerifyAttestationSignParams,
  SignedVerifyAttestation,
  X402PayOpts,
} from '../types.js';
import { BridgeError } from '../errors.js';
import { canonicalizeAddress } from '../address.js';

export type SignerAddress = { getAddress(): string };

export type TxResult = {
  status: string;
  hash: string;
  signedTx: object;
  confirmation?: { confirmed: true; nonce: number; receipt?: Record<string, unknown> };
};

/** Core submit surface required for Program.write / Contract.call. */
export type Signer = SignerAddress & {
  sendModuleCall(opts: ModuleCallOpts): Promise<TxResult>;
  callContract(opts: CallOpts): Promise<TxResult>;
  /** Optional domain capabilities — present on full Wallet. */
  signPermit?(params: PermitParams): Promise<SignedPermit>;
  signOrder?(params: OrderParams): Promise<SignedOrder>;
  signCancel?(params: OrderParams): Promise<SignedCancel>;
  signIncrementNonce?(params: IncrementNonceParams): Promise<SignedIncrementNonce>;
  signBridgeClaim?(params: BridgeClaimSignParams): Promise<SignedBridgeClaim>;
  signVerifyInstruction?(input: VerifyInstructionInput): Promise<SignedVerifyInstruction>;
  signVerifyAttestation?(params: VerifyAttestationSignParams): Promise<SignedVerifyAttestation>;
  payX402?(opts: X402PayOpts): Promise<unknown>;
  getNonce?(address?: string): Promise<{ address: string; nonce: number }>;
  getLatestBlock?(): Promise<unknown>;
  deployContract?(opts: DeployOpts): Promise<TxResult>;
};

export function requireCapability<K extends keyof Signer>(
  signer: Signer,
  name: K,
): NonNullable<Signer[K]> {
  const fn = signer[name];
  if (typeof fn !== 'function') {
    throw BridgeError.notConnected(
      `Signer is missing capability "${String(name)}" (need a full Wallet / WASM session)`,
    );
  }
  return fn as NonNullable<Signer[K]>;
}

export async function signerAccountNonce(wallet: Signer): Promise<number> {
  const getNonce = requireCapability(wallet, 'getNonce');
  const { nonce } = await getNonce.call(wallet);
  return nonce;
}

/**
 * The address a confirmed deploy actually created, read from the receipt the
 * confirmation carried. The node serialises `contract_address` as a 32-byte
 * array; a canonical `SQ:` string is accepted too. Undefined when the result
 * is unconfirmed or the receipt names no contract — a caller then only has a
 * prediction, and should say so.
 */
export function confirmedContractAddress(
  result: Pick<TxResult, 'confirmation'>,
): string | undefined {
  const raw = result.confirmation?.receipt?.contract_address;
  if (typeof raw === 'string') {
    return canonicalizeAddress(raw) ?? undefined;
  }
  if (
    Array.isArray(raw) &&
    raw.length === 32 &&
    raw.every((b) => Number.isInteger(b) && b >= 0 && b <= 255)
  ) {
    const hex = (raw as number[]).map((b) => b.toString(16).padStart(2, '0')).join('');
    return canonicalizeAddress('SQ:' + hex) ?? undefined;
  }
  return undefined;
}

/** The block height a confirmation's receipt reports, when it reports one. */
export function confirmedBlockHeight(result: Pick<TxResult, 'confirmation'>): number | undefined {
  const raw = result.confirmation?.receipt?.block_height;
  return typeof raw === 'number' && Number.isInteger(raw) && raw >= 0 ? raw : undefined;
}

export type MockSignerHandlers = {
  sendModuleCall?: Signer['sendModuleCall'];
  callContract?: Signer['callContract'];
};

/** In-memory Signer for Program unit tests (submit-only). */
export class MockSigner implements Signer {
  readonly address: string;
  private handlers: MockSignerHandlers;
  readonly calls: {
    sendModuleCall: ModuleCallOpts[];
    callContract: CallOpts[];
  } = { sendModuleCall: [], callContract: [] };

  constructor(address: string, handlers: MockSignerHandlers = {}) {
    this.address = address;
    this.handlers = handlers;
  }

  getAddress(): string {
    return this.address;
  }

  setHandlers(handlers: MockSignerHandlers): void {
    this.handlers = { ...this.handlers, ...handlers };
  }

  async sendModuleCall(opts: ModuleCallOpts) {
    this.calls.sendModuleCall.push(opts);
    if (this.handlers.sendModuleCall) return this.handlers.sendModuleCall(opts);
    return { status: 'ok', hash: '0x' + '11'.repeat(32), signedTx: {} };
  }

  async callContract(opts: CallOpts) {
    this.calls.callContract.push(opts);
    if (this.handlers.callContract) return this.handlers.callContract(opts);
    return { status: 'ok', hash: '0x' + '22'.repeat(32), signedTx: {} };
  }
}

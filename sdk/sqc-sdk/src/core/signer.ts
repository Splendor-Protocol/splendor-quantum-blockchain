/**
 * Signer — Program/Contract write surface (+ optional domain-signing capabilities).
 * WASM stays inside Wallet; Programs depend on this interface, not SplendorPQBridge.
 */
import type {
  ModuleCallOpts,
  WriteOpts,
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
  X402PayOpts,
} from '../types.js';
import { BridgeError } from '../errors.js';

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

/** Only the known write options, so caller extras can never override module/action/params. */
export function writeOpts(opts?: WriteOpts): WriteOpts {
  return {
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
    confirmTimeoutMs: opts?.confirmTimeoutMs,
    allowNonceConfirmation: opts?.allowNonceConfirmation,
  };
}

/**
 * Nonce actually signed into a submitted tx, read from its wire form. Null when the signer
 * (e.g. an extension) does not return the signed body.
 */
export function signedTxNonce(result: { signedTx?: object } | null | undefined): number | null {
  const raw = (result?.signedTx as { nonce?: unknown } | undefined)?.nonce;
  const n = typeof raw === 'string' && /^(0|[1-9]\d*)$/.test(raw) ? Number(raw) : raw;
  return typeof n === 'number' && Number.isSafeInteger(n) && n >= 0 ? n : null;
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

import type { Connection } from './core/connection.js';
import type { Transport } from './core/transport/types.js';
import { MODULE_ROUTER_ADDRESS } from './addresses/index.js';
import { buildModuleCall, type ModuleCall } from './core/module-call.js';
import { chainIo, type ChainIo } from './core/chain-io.js';
import { assertAddress } from './address.js';
import { BridgeError } from './errors.js';

const INSTRUCTION_ID_RE = /^[0-9a-fA-F]{64}$/;

export type VerifyInstruction = {
  version: number;
  instruction_id: string;
  purpose: string;
  origin: string;
  payload?: string;
  settlement_hint: string;
  settlement_action?: string;
  from?: string;
  to?: string;
  amount?: string;
  token?: string;
  mldsa_pubkey: string;
  sig_mldsa: string;
  slh_pubkey: string;
  sig_slh: string;
};

export class VerifyClient {
  private readonly io: ChainIo;

  constructor(source: Connection | Transport | ChainIo) {
    this.io = chainIo(source);
  }

  check(instruction: VerifyInstruction) {
    return this.io.post<{
      valid: boolean;
      reason?: string;
      digest?: string;
      signer?: string;
      instruction_id?: string;
      origin?: string;
      settlement_hint?: string;
      action?: string;
    }>('/verify/check', instruction, false);
  }

  async proof(instructionId: string, instance: string) {
    const id = String(instructionId ?? '').trim().replace(/^0x/i, '');
    if (!INSTRUCTION_ID_RE.test(id)) throw BridgeError.invalidParams('instruction id must be 32-byte hex');
    instance = assertAddress(instance);
    return this.io.get<Record<string, unknown>>(
      `/verify/proof/${encodeURIComponent(id)}?instance=${encodeURIComponent(instance)}`,
    );
  }

  attest(signedTx: object) {
    return this.io.post<{ status: string; hash: string }>(
      '/verify/attest',
      { tx: signedTx },
      true,
    );
  }

  settle(signedTx: object) {
    return this.io.post<{ status: string; hash: string }>(
      '/verify/settle',
      { tx: signedTx },
      true,
    );
  }

  moduleCall(action: string, params: Record<string, unknown>): ModuleCall {
    return buildModuleCall('verify', action, params);
  }

  routerAddress(): string {
    return MODULE_ROUTER_ADDRESS;
  }
}

/** Prefer `getVerify(connection)` — Transport-backed Connection. */
export function getVerify(source: Connection | Transport | ChainIo): VerifyClient {
  return new VerifyClient(source);
}

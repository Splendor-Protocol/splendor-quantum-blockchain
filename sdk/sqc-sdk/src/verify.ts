import type { Connection } from './core/connection.js';
import type { Transport } from './core/transport/types.js';
import { MODULE_ROUTER_ADDRESS } from './addresses/index.js';
import { buildModuleCall, type ModuleCall } from './core/module-call.js';

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

type ChainIo = Pick<Transport, 'get' | 'post'>;

function asIo(source: Connection | Transport | ChainIo): ChainIo {
  if ('transport' in source && source.transport && typeof source.transport.get === 'function') {
    return source.transport;
  }
  return source as ChainIo;
}

export class VerifyClient {
  private readonly io: ChainIo;

  constructor(source: Connection | Transport | ChainIo) {
    this.io = asIo(source);
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

  proof(instructionId: string, instance: string) {
    const id = String(instructionId).replace(/^0x/i, '');
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

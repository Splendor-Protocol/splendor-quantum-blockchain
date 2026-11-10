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

export type VerifyCheckResult = {
  valid: boolean;
  reason?: string;
  digest?: string;
  signer?: string;
  instruction_id?: string;
  origin?: string;
  settlement_hint?: string;
  action?: string;
};

const CHECK_STRING_FIELDS = [
  'reason',
  'digest',
  'signer',
  'instruction_id',
  'origin',
  'settlement_hint',
  'action',
] as const;

function asRecord(res: unknown, route: string): Record<string, unknown> {
  if (res == null || typeof res !== 'object' || Array.isArray(res)) {
    throw BridgeError.rpc(`${route}: node returned a malformed response`, res);
  }
  return res as Record<string, unknown>;
}

/** Only a literal `true` counts as valid; a node that answers `"true"` or `1` is not trusted. */
function parseCheck(res: unknown): VerifyCheckResult {
  const r = asRecord(res, '/verify/check');
  const out: VerifyCheckResult = { valid: r.valid === true };
  for (const k of CHECK_STRING_FIELDS) if (typeof r[k] === 'string') out[k] = r[k] as string;
  return out;
}

function parseSubmit(res: unknown, route: string): { status: string; hash: string } {
  const r = asRecord(res, route);
  if (typeof r.hash !== 'string' || !r.hash) throw BridgeError.rpc(`${route}: response has no tx hash`, res);
  return { status: typeof r.status === 'string' ? r.status : '', hash: r.hash };
}

export class VerifyClient {
  private readonly io: ChainIo;

  constructor(source: Connection | Transport | ChainIo) {
    this.io = chainIo(source);
  }

  async check(instruction: VerifyInstruction): Promise<VerifyCheckResult> {
    return parseCheck(await this.io.post<unknown>('/verify/check', instruction, false));
  }

  async proof(instructionId: string, instance: string): Promise<Record<string, unknown>> {
    const id = String(instructionId ?? '').trim().replace(/^0x/i, '');
    if (!INSTRUCTION_ID_RE.test(id)) throw BridgeError.invalidParams('instruction id must be 32-byte hex');
    instance = assertAddress(instance);
    const res = await this.io.get<unknown>(
      `/verify/proof/${encodeURIComponent(id)}?instance=${encodeURIComponent(instance)}`,
    );
    return asRecord(res, '/verify/proof');
  }

  async attest(signedTx: object): Promise<{ status: string; hash: string }> {
    return parseSubmit(await this.io.post<unknown>('/verify/attest', { tx: signedTx }, true), '/verify/attest');
  }

  async settle(signedTx: object): Promise<{ status: string; hash: string }> {
    return parseSubmit(await this.io.post<unknown>('/verify/settle', { tx: signedTx }, true), '/verify/settle');
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

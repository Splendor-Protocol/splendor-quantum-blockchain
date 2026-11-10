import type { Connection } from './core/connection.js';
import type { Transport } from './core/transport/types.js';
import { buildModuleCall, encodeModuleCallHex, type ModuleCall } from './core/module-call.js';
import { chainIo, type ChainIo } from './core/chain-io.js';
import { MODULE_ROUTER_ADDRESS } from './addresses/index.js';
import { assertAddress } from './address.js';
import { BridgeError } from './errors.js';

export const VALIDATOR_BOND_SPLD = 2500;

/** The node answers an empty validator set with a null `decoded`, which the transport raises. */
function isEmptyResult(e: unknown): boolean {
  return e instanceof Error && /decoded result is empty/i.test(e.message);
}

export class ValidatorsClient {
  private readonly io: ChainIo;

  constructor(source: Connection | Transport | ChainIo) {
    this.io = chainIo(source);
  }

  /** The node has no `/validators` route: every read is a `validators` module read on POST /call. */
  private async read<T>(action: string, params: Record<string, unknown> = {}): Promise<T> {
    const envelope = buildModuleCall('validators', action, params);
    if (typeof this.io.moduleCall === 'function') return this.io.moduleCall<T>(envelope);
    const res = await this.io.post<{ decoded?: unknown }>(
      '/call',
      { to: MODULE_ROUTER_ADDRESS, data: encodeModuleCallHex(envelope), encoding: 'hex', value: '0' },
      false,
    );
    if (res == null || res.decoded == null) throw BridgeError.rpc('moduleCall: decoded result is empty', res);
    return res.decoded as T;
  }

  async list(): Promise<{ validators: unknown[]; count: number; bond_spld: number }> {
    const [list, bond] = await Promise.all([
      this.read<unknown>('list').catch((e: unknown) => {
        if (isEmptyResult(e)) return [];
        throw e;
      }),
      this.read<{ spld?: string }>('bond').catch(() => null),
    ]);
    const validators = Array.isArray(list)
      ? list.filter((v) => v != null && typeof v === 'object' && !Array.isArray(v))
      : [];
    const spld = Number(bond?.spld);
    return {
      validators,
      count: validators.length,
      bond_spld: Number.isSafeInteger(spld) && spld > 0 ? spld : VALIDATOR_BOND_SPLD,
    };
  }

  async get(address: string): Promise<Record<string, unknown>> {
    const res = await this.read<unknown>('validator', { address: assertAddress(address) });
    if (res == null || typeof res !== 'object' || Array.isArray(res)) {
      throw BridgeError.rpc('validators validator: node returned a malformed response', res);
    }
    return res as Record<string, unknown>;
  }

  registerCall(pubkeyHex: string): ModuleCall {
    const pubkey = String(pubkeyHex ?? '').trim();
    if (!pubkey) throw BridgeError.invalidParams('validators register: pubkey is required');
    return buildModuleCall('validators', 'register', { pubkey });
  }

  unregisterCall(): ModuleCall {
    return buildModuleCall('validators', 'unregister', {});
  }

  finalizeExitCall(): ModuleCall {
    return buildModuleCall('validators', 'finalize_exit', {});
  }

  /** validators_admission_v1: allow `address` to register. Signer must be an active validator. */
  approveCall(address: string): ModuleCall {
    return buildModuleCall('validators', 'approve', { address: assertAddress(address) });
  }

  /** validators_admission_v1: withdraw an approval. Signer must be an active validator. */
  revokeCall(address: string): ModuleCall {
    return buildModuleCall('validators', 'revoke', { address: assertAddress(address) });
  }

  /** validators_admission_v1: whether `address` holds a standing approval (module read `approved`). */
  async isApproved(address: string): Promise<{ approved: boolean }> {
    const res = await this.read<{ approved?: boolean }>('approved', { address: assertAddress(address) });
    return { approved: res?.approved === true };
  }
}

/** Prefer `getValidators(connection)` — Transport-backed Connection. */
export function getValidators(source: Connection | Transport | ChainIo): ValidatorsClient {
  return new ValidatorsClient(source);
}

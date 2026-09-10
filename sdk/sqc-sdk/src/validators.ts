import type { Connection } from './core/connection.js';
import type { Transport } from './core/transport/types.js';
import { buildModuleCall, type ModuleCall } from './core/module-call.js';

export const VALIDATOR_BOND_SPLD = 2500;

type ChainIo = Pick<Transport, 'get' | 'post'>;

function asIo(source: Connection | Transport | ChainIo): ChainIo {
  if ('transport' in source && source.transport && typeof source.transport.get === 'function') {
    return source.transport;
  }
  return source as ChainIo;
}

export class ValidatorsClient {
  private readonly io: ChainIo;

  constructor(source: Connection | Transport | ChainIo) {
    this.io = asIo(source);
  }

  list() {
    return this.io.get<{ validators: unknown[]; count: number; bond_spld: number }>('/validators');
  }

  get(address: string) {
    return this.io.get<Record<string, unknown>>(
      `/validators/${encodeURIComponent(address)}`,
    );
  }

  registerCall(pubkeyHex: string): ModuleCall {
    return buildModuleCall('validators', 'register', { pubkey: pubkeyHex });
  }

  unregisterCall(): ModuleCall {
    return buildModuleCall('validators', 'unregister', {});
  }

  finalizeExitCall(): ModuleCall {
    return buildModuleCall('validators', 'finalize_exit', {});
  }

  /** validators_admission_v1: allow `address` to register. Signer must be an active validator. */
  approveCall(address: string): ModuleCall {
    return buildModuleCall('validators', 'approve', { address });
  }

  /** validators_admission_v1: withdraw an approval. Signer must be an active validator. */
  revokeCall(address: string): ModuleCall {
    return buildModuleCall('validators', 'revoke', { address });
  }

  /** validators_admission_v1: whether `address` holds a standing approval (module read `approved`). */
  isApproved(address: string) {
    return this.io.post<{ approved: boolean }>('/call', {
      module: 'validators',
      action: 'approved',
      params: { address },
    });
  }
}

/** Prefer `getValidators(connection)` — Transport-backed Connection. */
export function getValidators(source: Connection | Transport | ChainIo): ValidatorsClient {
  return new ValidatorsClient(source);
}

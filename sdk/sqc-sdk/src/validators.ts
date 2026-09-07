import { SqRpc } from './rpc.js';
import { buildModuleCall, type ModuleCall } from './core/module-call.js';

export const VALIDATOR_BOND_SPLD = 2500;

export class ValidatorsClient {
  constructor(private readonly rpc: SqRpc) {}

  list() {
    return this.rpc.listValidators();
  }

  get(address: string) {
    return this.rpc.getValidator(address);
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
}

export function getValidators(rpc: SqRpc): ValidatorsClient {
  return new ValidatorsClient(rpc);
}

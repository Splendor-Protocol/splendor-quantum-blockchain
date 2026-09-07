import { SqRpc } from './rpc.js';
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

export class VerifyClient {
  constructor(private readonly rpc: SqRpc) {}

  check(instruction: VerifyInstruction) {
    return this.rpc.verifyCheck(instruction);
  }

  proof(instructionId: string, instance: string) {
    return this.rpc.verifyProof(instructionId, instance);
  }

  attest(signedTx: object) {
    return this.rpc.verifyAttest(signedTx);
  }

  settle(signedTx: object) {
    return this.rpc.verifySettle(signedTx);
  }

  moduleCall(action: string, params: Record<string, unknown>): ModuleCall {
    return buildModuleCall('verify', action, params);
  }

  routerAddress(): string {
    return MODULE_ROUTER_ADDRESS;
  }
}

export function getVerify(rpc: SqRpc): VerifyClient {
  return new VerifyClient(rpc);
}

/**
 * Base Program class for native module clients (QRC-20, DEX, etc.).
 */
import type { Connection } from './connection.js';
import { buildModuleCall, type ModuleCall } from './module-call.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';

export type TxSubmitResult = {
  status: string;
  hash: string;
  signedTx: object;
  confirmation?: { confirmed: true; nonce: number };
};

export abstract class Program {
  constructor(
    protected readonly connection: Connection,
    protected readonly address: string,
  ) {}

  getAddress(): string {
    return this.address;
  }

  protected async read<T>(action: string, params: Record<string, unknown>): Promise<T> {
    const module = this.moduleName();
    const envelope = buildModuleCall(module, action, { token: this.address, ...params });
    return this.connection.moduleCall<T>(envelope);
  }

  protected async write(
    wallet: SplendorPQBridge,
    action: string,
    params: Record<string, unknown>,
    opts?: { fee?: string; waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<TxSubmitResult> {
    const module = this.moduleName();
    const fullParams =
      action === 'deploy'
        ? params
        : { token: this.address, ...params };
    return wallet.sendModuleCall({
      module,
      action,
      params: fullParams,
      fee: opts?.fee,
      waitForConfirmation: opts?.waitForConfirmation,
      confirmTimeoutMs: opts?.confirmTimeoutMs,
    });
  }

  protected abstract moduleName(): string;
}

export type { ModuleCall };

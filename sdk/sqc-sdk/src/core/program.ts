/**
 * Base Program class for native module clients (QRC-20, DEX, Exchange, etc.).
 */
import type { Connection } from './connection.js';
import { buildModuleCall, type ModuleCall } from './module-call.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { withSigner, type BoundClient, type WalletLike } from '../bound.js';

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

  /** Names of methods that take `wallet` as the first argument (for withSigner). */
  protected writeMethodNames(): ReadonlySet<string> {
    return new Set();
  }

  /**
   * Bind a wallet so write methods omit the wallet argument.
   * Example: `token.withSigner(wallet).transfer(to, amount)`
   */
  withSigner(wallet: WalletLike): BoundClient<this> {
    return withSigner(this, wallet, this.writeMethodNames());
  }

  /** JSON param name for this instance (`token`, `collection`, `pool`, `exchange`, `bridge`). */
  protected instanceParamKey(): string {
    return 'token';
  }

  protected instanceParams(extra: Record<string, unknown> = {}): Record<string, unknown> {
    return { [this.instanceParamKey()]: this.address, ...extra };
  }

  protected async read<T>(action: string, params: Record<string, unknown> = {}): Promise<T> {
    const module = this.moduleName();
    const envelope = buildModuleCall(module, action, this.instanceParams(params));
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
      action === 'deploy' || action === 'deploy_pool' ? params : this.instanceParams(params);
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

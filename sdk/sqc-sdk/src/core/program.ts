/**
 * Base Program class for module clients (routed native modules and adapters).
 */
import type { Connection } from './connection.js';
import { buildModuleCall, type ModuleCall } from './module-call.js';
import type { Signer } from './signer.js';
import { withSigner, type BoundClient, type WalletLike } from '../bound.js';

export type TxSubmitResult = {
  status: string;
  hash: string;
  signedTx: object;
  confirmation?: { confirmed: true; nonce: number; receipt?: Record<string, unknown> };
};

export abstract class Program {
  constructor(
    protected readonly connection: Connection,
    protected readonly address: string,
  ) {}

  getAddress(): string {
    return this.address;
  }

  /**
   * Declarative write method names for withSigner binding.
   * Prefer overriding this single list rather than maintaining a separate Set.
   */
  protected writeMethods(): readonly string[] {
    return [];
  }

  /** Names of methods that take `wallet` as the first argument (for withSigner). */
  protected writeMethodNames(): ReadonlySet<string> {
    return new Set(this.writeMethods());
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

  /** Override for module-specific deploy actions that omit the instance address. */
  protected isDeployAction(action: string): boolean {
    return action === 'deploy';
  }

  protected async read<T>(action: string, params: Record<string, unknown> = {}): Promise<T> {
    const module = this.moduleName();
    const envelope = buildModuleCall(module, action, this.instanceParams(params));
    return this.connection.moduleCall<T>(envelope);
  }

  protected async write(
    wallet: Signer,
    action: string,
    params: Record<string, unknown>,
    opts?: { fee?: string; waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<TxSubmitResult> {
    const module = this.moduleName();
    const fullParams = this.isDeployAction(action) ? params : this.instanceParams(params);
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

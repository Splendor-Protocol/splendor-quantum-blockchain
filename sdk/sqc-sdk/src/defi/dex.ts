/**
 * DEX (constant-product AMM) Program client.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { dexPoolAddress } from '../addresses/index.js';
import { BridgeError } from '../errors.js';
import { assertAddress } from '../address.js';

const DEX_WRITES = new Set(['addLiquidity', 'swap']);

/** On-chain `token_in` is uint8: 0 = token0, 1 = token1. */
export type DexTokenInSide = 0 | 1;

export class DEX extends Program {
  protected moduleName(): string {
    return 'dex';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return DEX_WRITES;
  }

  protected instanceParamKey(): string {
    return 'pool';
  }

  static predictPoolAddress(tokenA: string, tokenB: string): string {
    return dexPoolAddress(tokenA, tokenB);
  }

  async reserves(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('reserves', {});
  }

  /**
   * Resolve a token address to the pool side index (0|1) using reserves.
   */
  async resolveTokenInSide(tokenAddress: string): Promise<DexTokenInSide> {
    const addr = assertAddress(tokenAddress);
    const r = await this.reserves();
    const t0 = typeof r.token0 === 'string' ? assertAddress(r.token0) : '';
    const t1 = typeof r.token1 === 'string' ? assertAddress(r.token1) : '';
    if (addr === t0) return 0;
    if (addr === t1) return 1;
    throw BridgeError.rpc(
      `token ${tokenAddress} is not token0/token1 of pool ${this.getAddress()}`,
    );
  }

  async addLiquidity(
    wallet: SplendorPQBridge,
    params: {
      amount0Desired: string;
      amount1Desired: string;
      amount0Min?: string;
      amount1Min?: string;
    },
  ) {
    return this.write(wallet, 'add_liquidity', {
      amount0_desired: params.amount0Desired,
      amount1_desired: params.amount1Desired,
      ...(params.amount0Min != null ? { amount0_min: params.amount0Min } : {}),
      ...(params.amount1Min != null ? { amount1_min: params.amount1Min } : {}),
    });
  }

  /**
   * Swap. `tokenIn` must be the pool side index 0 (token0) or 1 (token1),
   * matching on-chain `dexSwapParams.token_in` (uint8).
   */
  async swap(
    wallet: SplendorPQBridge,
    params: { tokenIn: DexTokenInSide; amountIn: string; minAmountOut?: string },
  ) {
    const side = params.tokenIn;
    if (side !== 0 && side !== 1) {
      throw BridgeError.invalidAmount(`dex.swap tokenIn must be 0 or 1, got ${String(side)}`);
    }
    return this.write(wallet, 'swap', {
      token_in: side,
      amount_in: params.amountIn,
      ...(params.minAmountOut != null ? { min_amount_out: params.minAmountOut } : {}),
    });
  }
}

export async function deployPool(
  wallet: SplendorPQBridge,
  token0: string,
  token1: string,
  opts?: { fee?: string; waitForConfirmation?: boolean },
): Promise<{ address: string; result: Awaited<ReturnType<SplendorPQBridge['sendModuleCall']>> }> {
  const predicted = DEX.predictPoolAddress(token0, token1);
  const result = await wallet.sendModuleCall({
    module: 'dex',
    action: 'deploy_pool',
    params: { token0, token1 },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  return { address: predicted, result };
}

export function getPool(connection: Connection, address: string): DEX {
  return new DEX(connection, address);
}

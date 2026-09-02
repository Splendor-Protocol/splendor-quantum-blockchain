/**
 * DEX (constant-product AMM) Program client.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { dexPoolAddress } from '../addresses/index.js';

const DEX_WRITES = new Set(['addLiquidity', 'swap']);

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

  async swap(
    wallet: SplendorPQBridge,
    params: { tokenIn: string; amountIn: string; minAmountOut?: string },
  ) {
    return this.write(wallet, 'swap', {
      token_in: params.tokenIn,
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

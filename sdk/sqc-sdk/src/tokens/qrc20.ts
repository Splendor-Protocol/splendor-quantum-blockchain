/**
 * QRC20Program — typed client for native QRC-20 tokens.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { qrc20Address } from '../addresses/index.js';
import type { SignedPermit } from '../types.js';
import { fromUnits } from '../units.js';

const QRC20_WRITES = new Set([
  'transfer',
  'approve',
  'mint',
  'burn',
  'transferFrom',
  'submitPermit',
  'transferHuman',
  'approveHuman',
]);

export type QRC20DeployParams = {
  name: string;
  symbol: string;
  decimals: number;
  totalSupply: string;
  maxSupply?: string;
};

export type QRC20TokenInfo = {
  module: string;
  action: string;
  token: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  total_supply?: string;
  exists?: boolean;
};

export type QRC20Balance = {
  module: string;
  action: string;
  token: string;
  owner: string;
  balance: string;
  exists?: boolean;
};

export class QRC20 extends Program {
  protected moduleName(): string {
    return 'qrc20';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return QRC20_WRITES;
  }

  /** Predict token address before deploy. */
  static predictAddress(deployer: string, nonce: number): string {
    return qrc20Address(deployer, nonce);
  }

  private async decimalsOrThrow(): Promise<number> {
    const info = await this.tokenInfo();
    if (typeof info.decimals !== 'number') {
      throw new Error('tokenInfo().decimals unavailable');
    }
    return info.decimals;
  }

  /** Transfer using a human-readable amount (uses on-chain decimals). */
  async transferHuman(wallet: SplendorPQBridge, to: string, amountHuman: string) {
    const decimals = await this.decimalsOrThrow();
    return this.transfer(wallet, to, fromUnits(amountHuman, decimals));
  }

  /** Approve using a human-readable amount (uses on-chain decimals). */
  async approveHuman(wallet: SplendorPQBridge, spender: string, amountHuman: string) {
    const decimals = await this.decimalsOrThrow();
    return this.approve(wallet, spender, fromUnits(amountHuman, decimals));
  }

  async balanceOf(owner: string): Promise<string> {
    const res = await this.read<QRC20Balance>('balance_of', { owner });
    return res.balance;
  }

  async totalSupply(): Promise<string> {
    const res = await this.read<{ total_supply: string }>('total_supply', {});
    return res.total_supply;
  }

  async allowance(owner: string, spender: string): Promise<string> {
    const res = await this.read<{ allowance: string }>('allowance', { owner, spender });
    return res.allowance;
  }

  async tokenInfo(): Promise<QRC20TokenInfo> {
    return this.read<QRC20TokenInfo>('token_info', {});
  }

  async permitNonce(owner: string): Promise<number> {
    const res = await this.read<{ nonce: number }>('permit_nonce', { owner });
    return res.nonce;
  }

  async transfer(wallet: SplendorPQBridge, to: string, amount: string) {
    return this.write(wallet, 'transfer', { to, amount });
  }

  async approve(wallet: SplendorPQBridge, spender: string, amount: string) {
    return this.write(wallet, 'approve', { spender, amount });
  }

  async mint(wallet: SplendorPQBridge, to: string, amount: string) {
    return this.write(wallet, 'mint', { to, amount });
  }

  async burn(wallet: SplendorPQBridge, amount: string) {
    return this.write(wallet, 'burn', { amount });
  }

  async transferFrom(
    wallet: SplendorPQBridge,
    from: string,
    to: string,
    amount: string,
  ) {
    return this.write(wallet, 'transfer_from', { from, to, amount });
  }

  /** Submit on-chain permit (params from wallet.signPermit). */
  async submitPermit(wallet: SplendorPQBridge, signed: SignedPermit) {
    return this.write(wallet, 'permit', {
      token: signed.token,
      owner: signed.owner,
      spender: signed.spender,
      amount: signed.amount,
      deadline: signed.deadline,
      signature: signed.signature,
      owner_pubkey: signed.owner_pubkey,
    });
  }
}

/** Deploy a new QRC-20 token (static — no token address yet). */
export async function deployQRC20(
  wallet: SplendorPQBridge,
  params: QRC20DeployParams,
  opts?: { fee?: string; waitForConfirmation?: boolean },
): Promise<{ address: string; result: Awaited<ReturnType<SplendorPQBridge['sendModuleCall']>> }> {
  const { nonce } = await wallet.getNonce();
  const predicted = QRC20.predictAddress(wallet.getAddress(), nonce);
  const result = await wallet.sendModuleCall({
    module: 'qrc20',
    action: 'deploy',
    params: {
      name: params.name,
      symbol: params.symbol,
      decimals: params.decimals,
      total_supply: params.totalSupply,
      ...(params.maxSupply ? { max_supply: params.maxSupply } : {}),
    },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  return { address: predicted, result };
}

export function getToken(connection: Connection, address: string): QRC20 {
  return new QRC20(connection, address);
}

/**
 * QRC20Program — typed client for native QRC-20 tokens.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import { signerAccountNonce, signedTxNonce, writeOpts } from '../core/signer.js';
import { qrc20Address } from '../addresses/index.js';
import type { SignedPermit, WriteOpts } from '../types.js';
import { fromUnits } from '../units.js';
import { assertAddress } from '../address.js';
import { BridgeError } from '../errors.js';

const QRC20_WRITES = new Set([
  'transfer',
  'approve',
  'mint',
  'burn',
  'transferFrom',
  'submitPermit',
  'transferHuman',
  'approveHuman',
  'increaseAllowance',
  'decreaseAllowance',
  'burnFrom',
  'pause',
  'unpause',
  'transferOwnership',
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
  owner?: string;
};

export type QRC20Balance = {
  module: string;
  action: string;
  token: string;
  owner: string;
  balance: string;
  exists?: boolean;
};

export const MAX_TOKEN_DECIMALS = 36;

/** Token decimals must be an integer in 0..MAX_TOKEN_DECIMALS. */
export function assertDecimals(decimals: unknown): number {
  if (typeof decimals !== 'number' || !Number.isInteger(decimals) || decimals < 0 || decimals > MAX_TOKEN_DECIMALS) {
    throw BridgeError.invalidParams(`token decimals must be an integer from 0 to ${MAX_TOKEN_DECIMALS}`);
  }
  return decimals;
}

function assertAmount(amount: string, label = 'amount'): string {
  const s = String(amount).trim();
  if (!/^(0|[1-9]\d*)$/.test(s)) {
    throw BridgeError.invalidAmount(`${label}: expected non-negative integer string`);
  }
  return s;
}

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

  private pinnedDecimals: number | null = null;

  /**
   * Pin the decimals this app expects. Human-amount helpers then refuse to run when the
   * chain reports a different value, so a hostile RPC cannot inflate amounts by lying.
   */
  pinDecimals(decimals: number): this {
    this.pinnedDecimals = assertDecimals(decimals);
    return this;
  }

  private async decimalsOrThrow(): Promise<number> {
    const info = await this.tokenInfo();
    if (typeof info.decimals !== 'number') {
      throw BridgeError.rpc('tokenInfo().decimals unavailable');
    }
    const decimals = assertDecimals(info.decimals);
    if (this.pinnedDecimals != null && decimals !== this.pinnedDecimals) {
      throw BridgeError.rpc(`token decimals ${decimals} do not match the pinned ${this.pinnedDecimals}`);
    }
    return decimals;
  }

  /** Transfer using a human-readable amount (uses on-chain decimals). */
  async transferHuman(wallet: Signer, to: string, amountHuman: string, opts?: WriteOpts) {
    const decimals = await this.decimalsOrThrow();
    return this.transfer(wallet, to, fromUnits(amountHuman, decimals), opts);
  }

  /** Approve using a human-readable amount (uses on-chain decimals). */
  async approveHuman(wallet: Signer, spender: string, amountHuman: string, opts?: WriteOpts) {
    const decimals = await this.decimalsOrThrow();
    return this.approve(wallet, spender, fromUnits(amountHuman, decimals), opts);
  }

  async balanceOf(owner: string): Promise<string> {
    const res = await this.read<QRC20Balance>('balance_of', { owner: assertAddress(owner) });
    return res.balance;
  }

  async totalSupply(): Promise<string> {
    const res = await this.read<{ total_supply: string }>('total_supply', {});
    return res.total_supply;
  }

  async allowance(owner: string, spender: string): Promise<string> {
    const res = await this.read<{ allowance: string }>('allowance', {
      owner: assertAddress(owner),
      spender: assertAddress(spender),
    });
    return res.allowance;
  }

  async tokenInfo(): Promise<QRC20TokenInfo> {
    return this.read<QRC20TokenInfo>('token_info', {});
  }

  async permitNonce(owner: string): Promise<number> {
    const res = await this.read<{ permit_nonce: string | number }>('permit_nonce', {
      owner: assertAddress(owner),
    });
    const n = res.permit_nonce;
    if (typeof n === 'number') return n;
    if (typeof n === 'string' && /^(0|[1-9]\d*)$/.test(n)) return Number(n);
    throw BridgeError.rpc(`permit_nonce: unexpected value ${JSON.stringify(n)}`);
  }

  async paused(): Promise<boolean> {
    const res = await this.read<{ paused: boolean }>('paused', {});
    return !!res.paused;
  }

  async owner(): Promise<string> {
    const res = await this.read<{ owner: string }>('owner', {});
    return res.owner;
  }

  async transfer(wallet: Signer, to: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer', {
      to: assertAddress(to),
      amount: assertAmount(amount),
    }, opts);
  }

  async approve(wallet: Signer, spender: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'approve', {
      spender: assertAddress(spender),
      amount: assertAmount(amount),
    }, opts);
  }

  async mint(wallet: Signer, to: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'mint', {
      to: assertAddress(to),
      amount: assertAmount(amount),
    }, opts);
  }

  async burn(wallet: Signer, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'burn', { amount: assertAmount(amount) }, opts);
  }

  async transferFrom(
    wallet: Signer,
    from: string,
    to: string,
    amount: string,
    opts?: WriteOpts,
  ) {
    return this.write(wallet, 'transfer_from', {
      from: assertAddress(from),
      to: assertAddress(to),
      amount: assertAmount(amount),
    }, opts);
  }

  async increaseAllowance(wallet: Signer, spender: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'increase_allowance', {
      spender: assertAddress(spender),
      amount: assertAmount(amount),
    }, opts);
  }

  async decreaseAllowance(wallet: Signer, spender: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'decrease_allowance', {
      spender: assertAddress(spender),
      amount: assertAmount(amount),
    }, opts);
  }

  async burnFrom(wallet: Signer, from: string, amount: string, opts?: WriteOpts) {
    return this.write(wallet, 'burn_from', {
      from: assertAddress(from),
      amount: assertAmount(amount),
    }, opts);
  }

  async pause(wallet: Signer, opts?: WriteOpts) {
    return this.write(wallet, 'pause', {}, opts);
  }

  async unpause(wallet: Signer, opts?: WriteOpts) {
    return this.write(wallet, 'unpause', {}, opts);
  }

  async transferOwnership(wallet: Signer, newOwner: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer_ownership', { new_owner: assertAddress(newOwner) }, opts);
  }

  /** Submit on-chain permit (params from wallet.signPermit). */
  async submitPermit(wallet: Signer, signed: SignedPermit, opts?: WriteOpts) {
    return this.write(wallet, 'permit', {
      token: signed.token,
      owner: signed.owner,
      spender: signed.spender,
      amount: signed.amount,
      deadline: signed.deadline,
      signature: signed.signature,
      owner_pubkey: signed.owner_pubkey,
    }, opts);
  }
}

/** Deploy a new QRC-20 token (static — no token address yet). */
export async function deployQRC20(
  wallet: Signer,
  params: QRC20DeployParams,
  opts?: WriteOpts,
): Promise<{ address: string; result: Awaited<ReturnType<Signer['sendModuleCall']>> }> {
  const name = String(params.name ?? '').trim();
  const symbol = String(params.symbol ?? '').trim();
  if (!name || !symbol) throw BridgeError.invalidParams('token name and symbol are required');
  const decimals = assertDecimals(params.decimals);
  const totalSupply = assertAmount(params.totalSupply, 'totalSupply');
  const maxSupply = params.maxSupply ? assertAmount(params.maxSupply, 'maxSupply') : undefined;
  if (maxSupply != null && BigInt(maxSupply) < BigInt(totalSupply)) {
    throw BridgeError.invalidAmount('maxSupply must be at least totalSupply');
  }
  const nonce = await signerAccountNonce(wallet);
  const result = await wallet.sendModuleCall({
    module: 'qrc20',
    action: 'deploy',
    params: {
      name,
      symbol,
      decimals,
      total_supply: totalSupply,
      ...(maxSupply ? { max_supply: maxSupply } : {}),
    },
    ...writeOpts(opts),
  });
  const address = QRC20.predictAddress(wallet.getAddress(), signedTxNonce(result) ?? nonce);
  return { address, result };
}

export function getToken(connection: Connection, address: string): QRC20 {
  return new QRC20(connection, address);
}

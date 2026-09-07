/**
 * Exchange settlement Program client (Polymarket-shaped CTF + orders).
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import { requireCapability, signerAccountNonce } from '../core/signer.js';
import { exchangeAddress } from '../addresses/index.js';
import type { OrderParams, SignedOrder, SignedCancel, SignedIncrementNonce } from '../types.js';

export type ExchangeDeployParams = {
  name: string;
  uriPrefix?: string;
  redeemFeeBps?: number;
  feeRecipient?: string;
};

function blockHeight(block: unknown): number {
  if (!block || typeof block !== 'object') return 0;
  const b = block as Record<string, unknown>;
  if (typeof b.height === 'number') return b.height;
  const header = b.header as Record<string, unknown> | undefined;
  if (header && typeof header.height === 'number') return header.height;
  return 0;
}

const EXCHANGE_WRITES = new Set([
  'createCondition',
  'split',
  'merge',
  'match',
  'cancelOrder',
  'signOrder',
  'signCancel',
  'signAndSubmitIncrementNonce',
  'resolve',
  'redeem',
  'redeemFor',
  'pause',
  'unpause',
  'setOperator',
  'transferOwnership',
  'setRedeemFeeBps',
  'setFeeRecipient',
]);

export class Exchange extends Program {
  protected moduleName(): string {
    return 'exchange';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return EXCHANGE_WRITES;
  }

  protected instanceParamKey(): string {
    return 'exchange';
  }

  static predictAddress(deployer: string, nonce: number, height: number): string {
    return exchangeAddress(deployer, nonce, height);
  }

  async info(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('exchange_info', {});
  }

  async nonce(maker: string): Promise<number> {
    const res = await this.read<{ nonce: number }>('nonce', { maker });
    return res.nonce;
  }

  async orderStatus(hash: string): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('order_status', { hash });
  }

  async condition(id: number): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('condition_info', { condition: id });
  }

  async orderRemaining(hash: string): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('order_remaining', { hash });
  }

  async isOperator(address: string): Promise<boolean> {
    const res = await this.read<{ is_operator: boolean }>('is_operator', { address });
    return !!res.is_operator;
  }

  async position(holder: string, condition: number): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('position', { holder, condition });
  }

  /** Pure read — no exchange instance required on-chain. */
  async positionId(condition: number, outcome: number): Promise<Record<string, unknown>> {
    return this.connection.moduleCall<Record<string, unknown>>({
      module: 'exchange',
      action: 'position_id',
      params: { condition, outcome },
    });
  }

  async orderHash(order: SignedOrder | Record<string, unknown>): Promise<string> {
    const res = await this.read<{ hash: string }>('order_hash', { order });
    return res.hash;
  }

  async simulateMatch(params: {
    caller: string;
    takerOrder: SignedOrder | Record<string, unknown>;
    makerOrders: Array<SignedOrder | Record<string, unknown>>;
    fillShares: string[];
  }): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('simulate_match', {
      caller: params.caller,
      taker_order: params.takerOrder,
      maker_orders: params.makerOrders,
      fill_shares: params.fillShares,
    });
  }

  async createCondition(
    wallet: Signer,
    params: {
      collateral: string;
      resolver: string;
      endTime: number;
      outcomeCount?: number;
      questionHash: string;
    },
  ) {
    return this.write(wallet, 'create_condition', {
      collateral: params.collateral,
      resolver: params.resolver,
      end_time: params.endTime,
      outcome_count: params.outcomeCount ?? 2,
      question_hash: params.questionHash,
    });
  }

  async split(wallet: Signer, condition: number, amount: string) {
    return this.write(wallet, 'split', { condition, amount });
  }

  async merge(wallet: Signer, condition: number, amount: string) {
    return this.write(wallet, 'merge', { condition, amount });
  }

  async match(
    wallet: Signer,
    params: {
      takerOrder: SignedOrder | Record<string, unknown>;
      makerOrders: Array<SignedOrder | Record<string, unknown>>;
      fillShares: string[];
    },
  ) {
    return this.write(wallet, 'match', {
      taker_order: params.takerOrder,
      maker_orders: params.makerOrders,
      fill_shares: params.fillShares,
    });
  }

  async cancelOrder(wallet: Signer, signedCancel: SignedCancel) {
    return this.write(wallet, 'cancel_order', {
      order: signedCancel.order,
      cancel_signature: signedCancel.cancel_signature,
      maker_pubkey: signedCancel.maker_pubkey,
    });
  }

  /** Sign an order for this exchange instance (fills exchange address). */
  async signOrder(wallet: Signer, params: Omit<OrderParams, 'exchange'>): Promise<SignedOrder> {
    const sign = requireCapability(wallet, 'signOrder');
    return sign.call(wallet, { ...params, exchange: this.address });
  }

  async signCancel(wallet: Signer, params: Omit<OrderParams, 'exchange'>): Promise<SignedCancel> {
    const sign = requireCapability(wallet, 'signCancel');
    return sign.call(wallet, { ...params, exchange: this.address });
  }

  async signAndSubmitIncrementNonce(wallet: Signer, expectedNonce: number) {
    const sign = requireCapability(wallet, 'signIncrementNonce');
    const signed: SignedIncrementNonce = await sign.call(wallet, {
      exchange: this.address,
      expected_nonce: expectedNonce,
    });
    return this.write(wallet, 'increment_nonce', {
      maker: signed.maker,
      expected_nonce: signed.expected_nonce,
      signature: signed.signature,
      maker_pubkey: signed.maker_pubkey,
    });
  }

  async resolve(wallet: Signer, condition: number, outcome: number) {
    return this.write(wallet, 'resolve', { condition, outcome });
  }

  async redeem(wallet: Signer, condition: number) {
    return this.write(wallet, 'redeem', { condition });
  }

  async redeemFor(wallet: Signer, condition: number, holder: string) {
    return this.write(wallet, 'redeem_for', { condition, holder });
  }

  async pause(wallet: Signer) {
    return this.write(wallet, 'pause', {});
  }

  async unpause(wallet: Signer) {
    return this.write(wallet, 'unpause', {});
  }

  async setOperator(wallet: Signer, operator: string, active: boolean) {
    return this.write(wallet, 'set_operator', { operator, active });
  }

  async setRedeemFeeBps(wallet: Signer, redeemFeeBps: number) {
    return this.write(wallet, 'set_redeem_fee_bps', { redeem_fee_bps: redeemFeeBps });
  }

  async setFeeRecipient(wallet: Signer, feeRecipient: string) {
    return this.write(wallet, 'set_fee_recipient', { fee_recipient: feeRecipient });
  }

  async transferOwnership(wallet: Signer, newOwner: string) {
    return this.write(wallet, 'transfer_ownership', { new_owner: newOwner });
  }
}

export async function deployExchange(
  wallet: Signer,
  params: ExchangeDeployParams,
  opts?: { fee?: string; waitForConfirmation?: boolean; height?: number },
): Promise<{ address: string; height: number; result: Awaited<ReturnType<Signer['sendModuleCall']>> }> {
  const nonce = await signerAccountNonce(wallet);
  let height = opts?.height;
  if (height == null) {
    const getLatestBlock = requireCapability(wallet, 'getLatestBlock');
    const latest = await getLatestBlock.call(wallet);
    height = blockHeight(latest) + 1;
  }
  const predicted = Exchange.predictAddress(wallet.getAddress(), nonce, height);
  const result = await wallet.sendModuleCall({
    module: 'exchange',
    action: 'deploy',
    params: {
      name: params.name,
      ...(params.uriPrefix ? { uri_prefix: params.uriPrefix } : {}),
      ...(params.redeemFeeBps != null ? { redeem_fee_bps: params.redeemFeeBps } : {}),
      ...(params.feeRecipient ? { fee_recipient: params.feeRecipient } : {}),
    },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  return { address: predicted, height, result };
}

export function getExchange(connection: Connection, address: string): Exchange {
  return new Exchange(connection, address);
}

import { describe, expect, it, vi } from 'vitest';
import {
  qrc1155Address,
  exchangeAddress,
  bridgeAddress,
  bridgeSplvAddress,
  dexPoolAddress,
} from '../addresses/index.js';
import { QRC1155, deployQRC1155 } from '../tokens/qrc1155.js';
import { Exchange, deployExchange } from '../defi/exchange.js';
import { DEX, deployPool } from '../defi/dex.js';
import { BridgeModule, deployBridge } from '../bridge/module.js';
import { modules } from '../modules.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const OWNER = golden.address;
const A = 'SQ:' + 'aa'.repeat(32);
const B = 'SQ:' + 'bb'.repeat(32);

function mockWallet(extra: Partial<SplendorPQBridge> = {}): SplendorPQBridge {
  return {
    getAddress: () => OWNER,
    getNonce: async () => ({ nonce: 2 }),
    // On the wallet itself, not only inside connection: deployBridge and
    // deployExchange ask for it as a Signer capability now, so a double that
    // hides it behind connection no longer stands in for a wallet.
    getLatestBlock: async () => ({ height: 100 }),
    connection: {
      getLatestBlock: async () => ({ height: 100 }),
      moduleCall: vi.fn(),
    } as unknown as Connection,
    sendModuleCall: vi.fn(async (opts) => ({
      status: 'ok',
      hash: '0xh',
      signedTx: opts,
    })),
    signOrder: vi.fn(async (p) => ({ ...p, hash: 'ord', chain_id: golden.chainId, maker: OWNER })),
    ...extra,
  } as unknown as SplendorPQBridge;
}

describe('address helpers', () => {
  it('qrc1155 / exchange / bridge / dex are deterministic and distinct', () => {
    const c0 = qrc1155Address(OWNER, 0);
    const c1 = qrc1155Address(OWNER, 1);
    expect(c0).not.toBe(c1);
    expect(exchangeAddress(OWNER, 1, 10)).not.toBe(exchangeAddress(OWNER, 1, 11));
    const br = bridgeAddress(OWNER, 1, 5);
    expect(bridgeSplvAddress(br).startsWith('SQ:')).toBe(true);
    const pool = dexPoolAddress(A, B);
    expect(pool).toBe(dexPoolAddress(B, A));
  });
});

describe('module clients functional', () => {
  it('deployQRC1155 + mint envelope', async () => {
    const wallet = mockWallet();
    const { address } = await deployQRC1155(wallet, { name: 'Coll' });
    expect(address).toBe(QRC1155.predictAddress(OWNER, 2));
    const col = new QRC1155({ moduleCall: vi.fn() } as unknown as Connection, address);
    await col.mint(wallet, OWNER, '1', '10');
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[1][0]).toMatchObject({
      module: 'qrc1155',
      action: 'mint',
      params: { collection: address, to: OWNER, id: '1', amount: '10' },
    });
  });

  it('deployExchange + signOrder uses instance address', async () => {
    const wallet = mockWallet();
    const { address, height, confirmed, predictedAddress } = await deployExchange(wallet, {
      name: 'Book',
    });
    expect(height).toBe(101);
    // Unconfirmed: the prediction is all there is, and it is labelled as such.
    expect(address).toBe(Exchange.predictAddress(OWNER, 2, 101));
    expect(predictedAddress).toBe(address);
    expect(confirmed).toBe(false);
    const ex = new Exchange({ moduleCall: vi.fn() } as unknown as Connection, address);
    await ex.signOrder(wallet, {
      condition: 1,
      outcome: 0,
      side: 0,
      share_amount: '1',
      price: '500000',
      expiration: 0,
      nonce: 0,
    });
    expect(wallet.signOrder).toHaveBeenCalledWith(expect.objectContaining({ exchange: address }));
  });

  it('deployPool + swap', async () => {
    const wallet = mockWallet();
    const { address } = await deployPool(wallet, A, B);
    expect(address).toBe(DEX.predictPoolAddress(A, B));
    const pool = new DEX({ moduleCall: vi.fn() } as unknown as Connection, address);
    await pool.swap(wallet, { tokenIn: 0, amountIn: '100', minAmountOut: '1' });
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[1][0]).toMatchObject({
      module: 'dex',
      action: 'swap',
      params: { pool: address, token_in: 0, amount_in: '100', min_amount_out: '1' },
    });
  });

  it('deployBridge + claim', async () => {
    const wallet = mockWallet();
    const { address, splvAddress } = await deployBridge(wallet, {
      evmChainId: 2691,
      evmContract: '0x' + 'ab'.repeat(20),
      splvName: 'SPLV',
      splvSymbol: 'SPLV',
      splvMaxSupply: '1000',
      maxClaimAmount: '100',
      threshold: 1,
    });
    expect(splvAddress).toBe(BridgeModule.splvAddress(address));
    const br = new BridgeModule({ moduleCall: vi.fn() } as unknown as Connection, address);
    await br.claim(wallet, {
      id: 1,
      asset: 'spld',
      amount: '10',
      recipient: OWNER,
      evmTx: '0x' + 'cd'.repeat(32),
      attestations: [{ pubkey: 'YQ==', signature: 'Yg==' }],
    });
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[1][0].module).toBe('bridge');
    expect(send.mock.calls[1][0].action).toBe('claim');
  });

  it('modules factory returns typed clients', () => {
    const conn = { moduleCall: vi.fn() } as unknown as Connection;
    expect(modules.qrc20(conn, A).getAddress()).toBe(A);
    expect(modules.exchange(conn, A).getAddress()).toBe(A);
    expect(modules.dex(conn, A).getAddress()).toBe(A);
    expect(modules.bridge(conn, A).getAddress()).toBe(A);
    expect(modules.qrc1155(conn, A).getAddress()).toBe(A);
  });
});

// A deploy's address depends on the height it executes at. The prediction made
// before sending assumes the very next block; once confirmed, the receipt's
// contract_address is the truth and must win over the prediction.
describe('deploy helpers prefer the confirmed receipt address', () => {
  const CONFIRMED = 'SQ:' + 'cd'.repeat(32);
  const confirmedBytes = Array.from({ length: 32 }, () => 0xcd);
  const bridgeParams = {
    evmChainId: 2691,
    evmContract: '0x' + 'ab'.repeat(20),
    splvName: 'SPLV',
    splvSymbol: 'SPLV',
    splvMaxSupply: '1000',
    maxClaimAmount: '100',
    threshold: 1,
  };

  function confirmingWallet(receipt: Record<string, unknown>) {
    return mockWallet({
      sendModuleCall: vi.fn(async (opts) => ({
        status: 'ok',
        hash: '0xh',
        signedTx: opts,
        confirmation: { confirmed: true, nonce: 3, receipt },
      })),
    });
  }

  it('deployExchange reports the receipt address, not the next-height prediction', async () => {
    // The node serialises contract_address as a 32-byte array; the deploy
    // landed two blocks later than assumed.
    const wallet = confirmingWallet({ status: 1, contract_address: confirmedBytes, block_height: 103 });
    const r = await deployExchange(wallet, { name: 'Book' }, { waitForConfirmation: true });
    expect(r.predictedAddress).toBe(Exchange.predictAddress(OWNER, 2, 101));
    expect(r.address).toBe(CONFIRMED);
    expect(r.address).not.toBe(r.predictedAddress);
    expect(r.confirmed).toBe(true);
    expect(r.height).toBe(103);
  });

  it('deployBridge derives the SPLV address from the confirmed bridge address', async () => {
    const wallet = confirmingWallet({ status: 1, contract_address: confirmedBytes, block_height: 103 });
    const r = await deployBridge(wallet, bridgeParams, { waitForConfirmation: true });
    expect(r.address).toBe(CONFIRMED);
    expect(r.splvAddress).toBe(BridgeModule.splvAddress(CONFIRMED));
    expect(r.splvAddress).not.toBe(BridgeModule.splvAddress(r.predictedAddress));
    expect(r.confirmed).toBe(true);
    expect(r.height).toBe(103);
  });

  it('accepts a string-form contract_address and canonicalises it', async () => {
    const wallet = confirmingWallet({ status: 1, contract_address: 'SQ:' + 'CD'.repeat(32) });
    const r = await deployExchange(wallet, { name: 'Book' }, { waitForConfirmation: true });
    expect(r.address).toBe(CONFIRMED);
    expect(r.confirmed).toBe(true);
    expect(r.height).toBe(101); // no block_height in the receipt: the assumption stands
  });

  it('keeps the prediction, labelled unconfirmed, when the receipt names no contract', async () => {
    const wallet = confirmingWallet({ status: 1 });
    const r = await deployBridge(wallet, bridgeParams, { waitForConfirmation: true });
    expect(r.address).toBe(r.predictedAddress);
    expect(r.splvAddress).toBe(BridgeModule.splvAddress(r.predictedAddress));
    expect(r.confirmed).toBe(false);
  });

  it('rejects a malformed contract_address rather than trusting it', async () => {
    const wallet = confirmingWallet({ status: 1, contract_address: [1, 2, 3] });
    const r = await deployExchange(wallet, { name: 'Book' }, { waitForConfirmation: true });
    expect(r.address).toBe(r.predictedAddress);
    expect(r.confirmed).toBe(false);
  });
});

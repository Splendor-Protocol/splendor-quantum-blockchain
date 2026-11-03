import { describe, expect, it, vi } from 'vitest';
import { QRC1155, QRC1155_MAX_BATCH, deployQRC1155, getCollection } from '../../tokens/qrc1155.js';
import { BridgeError } from '../../errors.js';
import type { Connection } from '../../core/connection.js';
import type { Signer } from '../../core/signer.js';

const COLLECTION = 'SQ:' + 'ee'.repeat(32);
const OWNER = 'SQ:' + 'ab'.repeat(32);
const OTHER = 'SQ:' + 'cd'.repeat(32);
const TOO_BIG = (1n << 256n).toString();

type Env = { module: string; action: string; params: Record<string, unknown> };

function reader(answer: (env: Env) => unknown) {
  const moduleCall = vi.fn(async (env: Env) => answer(env));
  return { connection: { moduleCall } as unknown as Connection, moduleCall };
}

function signer(address = OWNER) {
  const sendModuleCall = vi.fn(async () => ({ status: 'submitted', hash: 'h', signedTx: { nonce: 3 } }));
  const wallet = {
    sendModuleCall,
    getAddress: () => address,
    getNonce: async () => ({ address, nonce: 1 }),
    callContract: vi.fn(),
  } as unknown as Signer;
  return { wallet, sendModuleCall };
}

function sent(fn: ReturnType<typeof vi.fn>, i = 0): Env {
  return (fn.mock.calls[i] as unknown[])[0] as Env;
}

describe('QRC1155 prod — reads', () => {
  it('[L1] balanceOf, balanceOfBatch, uri, totalSupply, isApprovedForAll scope to the collection', async () => {
    const { connection, moduleCall } = reader((env) => {
      switch (env.action) {
        case 'balance_of': return { balance: '5' };
        case 'balance_of_batch': return { balances: ['1', '2'] };
        case 'uri': return { uri: 'ipfs://x/1' };
        case 'total_supply': return { total_supply: '9' };
        case 'is_approved_for_all': return { approved: true };
        default: return {};
      }
    });
    const c = getCollection(connection, COLLECTION);
    expect(await c.balanceOf(OWNER, '1')).toBe('5');
    expect(await c.balanceOfBatch([OWNER, OTHER], ['1', '2'])).toEqual(['1', '2']);
    expect(await c.uri('1')).toBe('ipfs://x/1');
    expect(await c.totalSupply('1')).toBe('9');
    expect(await c.isApprovedForAll(OWNER, OTHER)).toBe(true);
    for (const call of moduleCall.mock.calls) {
      expect(call[0]).toMatchObject({ module: 'qrc1155', params: { collection: COLLECTION } });
    }
  });

  it('[L2] balanceOfBatch rejects length mismatch and over-cap batches before the node', async () => {
    const { connection, moduleCall } = reader(() => ({ balances: [] }));
    const c = getCollection(connection, COLLECTION);
    await expect(c.balanceOfBatch([OWNER], ['1', '2'])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    const n = QRC1155_MAX_BATCH + 1;
    await expect(c.balanceOfBatch(Array(n).fill(OWNER), Array(n).fill('1'))).rejects.toMatchObject({
      code: 'INVALID_PARAMS',
    });
    expect(moduleCall).not.toHaveBeenCalled();
  });

  it('[L2] ids that are negative, fractional, padded, or at 2^256 are refused', async () => {
    const { connection, moduleCall } = reader(() => ({ balance: '0' }));
    const c = getCollection(connection, COLLECTION);
    for (const bad of ['-1', '1.5', '01', '', 'abc', TOO_BIG]) {
      await expect(c.balanceOf(OWNER, bad)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    }
    expect(moduleCall).not.toHaveBeenCalled();
  });

  it('[L3] a module read failure surfaces as the BridgeError from the transport', async () => {
    const { connection } = reader(() => {
      throw BridgeError.rpc('moduleCall reverted: qrc1155: collection is not a deployed contract');
    });
    await expect(getCollection(connection, COLLECTION).balanceOf(OWNER, '1')).rejects.toMatchObject({ code: 'RPC' });
  });

  it('[L4] a batch read at the cap goes out as one call', async () => {
    const { connection, moduleCall } = reader((env) => ({
      balances: (env.params.ids as string[]).map(() => '1'),
    }));
    const ids = Array.from({ length: QRC1155_MAX_BATCH }, (_, i) => String(i));
    const out = await getCollection(connection, COLLECTION).balanceOfBatch(Array(ids.length).fill(OWNER), ids);
    expect(out).toHaveLength(QRC1155_MAX_BATCH);
    expect(moduleCall).toHaveBeenCalledTimes(1);
  });
});

describe('QRC1155 prod — writes', () => {
  const c = () => getCollection(reader(() => null).connection, COLLECTION);

  it('[L1] every write sends its action scoped to the collection with opts', async () => {
    const { wallet, sendModuleCall } = signer();
    const col = c();
    await col.mint(wallet, OTHER, '1', '10', 'ipfs://a', { fee: '5' });
    await col.mintBatch(wallet, OTHER, ['1', '2'], ['3', '4']);
    await col.transfer(wallet, OTHER, '1', '2');
    await col.batchTransfer(wallet, OTHER, ['1', '2'], ['1', '1'], OWNER);
    await col.setApprovalForAll(wallet, OTHER, true);
    await col.burn(wallet, '1', '1');
    await col.burnBatch(wallet, ['1', '2'], ['1', '1']);
    await col.transferOwnership(wallet, OTHER);
    expect(sendModuleCall.mock.calls.map((_, i) => sent(sendModuleCall, i).action)).toEqual([
      'mint', 'mint_batch', 'transfer', 'batch_transfer', 'set_approval_for_all', 'burn', 'burn_batch',
      'transfer_ownership',
    ]);
    expect(sent(sendModuleCall, 0)).toMatchObject({
      module: 'qrc1155',
      params: { collection: COLLECTION, to: OTHER, id: '1', amount: '10', uri: 'ipfs://a' },
      fee: '5',
    });
    expect(sent(sendModuleCall, 3).params).toMatchObject({ from: OWNER, ids: ['1', '2'] });
  });

  it('[L1] deploy predicts the address from the signed nonce', async () => {
    const { wallet, sendModuleCall } = signer();
    const { address } = await deployQRC1155(wallet, { name: 'Items', uriPrefix: 'ipfs://base/' });
    expect(address).toBe(QRC1155.predictAddress(OWNER, 3));
    expect(sent(sendModuleCall).params).toEqual({ name: 'Items', uri_prefix: 'ipfs://base/' });
  });

  it('[L2] batch writes reject empty, mismatched, and over-cap lists', async () => {
    const { wallet, sendModuleCall } = signer();
    const col = c();
    const n = QRC1155_MAX_BATCH + 1;
    const big = Array.from({ length: n }, (_, i) => String(i));
    await expect(col.mintBatch(wallet, OTHER, [], [])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.mintBatch(wallet, OTHER, ['1'], ['1', '2'])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.mintBatch(wallet, OTHER, big, big.map(() => '1'))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.batchTransfer(wallet, OTHER, big, big.map(() => '1'))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.burnBatch(wallet, big, big.map(() => '1'))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    expect(sendModuleCall).not.toHaveBeenCalled();
  });

  it('[L2] mint and burn refuse a zero amount; transfer passes zero through like the node', async () => {
    const { wallet, sendModuleCall } = signer();
    const col = c();
    await expect(col.mint(wallet, OTHER, '1', '0')).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    await expect(col.burn(wallet, '1', '0')).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    await expect(col.mintBatch(wallet, OTHER, ['1'], ['0'])).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    await expect(col.burnBatch(wallet, ['1'], ['0'])).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    expect(sendModuleCall).not.toHaveBeenCalled();
    await col.transfer(wallet, OTHER, '1', '0');
    expect(sendModuleCall).toHaveBeenCalledTimes(1);
  });

  it('[L2] amounts at 2^256 and malformed strings are refused', async () => {
    const { wallet, sendModuleCall } = signer();
    for (const bad of [TOO_BIG, '-1', '1e3', '1.0', ' ']) {
      await expect(c().transfer(wallet, OTHER, '1', bad)).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    }
    expect(sendModuleCall).not.toHaveBeenCalled();
  });

  it('[L2] name and uri byte caps match the node (64 / 2048)', async () => {
    const { wallet, sendModuleCall } = signer();
    await expect(deployQRC1155(wallet, { name: '' })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(deployQRC1155(wallet, { name: 'x'.repeat(65) })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(deployQRC1155(wallet, { name: 'é'.repeat(33) })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(deployQRC1155(wallet, { name: 'ok', uriPrefix: 'x'.repeat(2049) })).rejects.toMatchObject({
      code: 'INVALID_PARAMS',
    });
    await expect(c().mint(wallet, OTHER, '1', '1', 'x'.repeat(2049))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(c().mintBatch(wallet, OTHER, ['1', '2'], ['1', '1'], ['ok', 'x'.repeat(2049)])).rejects.toMatchObject({
      code: 'INVALID_PARAMS',
    });
    expect(sendModuleCall).not.toHaveBeenCalled();
    await deployQRC1155(wallet, { name: 'x'.repeat(64) });
    expect(sendModuleCall).toHaveBeenCalledTimes(1);
  });

  it('[L2] mintBatch accepts fewer uris than ids but not more', async () => {
    const { wallet, sendModuleCall } = signer();
    await c().mintBatch(wallet, OTHER, ['1', '2'], ['1', '1'], ['ipfs://one']);
    expect(sent(sendModuleCall).params).toMatchObject({ uris: ['ipfs://one'] });
    await expect(c().mintBatch(wallet, OTHER, ['1'], ['1'], ['a', 'b'])).rejects.toMatchObject({
      code: 'INVALID_PARAMS',
    });
  });

  it('[L2] self-approval and transfers to the collection address are refused before signing', async () => {
    const { wallet, sendModuleCall } = signer();
    await expect(c().setApprovalForAll(wallet, OWNER, true)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(c().setApprovalForAll(wallet, OTHER, 'yes' as never)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(c().transfer(wallet, COLLECTION, '1', '1')).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(c().batchTransfer(wallet, COLLECTION, ['1'], ['1'])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(c().transfer(wallet, '0xdead', '1', '1')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    expect(sendModuleCall).not.toHaveBeenCalled();
  });

  it('[L3] a locked signer surfaces its own error, not a validation error', async () => {
    const wallet = {
      getAddress: () => {
        throw BridgeError.notConnected('locked');
      },
      sendModuleCall: vi.fn(async () => {
        throw BridgeError.notConnected('locked');
      }),
      callContract: vi.fn(),
    } as unknown as Signer;
    await expect(c().setApprovalForAll(wallet, OTHER, true)).rejects.toMatchObject({ code: 'NOT_CONNECTED' });
  });

  it('[L3] a failed send propagates unchanged', async () => {
    const wallet = {
      getAddress: () => OWNER,
      sendModuleCall: vi.fn(async () => {
        throw BridgeError.txFailed('QRC1155 transfer: insufficient balance');
      }),
      callContract: vi.fn(),
    } as unknown as Signer;
    await expect(c().transfer(wallet, OTHER, '1', '5')).rejects.toMatchObject({ code: 'TX_FAILED' });
  });

  it('[L4] a full-cap mintBatch goes out as one transaction', async () => {
    const { wallet, sendModuleCall } = signer();
    const ids = Array.from({ length: QRC1155_MAX_BATCH }, (_, i) => String(i + 1));
    await c().mintBatch(wallet, OTHER, ids, ids.map(() => '1'));
    expect(sendModuleCall).toHaveBeenCalledTimes(1);
    expect((sent(sendModuleCall).params.ids as string[]).length).toBe(QRC1155_MAX_BATCH);
  });

  it('[L4] withSigner binds the wallet for every write method', async () => {
    const { wallet, sendModuleCall } = signer();
    const bound = c().withSigner(wallet as never);
    await bound.mint(OTHER, '1', '1');
    await bound.burnBatch(['1'], ['1']);
    expect(sendModuleCall).toHaveBeenCalledTimes(2);
  });
});

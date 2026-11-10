import { describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { QRC1155, QRC1155_MAX_BATCH, deployQRC1155, getCollection } from '../tokens/qrc1155.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const COLLECTION = 'SQ:' + 'aa'.repeat(32);
const OWNER = golden.address;
const OTHER = 'SQ:' + 'bb'.repeat(32);

type Envelope = { module: string; action: string; params: Record<string, unknown> };

function mockConnection(moduleCall: (e: Envelope) => unknown = () => ({})): Connection {
  return { moduleCall: vi.fn(async (e: Envelope) => moduleCall(e)), chainId: golden.chainId } as unknown as Connection;
}

function mockWallet(): SplendorPQBridge {
  return {
    getAddress: () => OWNER,
    getNonce: async () => ({ nonce: 4 }),
    sendModuleCall: vi.fn(async (opts: Envelope) => ({ status: 'ok', hash: '0xabc', signedTx: { nonce: 4, ...opts } })),
  } as unknown as SplendorPQBridge;
}

const sent = (w: SplendorPQBridge) => (w.sendModuleCall as ReturnType<typeof vi.fn>).mock.calls.map((c) => c[0] as Envelope);

describe('QRC1155 functional (mocked transport)', () => {
  it('reads inject the collection and unwrap the node fields', async () => {
    const seen: Envelope[] = [];
    const col = getCollection(
      mockConnection((e) => {
        seen.push(e);
        switch (e.action) {
          case 'balance_of':
            return { balance: '5' };
          case 'balance_of_batch':
            return { balances: ['1', '2'] };
          case 'uri':
            return { uri: 'ipfs://x/1' };
          case 'is_approved_for_all':
            return { approved: true };
          case 'total_supply':
            return { total_supply: '9' };
          default:
            return { name: 'Col' };
        }
      }),
      COLLECTION,
    );
    expect(await col.balanceOf(OWNER, '1')).toBe('5');
    expect(await col.balanceOfBatch([OWNER, OTHER], ['1', '2'])).toEqual(['1', '2']);
    expect(await col.uri('1')).toBe('ipfs://x/1');
    expect(await col.isApprovedForAll(OWNER, OTHER)).toBe(true);
    expect(await col.totalSupply('1')).toBe('9');
    expect(await col.collectionInfo()).toEqual({ name: 'Col' });
    expect(seen.every((e) => e.module === 'qrc1155' && e.params.collection === COLLECTION)).toBe(true);
  });

  it('writes send the node action names and params', async () => {
    const w = mockWallet();
    const col = new QRC1155(mockConnection(), COLLECTION);
    await col.mint(w, OTHER, '1', '10', 'ipfs://one');
    await col.mintBatch(w, OTHER, ['1', '2'], ['3', '4'], ['ipfs://a']);
    await col.transfer(w, OTHER, '1', '2');
    await col.batchTransfer(w, OTHER, ['1', '2'], ['1', '1']);
    await col.setApprovalForAll(w, OTHER, true);
    await col.burn(w, '1', '1');
    await col.burnBatch(w, ['1'], ['1']);
    await col.transferOwnership(w, OTHER);
    expect(sent(w).map((e) => e.action)).toEqual([
      'mint',
      'mint_batch',
      'transfer',
      'batch_transfer',
      'set_approval_for_all',
      'burn',
      'burn_batch',
      'transfer_ownership',
    ]);
    expect(sent(w)[0]!.params).toMatchObject({ collection: COLLECTION, to: OTHER, id: '1', amount: '10', uri: 'ipfs://one' });
    expect(sent(w)[1]!.params).toMatchObject({ ids: ['1', '2'], amounts: ['3', '4'], uris: ['ipfs://a'] });
    expect(sent(w)[7]!.params).toMatchObject({ new_owner: OTHER });
  });

  it('refuses bad input before signing', async () => {
    const w = mockWallet();
    const col = new QRC1155(mockConnection(), COLLECTION);
    await expect(col.mint(w, OTHER, '1', '0')).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    await expect(col.mint(w, OTHER, '-1', '1')).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.transfer(w, COLLECTION, '1', '1')).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.batchTransfer(w, OTHER, ['1'], ['1', '2'])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    const big = Array.from({ length: QRC1155_MAX_BATCH + 1 }, (_, i) => String(i));
    await expect(col.burnBatch(w, big, big.map(() => '1'))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.setApprovalForAll(w, OWNER, true)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(col.mintBatch(w, OTHER, ['1'], ['1'], ['a', 'b'])).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    expect(sent(w)).toEqual([]);
  });

  it('deployQRC1155 sends deploy and predicts the collection address from the nonce', async () => {
    const w = mockWallet();
    const { address } = await deployQRC1155(w, { name: '  My Col ', uriPrefix: 'ipfs://base/' });
    expect(sent(w)[0]).toMatchObject({ module: 'qrc1155', action: 'deploy', params: { name: 'My Col', uri_prefix: 'ipfs://base/' } });
    expect(address).toBe(QRC1155.predictAddress(OWNER, 4));
    await expect(deployQRC1155(w, { name: '' })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(deployQRC1155(w, { name: 'x'.repeat(65) })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
  });
});

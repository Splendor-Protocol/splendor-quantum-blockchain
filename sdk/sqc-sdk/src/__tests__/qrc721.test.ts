import { describe, expect, it, vi } from 'vitest';
import { QRC721, deployQRC721, getNftCollection } from '../tokens/qrc721.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';

const COLLECTION = 'SQ:' + 'ee'.repeat(32);
const OWNER = 'SQ:' + 'ab'.repeat(32);

function conn(answer: (env: { module: string; action: string; params: Record<string, unknown> }) => unknown) {
  const moduleCall = vi.fn(async (env: { module: string; action: string; params: Record<string, unknown> }) =>
    answer(env),
  );
  return { connection: { moduleCall } as unknown as Connection, moduleCall };
}

describe('QRC721 reads', () => {
  it('ownerOf decodes a 32-byte array and scopes the call to the collection', async () => {
    const { connection, moduleCall } = conn(() => Array(32).fill(0xab));
    const nft = getNftCollection(connection, COLLECTION);
    expect(await nft.ownerOf('7')).toBe(OWNER);
    expect(moduleCall.mock.calls[0]![0]).toMatchObject({
      module: 'qrc721',
      action: 'owner_of',
      params: { collection: COLLECTION, id: '7' },
    });
  });

  it('ownerOf accepts an SQ: string and rejects junk', async () => {
    expect(await new QRC721(conn(() => OWNER).connection, COLLECTION).ownerOf('1')).toBe(OWNER);
    await expect(new QRC721(conn(() => [1, 2, 3]).connection, COLLECTION).ownerOf('1')).rejects.toMatchObject({
      code: 'RPC',
    });
  });

  it('balanceOf and collectionInfo accept numbers or strings', async () => {
    expect(await new QRC721(conn(() => 3).connection, COLLECTION).balanceOf(OWNER)).toBe('3');
    const info = await new QRC721(
      conn(() => ({ name: 'Art', symbol: 'ART', owner: OWNER, total_supply: 12 })).connection,
      COLLECTION,
    ).collectionInfo();
    expect(info).toEqual({ name: 'Art', symbol: 'ART', owner: OWNER, total_supply: '12' });
  });

  it('rejects non-integer token ids before calling the node', async () => {
    const { connection, moduleCall } = conn(() => OWNER);
    await expect(new QRC721(connection, COLLECTION).ownerOf('-1')).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    expect(moduleCall).not.toHaveBeenCalled();
  });
});

describe('deployQRC721', () => {
  it('validates name and symbol length', async () => {
    const wallet = { sendModuleCall: vi.fn(), getAddress: () => OWNER } as unknown as Signer;
    await expect(deployQRC721(wallet, { name: '', symbol: 'X' })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(deployQRC721(wallet, { name: 'x'.repeat(65), symbol: 'X' })).rejects.toMatchObject({
      code: 'INVALID_PARAMS',
    });
    expect(wallet.sendModuleCall).not.toHaveBeenCalled();
  });
});

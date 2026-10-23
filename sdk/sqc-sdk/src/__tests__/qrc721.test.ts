import { describe, expect, it, vi } from 'vitest';
import { QRC721, deployQRC721, getNftCollection } from '../tokens/qrc721.js';
import { qrc721Address } from '../addresses/index.js';
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

  it('collectionInfo decodes a byte-array owner', async () => {
    const info = await new QRC721(
      conn(() => ({ name: 'Art', symbol: 'ART', owner: Array(32).fill(0xab), total_supply: '1' })).connection,
      COLLECTION,
    ).collectionInfo();
    expect(info.owner).toBe(OWNER);
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

  it('predicts the address from the nonce the tx was signed with', async () => {
    const sendModuleCall = vi.fn(async () => ({ status: 'submitted', hash: 'h', signedTx: { nonce: 9 } }));
    const wallet = {
      sendModuleCall,
      getAddress: () => OWNER,
      getNonce: async () => ({ nonce: 4 }),
    } as unknown as Signer;
    const { address } = await deployQRC721(wallet, { name: 'Art', symbol: 'ART' }, { fee: '7', module: 'evil' } as never);
    expect(address).toBe(QRC721.predictAddress(OWNER, 9));
    expect(address).not.toBe(QRC721.predictAddress(OWNER, 4));
    expect(sendModuleCall.mock.calls[0]![0]).toMatchObject({ module: 'qrc721', action: 'deploy', fee: '7' });
  });

  it('falls back to the pre-send nonce when the signer returns no signed body', async () => {
    const wallet = {
      sendModuleCall: vi.fn(async () => ({ status: 'submitted', hash: 'h' })),
      getAddress: () => OWNER,
      getNonce: async () => ({ nonce: 4 }),
    } as unknown as Signer;
    const { address } = await deployQRC721(wallet, { name: 'Art', symbol: 'ART' });
    expect(address).toBe(QRC721.predictAddress(OWNER, 4));
  });
});

describe('QRC721 writes', () => {
  it('forward fee and confirmation options without overriding the call', async () => {
    const sendModuleCall = vi.fn(async () => ({ status: 'submitted', hash: 'h' }));
    const wallet = { sendModuleCall, getAddress: () => OWNER } as unknown as Signer;
    const nft = getNftCollection(conn(() => null).connection, COLLECTION);
    await nft.mint(wallet, OWNER, '1', undefined, {
      fee: '5',
      waitForConfirmation: true,
      allowNonceConfirmation: true,
      action: 'burn',
    } as never);
    expect(sendModuleCall.mock.calls[0]![0]).toMatchObject({
      module: 'qrc721',
      action: 'mint',
      fee: '5',
      waitForConfirmation: true,
      allowNonceConfirmation: true,
    });
  });

  it('transfer and burn send their action scoped to the collection, with opts', async () => {
    const sendModuleCall = vi.fn(async () => ({ status: 'submitted', hash: 'h' }));
    const wallet = { sendModuleCall, getAddress: () => OWNER } as unknown as Signer;
    const nft = getNftCollection(conn(() => null).connection, COLLECTION);
    const to = 'SQ:' + 'CD'.repeat(32);
    await nft.transfer(wallet, to, '3', { fee: '9', waitForConfirmation: false });
    await nft.burn(wallet, 3 as unknown as string, { fee: '11' });
    expect(sendModuleCall.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
      expect.objectContaining({
        module: 'qrc721',
        action: 'transfer',
        params: { collection: COLLECTION, to: to.replace(/CD/g, 'cd'), id: '3' },
        fee: '9',
        waitForConfirmation: false,
      }),
      expect.objectContaining({ module: 'qrc721', action: 'burn', params: { collection: COLLECTION, id: '3' }, fee: '11' }),
    ]);
  });

  it('transfer and burn reject bad input before signing', async () => {
    const sendModuleCall = vi.fn();
    const wallet = { sendModuleCall, getAddress: () => OWNER } as unknown as Signer;
    const nft = getNftCollection(conn(() => null).connection, COLLECTION);
    await expect(nft.transfer(wallet, '0xdead', '1')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    await expect(nft.burn(wallet, '1.5')).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(nft.mint(wallet, OWNER, '1', 'x'.repeat(2049))).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    expect(sendModuleCall).not.toHaveBeenCalled();
  });
});

describe('qrc721Address', () => {
  it('is deterministic per deployer and nonce, and matches predictAddress', () => {
    const a = qrc721Address(OWNER, 1);
    expect(a).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(qrc721Address(OWNER, 1)).toBe(a);
    expect(qrc721Address(OWNER, 2)).not.toBe(a);
    expect(QRC721.predictAddress(OWNER, 1)).toBe(a);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Connection, Signer } from '@splendor/sqc-sdk';
import { DEMO_CONTRACT_ADDRESS, DEMO_TOKEN_ADDRESS } from '@splendor/dapp-spec';
import { Contract } from '@splendor/sqc-sdk/contracts';
import { QRC1155, QRC20, QRC721, deployQRC1155, deployQRC20, deployQRC721 } from '@splendor/sqc-sdk/tokens';
import { DEMO_RECIPIENT, MOCK_ADDRESS, MOCK_CONFIRM_MS, createPlaygroundMock } from './mock';

function setup(balance = '10') {
  const ctrl = createPlaygroundMock({ network: 'testnet', state: 'connected', balance });
  const wallet = ctrl.wallet as unknown as Signer & { sendTransfer(o: object): Promise<{ hash: string }>; callContract(o: object): Promise<unknown> };
  const conn = ctrl.wallet.connection as unknown as Connection & { getTxReceipt(h: string): Promise<{ status?: string }> };
  const spld = async () => (await ctrl.getProvider().getBalance(MOCK_ADDRESS)).balance_spld;
  return { ctrl, wallet, conn, spld };
}

afterEach(() => vi.useRealTimers());

describe('createPlaygroundMock', () => {
  it('charges SPLD transfers and their fee', async () => {
    const { wallet, spld } = setup('10');
    await wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '1' });
    expect(await spld()).toBe('8.999979');
  });

  it('fails a transfer the balance cannot cover, without charging anything', async () => {
    const { wallet, spld } = setup('1');
    await expect(wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '5' })).rejects.toThrow(/Insufficient/);
    expect(await spld()).toBe('1.0');
  });

  it('a deployed QRC-20 holds its supply and moves on transfer', async () => {
    const { wallet, conn } = setup();
    const { address } = await deployQRC20(wallet, { name: 'My Token', symbol: 'MYT', decimals: 6, totalSupply: '5000000' });
    const token = new QRC20(conn, address);
    expect(await token.tokenInfo()).toMatchObject({ symbol: 'MYT', decimals: 6, total_supply: '5000000' });
    expect(await token.balanceOf(MOCK_ADDRESS)).toBe('5000000');
    await token.transfer(wallet, DEMO_RECIPIENT, '2000000');
    expect(await token.balanceOf(MOCK_ADDRESS)).toBe('3000000');
    expect(await token.balanceOf(DEMO_RECIPIENT)).toBe('2000000');
  });

  it('two deploys get two different tokens', async () => {
    const { wallet } = setup();
    const a = await deployQRC20(wallet, { name: 'A', symbol: 'A', decimals: 18, totalSupply: '1' });
    const b = await deployQRC20(wallet, { name: 'B', symbol: 'B', decimals: 18, totalSupply: '1' });
    expect(a.address).not.toBe(b.address);
  });

  it('QRC-1155 mints add to the balance', async () => {
    const { wallet, conn } = setup();
    const { address } = await deployQRC1155(wallet, { name: 'Genesis' });
    const c = new QRC1155(conn, address);
    expect(await c.balanceOf(MOCK_ADDRESS, '1')).toBe('0');
    await c.mint(wallet, MOCK_ADDRESS, '1', '2');
    await c.mint(wallet, MOCK_ADDRESS, '1', '1');
    expect(await c.balanceOf(MOCK_ADDRESS, '1')).toBe('3');
  });

  it('QRC-721 mints once per id', async () => {
    const { wallet, conn } = setup();
    const { address } = await deployQRC721(wallet, { name: 'Art', symbol: 'ART' });
    const n = new QRC721(conn, address);
    await n.mint(wallet, MOCK_ADDRESS, '7');
    expect(await n.ownerOf('7')).toBe(MOCK_ADDRESS);
    expect(await n.balanceOf(MOCK_ADDRESS)).toBe('1');
    await expect(n.mint(wallet, MOCK_ADDRESS, '7')).rejects.toThrow(/already minted/);
  });

  it('contract writes are visible to reads', async () => {
    const { wallet, conn } = setup();
    const contract = 'SQ:' + '42'.repeat(32);
    await wallet.callContract({ contract, function: 'set(uint256)', args: ['42'] });
    expect(await new Contract(conn, contract).readUint256('get()')).toBe(42n);
  });

  it('seeds the demo token as a QRC-20 the mock user holds', async () => {
    const { conn } = setup();
    const token = new QRC20(conn, DEMO_TOKEN_ADDRESS);
    const unit = 10n ** 18n;
    expect(await token.tokenInfo()).toMatchObject({ name: 'Demo Token', symbol: 'DEMO', decimals: 18, total_supply: String(1_000_000n * unit) });
    expect(await token.balanceOf(MOCK_ADDRESS)).toBe(String(25_000n * unit));
    expect(await token.balanceOf(DEMO_RECIPIENT)).toBe('0');
  });

  it('answers token reads on the contract-call path too', async () => {
    const { conn } = setup();
    const c = new Contract(conn, DEMO_TOKEN_ADDRESS);
    const unit = 10n ** 18n;
    expect(await c.readUint256('totalSupply()')).toBe(1_000_000n * unit);
    expect(await c.readUint256('decimals()')).toBe(18n);
    expect(await c.readUint256('balanceOf(address)', [MOCK_ADDRESS])).toBe(25_000n * unit);
  });

  it('seeds the demo contract with 42 and a contract-form style write changes it', async () => {
    const { wallet, conn } = setup();
    const c = new Contract(conn, DEMO_CONTRACT_ADDRESS);
    expect(await c.readUint256('get()')).toBe(42n);
    await wallet.callContract({ contract: DEMO_CONTRACT_ADDRESS, function: 'set(uint256)', args: ['7'] });
    expect(await c.readUint256('get()')).toBe(7n);
  });

  it('a pasted QRC-721 collection has owned, foreign and unminted ids; only owned ids move', async () => {
    const { wallet, conn } = setup();
    const n = new QRC721(conn, 'SQ:' + '71'.repeat(32));
    expect(await n.ownerOf('1')).toBe(MOCK_ADDRESS);
    expect(await n.ownerOf('4')).toBe(DEMO_RECIPIENT);
    await expect(n.ownerOf('7')).rejects.toThrow(/does not exist/);
    expect((await n.collectionInfo()).owner).toBe(MOCK_ADDRESS);
    await expect(n.transfer(wallet, MOCK_ADDRESS, '4')).rejects.toThrow(/do not own/);
    await n.transfer(wallet, DEMO_RECIPIENT, '2');
    expect(await n.ownerOf('2')).toBe(DEMO_RECIPIENT);
  });

  it('a bridge reports status and records operator claims once', async () => {
    const { wallet, conn, spld } = setup('10');
    const { BridgeModule } = await import('@splendor/sqc-sdk/bridge');
    const b = new BridgeModule(conn, 'SQ:' + 'b1'.repeat(32));
    expect(await b.info()).toMatchObject({ paused: false, threshold: 2 });
    expect(await b.claimed(1)).toMatchObject({ claimed: true, height: 1050 });
    expect(await b.claimed(2)).toMatchObject({ claimed: false });
    const att = [{ pubkey: 'a', signature: 'b' }, { pubkey: 'c', signature: 'd' }];
    const claim = { id: 2, asset: 'spld' as const, amount: String(5n * 10n ** 18n), recipient: MOCK_ADDRESS, evmTx: '0xabc', attestations: att };
    await b.claim(wallet, claim);
    expect(await b.claimed(2)).toMatchObject({ claimed: true });
    expect(await spld()).toBe('14.999979');
    await expect(b.claim(wallet, claim)).rejects.toThrow(/already claimed/);
    await expect(b.claim(wallet, { ...claim, id: 3, attestations: att.slice(0, 1) })).rejects.toThrow(/attestations/);
  });

  it('lists three demo validators with the 2500 SPLD bond', async () => {
    const { conn } = setup();
    const list = await conn.moduleCall<Array<{ status: number; bond: number }>>({ module: 'validators', action: 'list', params: {} });
    expect(list).toHaveLength(3);
    expect(list.every((v) => v.status === 1 && BigInt(v.bond) === 2500n * 10n ** 18n)).toBe(true);
    expect(await conn.moduleCall({ module: 'validators', action: 'bond', params: {} })).toMatchObject({ spld: '2500' });
  });

  it('emits Transfer logs for token moves and serves them through getLogs with the head height', async () => {
    const { TOPIC_TRANSFER, mergeActivity } = await import('@splendor/connect-react');
    const { wallet, conn } = setup();
    const c = conn as unknown as { getStatus(): Promise<{ height: number }>; getLogs(f: object): Promise<{ logs: unknown[] }> };
    const before = (await c.getStatus()).height;
    expect(before).toBe(1200);
    const seeded = await c.getLogs({ topics: [TOPIC_TRANSFER, '', MOCK_ADDRESS.slice(3)], from_block: 0, to_block: before });
    expect(mergeActivity(seeded.logs, MOCK_ADDRESS).map((i) => i.direction)).toEqual(['in', 'in']);
    await new QRC20(conn, DEMO_TOKEN_ADDRESS).transfer(wallet, DEMO_RECIPIENT, '1000');
    const head = (await c.getStatus()).height;
    expect(head).toBe(before + 1);
    const sent = await c.getLogs({ topics: [TOPIC_TRANSFER, MOCK_ADDRESS.slice(3)], from_block: head, to_block: head });
    expect(mergeActivity(sent.logs, MOCK_ADDRESS)).toMatchObject([{ direction: 'out', amount: '1000', blockHeight: head }]);
  });

  it('settles x402 payments as SPLD sends', async () => {
    const { ctrl, spld } = setup('10');
    const w = ctrl.wallet as unknown as { payX402(o: object): Promise<{ settle: { status: string } }> };
    const res = await w.payX402({ to: DEMO_RECIPIENT, amount: String(10n ** 18n), settle: true });
    expect(res.settle.status).toBe('confirmed');
    expect(await spld()).toBe('8.999979');
    await expect(w.payX402({ to: DEMO_RECIPIENT, amount: '2', maxAmount: '1' })).rejects.toThrow(/exceeds maxAmount/);
  });

  it('sends stay pending until the mock confirms them', async () => {
    vi.useFakeTimers();
    const { wallet, conn } = setup();
    const res = await wallet.sendTransfer({ to: DEMO_RECIPIENT, amountSPLD: '1' });
    expect(res).not.toHaveProperty('confirmation');
    expect((await conn.getTxReceipt(res.hash)).status).toBeUndefined();
    vi.advanceTimersByTime(MOCK_CONFIRM_MS);
    expect((await conn.getTxReceipt(res.hash)).status).toBe('success');
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Connection, Signer } from '@splendor/sqc-sdk';
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

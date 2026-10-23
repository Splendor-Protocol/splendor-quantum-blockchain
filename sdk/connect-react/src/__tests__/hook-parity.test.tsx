/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, fireEvent, render, renderHook, waitFor, within } from '@testing-library/react';
import {
  useCallContract,
  useDeployBridge,
  useDeployBytecode,
  useDeployCollection,
  useDeployQRC721,
  useDeployToken,
  useModuleCall,
  usePreflight,
  useQRC1155,
  useQRC721,
} from '../react/hooks.js';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { SignInButton } from '../widgets/SignInButton.js';
import { resolveThemeAttr } from '../theme/themes.js';
import { __resetPendingTxCache, loadPendingTxs } from '../core/pendingTx.js';
import { __resetRecentTokens, getRecentTokens, rememberDeployedToken } from '../core/recentTokens.js';
import { createMockConnectController, mockWallet, MockSplendorProvider, type MockConnectController } from '../testing.js';
import { WIDGET_MANIFESTS } from '../manifest.js';
import * as root from '../index.js';

const COLLECTION = 'SQ:' + 'ee'.repeat(32);
const TO = 'SQ:' + 'cd'.repeat(32);

let n = 0;
const freshHash = () => (++n).toString(16).padStart(64, '0');

function setup(walletOpts: Parameters<typeof mockWallet>[0] = {}): { ctrl: MockConnectController; wrapper: (p: { children: ReactNode }) => React.ReactElement } {
  const wallet = mockWallet({ hash: freshHash, ...walletOpts });
  const ctrl = createMockConnectController({ network: 'testnet', wallet, balance: '10' });
  const wrapper = ({ children }: { children: ReactNode }) => React.createElement(MockSplendorProvider, { controller: ctrl }, children);
  return { ctrl, wrapper };
}

beforeEach(() => {
  localStorage.clear();
  __resetPendingTxCache();
  __resetRecentTokens();
});
afterEach(() => cleanup());

describe('tx hooks keep stable action identities', () => {
  it('useQRC1155 actions survive a re-render', () => {
    const { wrapper } = setup();
    const { result, rerender } = renderHook(() => useQRC1155(COLLECTION), { wrapper });
    const first = result.current.mint;
    rerender();
    expect(result.current.mint).toBe(first);
  });
});

describe('deploy hooks: single flight, pending list, explorer link', () => {
  it('useDeployCollection records an unconfirmed deploy and links the explorer', async () => {
    const { ctrl, wrapper } = setup({ confirm: false });
    const { result } = renderHook(() => useDeployCollection(), { wrapper });
    await act(async () => {
      await result.current.deploy({ name: 'Genesis' });
    });
    expect(result.current.status).toBe('success');
    expect(result.current.address).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(result.current.explorerUrl).toContain('/tx/');
    expect(loadPendingTxs().map((p) => p.summary)).toEqual([expect.stringContaining('Deploy collection Genesis')]);
    expect(ctrl.wallet.calls.find((c) => c.method === 'sendModuleCall')!.args[0]).toMatchObject({ module: 'qrc1155', action: 'deploy' });
  });

  it('the mock wallet advances its nonce, so consecutive deploys get distinct addresses', async () => {
    const { wrapper } = setup();
    const { result } = renderHook(() => useDeployCollection(), { wrapper });
    const addrs: string[] = [];
    for (const name of ['A', 'B']) {
      await act(async () => {
        addrs.push((await result.current.deploy({ name })).address);
      });
    }
    expect(addrs[0]).not.toBe(addrs[1]);
  });

  it('a second deploy while one runs is refused instead of signing twice', async () => {
    const { ctrl, wrapper } = setup();
    const { result } = renderHook(() => useDeployCollection(), { wrapper });
    let second: Promise<unknown> | undefined;
    await act(async () => {
      const first = result.current.deploy({ name: 'A' });
      second = result.current.deploy({ name: 'B' }).catch((e: unknown) => e);
      await first;
    });
    expect(await second).toMatchObject({ code: expect.stringMatching(/BUSY/) });
    expect(ctrl.wallet.calls.filter((c) => c.method === 'sendModuleCall')).toHaveLength(1);
  });

  it('a failed deploy exposes safe error copy', async () => {
    const { wrapper } = setup({ fail: { sendModuleCall: new Error('User rejected transaction') } });
    const { result } = renderHook(() => useDeployCollection(), { wrapper });
    await act(async () => {
      await result.current.deploy({ name: 'X' }).catch(() => undefined);
    });
    expect(result.current.status).toBe('error');
    expect(result.current.errorMessage).toBeTruthy();
  });

  it('useModuleCall records the send in the pending list', async () => {
    const { wrapper } = setup({ confirm: false });
    const { result } = renderHook(() => useModuleCall(), { wrapper });
    await act(async () => {
      await result.current.call({ module: 'verify', action: 'register', params: { name: 'x' } });
    });
    expect(result.current.explorerUrl).toContain('/tx/');
    expect(loadPendingTxs()[0]).toMatchObject({ kind: 'module', summary: 'verify.register' });
  });
});

describe('QRC-721 hooks', () => {
  it('deploys a collection and mints through the qrc721 module', async () => {
    const { ctrl, wrapper } = setup();
    const { result } = renderHook(() => ({ deployer: useDeployQRC721(), nft: useQRC721(COLLECTION) }), { wrapper });
    await act(async () => {
      await result.current.deployer.deploy({ name: 'Art', symbol: 'ART' });
    });
    expect(result.current.deployer.address).toMatch(/^SQ:/);
    await act(async () => {
      await result.current.nft.mint(TO, '7');
    });
    const sends = ctrl.wallet.calls.filter((c) => c.method === 'sendModuleCall').map((c) => c.args[0]);
    expect(sends).toEqual([
      expect.objectContaining({ module: 'qrc721', action: 'deploy', params: { name: 'Art', symbol: 'ART' } }),
      expect.objectContaining({ module: 'qrc721', action: 'mint', params: expect.objectContaining({ to: TO, id: '7' }) }),
    ]);
    expect(result.current.nft.status).toBe('success');
  });

  it('reads ownership and collection info without a connected wallet', async () => {
    const wallet = mockWallet({
      moduleCall: ({ action }) =>
        action === 'owner_of' ? TO : action === 'balance_of' ? 2 : { name: 'Art', symbol: 'ART', owner: TO, total_supply: 5 },
    });
    const ctrl = createMockConnectController({ network: 'testnet', wallet, status: 'disconnected' });
    const wrapper = ({ children }: { children: ReactNode }) => React.createElement(MockSplendorProvider, { controller: ctrl }, children);
    const { result } = renderHook(() => useQRC721(COLLECTION), { wrapper });
    expect(await result.current.ownerOf('7')).toBe(TO);
    expect(await result.current.balanceOf(TO)).toBe('2');
    expect(await result.current.collectionInfo()).toEqual({ name: 'Art', symbol: 'ART', owner: TO, total_supply: '5' });
    expect(wallet.calls.filter((c) => c.method === 'connection.moduleCall').map((c) => (c.args[0] as { action: string }).action)).toEqual([
      'owner_of',
      'balance_of',
      'token_info',
    ]);
    await expect(result.current.mint(TO, '1')).rejects.toThrow(/not ready/i);
    expect(wallet.calls.some((c) => c.method === 'sendModuleCall')).toBe(false);
  });

  it('mint, transfer and burn forward WriteOpts and record a pending send each', async () => {
    const { ctrl, wrapper } = setup({ confirm: false });
    const { result } = renderHook(() => useQRC721(COLLECTION), { wrapper });
    await act(async () => {
      await result.current.mint(TO, '1', 'ipfs://x', { fee: '1111' });
    });
    await act(async () => {
      await result.current.transfer(TO, '1', { fee: '2222', waitForConfirmation: false });
    });
    await act(async () => {
      await result.current.burn('1', { fee: '3333' });
    });
    const sends = ctrl.wallet.calls.filter((c) => c.method === 'sendModuleCall').map((c) => c.args[0]);
    expect(sends).toEqual([
      expect.objectContaining({ module: 'qrc721', action: 'mint', params: { collection: COLLECTION, to: TO, id: '1', uri: 'ipfs://x' }, fee: '1111' }),
      expect.objectContaining({ module: 'qrc721', action: 'transfer', params: { collection: COLLECTION, to: TO, id: '1' }, fee: '2222', waitForConfirmation: false }),
      expect.objectContaining({ module: 'qrc721', action: 'burn', params: { collection: COLLECTION, id: '1' }, fee: '3333' }),
    ]);
    expect(loadPendingTxs().map((p) => p.summary)).toEqual(
      expect.arrayContaining([`Mint NFT #1 to ${TO}`, `Send NFT #1 to ${TO}`, 'Burn NFT #1']),
    );
  });
});

describe('contract, bridge and preflight hooks', () => {
  const CONTRACT = 'SQ:' + '12'.repeat(32);

  function disconnected() {
    const wallet = mockWallet();
    const ctrl = createMockConnectController({ network: 'testnet', wallet, status: 'disconnected' });
    const wrapper = ({ children }: { children: ReactNode }) => React.createElement(MockSplendorProvider, { controller: ctrl }, children);
    return { wallet, wrapper };
  }

  it('useCallContract sends through callContract and records the call', async () => {
    const { ctrl, wrapper } = setup({ confirm: false });
    const { result } = renderHook(() => useCallContract(), { wrapper });
    const opts = { contract: CONTRACT, function: 'set(uint256)', args: ['42'], fee: '5000' };
    await act(async () => {
      await result.current.callContract(opts as never);
    });
    expect(ctrl.wallet.calls.find((c) => c.method === 'callContract')!.args[0]).toEqual(opts);
    expect(result.current.status).toBe('success');
    expect(result.current.explorerUrl).toContain('/tx/');
    expect(loadPendingTxs()[0]).toMatchObject({ kind: 'call', summary: `Call set(uint256) on ${CONTRACT}` });
  });

  it('useCallContract refuses without a wallet', async () => {
    const { wallet, wrapper } = disconnected();
    const { result } = renderHook(() => useCallContract(), { wrapper });
    await expect(result.current.callContract({ contract: CONTRACT } as never)).rejects.toThrow(/not connected/i);
    expect(wallet.calls.some((c) => c.method === 'callContract')).toBe(false);
  });

  it('useDeployBytecode deploys with the given fee and predicts the address from the signed nonce', async () => {
    const { ctrl, wrapper } = setup();
    const { result } = renderHook(() => useDeployBytecode(), { wrapper });
    let address = '';
    await act(async () => {
      address = (await result.current.deploy('0x0061736d', { fee: '7000' })).address;
    });
    expect(ctrl.wallet.calls.find((c) => c.method === 'deployContract')!.args[0]).toEqual({ bytecode: '0x0061736d', fee: '7000' });
    expect(address).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(result.current.address).toBe(address);
    expect(result.current.status).toBe('success');
  });

  it('useDeployBytecode refuses without a wallet', async () => {
    const { wallet, wrapper } = disconnected();
    const { result } = renderHook(() => useDeployBytecode(), { wrapper });
    await act(async () => {
      await expect(result.current.deploy('0x00')).rejects.toThrow(/not connected/i);
    });
    expect(result.current.status).toBe('error');
    expect(wallet.calls.some((c) => c.method === 'deployContract')).toBe(false);
  });

  const bridgeParams = {
    evmChainId: 1,
    evmContract: '0x' + 'ab'.repeat(20),
    splvName: 'Bridged SPLV',
    splvSymbol: 'SPLV',
    splvMaxSupply: '1000000',
    maxClaimAmount: '1000',
    threshold: 2,
  };

  it('useDeployBridge sends bridge.deploy and returns the bridge and SPLV addresses', async () => {
    const { ctrl, wrapper } = setup();
    const { result } = renderHook(() => useDeployBridge(), { wrapper });
    let res = { address: '', splvAddress: '' };
    await act(async () => {
      res = await result.current.deploy({ ...bridgeParams, fee: '9000' });
    });
    expect(ctrl.wallet.calls.find((c) => c.method === 'sendModuleCall')!.args[0]).toMatchObject({
      module: 'bridge',
      action: 'deploy',
      params: { evm_chain_id: 1, splv_symbol: 'SPLV', splv_max_supply: '1000000', max_claim_amount: '1000', threshold: 2 },
      fee: '9000',
    });
    expect(res.address).toMatch(/^SQ:/);
    expect(res.splvAddress).toMatch(/^SQ:/);
    expect(res.splvAddress).not.toBe(res.address);
    expect(result.current.address).toBe(res.address);
  });

  it('useDeployBridge rejects bad params before signing', async () => {
    const { ctrl, wrapper } = setup();
    const { result } = renderHook(() => useDeployBridge(), { wrapper });
    await act(async () => {
      await expect(result.current.deploy({ ...bridgeParams, threshold: 0 })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    });
    expect(result.current.status).toBe('error');
    expect(result.current.errorMessage).toBeTruthy();
    expect(ctrl.wallet.calls.some((c) => c.method === 'sendModuleCall')).toBe(false);
  });

  it('usePreflight returns the wallet preflight and surfaces failures', async () => {
    const { ctrl, wrapper } = setup();
    const preflight = vi.fn(async () => ({ ok: true, balance: '10', fee: '1000' }));
    (ctrl.wallet as unknown as { preflight: typeof preflight }).preflight = preflight;
    const { result } = renderHook(() => usePreflight(), { wrapper });
    await act(async () => {
      await result.current.preflight({ amountSPLD: '1' });
    });
    expect(preflight).toHaveBeenCalledWith({ amountSPLD: '1' });
    expect(result.current).toMatchObject({ status: 'success', result: { ok: true } });

    preflight.mockRejectedValueOnce(new Error('insufficient balance'));
    await act(async () => {
      await expect(result.current.preflight()).rejects.toThrow(/insufficient/);
    });
    expect(preflight).toHaveBeenLastCalledWith({});
    expect(result.current.status).toBe('error');
    expect(result.current.errorMessage).toBeTruthy();
    act(() => result.current.reset());
    expect(result.current).toMatchObject({ status: 'idle', result: null, error: null });
  });

  it('usePreflight refuses without a wallet', async () => {
    const { wrapper } = disconnected();
    const { result } = renderHook(() => usePreflight(), { wrapper });
    await expect(result.current.preflight()).rejects.toThrow(/not connected/i);
    expect(result.current.status).toBe('idle');
  });
});

describe('recently deployed tokens', () => {
  it('useDeployToken remembers the token for the current network', async () => {
    const { wrapper } = setup();
    const { result } = renderHook(() => useDeployToken(), { wrapper });
    await act(async () => {
      await result.current.deploy({ name: 'My Token', symbol: 'MYT', decimals: 18, totalSupply: '1000' });
    });
    expect(getRecentTokens()).toEqual([{ address: result.current.address, symbol: 'MYT', network: 'testnet' }]);
  });

  it('TokenPanel offers tokens deployed on this network and fills the address', async () => {
    rememberDeployedToken({ address: COLLECTION, symbol: 'MYT', network: 'testnet' });
    rememberDeployedToken({ address: TO, symbol: 'OTHER', network: 'mainnet' });
    const { wrapper } = setup();
    const view = render(React.createElement(TokenPanel, { showFee: false }), { wrapper });
    const chips = within(await within(view.container).findByTestId('sqc-recent-tokens'));
    expect(chips.queryByText('OTHER')).toBeNull();
    fireEvent.click(chips.getByRole('button', { name: 'MYT' }));
    expect((view.container.querySelector('input') as HTMLInputElement).value).toBe(COLLECTION);
  });

  it('ignores malformed entries', () => {
    rememberDeployedToken({ address: 'nope', symbol: 'X', network: 'testnet' });
    rememberDeployedToken({ address: COLLECTION, symbol: '<script>', network: 'testnet' });
    expect(getRecentTokens()).toEqual([]);
  });
});

describe('SignInButton', () => {
  it('routes a rejected async onSuccess to onError instead of an unhandled rejection', async () => {
    const { wrapper } = setup();
    const onError = vi.fn();
    const onSuccess = vi.fn(async () => {
      throw new Error('server down');
    });
    const view = render(React.createElement(SignInButton, { onSuccess, onError }), { wrapper });
    fireEvent.click(within(view.container).getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: 'server down' })));
    expect(onSuccess).toHaveBeenCalledOnce();
  });
});

describe('widget manifests', () => {
  it.each(WIDGET_MANIFESTS.filter((m) => m.placeable).map((m) => [m.id, m] as const))(
    '%s renders with its manifest defaults',
    (_id, m) => {
      const Component = (root as unknown as Record<string, React.ComponentType<Record<string, unknown>>>)[m.importName]!;
      const { wrapper } = setup();
      const view = render(React.createElement(Component, { ...m.defaults }), { wrapper });
      expect(view.container.firstChild).toBeTruthy();
    },
  );
});

describe('theme base with overrides', () => {
  it('keeps the base theme when overrides are set', () => {
    expect(resolveThemeAttr({ base: 'light', accent: '#ff0000' })).toBe('light');
    expect(resolveThemeAttr({ accent: '#ff0000' })).toBe('splendor');
    expect(resolveThemeAttr({ base: 'nope' as never })).toBe('splendor');
  });

  it('MockSplendorProvider applies the theme', () => {
    const view = render(React.createElement(MockSplendorProvider, { theme: { base: 'light', radius: '4px' } }, 'x'));
    const root = view.container.querySelector('.sqc-root') as HTMLElement;
    expect(root.dataset.sqcTheme).toBe('light');
    expect(root.style.getPropertyValue('--sqc-radius')).toBe('4px');
  });
});

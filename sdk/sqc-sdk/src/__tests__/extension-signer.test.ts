/**
 * Extension provider aliases + ExtensionSigner (mock provider, no Chrome).
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { CHAIN_ID } from '../derive.js';
import {
  toCanonicalMethod,
  toExtensionMethod,
  toExtensionParams,
  requestWithAlias,
  isExtensionProvider,
} from '../wallet/extension-provider.js';
import { ExtensionSigner } from '../wallet/extension-signer.js';
import { createSplendorProvider, uninstallSplendorProvider } from '../injected-provider.js';
import { createWalletClient } from '../clients.js';
import { resetWasmLoader } from '../wasm-loader.js';
import { BridgeError } from '../errors.js';

describe('extension method aliases', () => {
  it('maps sqc_* to splendor_* canonical', () => {
    expect(toCanonicalMethod('sqc_accounts')).toBe('splendor_accounts');
    expect(toCanonicalMethod('sqc_requestAccounts')).toBe('splendor_connect');
    expect(toCanonicalMethod('sqc_sign')).toBe('splendor_signMessage');
  });

  it('maps splendor_* to sqc_* / wallet_* for extension calls', () => {
    expect(toExtensionMethod('splendor_accounts')).toBe('sqc_accounts');
    expect(toExtensionMethod('splendor_signMessage')).toBe('sqc_sign');
    expect(toExtensionMethod('splendor_disconnect')).toBe('wallet_disconnect');
    expect(toCanonicalMethod('wallet_disconnect')).toBe('splendor_disconnect');
  });

  it('toExtensionParams coerces objects to a one-element array', () => {
    expect(toExtensionParams(undefined)).toEqual([]);
    expect(toExtensionParams(null)).toEqual([]);
    expect(toExtensionParams([1, 2])).toEqual([1, 2]);
    expect(toExtensionParams({ to: 'SQ:ab' })).toEqual([{ to: 'SQ:ab' }]);
  });

  it('requestWithAlias always sends array params', async () => {
    const provider = {
      request: vi.fn(async () => ['SQ:' + 'ab'.repeat(32)]),
    };
    await requestWithAlias(provider, 'sqc_accounts', { ignored: true });
    expect(provider.request).toHaveBeenCalledWith({
      method: 'sqc_accounts',
      params: [{ ignored: true }],
    });
  });

  it('requestWithAlias falls back on unsupported', async () => {
    const provider = {
      request: vi.fn(async ({ method }: { method: string }) => {
        if (method === 'sqc_accounts') return ['SQ:' + 'ab'.repeat(32)];
        throw new Error('unsupported method');
      }),
    };
    const out = await requestWithAlias(provider, 'splendor_accounts');
    expect(out).toEqual(['SQ:' + 'ab'.repeat(32)]);
    expect(provider.request).toHaveBeenCalled();
  });

  it('isExtensionProvider accepts isExtension or non-in-page isSplendor', () => {
    const req = async () => null;
    expect(isExtensionProvider({ isSplendor: true, request: req })).toBe(true);
    expect(isExtensionProvider({ isSplendor: true, isInPage: true, request: req })).toBe(false);
    expect(isExtensionProvider({ isExtension: true, request: req })).toBe(true);
    // Page scripts must not spoof via isExtension + isInPage.
    expect(
      isExtensionProvider({ isExtension: true, isInPage: true, isSplendor: true, request: req }),
    ).toBe(false);
  });
});

describe('ExtensionSigner', () => {
  const addr = 'SQ:' + 'cd'.repeat(32);
  const HASH = '0x' + 'de'.repeat(32);
  const MOD_HASH = '0x' + 'ef'.repeat(32);

  function mockProvider(
    handlers: Record<string, (params?: unknown) => unknown> = {},
  ) {
    const listeners = new Map<string, Set<(...args: unknown[]) => void>>();
    return {
      isSplendor: true as const,
      request: vi.fn(async ({ method, params }: { method: string; params?: unknown }) => {
        if (handlers[method]) return handlers[method]!(params);
        if (method === 'sqc_chainId') return CHAIN_ID;
        if (method === 'sqc_requestAccounts' || method === 'splendor_connect') return [addr];
        if (method === 'sqc_accounts' || method === 'splendor_accounts') return [addr];
        if (method === 'wallet_disconnect' || method === 'sqc_disconnect') return null;
        if (method === 'sqc_sign' || method === 'splendor_signMessage') {
          return { address: addr, signature: 'c2ln', publicKeyB64: 'cGs=', message: 'hi' };
        }
        if (method === 'sqc_sendTransaction') {
          return HASH;
        }
        throw new Error(`unexpected ${method}`);
      }),
      on(event: string, fn: (...args: unknown[]) => void) {
        if (!listeners.has(event)) listeners.set(event, new Set());
        listeners.get(event)!.add(fn);
      },
      removeListener(event: string, fn: (...args: unknown[]) => void) {
        listeners.get(event)?.delete(fn);
      },
      emit(event: string, ...args: unknown[]) {
        listeners.get(event)?.forEach((fn) => fn(...args));
      },
    };
  }

  function connection() {
    return new Connection({
      rpcUrl: 'http://127.0.0.1:9',
      chainId: CHAIN_ID,
      retries: 0,
    });
  }

  it('connects via mock extension provider', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    const info = await wallet.connect();
    expect(info.address).toBe(addr);
    expect(info.postQuantumSecure).toBe(true);
    const signed = await wallet.signMessage('hello');
    expect(signed.signatureB64).toBeTruthy();
    wallet.disconnect();
  });

  it('rejects signMessage with raw:true (same as inject)', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const callsBefore = provider.request.mock.calls.length;
    await expect(wallet.signMessage('x', { raw: true })).rejects.toMatchObject({
      code: 'USER_REJECTED',
    });
    await expect(wallet.signMessage('x', { raw: true })).rejects.toBeInstanceOf(BridgeError);
    expect(provider.request.mock.calls.length).toBe(callsBefore);
    wallet.disconnect();
  });

  it('waitForConfirmation delegates to connection with from default', async () => {
    const provider = mockProvider();
    const conn = connection();
    const wait = vi.spyOn(conn, 'waitForConfirmation').mockResolvedValue({
      confirmed: true as const,
      nonce: 2,
    });
    const wallet = new ExtensionSigner({ connection: conn, provider, networkId: 'local' });
    await wallet.connect();
    const out = await wallet.waitForConfirmation({ nonceBefore: 1, hash: '0xabc' });
    expect(out).toEqual({ confirmed: true, nonce: 2 });
    expect(wait).toHaveBeenCalledWith({
      nonceBefore: 1,
      hash: '0xabc',
      from: addr,
    });
    wallet.disconnect();
  });

  it('sendTransfer confirmation requires a receipt unless allowNonceConfirmation is set', async () => {
    const provider = mockProvider();
    const conn = connection();
    vi.spyOn(conn, 'getNonce').mockResolvedValue({ address: addr, nonce: 1 } as never);
    const wait = vi.spyOn(conn, 'waitForConfirmation').mockResolvedValue({ confirmed: true as const, nonce: 2 });
    const wallet = new ExtensionSigner({ connection: conn, provider, networkId: 'local' });
    await wallet.connect();
    await wallet.sendTransfer({ to: addr, amountSPLD: '1', waitForConfirmation: true });
    expect(wait.mock.calls.at(-1)![0]).toMatchObject({ nonceBefore: 1, requireReceipt: true });
    await wallet.sendTransfer({ to: addr, amountSPLD: '1', waitForConfirmation: true, allowNonceConfirmation: true });
    expect(wait.mock.calls.at(-1)![0]).toMatchObject({ requireReceipt: false });
    wallet.disconnect();
  });

  it('signMessage sends array params [message, encoding]', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    await wallet.signMessage('hello');
    const signCall = provider.request.mock.calls.find(
      (c) => c[0]?.method === 'sqc_sign' || c[0]?.method === 'splendor_signMessage',
    );
    expect(Array.isArray(signCall?.[0]?.params)).toBe(true);
    expect(signCall?.[0]?.params?.[1]).toEqual({ encoding: 'utf8' });
    wallet.disconnect();
  });

  it('sendTransfer uses flat body and normalizes hash string', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const out = await wallet.sendTransfer({
      to: addr,
      amountSPLD: '0.01',
      fee: '1000',
    });
    expect(out.hash).toBe(HASH);
    expect(out.status).toBe('ok');
    const sendCall = provider.request.mock.calls.find((c) => c[0]?.method === 'sqc_sendTransaction');
    expect(sendCall?.[0]?.params).toEqual([{ to: addr, amount: '10000000000000000', fee: '1000' }]);
    wallet.disconnect();
  });

  it('sendTransfer converts decimal amountSPLD to integer base units', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    await wallet.sendTransfer({ to: addr, amountSPLD: '0.001' });
    const sendCall = provider.request.mock.calls.find((c) => c[0]?.method === 'sqc_sendTransaction');
    const body = sendCall?.[0]?.params?.[0] as Record<string, unknown>;
    expect(body.amount).toBe('1000000000000000');
    expect(body).not.toHaveProperty('amountSPLD');
    wallet.disconnect();
  });

  it('disconnect calls wallet_disconnect', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    wallet.disconnect();
    expect(
      provider.request.mock.calls.some((c) => c[0]?.method === 'wallet_disconnect'),
    ).toBe(true);
  });

  it('lock event from provider emits wallet lock', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const locks: unknown[] = [];
    wallet.on('lock', (p) => locks.push(p));
    provider.emit('lock');
    expect(locks).toHaveLength(1);
    expect(wallet.getConnectionState()).toBe('disconnected');
  });

  it('chainChanged string from provider maps to structured event', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const seen: unknown[] = [];
    wallet.on('chainChanged', (p) => seen.push(p));
    provider.emit('chainChanged', 'splendor-quantum-testnet-1');
    expect(seen[0]).toMatchObject({ networkId: 'local' });
    wallet.disconnect();
  });

  it('refreshWalletChainId reads sqc_chainId and chainChanged keeps it current', async () => {
    const provider = mockProvider({ sqc_chainId: () => CHAIN_ID });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    expect(wallet.walletChainId).toBeNull();
    expect(await wallet.refreshWalletChainId()).toBe(CHAIN_ID);
    provider.emit('chainChanged', 'splendor-quantum-testnet-1');
    expect(wallet.walletChainId).toBe('splendor-quantum-testnet-1');
    wallet.disconnect();
  });

  it('sendTransfer refuses with CHAIN_MISMATCH when the extension is on another chain', async () => {
    const provider = mockProvider({ sqc_chainId: () => 'splendor-quantum-testnet-1' });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const err = await wallet.sendTransfer({ to: addr, amountSPLD: '1' }).catch((e) => e);
    expect(err).toBeInstanceOf(BridgeError);
    expect(err).toMatchObject({ code: 'CHAIN_MISMATCH' });
    expect(err.userHint).toContain('splendor-quantum-testnet-1');
    expect(provider.request.mock.calls.some((c) => c[0]?.method === 'sqc_sendTransaction')).toBe(
      false,
    );
    wallet.disconnect();
  });

  it('sendModuleCall and preflight refuse on chain mismatch before touching the wallet', async () => {
    const provider = mockProvider({
      sqc_chainId: () => 'splendor-quantum-devnet-1',
      sqc_moduleCall: () => '0xshould-not-run',
    });
    const conn = connection();
    vi.spyOn(conn, 'assertChainId').mockResolvedValue(undefined as never);
    const wallet = new ExtensionSigner({ connection: conn, provider, networkId: 'local' });
    await wallet.connect();
    await expect(
      wallet.sendModuleCall({ module: 'qrc20', action: 'transfer', params: {} }),
    ).rejects.toMatchObject({ code: 'CHAIN_MISMATCH' });
    await expect(wallet.preflight({ amountSPLD: '1' })).rejects.toMatchObject({
      code: 'CHAIN_MISMATCH',
    });
    expect(provider.request.mock.calls.some((c) => c[0]?.method === 'sqc_moduleCall')).toBe(false);
    wallet.disconnect();
  });

  it('sends when the extension chain matches; refuses an unknown chain unless opted out', async () => {
    const matching = mockProvider({ sqc_chainId: () => CHAIN_ID });
    const w1 = new ExtensionSigner({ connection: connection(), provider: matching, networkId: 'local' });
    await w1.connect();
    expect((await w1.sendTransfer({ to: addr, amountSPLD: '1' })).hash).toBe(HASH);
    w1.disconnect();

    const legacy = mockProvider({ sqc_chainId: () => { throw new Error('unsupported method'); } });
    const w2 = new ExtensionSigner({ connection: connection(), provider: legacy, networkId: 'local' });
    await w2.connect();
    await expect(w2.sendTransfer({ to: addr, amountSPLD: '1' })).rejects.toMatchObject({
      code: 'CHAIN_MISMATCH',
    });
    expect(legacy.request.mock.calls.some((c) => c[0]?.method === 'sqc_sendTransaction')).toBe(false);
    w2.disconnect();

    const optedOut = mockProvider({ sqc_chainId: () => { throw new Error('unsupported method'); } });
    const w3 = new ExtensionSigner({
      connection: connection(),
      provider: optedOut,
      networkId: 'local',
      allowUnknownWalletChain: true,
    });
    await w3.connect();
    expect((await w3.sendTransfer({ to: addr, amountSPLD: '1' })).hash).toBe(HASH);
    w3.disconnect();
  });

  it('sendModuleCall refuses params the extension cannot base64-encode', async () => {
    const provider = mockProvider({ sqc_moduleCall: () => '0xshould-not-run' });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const err = await wallet
      .sendModuleCall({ module: 'qrc20', action: 'transfer', params: { token: 'SQ:…', to: addr, amount: '1' } })
      .catch((e) => e);
    expect(err).toMatchObject({ code: 'INVALID_PARAMS' });
    expect(err.message).toContain('U+2026');
    expect(err.userHint).toMatch(/cannot encode/);
    expect(provider.request.mock.calls.some((c) => c[0]?.method === 'sqc_moduleCall')).toBe(false);
    wallet.disconnect();
  });

  it('sendModuleCall accepts Latin-1 params', async () => {
    const provider = mockProvider({ sqc_moduleCall: () => MOD_HASH });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const out = await wallet.sendModuleCall({
      module: 'qrc20',
      action: 'transfer',
      params: { token: addr, to: addr, amount: '1', memo: 'café' },
    });
    expect(out.hash).toBe(MOD_HASH);
    wallet.disconnect();
  });

  it('sendModuleCall does not fall back to send on USER_REJECTED', async () => {
    const provider = mockProvider({
      sqc_moduleCall: () => {
        const e = new Error('User rejected') as Error & { code: number };
        e.code = 4001;
        throw e;
      },
      sqc_sendTransaction: () => '0xshould-not-run',
    });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    await expect(
      wallet.sendModuleCall({ module: 'qrc20', action: 'transfer', params: {} }),
    ).rejects.toMatchObject({ code: 'USER_REJECTED' });
    expect(provider.request.mock.calls.some((c) => c[0]?.method === 'sqc_sendTransaction')).toBe(
      false,
    );
    wallet.disconnect();
  });

  it('maps 4100 / 4902 via req to BridgeError codes', async () => {
    const provider = mockProvider({
      sqc_sign: () => {
        const e = new Error('Wallet locked') as Error & { code: number };
        e.code = 4100;
        throw e;
      },
    });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    await expect(wallet.signMessage('x')).rejects.toMatchObject({ code: 'NOT_CONNECTED' });

    const provider2 = mockProvider({
      sqc_sign: () => {
        const e = new Error('chain mismatch') as Error & { code: number };
        e.code = 4902;
        throw e;
      },
    });
    const wallet2 = new ExtensionSigner({
      connection: connection(),
      provider: provider2,
      networkId: 'local',
    });
    await wallet2.connect();
    await expect(wallet2.signMessage('x')).rejects.toMatchObject({ code: 'CHAIN_MISMATCH' });
    wallet.disconnect();
    wallet2.disconnect();
  });

  it('4001 does not trigger alias retry on requestWithAlias', async () => {
    const provider = {
      request: vi.fn(async () => {
        const e = new Error('User rejected') as Error & { code: number };
        e.code = 4001;
        throw e;
      }),
    };
    await expect(requestWithAlias(provider, 'splendor_accounts')).rejects.toMatchObject({
      code: 4001,
    });
    expect(provider.request).toHaveBeenCalledTimes(1);
  });
});

describe('in-page inject dual methods', () => {
  afterEach(() => {
    uninstallSplendorProvider();
    resetWasmLoader();
  });

  it('accepts sqc_accounts alias when wallet connected', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    // Avoid WASM if missing — only test alias routing with unlocked mock-like path:
    // Use createSplendorProvider after manually setting getAddresses via seed if WASM available.
    try {
      await wallet.connect({ mode: 'seed', seed: '01'.repeat(32) });
    } catch {
      // WASM missing in this environment — skip
      wallet.disconnect();
      return;
    }
    const provider = createSplendorProvider({
      wallet,
      networkId: 'local',
      onApprove: async () => true,
    });
    const accounts = await provider.request({ method: 'sqc_accounts' });
    expect(Array.isArray(accounts)).toBe(true);
    expect((accounts as string[])[0]).toMatch(/^SQ:/);
    wallet.disconnect();
  }, 30_000);
});

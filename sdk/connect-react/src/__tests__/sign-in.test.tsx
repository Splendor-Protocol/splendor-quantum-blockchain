/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import {
  renderHook,
  act,
  render,
  fireEvent,
  within,
  waitFor,
  cleanup,
} from '@testing-library/react';
import { useSignIn, createAuthMessage } from '../react/hooks.js';
import { parseAuthMessage, checkAuthMessage, authCheckMessage } from '../react/auth.js';
import { SignInButton } from '../widgets/SignInButton.js';
import * as hooksEntry from '../hooks.js';
import { ControllerContext, NetworkSwitchContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);

function makeState(overrides: Partial<ConnectControllerState> = {}): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: {
      address: ADDR,
      evmAddress: undefined,
      connectorId: 'seed',
      keyOrigin: 'native',
      postQuantumSecure: true,
    },
    info: null,
    error: null,
    modal: 'closed',
    connectors: [],
    activeConnectorId: 'seed',
    network: { id: 'local', chainId: 'splendor-quantum-1' },
    ...overrides,
  } as unknown as ConnectControllerState;
}

function makeController(
  state: ConnectControllerState,
  signMessage = vi.fn(async (message: string) => ({
    message,
    signatureB64: 'sig',
    publicKeyB64: 'pk',
  })),
): ConnectController {
  const wallet = { signMessage };
  return {
    getState: () => state,
    subscribe: () => () => undefined,
    getWallet: () => wallet,
    getProvider: () => ({}),
    getWalletEpoch: () => 0,
  } as unknown as ConnectController;
}

function wrapperFor(ctrl: ConnectController) {
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(
      ControllerContext.Provider,
      { value: ctrl },
      React.createElement(
        NetworkSwitchContext.Provider,
        {
          value: {
            switchNetwork: vi.fn(async () => undefined) as (id: never) => Promise<void>,
            allowPlaceholderNetworks: true,
            networkId: 'local' as never,
            switching: false,
          },
        },
        children,
      ),
    );
  };
}

describe('createAuthMessage', () => {
  it('builds a Connect v2 challenge with optional fields in order', () => {
    const msg = createAuthMessage({
      domain: 'https://app.example.com',
      statement: 'Welcome',
      address: ADDR,
      chainId: 'splendor-quantum-1',
      nonce: 'n1',
      issuedAt: 100,
      expirationTime: 200,
    });
    expect(msg.split('\n')).toEqual([
      'Splendor Connect v2',
      'Sign in to https://app.example.com',
      'Welcome',
      `Address: ${ADDR}`,
      'Chain ID: splendor-quantum-1',
      'Nonce: n1',
      'Issued At: 100',
      'Expiration Time: 200',
    ]);
  });

  it('omits unset optional fields', () => {
    const msg = createAuthMessage({ domain: 'd', issuedAt: 1 });
    expect(msg).toBe('Splendor Connect v2\nSign in to d\nIssued At: 1');
  });
});

describe('useSignIn', () => {
  it('signs a message bound to the connected address and chain', async () => {
    const signMessage = vi.fn(async (message: string) => ({
      message,
      signatureB64: 'sig',
      publicKeyB64: 'pk',
    }));
    const ctrl = makeController(makeState(), signMessage);
    const { result } = renderHook(() => useSignIn(), { wrapper: wrapperFor(ctrl) });

    await act(async () => {
      const out = await result.current.signIn({ domain: 'https://app.example.com', issuedAt: 5 });
      expect(out).toMatchObject({
        signatureB64: 'sig',
        publicKeyB64: 'pk',
        address: ADDR,
        messageDomain: 'Splendor Connect v2',
      });
    });

    const signed = signMessage.mock.calls[0][0];
    expect(signed).toContain(`Address: ${ADDR}`);
    expect(signed).toContain('Chain ID: splendor-quantum-1');
    expect(result.current.status).toBe('success');
    expect(result.current.result?.address).toBe(ADDR);
  });

  it('surfaces wallet errors and reset clears state', async () => {
    const signMessage = vi.fn(async () => {
      throw new Error('rejected');
    });
    const ctrl = makeController(makeState(), signMessage);
    const { result } = renderHook(() => useSignIn(), { wrapper: wrapperFor(ctrl) });

    await act(async () => {
      await expect(result.current.signIn({ domain: 'd' })).rejects.toThrow(/rejected/);
    });
    expect(result.current.status).toBe('error');
    expect(result.current.errorMessage).toBeTruthy();

    act(() => result.current.reset());
    expect(result.current.status).toBe('idle');
    expect(result.current.error).toBeNull();
  });

  it('throws when disconnected', async () => {
    const ctrl = makeController(makeState({ status: 'disconnected', account: null }));
    const { result } = renderHook(() => useSignIn(), { wrapper: wrapperFor(ctrl) });
    await expect(result.current.signIn({ domain: 'd' })).rejects.toThrow(/not connected/i);
  });
});

describe('parseAuthMessage', () => {
  it('round-trips every field createAuthMessage writes', () => {
    const msg = createAuthMessage({
      domain: 'https://app.example.com',
      statement: 'Welcome',
      address: ADDR,
      chainId: 'splendor-quantum-1',
      nonce: 'n1',
      issuedAt: 100,
      expirationTime: 200,
    });
    expect(parseAuthMessage(msg)).toEqual({
      domain: 'https://app.example.com',
      statement: 'Welcome',
      address: ADDR,
      chainId: 'splendor-quantum-1',
      nonce: 'n1',
      issuedAt: 100,
      expirationTime: 200,
    });
  });

  it('parses a minimal message without statement or nonce', () => {
    expect(parseAuthMessage(createAuthMessage({ domain: 'd', issuedAt: 1 }))).toEqual({
      domain: 'd',
      issuedAt: 1,
    });
  });

  it.each([
    ['wrong prefix', 'Splendor Connect v1\nSign in to d\nIssued At: 1'],
    ['missing sign-in line', 'Splendor Connect v2\nIssued At: 1'],
    ['empty domain', 'Splendor Connect v2\nSign in to \nIssued At: 1'],
    ['missing Issued At', 'Splendor Connect v2\nSign in to d\nNonce: n'],
    ['non-numeric Issued At', 'Splendor Connect v2\nSign in to d\nIssued At: soon'],
    ['duplicate field', 'Splendor Connect v2\nSign in to d\nIssued At: 1\nIssued At: 2'],
    ['stray line after fields', 'Splendor Connect v2\nSign in to d\nIssued At: 1\nextra'],
    ['eth SIWE text', 'app.example.com wants you to sign in with your Ethereum account:'],
  ])('rejects %s', (_label, msg) => {
    expect(parseAuthMessage(msg)).toBeNull();
  });
});

describe('checkAuthMessage', () => {
  const base = { domain: 'https://app.example.com', issuedAt: 1000, expirationTime: 1300 };

  it('accepts a fresh challenge for this domain', () => {
    const res = checkAuthMessage(createAuthMessage(base), {
      domain: 'https://app.example.com/',
      now: 1100,
    });
    expect(res.ok).toBe(true);
  });

  it('matches domain case-insensitively and ignores trailing slash', () => {
    const res = checkAuthMessage(createAuthMessage(base), {
      domain: 'HTTPS://APP.EXAMPLE.COM',
      now: 1100,
    });
    expect(res.ok).toBe(true);
  });

  it('rejects a different domain', () => {
    expect(
      checkAuthMessage(createAuthMessage(base), { domain: 'https://evil.example', now: 1100 }),
    ).toEqual({ ok: false, reason: 'domain' });
  });

  it('rejects when expired (including exactly at expiry)', () => {
    const msg = createAuthMessage(base);
    expect(checkAuthMessage(msg, { domain: base.domain, now: 1300 })).toEqual({
      ok: false,
      reason: 'expired',
    });
    expect(checkAuthMessage(msg, { domain: base.domain, now: 5000 }).ok).toBe(false);
  });

  it('rejects Issued At beyond the allowed skew', () => {
    const msg = createAuthMessage({ domain: base.domain, issuedAt: 2000 });
    expect(checkAuthMessage(msg, { domain: base.domain, now: 1000, maxSkewSec: 60 })).toEqual({
      ok: false,
      reason: 'not-yet-valid',
    });
    expect(checkAuthMessage(msg, { domain: base.domain, now: 1990, maxSkewSec: 60 }).ok).toBe(true);
  });

  it('rejects malformed input', () => {
    expect(checkAuthMessage('hello', { domain: 'd' })).toEqual({ ok: false, reason: 'malformed' });
  });

  it('has a user-facing message for every reason', () => {
    for (const r of ['malformed', 'domain', 'expired', 'not-yet-valid'] as const) {
      expect(authCheckMessage(r)).toBeTruthy();
    }
  });
});

describe('SignInButton', () => {
  afterEach(() => cleanup());

  function renderButton(
    state: ConnectControllerState,
    props: React.ComponentProps<typeof SignInButton> = {},
    signMessage?: ReturnType<typeof vi.fn>,
  ) {
    const sign =
      signMessage ??
      vi.fn(async (message: string) => ({ message, signatureB64: 'sig', publicKeyB64: 'pk' }));
    const ctrl = {
      ...makeController(state, sign as never),
      openModal: vi.fn(),
    } as unknown as ConnectController;
    const view = render(React.createElement(SignInButton, props), { wrapper: wrapperFor(ctrl) });
    return { root: within(view.container), sign, ctrl };
  }

  it('shows Connect when disconnected', () => {
    const { root, ctrl } = renderButton(makeState({ status: 'disconnected', account: null }));
    fireEvent.click(root.getByRole('button', { name: /^Connect$/i }));
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });

  it('shows Unlock when locked', () => {
    const { root, ctrl } = renderButton(makeState({ status: 'locked' }));
    expect(root.getByText(/Wallet locked/i)).toBeTruthy();
    fireEvent.click(root.getByRole('button', { name: /^Unlock$/i }));
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });

  it('signs a checked challenge bound to the account and calls onSignedIn', async () => {
    const onSignedIn = vi.fn();
    const origin = window.location.origin;
    const { root, sign } = renderButton(makeState(), {
      statement: 'Welcome back',
      nonce: 'srv-nonce',
      onSignedIn,
    });
    expect(root.getByText(new RegExp(`Welcome back · ${origin}`))).toBeTruthy();

    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1));

    const msg = sign.mock.calls[0][0] as string;
    const parsed = parseAuthMessage(msg);
    expect(parsed).toMatchObject({
      domain: origin,
      statement: 'Welcome back',
      address: ADDR,
      chainId: 'splendor-quantum-1',
      nonce: 'srv-nonce',
    });
    expect(parsed!.expirationTime! - parsed!.issuedAt).toBe(300);
    expect(onSignedIn.mock.calls[0][0]).toMatchObject({ address: ADDR, signatureB64: 'sig' });
    expect(root.getByRole('status').textContent).toMatch(/Signed in as/);
  });

  it('awaits an async nonce provider before signing', async () => {
    const nonce = vi.fn(async () => 'fetched-nonce');
    const { root, sign } = renderButton(makeState(), { nonce });
    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(sign).toHaveBeenCalled());
    expect(sign.mock.calls[0][0]).toContain('Nonce: fetched-nonce');
  });

  it('refuses to sign a challenge for another domain', async () => {
    const { root, sign } = renderButton(makeState(), { domain: 'https://evil.example' });
    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(root.getByRole('alert').textContent).toMatch(/different site/i));
    expect(sign).not.toHaveBeenCalled();
  });

  it('refuses to sign an already-expired challenge', async () => {
    const { root, sign } = renderButton(makeState(), { expiresInSec: 0 });
    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(root.getByRole('alert').textContent).toMatch(/expired/i));
    expect(sign).not.toHaveBeenCalled();
  });

  it('shows nonce fetch failures without signing', async () => {
    const nonce = vi.fn(async () => {
      throw new Error('nonce service down');
    });
    const { root, sign } = renderButton(makeState(), { nonce });
    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(root.getByRole('alert').textContent).toMatch(/nonce service down/));
    expect(sign).not.toHaveBeenCalled();
  });

  it('surfaces wallet rejection', async () => {
    const sign = vi.fn(async () => {
      throw new Error('user rejected');
    });
    const { root } = renderButton(makeState(), {}, sign);
    fireEvent.click(root.getByRole('button', { name: /^Sign in$/i }));
    await waitFor(() => expect(root.getByRole('alert')).toBeTruthy());
    expect(root.queryByRole('status')).toBeNull();
  });
});

describe('./hooks subpath', () => {
  it('exports the same sign-in and account DTO helpers as the root entry', () => {
    expect(typeof hooksEntry.useSignIn).toBe('function');
    expect(typeof hooksEntry.createAuthMessage).toBe('function');
    expect(typeof hooksEntry.parseAuthMessage).toBe('function');
    expect(typeof hooksEntry.checkAuthMessage).toBe('function');
    expect(typeof hooksEntry.useAccountDto).toBe('function');
  });
});

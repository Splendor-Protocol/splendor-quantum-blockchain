/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { isBridgeError } from '@splendor/sqc-sdk';
import { describeSignRequest, withSignApproval, type SignRequest } from '../core/signApproval.js';
import { createConnectController, type ConnectController } from '../core/controller.js';
import type { Connector } from '../connectors/types.js';
import { SignApprovalModal } from '../ui/SignApprovalModal.js';
import { ControllerContext } from '../react/context.js';
import type { ConnectControllerState } from '../core/types.js';

const TO = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

class FakeWallet {
  calls: string[] = [];
  label = 'fake';
  async sendTransfer(opts: { to: string; amountSPLD?: string }) {
    this.calls.push('sendTransfer');
    return { hash: 'h', to: opts.to };
  }
  async signMessage(message: string) {
    this.calls.push('signMessage');
    return { message };
  }
  /** Calls a gated method internally — must not prompt twice. */
  async signAndSendTransaction() {
    this.calls.push('signAndSendTransaction');
    return this.signTransaction();
  }
  async signTransaction() {
    this.calls.push('signTransaction');
    return { signed: true };
  }
  getDeriveInfo() {
    return null;
  }
}

describe('withSignApproval', () => {
  it('runs the call after approval and passes the request summary', async () => {
    const seen: SignRequest[] = [];
    const w = withSignApproval(new FakeWallet(), (req) => {
      seen.push(req);
      return true;
    });
    await expect(w.sendTransfer({ to: TO, amountSPLD: '1.5' })).resolves.toMatchObject({ hash: 'h' });
    expect(seen).toHaveLength(1);
    expect(seen[0]).toMatchObject({ kind: 'transfer', to: TO, amount: '1.5 SPLD' });
  });

  it('throws USER_REJECTED and never calls the wallet when rejected', async () => {
    const inner = new FakeWallet();
    const w = withSignApproval(inner, async () => false);
    const err = await w.signMessage('hello').catch((e: unknown) => e);
    expect(isBridgeError(err) && err.code).toBe('USER_REJECTED');
    expect(inner.calls).toEqual([]);
  });

  it('prompts once for internal calls and leaves other members alone', async () => {
    const approve = vi.fn(() => true);
    const w = withSignApproval(new FakeWallet(), approve);
    await w.signAndSendTransaction();
    expect(approve).toHaveBeenCalledTimes(1);
    expect(w.label).toBe('fake');
    expect(w.getDeriveInfo()).toBeNull();
    expect(approve).toHaveBeenCalledTimes(1);
    expect(w.sendTransfer).toBe(w.sendTransfer);
  });
});

describe('describeSignRequest', () => {
  it('summarises transfers, calls, modules and messages', () => {
    expect(describeSignRequest('sendTransfer', [{ to: TO, amount: '5' }])).toMatchObject({
      kind: 'transfer',
      amount: '5 atto',
    });
    expect(describeSignRequest('callContract', [{ contract: TO, function: 'set(uint256)' }]).summary).toMatch(
      /Call set\(uint256\)/,
    );
    expect(
      describeSignRequest('sendModuleCall', [{ module: 'qrc20', action: 'transfer', params: { to: TO, amount: '7' } }]),
    ).toMatchObject({ kind: 'module', to: TO, amount: '7' });
    expect(describeSignRequest('signMessage', ['hi'])).toMatchObject({ kind: 'message', message: 'hi' });
    expect(describeSignRequest('signPermit', [{}]).kind).toBe('sign');
  });
});

function dummyConnector(): Connector {
  return {
    id: 'dummy',
    name: 'Dummy',
    keyOrigin: 'native',
    postQuantumSecure: true,
    async ready() {
      return true;
    },
    async connect() {
      throw new Error('not used');
    },
  } as unknown as Connector;
}

function makeController(onBeforeSign?: Parameters<typeof createConnectController>[0]['onBeforeSign']) {
  return createConnectController({
    network: 'local',
    connectors: [dummyConnector()],
    allowUnpinnedWasm: true,
    autoReconnect: false,
    onBeforeSign,
  });
}

describe('controller sign gate', () => {
  it('rejects in-page signing when the approver says no', async () => {
    const approver = vi.fn(() => false);
    const ctrl = makeController(approver);
    const err = await ctrl.getWallet().signMessage('hello').catch((e: unknown) => e);
    expect(isBridgeError(err) && err.code).toBe('USER_REJECTED');
    expect(approver).toHaveBeenCalledWith(expect.objectContaining({ kind: 'message', message: 'hello' }));
    ctrl.destroy();
  });

  it('returns a stable wallet object across calls', () => {
    const ctrl = makeController(() => true);
    expect(ctrl.getWallet()).toBe(ctrl.getWallet());
    ctrl.destroy();
  });

  it("fails closed in 'modal' mode when no approval UI is mounted", async () => {
    const ctrl = makeController('modal');
    const err = await ctrl.getWallet().signMessage('x').catch((e: unknown) => e);
    expect(isBridgeError(err) && err.code).toBe('USER_REJECTED');
    expect(String((err as Error).message)).toMatch(/SignApprovalModal/);
    ctrl.destroy();
  });

  it("'modal' mode exposes the request in state and resolves on answer", async () => {
    const ctrl = makeController('modal');
    const detach = ctrl.attachSignApprovalUi!();
    const pending = ctrl.getWallet().signMessage('approve me').catch((e: unknown) => e);
    await Promise.resolve();
    expect(ctrl.getState().signRequest).toMatchObject({ kind: 'message', message: 'approve me' });
    ctrl.resolveSignRequest!(false);
    const err = await pending;
    expect(isBridgeError(err) && err.code).toBe('USER_REJECTED');
    expect(ctrl.getState().signRequest).toBeNull();
    detach();
    ctrl.destroy();
  });

  it('a pending request is rejected when the session disconnects', async () => {
    const ctrl = makeController('modal');
    ctrl.attachSignApprovalUi!();
    const pending = ctrl.getWallet().signMessage('bye').catch((e: unknown) => e);
    await Promise.resolve();
    await ctrl.disconnect();
    const err = await pending;
    expect(isBridgeError(err) && err.code).toBe('USER_REJECTED');
    ctrl.destroy();
  });

  it('a second request while one is pending is BUSY', async () => {
    const ctrl = makeController('modal');
    ctrl.attachSignApprovalUi!();
    const first = ctrl.getWallet().signMessage('one').catch((e: unknown) => e);
    await Promise.resolve();
    const second = await ctrl.getWallet().signMessage('two').catch((e: unknown) => e);
    expect(isBridgeError(second) && second.code).toBe('BUSY');
    ctrl.resolveSignRequest!(false);
    await first;
    ctrl.destroy();
  });
});

describe('SignApprovalModal', () => {
  function fakeController(req: SignRequest | null) {
    const state = {
      status: 'connected',
      account: { address: 'SQ:' + 'ab'.repeat(32) },
      network: { name: 'Local', chainId: 'splendor-quantum-1' },
      signRequest: req,
    } as unknown as ConnectControllerState;
    const ctrl = {
      getState: () => state,
      subscribe: () => () => undefined,
      resolveSignRequest: vi.fn(),
      attachSignApprovalUi: vi.fn(() => () => undefined),
    } as unknown as ConnectController;
    const Wrap = ({ children }: { children: ReactNode }) =>
      React.createElement(ControllerContext.Provider, { value: ctrl }, children);
    return { ctrl, Wrap };
  }

  it('registers itself, shows the request and answers Approve / Reject', () => {
    const { ctrl, Wrap } = fakeController({
      kind: 'transfer',
      method: 'sendTransfer',
      to: TO,
      amount: '2 SPLD',
      summary: 'Send 2 SPLD',
    });
    render(React.createElement(SignApprovalModal), { wrapper: Wrap });
    expect(ctrl.attachSignApprovalUi).toHaveBeenCalled();
    const panel = screen.getByTestId('sqc-sign-approval');
    expect(panel.textContent).toContain('Send 2 SPLD');
    expect(panel.textContent).toContain(TO);
    expect(panel.textContent).toContain('splendor-quantum-1');
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    expect(ctrl.resolveSignRequest).toHaveBeenCalledWith(true);
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    expect(ctrl.resolveSignRequest).toHaveBeenCalledWith(false);
  });

  it('renders the message text for sign-in requests and nothing when idle', () => {
    const a = fakeController({ kind: 'message', method: 'signMessage', message: 'line1\nline2', summary: 'Sign a message' });
    render(React.createElement(SignApprovalModal), { wrapper: a.Wrap });
    expect(document.querySelector('.sqc-sign-approval__message')?.textContent).toBe('line1\nline2');
    cleanup();
    const b = fakeController(null);
    render(React.createElement(SignApprovalModal), { wrapper: b.Wrap });
    expect(screen.queryByTestId('sqc-sign-approval')).toBeNull();
  });

  it('Escape rejects', () => {
    const { ctrl, Wrap } = fakeController({ kind: 'deploy', method: 'deployContract', summary: 'Deploy a contract' });
    render(React.createElement(SignApprovalModal), { wrapper: Wrap });
    act(() => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    });
    expect(ctrl.resolveSignRequest).toHaveBeenCalledWith(false);
  });
});

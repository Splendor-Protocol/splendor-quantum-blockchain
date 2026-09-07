import { describe, expect, it } from 'vitest';
import {
  createStubConnectController,
  connectErrorMessage,
} from '../core/controller.js';
import { BridgeError } from '@splendor/sqc-sdk';

describe('createStubConnectController', () => {
  it('reports disconnected and isStub', () => {
    const stub = createStubConnectController([]);
    expect(stub.isStub).toBe(true);
    expect(stub.getState().status).toBe('disconnected');
    expect(stub.getState().account).toBeNull();
  });

  it('rejects connect during SSR', async () => {
    const stub = createStubConnectController([]);
    await expect(stub.connect('metamask')).rejects.toThrow(/SSR/);
  });

  it('openModal updates state without wallet', () => {
    const stub = createStubConnectController([]);
    stub.openModal('connect');
    expect(stub.getState().modal).toBe('connect');
    stub.closeModal();
    expect(stub.getState().modal).toBe('closed');
  });

  it('exposes connectingStep and softDisconnect', async () => {
    const stub = createStubConnectController([]);
    expect(stub.getState().connectingStep).toBeNull();
    await stub.softDisconnect();
    expect(await stub.tryAutoReconnect({ force: true })).toBe(false);
  });
});

describe('connectErrorMessage extras', () => {
  it('maps TIMEOUT and NOT_CONNECTED', () => {
    expect(connectErrorMessage(BridgeError.timeout('x'))).toMatch(/timed out/i);
    expect(connectErrorMessage(BridgeError.notConnected())).toMatch(/not connected/i);
  });
});

import { describe, expect, it } from 'vitest';
import { BridgeError } from '@splendor/sqc-sdk';
import { connectErrorMessage } from '../core/controller.js';

describe('connectErrorMessage', () => {
  it('maps known BridgeError codes to safe UI copy', () => {
    expect(connectErrorMessage(BridgeError.userRejected())).toMatch(/rejected/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toMatch(/wallet/i);
    expect(connectErrorMessage(BridgeError.wasm('fail'))).toMatch(/Signer failed/i);
    expect(connectErrorMessage(BridgeError.network('down'))).toMatch(/Network/i);
    expect(connectErrorMessage(BridgeError.rpc('boom'))).toMatch(/Network/i);
    expect(connectErrorMessage(BridgeError.chainMismatch('a', 'b'))).toMatch(/Chain mismatch/i);
    expect(connectErrorMessage(BridgeError.timeout('slow'))).toMatch(/timed out/i);
    expect(connectErrorMessage(BridgeError.notConnected())).toMatch(/not connected/i);
    expect(connectErrorMessage(BridgeError.txFailed('reverted'))).toMatch(/failed/i);
    // Do not leak arbitrary BridgeError messages into the UI.
    expect(connectErrorMessage(new BridgeError('UNKNOWN', 'weird'))).toBe('Connection failed');
  });

  it('maps plain Error heuristics without leaking raw text', () => {
    expect(connectErrorMessage(new Error('EIP-1193 provider not found'))).toMatch(/wallet/i);
    expect(connectErrorMessage(new Error('User rejected the request'))).toMatch(/rejected/i);
    expect(connectErrorMessage(new Error('custom fail'))).toBe('Connection failed');
  });

  it('handles non-Error values', () => {
    expect(connectErrorMessage('x')).toBe('Connection failed');
  });
});

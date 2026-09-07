import { describe, expect, it } from 'vitest';
import { BridgeError } from '@splendor/sqc-sdk';
import { connectErrorMessage } from '../core/controller.js';

describe('connectErrorMessage', () => {
  it('maps known BridgeError codes', () => {
    expect(connectErrorMessage(BridgeError.userRejected())).toMatch(/rejected/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toMatch(/wallet/i);
    expect(connectErrorMessage(BridgeError.wasm('fail'))).toMatch(/Signer failed/i);
    expect(connectErrorMessage(BridgeError.network('down'))).toMatch(/down|Network/i);
    expect(connectErrorMessage(BridgeError.rpc('boom'))).toMatch(/boom|Network/i);
    expect(connectErrorMessage(BridgeError.chainMismatch('a', 'b'))).toMatch(/Chain mismatch/i);
    expect(connectErrorMessage(BridgeError.timeout('slow'))).toMatch(/timed out/i);
    expect(connectErrorMessage(BridgeError.notConnected())).toMatch(/not connected/i);
    expect(connectErrorMessage(new BridgeError('UNKNOWN', 'weird'))).toMatch(/weird/i);
  });

  it('maps plain Error heuristics', () => {
    expect(connectErrorMessage(new Error('EIP-1193 provider not found'))).toMatch(/wallet/i);
    expect(connectErrorMessage(new Error('User rejected the request'))).toMatch(/rejected/i);
    expect(connectErrorMessage(new Error('custom fail'))).toBe('custom fail');
  });

  it('handles non-Error values', () => {
    expect(connectErrorMessage('x')).toBe('Connection failed');
  });
});

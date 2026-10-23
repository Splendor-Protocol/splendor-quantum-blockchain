import { describe, expect, it } from 'vitest';
import { BridgeError } from '@splendor/sqc-sdk';
import { connectErrorMessage, errorFromWalletPayload } from '../core/controller.js';
import { notReady } from '../react/hookErrors.js';

describe('connectErrorMessage', () => {
  it('maps known BridgeError codes to safe UI copy', () => {
    expect(connectErrorMessage(BridgeError.userRejected())).toMatch(/rejected/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toBe('Install Splendor Quantum Wallet');
    expect(connectErrorMessage(BridgeError.wasm('fail'))).toMatch(/Signer failed/i);
    expect(connectErrorMessage(BridgeError.network('down'))).toMatch(/Network/i);
    expect(connectErrorMessage(BridgeError.rpc('boom'))).toMatch(/Network/i);
    expect(connectErrorMessage(BridgeError.rpcUnavailable('all down'))).toMatch(/unavailable/i);
    expect(connectErrorMessage(BridgeError.chainMismatch('a', 'b'))).toMatch(/Chain mismatch/i);
    expect(connectErrorMessage(BridgeError.timeout('slow'))).toMatch(/timed out/i);
    expect(connectErrorMessage(BridgeError.notConnected())).toMatch(/unlock|connect/i);
    expect(connectErrorMessage(BridgeError.txFailed('reverted'))).toMatch(/failed/i);
    // Do not leak arbitrary BridgeError messages into the UI.
    expect(connectErrorMessage(new BridgeError('UNKNOWN', 'weird'))).toBe('Connection failed');
  });

  it('maps BUSY without leaking raw mutex text', () => {
    const msg = connectErrorMessage(BridgeError.busy('internal mutex hold'));
    expect(msg).toMatch(/progress|busy|wait|connecting/i);
    expect(msg).not.toMatch(/mutex/i);
  });

  it('maps plain Error heuristics without leaking raw text', () => {
    expect(connectErrorMessage(new Error('EIP-1193 provider not found'))).toBe(
      'Install Splendor Quantum Wallet',
    );
    expect(connectErrorMessage(new Error('User rejected the request'))).toMatch(/rejected/i);
    expect(connectErrorMessage(new Error('Wallet locked'))).toMatch(/Unlock|connect/i);
    expect(connectErrorMessage(new Error('custom fail'))).toBe('Connection failed');
  });

  it('handles non-Error values', () => {
    expect(connectErrorMessage('x')).toBe('Connection failed');
  });

  it('NOT_CONNECTED / CHAIN_MISMATCH / NO_PROVIDER stay clear (not network)', () => {
    expect(connectErrorMessage(BridgeError.notConnected())).not.toMatch(/Network request failed/i);
    expect(connectErrorMessage(BridgeError.chainMismatch('a', 'b'))).toMatch(/Chain mismatch/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toMatch(/Install Splendor Quantum Wallet/);
  });
});

describe('connectErrorMessage in a transaction context', () => {
  it('never says "Connection failed" for a transaction', () => {
    expect(connectErrorMessage(new Error('custom fail'), 'tx')).toBe('Transaction failed — try again');
    expect(connectErrorMessage(new BridgeError('UNKNOWN', 'weird'), 'tx')).toBe('Transaction failed — try again');
    expect(connectErrorMessage('x', 'tx')).toBe('Transaction failed — try again');
    // Specific mappings are unchanged.
    expect(connectErrorMessage(BridgeError.userRejected(), 'tx')).toMatch(/rejected/i);
    expect(connectErrorMessage(BridgeError.timeout('slow'), 'tx')).toMatch(/timed out/i);
  });

  it('BUSY says a transaction is in progress, not a connection', () => {
    expect(connectErrorMessage(new BridgeError('BUSY', 'locked'), 'tx')).toMatch(/transaction is already in progress/i);
    expect(connectErrorMessage(new BridgeError('BUSY', 'locked'))).toMatch(/connection already in progress/i);
    expect(connectErrorMessage(BridgeError.txBusy(), 'tx')).toMatch(/transaction is already in progress/i);
  });

  it('INVALID_PARAMS shows the SDK message, on one line', () => {
    expect(connectErrorMessage(BridgeError.invalidParams('id: expected a non-negative integer'), 'tx')).toBe('id: expected a non-negative integer');
    expect(connectErrorMessage(BridgeError.invalidParams(`bad\nvalue${'x'.repeat(300)}`))).toMatch(/^bad valuex+…$/);
    expect(connectErrorMessage(BridgeError.invalidParams('bad\nvalue x')).length).toBeLessThanOrEqual(160);
  });

  it('hook "not ready" errors carry UI copy', () => {
    expect(connectErrorMessage(notReady('Wallet or token not ready'), 'tx')).toBe('Connect your wallet to continue');
    expect(connectErrorMessage(notReady('Wallet or token not ready', 'token'), 'tx')).toBe('Set the token address first');
    expect(notReady('Wallet or token not ready', 'token')).toMatchObject({ code: 'INVALID_PARAMS', message: 'Wallet or token not ready' });
    expect(notReady('Wallet not connected')).toMatchObject({ code: 'NOT_CONNECTED' });
  });
});

describe('errorFromWalletPayload', () => {
  it('keeps known codes, including INVALID_PARAMS, and maps others to UNKNOWN', () => {
    expect(errorFromWalletPayload({ code: 'INVALID_PARAMS', message: 'amount must be positive' })).toMatchObject({
      code: 'INVALID_PARAMS',
      message: 'amount must be positive',
    });
    expect(errorFromWalletPayload({ code: 'USER_REJECTED', message: 'no' }).code).toBe('USER_REJECTED');
    expect(errorFromWalletPayload({ code: 'SOMETHING_NEW', message: 'x' }).code).toBe('UNKNOWN');
  });

  it('keeps a short, single-line userHint and ignores other values', () => {
    expect(errorFromWalletPayload({ code: 'RPC', message: 'raw', userHint: 'Node is busy\r\ntry later' }).userHint).toBe('Node is busy try later');
    expect(errorFromWalletPayload({ code: 'RPC', message: 'raw', userHint: 'h'.repeat(400) }).userHint).toHaveLength(160);
    expect(errorFromWalletPayload({ code: 'RPC', message: 'raw', userHint: 42 }).userHint).toBeUndefined();
    expect(errorFromWalletPayload({ code: 'RPC', message: 'raw', userHint: '   ' }).userHint).toBeUndefined();
    expect(connectErrorMessage(errorFromWalletPayload({ code: 'RPC', message: 'raw body', userHint: 'Node is busy' }))).toBe('Node is busy');
  });
});

import { describe, expect, it } from 'vitest';
import { checkAuthMessage, createAuthMessage, parseAuthMessage } from '../react/auth.js';

const DOMAIN = 'https://app.example.com';
const ADDR = 'SQ:' + 'ab'.repeat(32);
const NOW = 1_800_000_000;

function msg(extra: Partial<Parameters<typeof createAuthMessage>[0]> = {}) {
  return createAuthMessage({ domain: DOMAIN, nonce: 'n0nce123', issuedAt: NOW, address: ADDR, chainId: 'splendor-quantum-1', ...extra });
}

describe('sign-in message hardening', () => {
  it.each(['domain', 'statement', 'nonce', 'chainId', 'address'] as const)(
    'rejects line breaks and control characters in %s',
    (field) => {
      for (const bad of ['x\nNonce: attacker', 'x\r\ny', 'x\u0000y', 'x\u2028y']) {
        expect(() => msg({ [field]: bad })).toThrow();
      }
    },
  );

  it('cannot smuggle a second field through the statement', () => {
    expect(() => msg({ statement: 'Welcome\nAddress: SQ:' + 'cd'.repeat(32) })).toThrow();
  });

  it('checks the expected nonce, address and chain', () => {
    const m = msg();
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW, expectedNonce: 'n0nce123' }).ok).toBe(true);
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW, expectedNonce: 'other' })).toEqual({ ok: false, reason: 'nonce' });
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW, expectedAddress: ADDR.toUpperCase() }).ok).toBe(true);
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW, expectedAddress: 'SQ:' + 'cd'.repeat(32) })).toEqual({
      ok: false,
      reason: 'address',
    });
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW, expectedChainId: 'other-chain' })).toEqual({ ok: false, reason: 'chain' });
  });

  it('rejects messages without an expiry once they are older than maxAgeSec (default 600 s)', () => {
    const m = msg();
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW + 600 }).ok).toBe(true);
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW + 601 })).toEqual({ ok: false, reason: 'too-old' });
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW + 601, maxAgeSec: 3600 }).ok).toBe(true);
  });

  it('honours an explicit expiry instead of maxAgeSec', () => {
    const m = msg({ expirationTime: NOW + 7200 });
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW + 3600 }).ok).toBe(true);
    expect(checkAuthMessage(m, { domain: DOMAIN, now: NOW + 7200 })).toEqual({ ok: false, reason: 'expired' });
  });

  it('does not parse messages with injected or unknown lines', () => {
    const m = msg();
    expect(parseAuthMessage(m)).not.toBeNull();
    expect(parseAuthMessage(m + '\nNonce: second')).toBeNull();
    expect(parseAuthMessage(m.replace('\n', '\nInjected: yes\n'))).toBeNull();
  });
});

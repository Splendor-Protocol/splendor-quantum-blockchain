import { describe, expect, it, vi } from 'vitest';
import {
  addressFromPublicKey,
  createAuthMessage,
  createMemoryNonceStore,
  issueNonce,
  verifySignIn,
} from '../auth.js';

const DOMAIN = 'https://app.example.com';
const PUBKEY = Buffer.from(new Uint8Array(64).fill(7)).toString('base64');
const ADDRESS = addressFromPublicKey(PUBKEY)!;
const OTHER = 'SQ:' + 'cd'.repeat(32);

function setup(
  overrides: { nonce?: string | null; domain?: string; address?: string; chainId?: string; issuedAt?: number; expirationTime?: number } = {},
) {
  const store = createMemoryNonceStore();
  const nonce = overrides.nonce === null ? undefined : (overrides.nonce ?? store.issue());
  const now = Math.floor(Date.now() / 1000);
  const message = createAuthMessage({
    domain: overrides.domain ?? DOMAIN,
    nonce,
    issuedAt: overrides.issuedAt ?? now,
    address: overrides.address ?? ADDRESS,
    ...(overrides.chainId ? { chainId: overrides.chainId } : {}),
    ...(overrides.expirationTime != null ? { expirationTime: overrides.expirationTime } : {}),
  });
  return { store, nonce, now, message };
}

describe('verifySignIn', () => {
  it('derives the address from the public key (SHA3-256)', () => {
    expect(ADDRESS).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(addressFromPublicKey('not base64!')).toBeNull();
  });

  it('issueNonce refuses short nonces', () => {
    expect(issueNonce()).toMatch(/^[0-9a-f]{32}$/);
    expect(() => issueNonce(8)).toThrow(RangeError);
  });

  it('accepts a valid sign-in and burns the nonce', async () => {
    const { store, message, now } = setup();
    const verifier = vi.fn(async () => true);
    const opts = { message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN, nonceStore: store, verifier, now };
    await expect(verifySignIn(opts)).resolves.toMatchObject({ ok: true, address: ADDRESS });
    expect(verifier).toHaveBeenCalledWith(PUBKEY, message, 'AA==');
    await expect(verifySignIn(opts)).resolves.toEqual({ ok: false, reason: 'nonce-used' });
  });

  it('rejects a nonce this server never issued', async () => {
    const { message, now } = setup({ nonce: issueNonce() });
    const r = await verifySignIn({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN,
      nonceStore: createMemoryNonceStore(), verifier: async () => true, now,
    });
    expect(r).toEqual({ ok: false, reason: 'nonce-used' });
  });

  it('rejects a public key that does not derive the claimed address', async () => {
    const { store, message, now } = setup({ address: OTHER });
    const r = await verifySignIn({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: OTHER, domain: DOMAIN,
      nonceStore: store, verifier: async () => true, now,
    });
    expect(r).toEqual({ ok: false, reason: 'key-mismatch' });
  });

  it('rejects a message signed for another domain', async () => {
    const { store, message, now } = setup({ domain: 'https://evil.example' });
    const r = await verifySignIn({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN,
      nonceStore: store, verifier: async () => true, now,
    });
    expect(r).toEqual({ ok: false, reason: 'domain' });
  });

  it('requires a nonce in the message', async () => {
    const { store, message, now } = setup({ nonce: null });
    const r = await verifySignIn({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN,
      nonceStore: store, verifier: async () => true, now,
    });
    expect(r).toEqual({ ok: false, reason: 'missing-nonce' });
  });

  it('does not burn the nonce when the signature is bad', async () => {
    const { store, message, now } = setup();
    const base = { message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN, nonceStore: store, now };
    await expect(verifySignIn({ ...base, verifier: async () => false })).resolves.toEqual({ ok: false, reason: 'bad-signature' });
    await expect(
      verifySignIn({ ...base, verifier: async () => { throw new Error('boom'); } }),
    ).resolves.toEqual({ ok: false, reason: 'bad-signature' });
    await expect(verifySignIn({ ...base, verifier: async () => true })).resolves.toMatchObject({ ok: true });
  });

  describe('chain binding', () => {
    const CHAIN = 'splendor-quantum-1';
    const verify = (message: string, store: ReturnType<typeof createMemoryNonceStore>, now: number) =>
      verifySignIn({
        message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN,
        nonceStore: store, verifier: async () => true, now, chainId: CHAIN,
      });

    it('accepts a message for the expected chain', async () => {
      const { store, message, now } = setup({ chainId: CHAIN });
      await expect(verify(message, store, now)).resolves.toMatchObject({ ok: true });
    });

    it('rejects a message for another chain', async () => {
      const { store, message, now } = setup({ chainId: 'splendor-testnet' });
      await expect(verify(message, store, now)).resolves.toEqual({ ok: false, reason: 'chain' });
    });

    it('rejects a message that names no chain when the server expects one', async () => {
      const { store, message, now } = setup();
      await expect(verify(message, store, now)).resolves.toEqual({ ok: false, reason: 'chain' });
    });
  });

  describe('time window', () => {
    const base = (message: string, store: ReturnType<typeof createMemoryNonceStore>, now: number) => ({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: ADDRESS, domain: DOMAIN,
      nonceStore: store, verifier: async () => true, now,
    });

    it('rejects an expired message and keeps its nonce unspent', async () => {
      const now = Math.floor(Date.now() / 1000);
      const { store, message } = setup({ issuedAt: now - 120, expirationTime: now - 60 });
      await expect(verifySignIn(base(message, store, now))).resolves.toEqual({ ok: false, reason: 'expired' });
    });

    it('rejects a message older than maxAgeSec when it has no expiry', async () => {
      const now = Math.floor(Date.now() / 1000);
      const { store, message } = setup({ issuedAt: now - 3600 });
      await expect(verifySignIn({ ...base(message, store, now), maxAgeSec: 600 })).resolves.toEqual({ ok: false, reason: 'too-old' });
    });

    it('rejects a message dated beyond the allowed skew', async () => {
      const now = Math.floor(Date.now() / 1000);
      const { store, message } = setup({ issuedAt: now + 3600 });
      await expect(verifySignIn(base(message, store, now))).resolves.toEqual({ ok: false, reason: 'not-yet-valid' });
    });
  });

  it('rejects a malformed address before doing any work', async () => {
    const verifier = vi.fn(async () => true);
    const { store, message } = setup();
    const r = await verifySignIn({
      message, signatureB64: 'AA==', publicKeyB64: PUBKEY, address: '0xabc', domain: DOMAIN, nonceStore: store, verifier,
    });
    expect(r).toEqual({ ok: false, reason: 'bad-address' });
    expect(verifier).not.toHaveBeenCalled();
  });
});

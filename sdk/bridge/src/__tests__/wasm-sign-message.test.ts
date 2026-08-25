import { describe, expect, it, afterEach } from 'vitest';
import { SplendorPQBridge } from '../index.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

// Proves the Splendor Connect v2 foundation: the WASM signs an arbitrary
// challenge with the quantum (ML-DSA-65 / FIPS 204) key, and the SAME WASM
// VERIFIES it — server-side, in Node. This is the exact capability a Node
// Connect gateway needs to authenticate an SQ: identity, since there is no
// native-JS ML-DSA-65 verifier.

const GOLDEN_SEED = '01'.repeat(32);

// A realistic v2 connect challenge (carries the SQ: address + string chain-id,
// unlike v1's EVM `Address: 0x…` / numeric chain).
const challenge = [
  'Splendor Connect v2',
  'Sign in to https://quantum-explorer.splendor.org',
  '',
  `Address: ${golden.address}`,
  'Session: 0123456789abcdef0123456789abcdef',
  'Nonce: fedcba9876543210fedcba9876543210',
  'Chain ID: splendor-quantum-1',
  'Expires: 1893456000',
].join('\n');

const bridges: SplendorPQBridge[] = [];
afterEach(() => {
  for (const b of bridges) {
    try {
      b.disconnect();
    } catch {
      /* ignore */
    }
  }
  bridges.length = 0;
});
function track(b: SplendorPQBridge) {
  bridges.push(b);
  return b;
}

describe('Connect v2 — quantum message sign/verify (Node round-trip)', () => {
  it('signs a challenge and verifies it with the same WASM', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);

    const signed = await b.signMessage(challenge);
    expect(signed.address).toMatch(/^SQ:[0-9a-f]{64}$/);
    expect(signed.publicKeyB64).toBeTruthy();
    expect(signed.signatureB64).toBeTruthy();

    // The gateway rebuilds the challenge and verifies (pure, no session).
    const ok = await b.verifyMessage(signed.publicKeyB64, challenge, signed.signatureB64);
    expect(ok).toBe(true);
  }, 30_000);

  it('rejects a tampered message', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const signed = await b.signMessage(challenge);
    const bad = await b.verifyMessage(signed.publicKeyB64, challenge + ' ', signed.signatureB64);
    expect(bad).toBe(false);
  }, 30_000);

  it('rejects a signature from a different key', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const signed = await b.signMessage(challenge);

    // A different identity's public key must not verify this signature.
    const other = track(new SplendorPQBridge());
    await other.connectFromSeed('02'.repeat(32));
    const otherSigned = await other.signMessage(challenge);
    expect(otherSigned.publicKeyB64).not.toBe(signed.publicKeyB64);

    const crossed = await b.verifyMessage(otherSigned.publicKeyB64, challenge, signed.signatureB64);
    expect(crossed).toBe(false);
  }, 30_000);
});

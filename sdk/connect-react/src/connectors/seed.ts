import { bytesToHex, type DeriveInfo } from '@splendor/sqc-sdk';
import type { Connector, ConnectorContext } from './types.js';

export type SeedConnectorOpts = {
  name?: string;
  icon?: string;
};

/** Normalize and validate a 32-byte seed hex (optional 0x prefix). */
export function assertSeedHex(raw: string): string {
  const trimmed = String(raw ?? '').trim();
  if (!trimmed) {
    throw new Error('Seed hex is required');
  }
  const hex = trimmed.startsWith('0x') || trimmed.startsWith('0X') ? trimmed.slice(2) : trimmed;
  if (!/^[0-9a-fA-F]{64}$/.test(hex)) {
    throw new Error('Seed must be 32 bytes (64 hex characters)');
  }
  return hex.toLowerCase();
}

/**
 * Fresh 32-byte ML-DSA-65 seed from the platform CSPRNG.
 *
 * The whole point of this path is that no secp256k1 key stands behind the
 * account, so the entropy has to be unguessable on its own — a seed anyone can
 * predict is worth no more than one derived from an ECDSA signature. Throws
 * rather than falling back to Math.random when no CSPRNG is available.
 */
export function generateSeedHex(): string {
  const csprng = globalThis.crypto;
  if (!csprng?.getRandomValues) {
    throw new Error('No CSPRNG available — cannot generate a post-quantum seed here');
  }
  const bytes = new Uint8Array(32);
  csprng.getRandomValues(bytes);
  return bytesToHex(bytes);
}

/**
 * Native ML-DSA-65 connector — the primary way to hold a Splendor account.
 *
 * The seed is post-quantum entropy the user creates or imports, so the
 * account's security rests on ML-DSA-65 alone: no Ethereum key exists that an
 * adversary could break to reproduce it. Not marked `advanced` — this is the
 * first action in the connect modal, not a developer escape hatch. Its
 * `connectFields` presence is what keeps the controller from persisting or
 * silently resuming it, since the secret is typed in each session.
 */
export function seedConnector(opts: SeedConnectorOpts = {}): Connector {
  return {
    id: 'seed',
    name: opts.name ?? 'Splendor Quantum Wallet',
    icon: opts.icon,
    keyOrigin: 'native',
    postQuantumSecure: true,
    connectFields: [
      {
        name: 'seed',
        type: 'password',
        label: 'ML-DSA-65 seed',
        placeholder: '32-byte seed hex (64 chars)',
      },
    ],
    ready() {
      return true;
    },
    async connect(ctx: ConnectorContext, connectOpts?: Record<string, unknown>): Promise<DeriveInfo> {
      const seed = assertSeedHex(String(connectOpts?.seed ?? ''));
      return ctx.wallet.connectFromSeed(seed);
    },
  };
}

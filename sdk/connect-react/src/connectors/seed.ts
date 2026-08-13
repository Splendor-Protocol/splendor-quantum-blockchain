import type { DeriveInfo } from '@splendor/pq-bridge';
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
 * Advanced connector: unlock from a 32-byte hex seed (dev / agent use).
 * UI should gate this behind an Advanced accordion and never log the seed.
 */
export function seedConnector(opts: SeedConnectorOpts = {}): Connector {
  return {
    id: 'seed',
    name: opts.name ?? 'Seed (Advanced)',
    icon: opts.icon,
    advanced: true,
    connectFields: [
      {
        name: 'seed',
        type: 'password',
        label: 'Seed hex',
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

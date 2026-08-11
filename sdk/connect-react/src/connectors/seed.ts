import type { DeriveInfo } from '@splendor/pq-bridge';
import type { Connector, ConnectorContext } from './types.js';

export type SeedConnectorOpts = {
  name?: string;
  icon?: string;
};

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
    ready() {
      return true;
    },
    async connect(ctx: ConnectorContext, connectOpts?: Record<string, unknown>): Promise<DeriveInfo> {
      const seed = String(connectOpts?.seed ?? '').trim();
      if (!seed) {
        throw new Error('Seed hex is required');
      }
      return ctx.wallet.connectFromSeed(seed);
    },
  };
}

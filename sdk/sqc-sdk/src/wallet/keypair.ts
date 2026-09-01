/**
 * Native ML-DSA keypair helpers (post-quantum seed path).
 */
import { randomBytes } from '@noble/hashes/utils';

export class Keypair {
  readonly seedHex: string;

  private constructor(seedHex: string) {
    this.seedHex = seedHex;
  }

  /** Generate a new 32-byte CSPRNG seed. */
  static generate(): Keypair {
    const seed = randomBytes(32);
    return new Keypair(
      Array.from(seed)
        .map((b) => b.toString(16).padStart(2, '0'))
        .join(''),
    );
  }

  /** Import from 32-byte hex seed. */
  static fromSeed(seedHex: string): Keypair {
    const h = seedHex.replace(/^0x/i, '');
    if (!/^[0-9a-fA-F]{64}$/.test(h)) {
      throw new Error('Keypair.fromSeed: seed must be 32 bytes (64 hex chars)');
    }
    return new Keypair(h.toLowerCase());
  }
}

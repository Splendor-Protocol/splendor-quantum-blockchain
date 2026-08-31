/**
 * The per-IP status cooldown in isolation, against an in-memory store.
 */
import { describe, expect, it } from 'vitest';
import { checkStatusCooldown, ipBucket, statusKey } from '../src/ratelimit';

class MemStore {
  readonly data = new Map<string, string>();
  async get(key: string) {
    return this.data.get(key) ?? null;
  }
  async put(key: string, value: string) {
    this.data.set(key, value);
  }
}

describe('ipBucket', () => {
  it('uses IPv4 whole', () => {
    expect(ipBucket('203.0.113.7')).toBe('203.0.113.7');
    expect(ipBucket('  203.0.113.7 ')).toBe('203.0.113.7');
  });

  it('collapses IPv6 to its /64', () => {
    expect(ipBucket('2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff')).toBe('2001:db8:aaaa:bbbb::/64');
    expect(ipBucket('2001:DB8:AAAA:BBBB::1')).toBe('2001:db8:aaaa:bbbb::/64');
  });

  it('uses IPv4-mapped IPv6 whole', () => {
    expect(ipBucket('::ffff:203.0.113.7')).toBe('::ffff:203.0.113.7');
  });

  it('uses garbage whole (narrow beats wide)', () => {
    expect(ipBucket('not-an-ip')).toBe('not-an-ip');
  });
});

describe('checkStatusCooldown', () => {
  it('allows the first request and stamps the bucket', async () => {
    const store = new MemStore();
    const d = await checkStatusCooldown(store, '203.0.113.7', 1000, 2000);
    expect(d.allowed).toBe(true);
    expect(store.data.get(statusKey('203.0.113.7'))).toBe('1000');
  });

  it('denies inside the window with a sane retry-after', async () => {
    const store = new MemStore();
    await checkStatusCooldown(store, '203.0.113.7', 1000, 2000);
    const d = await checkStatusCooldown(store, '203.0.113.7', 1500, 2000);
    expect(d.allowed).toBe(false);
    if (!d.allowed) expect(d.retryAfterSeconds).toBe(2); // 1500ms left → ceil 2s
  });

  it('allows again once the window has passed', async () => {
    const store = new MemStore();
    await checkStatusCooldown(store, '203.0.113.7', 1000, 2000);
    const d = await checkStatusCooldown(store, '203.0.113.7', 3200, 2000);
    expect(d.allowed).toBe(true);
  });

  it('treats a clock that went backwards as still cooling down', async () => {
    const store = new MemStore();
    await checkStatusCooldown(store, '203.0.113.7', 10_000, 2000);
    const d = await checkStatusCooldown(store, '203.0.113.7', 5000, 2000);
    expect(d.allowed).toBe(false);
  });

  it('ignores a corrupt stamp rather than throwing', async () => {
    const store = new MemStore();
    store.data.set(statusKey('203.0.113.7'), 'garbage');
    const d = await checkStatusCooldown(store, '203.0.113.7', 1000, 2000);
    expect(d.allowed).toBe(true);
  });

  it('always allows when there is no IP or no cooldown', async () => {
    const store = new MemStore();
    expect((await checkStatusCooldown(store, null, 1000, 2000)).allowed).toBe(true);
    expect((await checkStatusCooldown(store, '1.2.3.4', 1000, 0)).allowed).toBe(true);
    expect(store.data.size).toBe(0);
  });
});

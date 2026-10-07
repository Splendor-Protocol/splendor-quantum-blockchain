import { describe, expect, it, vi } from 'vitest';
import { invalidateSharedReads, sharedRead } from '../core/readCache.js';

describe('sharedRead', () => {
  it('dedupes concurrent reads of the same key on the same source', async () => {
    const source = {};
    const fetcher = vi.fn(async () => 'v');
    const [a, b] = await Promise.all([sharedRead(source, 'k', fetcher), sharedRead(source, 'k', fetcher)]);
    expect(a).toBe('v');
    expect(b).toBe('v');
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('reuses a fresh result, and force or invalidate refetches', async () => {
    const source = {};
    let n = 0;
    const fetcher = vi.fn(async () => ++n);
    expect(await sharedRead(source, 'bal|a', fetcher, { maxAgeMs: 60_000 })).toBe(1);
    expect(await sharedRead(source, 'bal|a', fetcher, { maxAgeMs: 60_000 })).toBe(1);
    expect(await sharedRead(source, 'bal|a', fetcher, { maxAgeMs: 60_000, force: true })).toBe(2);
    invalidateSharedReads(source, 'bal|');
    expect(await sharedRead(source, 'bal|a', fetcher, { maxAgeMs: 60_000 })).toBe(3);
  });

  it('keeps sources apart and does not cache failures', async () => {
    const a = {};
    const b = {};
    expect(await sharedRead(a, 'k', async () => 'a', { maxAgeMs: 60_000 })).toBe('a');
    expect(await sharedRead(b, 'k', async () => 'b', { maxAgeMs: 60_000 })).toBe('b');
    await expect(sharedRead(a, 'x', async () => Promise.reject(new Error('down')))).rejects.toThrow('down');
    expect(await sharedRead(a, 'x', async () => 'ok', { maxAgeMs: 60_000 })).toBe('ok');
  });
});

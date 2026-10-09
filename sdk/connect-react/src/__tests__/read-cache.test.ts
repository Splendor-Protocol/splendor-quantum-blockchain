import { describe, expect, it, vi } from 'vitest';
import { invalidateSharedReads, onSharedReadsInvalidated, sharedRead } from '../core/readCache.js';

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

  it('invalidation drops an in-flight read so the next read starts fresh', async () => {
    const source = {};
    let release!: (v: string) => void;
    const slow = sharedRead(source, 'bal|a', () => new Promise<string>((r) => (release = r)));
    invalidateSharedReads(source, 'bal|');
    const fresh = vi.fn(async () => 'new');
    expect(await sharedRead(source, 'bal|a', fresh)).toBe('new');
    expect(fresh).toHaveBeenCalledTimes(1);
    release('old');
    expect(await slow).toBe('old');
  });

  it('a failed stale read does not evict the newer entry for the same key', async () => {
    const source = {};
    let fail!: (e: Error) => void;
    const stale = sharedRead(source, 'k', () => new Promise<string>((_, rej) => (fail = rej)));
    invalidateSharedReads(source, 'k');
    expect(await sharedRead(source, 'k', async () => 'v', { maxAgeMs: 60_000 })).toBe('v');
    fail(new Error('down'));
    await expect(stale).rejects.toThrow('down');
    const again = vi.fn(async () => 'other');
    expect(await sharedRead(source, 'k', again, { maxAgeMs: 60_000 })).toBe('v');
    expect(again).not.toHaveBeenCalled();
  });

  it('notifies listeners for the source with the prefix, until unsubscribed', () => {
    const source = {};
    const other = {};
    const fn = vi.fn();
    const off = onSharedReadsInvalidated(source, fn);
    invalidateSharedReads(other, 'bal|');
    expect(fn).not.toHaveBeenCalled();
    invalidateSharedReads(source, 'bal|');
    expect(fn).toHaveBeenCalledWith('bal|');
    off();
    invalidateSharedReads(source, 'bal|');
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from './client';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

/** A fetch that never answers until its signal aborts, like a stalled API. */
function hangingFetch() {
  return vi.fn((_url: string, init?: RequestInit) =>
    new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }),
  );
}

describe('api client', () => {
  it('turns a stalled request into an ApiError with code TIMEOUT', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', hangingFetch());
    const p = api.health();
    const assertion = expect(p).rejects.toMatchObject({ code: 'TIMEOUT', status: 0 });
    await vi.advanceTimersByTimeAsync(20_001);
    await assertion;
    await expect(p).rejects.toBeInstanceOf(ApiError);
  });

  it('passes a caller abort through as an abort, not a network error', async () => {
    vi.stubGlobal('fetch', hangingFetch());
    const ctrl = new AbortController();
    const p = api.getShared('abc', ctrl.signal);
    ctrl.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('reports an unreachable API as status 0 without a code', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))));
    await expect(api.health()).rejects.toMatchObject({ status: 0, code: undefined, message: 'Playground API is not reachable' });
  });

  it('keeps the API error code and message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ error: 'Sign-in failed', code: 'AUTH_FAILED' }), { status: 401 })),
    );
    await expect(api.me()).rejects.toMatchObject({ status: 401, code: 'AUTH_FAILED', message: 'Sign-in failed' });
  });
});

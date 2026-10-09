import { describe, expect, it, vi, afterEach } from 'vitest';
import { subscribe } from '../core/subscribe.js';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('subscribe functional', () => {
  it('parses SSE data lines and skips heartbeats (Node stream)', async () => {
    const events: unknown[] = [];
    const chunks = [
      'data: [heartbeat]\n',
      'data: {"type":"new_block","height":1}\n',
      'data: {"type":"new_tx","hash":"0x1"}\n',
    ];
    let i = 0;
    const reader = {
      read: vi.fn(async () => {
        if (i >= chunks.length) return { done: true, value: undefined };
        const value = new TextEncoder().encode(chunks[i++]);
        return { done: false, value };
      }),
      cancel: vi.fn(async () => undefined),
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        body: { getReader: () => reader },
      })),
    );
    // Force Node path
    vi.stubGlobal('EventSource', undefined);

    const sub = subscribe('https://rpc.test', ['new_block', 'new_tx'], (ev) => events.push(ev), undefined, {
      reconnect: false,
    });
    await new Promise((r) => setTimeout(r, 30));
    expect(events).toEqual([
      { type: 'new_block', height: 1 },
      { type: 'new_tx', hash: '0x1' },
    ]);
    sub.close();
    expect(reader.cancel).toHaveBeenCalled();
  });

  it('reconnects the Node stream and sends Last-Event-ID', async () => {
    const events: unknown[] = [];
    const errors: unknown[] = [];
    const fetches: RequestInit[] = [];
    let round = 0;
    vi.stubGlobal('EventSource', undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        fetches.push(init ?? {});
        const thisRound = round++;
        let i = 0;
        const chunks =
          thisRound === 0
            ? ['id: 7\n', 'data: {"type":"new_block","height":1}\n']
            : ['id: 8\n', 'data: {"type":"new_block","height":2}\n'];
        return {
          ok: true,
          body: {
            getReader: () => ({
              read: async () => {
                if (i >= chunks.length) return { done: true, value: undefined };
                return { done: false, value: new TextEncoder().encode(chunks[i++]) };
              },
              cancel: async () => undefined,
            }),
          },
        };
      }),
    );

    const sub = subscribe(
      'https://rpc.test',
      'new_block',
      (ev) => events.push(ev),
      (err) => errors.push(err),
      { reconnect: true, backoffMs: 10, maxRetries: 1 },
    );
    await new Promise((r) => setTimeout(r, 80));
    sub.close();
    // Every round delivers data, which resets the retry budget, so it keeps reconnecting.
    expect(events.slice(0, 2)).toEqual([
      { type: 'new_block', height: 1 },
      { type: 'new_block', height: 2 },
    ]);
    expect(fetches.length).toBeGreaterThanOrEqual(2);
    const second = fetches[1]?.headers as Record<string, string>;
    expect(second['Last-Event-ID']).toBe('7');
    expect(errors.length).toBeGreaterThan(0);
  });

  it('accepts string[] rpcUrls without throw', () => {
    vi.stubGlobal('EventSource', undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        body: {
          getReader: () => ({
            read: async () => ({ done: true, value: undefined }),
            cancel: async () => undefined,
          }),
        },
      })),
    );
    const sub = subscribe(
      ['https://rpc-a.test', 'https://rpc-b.test'],
      'new_block',
      () => undefined,
      undefined,
      { reconnect: false },
    );
    expect(sub).toHaveProperty('close');
    sub.close();
  });

  it('rotates multi-URL on Node stream error', async () => {
    vi.stubGlobal('EventSource', undefined);
    const urlsHit: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        urlsHit.push(url);
        if (url.startsWith('https://rpc-a.test')) {
          throw new TypeError('primary down');
        }
        let i = 0;
        const chunks = ['data: {"type":"new_block","height":9}\n'];
        return {
          ok: true,
          body: {
            getReader: () => ({
              read: async () => {
                if (i >= chunks.length) return { done: true, value: undefined };
                return { done: false, value: new TextEncoder().encode(chunks[i++]) };
              },
              cancel: async () => undefined,
            }),
          },
        };
      }),
    );

    const events: unknown[] = [];
    let sub!: { close: () => void };
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        sub.close();
        reject(new Error('timed out waiting for rotated subscribe event'));
      }, 2000);
      sub = subscribe(
        ['https://rpc-a.test', 'https://rpc-b.test'],
        'new_block',
        (ev) => {
          events.push(ev);
          clearTimeout(timer);
          sub.close();
          resolve();
        },
        undefined,
        { reconnect: true, backoffMs: 5, maxRetries: 0 },
      );
    });
    expect(urlsHit.some((u) => u.startsWith('https://rpc-a.test'))).toBe(true);
    expect(urlsHit.some((u) => u.startsWith('https://rpc-b.test'))).toBe(true);
    expect(events).toEqual([{ type: 'new_block', height: 9 }]);
  });
});

describe('subscribe Node retry policy', () => {
  function failingFetch(hits: string[], okHost?: string) {
    return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      hits.push(`${url}|${(init?.headers as Record<string, string>)['Last-Event-ID'] ?? ''}`);
      if (okHost && url.startsWith(okHost)) {
        let i = 0;
        const chunks = ['id: 42\n', 'data: {"type":"new_block","height":5}\n'];
        return {
          ok: true,
          body: {
            getReader: () => ({
              read: async () =>
                i < chunks.length
                  ? { done: false, value: new TextEncoder().encode(chunks[i++]) }
                  : { done: true, value: undefined },
              cancel: async () => undefined,
            }),
          },
        };
      }
      throw new TypeError('down');
    });
  }

  it('stops after maxRetries full passes when no connection ever delivers data', async () => {
    vi.stubGlobal('EventSource', undefined);
    const hits: string[] = [];
    vi.stubGlobal('fetch', failingFetch(hits));
    const sub = subscribe(['https://a.test', 'https://b.test'], 'new_block', () => undefined, () => undefined, {
      backoffMs: 1,
      maxRetries: 1,
    });
    await new Promise((r) => setTimeout(r, 150));
    sub.close();
    expect(hits.map((h) => h.split('/ws')[0])).toEqual([
      'https://a.test',
      'https://b.test',
      'https://a.test',
      'https://b.test',
    ]);
  });

  it('carries the latest event id to the next URL', async () => {
    vi.stubGlobal('EventSource', undefined);
    const hits: string[] = [];
    vi.stubGlobal('fetch', failingFetch(hits, 'https://a.test'));
    const sub = subscribe(['https://a.test', 'https://b.test'], 'new_block', () => undefined, () => undefined, {
      backoffMs: 1,
      maxRetries: 0,
    });
    await new Promise((r) => setTimeout(r, 100));
    sub.close();
    expect(hits[0]).toMatch(/^https:\/\/a\.test.*\|$/);
    expect(hits[1]).toMatch(/^https:\/\/b\.test.*\|42$/);
  });

  it('an aborted signal closes the subscription', async () => {
    vi.stubGlobal('EventSource', undefined);
    const hits: string[] = [];
    vi.stubGlobal('fetch', failingFetch(hits));
    const ac = new AbortController();
    subscribe('https://a.test', 'new_block', () => undefined, () => undefined, { backoffMs: 1, signal: ac.signal });
    await new Promise((r) => setTimeout(r, 20));
    ac.abort();
    const n = hits.length;
    await new Promise((r) => setTimeout(r, 60));
    expect(hits.length).toBe(n);
  });
});

class FakeEventSource {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  static instances: FakeEventSource[] = [];
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: ((e: unknown) => void) | null = null;
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

describe('subscribe browser EventSource', () => {
  afterEach(() => {
    FakeEventSource.instances = [];
    vi.useRealTimers();
  });

  it('leaves transient single-URL errors to EventSource', () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.useFakeTimers();
    const onError = vi.fn();
    const sub = subscribe('https://rpc.test', 'new_block', () => undefined, onError, { backoffMs: 5 });
    const es = FakeEventSource.instances[0]!;
    es.readyState = FakeEventSource.CONNECTING;
    es.onerror?.(new Event('error'));
    vi.advanceTimersByTime(10_000);
    expect(onError).toHaveBeenCalledTimes(1);
    expect(es.closed).toBe(false);
    expect(FakeEventSource.instances).toHaveLength(1);
    sub.close();
  });

  it('rebuilds a single-URL EventSource after it gives up (CLOSED)', () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.useFakeTimers();
    const events: unknown[] = [];
    const sub = subscribe('https://rpc.test', 'new_block', (ev) => events.push(ev), undefined, { backoffMs: 5 });
    const first = FakeEventSource.instances[0]!;
    first.readyState = FakeEventSource.CLOSED;
    first.onerror?.(new Event('error'));
    vi.advanceTimersByTime(10_000);
    expect(FakeEventSource.instances).toHaveLength(2);
    const second = FakeEventSource.instances[1]!;
    expect(second.url).toBe(first.url);
    second.onmessage?.({ data: '{"type":"new_block","height":3}' });
    expect(events).toEqual([{ type: 'new_block', height: 3 }]);
    sub.close();
    expect(second.closed).toBe(true);
  });

  it('honours reconnect:false and maxRetries, and close cancels a pending rebuild', () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.useFakeTimers();
    subscribe('https://rpc.test', 'new_block', () => undefined, undefined, { reconnect: false });
    FakeEventSource.instances[0]!.readyState = FakeEventSource.CLOSED;
    FakeEventSource.instances[0]!.onerror?.(new Event('error'));
    vi.advanceTimersByTime(10_000);
    expect(FakeEventSource.instances).toHaveLength(1);

    FakeEventSource.instances = [];
    const sub = subscribe('https://rpc.test', 'new_block', () => undefined, undefined, { backoffMs: 5 });
    FakeEventSource.instances[0]!.readyState = FakeEventSource.CLOSED;
    FakeEventSource.instances[0]!.onerror?.(new Event('error'));
    sub.close();
    vi.advanceTimersByTime(10_000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('closes the EventSource when the signal aborts', () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    const ac = new AbortController();
    subscribe('https://rpc.test', 'new_block', () => undefined, undefined, { signal: ac.signal });
    ac.abort();
    expect(FakeEventSource.instances[0]!.closed).toBe(true);
  });
});

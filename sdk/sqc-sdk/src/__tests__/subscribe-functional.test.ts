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

    const sub = subscribe('http://rpc.test', ['new_block', 'new_tx'], (ev) => events.push(ev), undefined, {
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
      'http://rpc.test',
      'new_block',
      (ev) => events.push(ev),
      (err) => errors.push(err),
      { reconnect: true, backoffMs: 10, maxRetries: 1 },
    );
    await new Promise((r) => setTimeout(r, 80));
    sub.close();
    expect(events).toEqual([
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
      ['http://rpc-a.test', 'http://rpc-b.test'],
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
        if (url.startsWith('http://rpc-a.test')) {
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
        ['http://rpc-a.test', 'http://rpc-b.test'],
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
    expect(urlsHit.some((u) => u.startsWith('http://rpc-a.test'))).toBe(true);
    expect(urlsHit.some((u) => u.startsWith('http://rpc-b.test'))).toBe(true);
    expect(events).toEqual([{ type: 'new_block', height: 9 }]);
  });
});

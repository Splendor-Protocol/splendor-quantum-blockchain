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

    const sub = subscribe('http://rpc.test', ['new_block', 'new_tx'], (ev) => events.push(ev));
    await new Promise((r) => setTimeout(r, 30));
    expect(events).toEqual([
      { type: 'new_block', height: 1 },
      { type: 'new_tx', hash: '0x1' },
    ]);
    sub.close();
    expect(reader.cancel).toHaveBeenCalled();
  });
});

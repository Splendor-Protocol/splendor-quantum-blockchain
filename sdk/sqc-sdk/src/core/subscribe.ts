/**
 * Real-time node event subscriptions via /ws (SSE).
 *
 * Browser EventSource reconnects and sends Last-Event-ID on its own.
 * The Node fetch-stream path implements the same loop: disconnect
 * reporting, exponential backoff, and Last-Event-ID backfill.
 */
import type { Subscription, WsEvent, WsEventType } from './explorer-types.js';
import { BridgeError } from '../errors.js';

export type SubscribeOptions = {
  onError?: (err: unknown) => void;
  /** Node fetch-stream only. Default true. */
  reconnect?: boolean;
  /** Node fetch-stream only. Default Infinity. */
  maxRetries?: number;
  /** Initial reconnect delay in ms. Default 250. */
  backoffMs?: number;
  lastEventId?: string;
  signal?: AbortSignal;
};

function eventsParam(events: WsEventType | WsEventType[]): string {
  return Array.isArray(events) ? events.join(',') : events;
}

function parseSseLines(
  buffer: string,
  onEvent: (ev: WsEvent) => void,
  onId?: (id: string) => void,
): string {
  const lines = buffer.split('\n');
  const rest = lines.pop() ?? '';
  for (const line of lines) {
    if (line.startsWith('id:')) {
      const id = line.slice(3).trim();
      if (id) onId?.(id);
      continue;
    }
    if (!line.startsWith('data:')) continue;
    const payload = line.slice(5).trim();
    if (!payload || payload === '[heartbeat]') continue;
    try {
      onEvent(JSON.parse(payload) as WsEvent);
    } catch {
      /* ignore parse errors */
    }
  }
  return rest;
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new Error('aborted'));
      return;
    }
    const timer = setTimeout(resolve, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal?.reason ?? new Error('aborted'));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

async function nodeStreamOnce(
  url: string,
  lastEventId: string | undefined,
  onEvent: (ev: WsEvent) => void,
  onId: (id: string) => void,
  signal?: AbortSignal,
): Promise<'closed' | 'disconnected'> {
  const headers: Record<string, string> = { Accept: 'text/event-stream' };
  if (lastEventId) headers['Last-Event-ID'] = lastEventId;
  const res = await fetch(url, { headers, signal });
  if (!res.ok || !res.body) {
    throw BridgeError.network(`subscribe failed: ${res.status}`);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return 'disconnected';
      buffer += decoder.decode(value, { stream: true });
      buffer = parseSseLines(buffer, onEvent, onId);
    }
  } finally {
    void reader.cancel();
  }
}

async function nodeStreamSubscribe(
  url: string,
  onEvent: (ev: WsEvent) => void,
  options: SubscribeOptions,
  closed: () => boolean,
): Promise<void> {
  let lastId = options.lastEventId;
  let attempt = 0;
  const reconnect = options.reconnect !== false;
  const maxRetries = options.maxRetries ?? Number.POSITIVE_INFINITY;
  const baseBackoff = options.backoffMs ?? 250;
  const onError = options.onError;

  while (!closed()) {
    try {
      const result = await nodeStreamOnce(
        url,
        lastId,
        onEvent,
        (id) => {
          lastId = id;
        },
        options.signal,
      );
      if (closed()) return;
      onError?.(BridgeError.network(`subscribe ${result}`));
    } catch (e) {
      if (closed() || options.signal?.aborted) return;
      onError?.(e);
    }
    if (!reconnect) return;
    attempt += 1;
    if (attempt > maxRetries) return;
    const delay = Math.min(baseBackoff * 2 ** Math.min(attempt - 1, 5), 8_000);
    try {
      await sleep(delay, options.signal);
    } catch {
      return;
    }
  }
}

/**
 * Subscribe to node events. Uses EventSource in browser, fetch stream in Node.
 * Accepts one URL or a failover list (rotates on error / disconnect).
 */
export function subscribe(
  rpcUrl: string | string[],
  events: WsEventType | WsEventType[],
  onEvent: (ev: WsEvent) => void,
  onError?: (err: unknown) => void,
  options?: SubscribeOptions,
): Subscription {
  const urls = (Array.isArray(rpcUrl) ? rpcUrl : [rpcUrl])
    .map((u) => String(u).trim().replace(/\/$/, ''))
    .filter(Boolean);
  if (!urls.length) {
    throw BridgeError.rpc('subscribe requires at least one rpcUrl');
  }

  const opts: SubscribeOptions = { ...options, onError: options?.onError ?? onError };
  let urlIndex = 0;
  let closed = false;
  let activeClose: (() => void) | null = null;

  const startAt = (index: number) => {
    if (closed) return;
    urlIndex = index % urls.length;
    const base = urls[urlIndex]!;
    const url = `${base}/ws?subscribe=${encodeURIComponent(eventsParam(events))}`;

    if (typeof EventSource !== 'undefined') {
      const source = new EventSource(url);
      source.onmessage = (event) => {
        try {
          onEvent(JSON.parse(event.data) as WsEvent);
        } catch {
          /* heartbeat */
        }
      };
      source.onerror = (err) => {
        opts.onError?.(err);
        source.close();
        if (!closed && urls.length > 1) {
          // Rotate to next RPC on stream error.
          setTimeout(() => startAt(urlIndex + 1), opts.backoffMs ?? 250);
        }
      };
      activeClose = () => source.close();
      return;
    }

    const controller = new AbortController();
    if (opts.signal) {
      if (opts.signal.aborted) controller.abort();
      else opts.signal.addEventListener('abort', () => controller.abort(), { once: true });
    }
    const localOpts: SubscribeOptions = { ...opts, signal: controller.signal };
    activeClose = () => {
      controller.abort();
    };
    void nodeStreamSubscribe(url, onEvent, localOpts, () => closed)
      .then(() => {
        if (!closed && urls.length > 1 && localOpts.reconnect !== false) {
          startAt(urlIndex + 1);
        }
      })
      .catch((e) => {
        if (!closed && opts.onError) opts.onError(e);
        if (!closed && urls.length > 1 && localOpts.reconnect !== false) {
          startAt(urlIndex + 1);
        }
      });
  };

  startAt(0);
  return {
    close: () => {
      closed = true;
      activeClose?.();
    },
  };
}

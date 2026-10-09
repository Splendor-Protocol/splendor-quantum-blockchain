/**
 * Real-time node event subscriptions via /ws (SSE).
 *
 * Browser EventSource reconnects and sends Last-Event-ID on its own; when it gives up
 * (CLOSED), the stream is rebuilt with backoff. The Node fetch-stream path implements
 * the same loop: disconnect
 * reporting, exponential backoff, and Last-Event-ID backfill.
 */
import type { Subscription, WsEvent, WsEventType } from './explorer-types.js';
import { BridgeError } from '../errors.js';

export type SubscribeOptions = {
  onError?: (err: unknown) => void;
  /**
   * Default true. Browser: rebuild the EventSource after it gives up (CLOSED);
   * transient drops are retried by EventSource itself.
   */
  reconnect?: boolean;
  /**
   * Default Infinity. Browser: counts EventSource rebuilds, reset on open. Node: counts full
   * passes over the URL list, reset once a connection delivers data.
   */
  maxRetries?: number;
  /** Initial reconnect delay in ms. Default 250. */
  backoffMs?: number;
  /** Node only: resume point sent as `Last-Event-ID` (the browser tracks its own). */
  lastEventId?: string;
  /** Aborting closes the subscription, same as `close()`. */
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

/** Exponential backoff capped at 8s, with up to 25% jitter so clients do not reconnect in lockstep. */
export function backoffDelay(baseMs: number, attempt: number): number {
  const capped = Math.min(baseMs * 2 ** Math.min(Math.max(attempt - 1, 0), 5), 8_000);
  return Math.round(capped * (1 + Math.random() * 0.25));
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new Error('aborted'));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal?.reason ?? new Error('aborted'));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
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

/**
 * Node reconnect loop. With several URLs each failure moves to the next one, and a retry is
 * counted once per full pass. The latest event id carries across URLs, and the retry count
 * resets once a connection delivers data.
 */
async function nodeStreamSubscribe(
  urls: string[],
  onEvent: (ev: WsEvent) => void,
  options: SubscribeOptions,
  closed: () => boolean,
): Promise<void> {
  let lastId = options.lastEventId;
  let attempt = 0;
  let index = 0;
  const reconnect = options.reconnect !== false;
  const maxRetries = options.maxRetries ?? Number.POSITIVE_INFINITY;
  const baseBackoff = options.backoffMs ?? 250;
  const onError = options.onError;

  while (!closed()) {
    let gotData = false;
    try {
      const result = await nodeStreamOnce(
        urls[index]!,
        lastId,
        (ev) => {
          gotData = true;
          onEvent(ev);
        },
        (id) => {
          gotData = true;
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
    if (gotData) attempt = 0;
    index = (index + 1) % urls.length;
    if (index === 0) {
      attempt += 1;
      if (attempt > maxRetries) return;
    }
    const delay = backoffDelay(baseBackoff, Math.max(attempt, 1));
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
  let browserAttempt = 0;
  let restartTimer: ReturnType<typeof setTimeout> | null = null;

  const wsUrl = (base: string) => `${base}/ws?subscribe=${encodeURIComponent(eventsParam(events))}`;

  const startAt = (index: number) => {
    if (closed) return;
    urlIndex = index % urls.length;
    const url = wsUrl(urls[urlIndex]!);

    if (typeof EventSource !== 'undefined') {
      const source = new EventSource(url);
      source.onopen = () => {
        browserAttempt = 0;
      };
      source.onmessage = (event) => {
        try {
          onEvent(JSON.parse(event.data) as WsEvent);
        } catch {
          /* heartbeat */
        }
      };
      source.onerror = (err) => {
        opts.onError?.(err);
        if (closed) return;
        const multi = urls.length > 1;
        // With one URL, EventSource retries transient drops itself (and resends Last-Event-ID).
        if (!multi && source.readyState !== EventSource.CLOSED) return;
        source.close();
        if (opts.reconnect === false) return;
        browserAttempt += 1;
        if (browserAttempt > (opts.maxRetries ?? Number.POSITIVE_INFINITY)) return;
        restartTimer = setTimeout(
          () => startAt(multi ? urlIndex + 1 : urlIndex),
          backoffDelay(opts.backoffMs ?? 250, browserAttempt),
        );
      };
      activeClose = () => {
        if (restartTimer) clearTimeout(restartTimer);
        source.close();
      };
      return;
    }

    const controller = new AbortController();
    const localOpts: SubscribeOptions = { ...opts, signal: controller.signal };
    activeClose = () => {
      controller.abort();
    };
    void nodeStreamSubscribe(urls.map(wsUrl), onEvent, localOpts, () => closed).catch((e) => {
      if (!closed && opts.onError) opts.onError(e);
    });
  };

  const close = () => {
    closed = true;
    activeClose?.();
  };
  if (opts.signal?.aborted) {
    closed = true;
  } else {
    opts.signal?.addEventListener('abort', close, { once: true });
  }

  startAt(0);
  return { close };
}

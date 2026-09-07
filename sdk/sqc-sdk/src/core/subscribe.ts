/**
 * Real-time node event subscriptions via /ws (SSE).
 */
import type { Subscription, WsEvent, WsEventType } from './explorer-types.js';

function eventsParam(events: WsEventType | WsEventType[]): string {
  return Array.isArray(events) ? events.join(',') : events;
}

function parseSseLines(buffer: string, onEvent: (ev: WsEvent) => void): string {
  const lines = buffer.split('\n');
  const rest = lines.pop() ?? '';
  for (const line of lines) {
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

async function nodeStreamSubscribe(
  url: string,
  onEvent: (ev: WsEvent) => void,
  onError?: (err: unknown) => void,
  signal?: AbortSignal,
): Promise<Subscription> {
  const res = await fetch(url, {
    headers: { Accept: 'text/event-stream' },
    signal,
  });
  if (!res.ok || !res.body) {
    throw new Error(`subscribe failed: ${res.status}`);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let closed = false;

  (async () => {
    try {
      while (!closed) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        buffer = parseSseLines(buffer, onEvent);
      }
    } catch (e) {
      if (!closed && onError) onError(e);
    }
  })();

  return {
    close: () => {
      closed = true;
      void reader.cancel();
    },
  };
}

/**
 * Subscribe to node events. Uses EventSource in browser, fetch stream in Node.
 */
export function subscribe(
  rpcUrl: string,
  events: WsEventType | WsEventType[],
  onEvent: (ev: WsEvent) => void,
  onError?: (err: unknown) => void,
): Subscription {
  const base = rpcUrl.replace(/\/$/, '');
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
      if (onError) onError(err);
    };
    return { close: () => source.close() };
  }

  const controller = new AbortController();
  let sub: Subscription = { close: () => controller.abort() };
  void nodeStreamSubscribe(url, onEvent, onError, controller.signal)
    .then((s) => {
      sub = s;
    })
    .catch((e) => {
      if (onError) onError(e);
    });
  return {
    close: () => sub.close(),
  };
}

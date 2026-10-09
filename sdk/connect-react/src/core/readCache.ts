/**
 * Shared read cache for polled hooks. Keyed per read source (provider / connection object)
 * so two providers on the same network id never share results.
 */
type Entry = { at: number; value: unknown; inflight: Promise<unknown> | null };

const MAX_ENTRIES_PER_SOURCE = 200;
const caches = new WeakMap<object, Map<string, Entry>>();

function cacheFor(source: object): Map<string, Entry> {
  let map = caches.get(source);
  if (!map) {
    map = new Map();
    caches.set(source, map);
  }
  return map;
}

/**
 * Run `fetcher` at most once per `key` at a time, and reuse a result younger than `maxAgeMs`.
 * `force` skips the cached value (still joins an in-flight request).
 */
export function sharedRead<T>(
  source: object,
  key: string,
  fetcher: () => Promise<T>,
  opts: { maxAgeMs?: number; force?: boolean } = {},
): Promise<T> {
  const map = cacheFor(source);
  const hit = map.get(key);
  if (hit?.inflight) return hit.inflight as Promise<T>;
  const maxAgeMs = opts.maxAgeMs ?? 0;
  if (hit && !opts.force && maxAgeMs > 0 && Date.now() - hit.at < maxAgeMs) {
    return Promise.resolve(hit.value as T);
  }
  const entry: Entry = hit ?? { at: 0, value: undefined, inflight: null };
  const p = fetcher().then(
    (value) => {
      entry.value = value;
      entry.at = Date.now();
      entry.inflight = null;
      return value;
    },
    (e: unknown) => {
      entry.inflight = null;
      if (entry.at === 0 && map.get(key) === entry) map.delete(key);
      throw e;
    },
  );
  entry.inflight = p;
  if (!hit) {
    if (map.size >= MAX_ENTRIES_PER_SOURCE) {
      const oldest = map.keys().next().value;
      if (oldest !== undefined) map.delete(oldest);
    }
    map.set(key, entry);
  }
  return p;
}

type Listener = (prefix: string) => void;
const listeners = new WeakMap<object, Set<Listener>>();

/**
 * Drop cached reads whose key starts with `prefix` (all keys when omitted), including
 * in-flight ones, so the next read starts fresh; then tell subscribed hooks to refetch.
 */
export function invalidateSharedReads(source: object, prefix = ''): void {
  const map = caches.get(source);
  if (map) {
    for (const k of [...map.keys()]) {
      if (k.startsWith(prefix)) map.delete(k);
    }
  }
  for (const fn of [...(listeners.get(source) ?? [])]) fn(prefix);
}

/** Called with the prefix whenever reads on `source` are invalidated. Returns an unsubscribe. */
export function onSharedReadsInvalidated(source: object, fn: Listener): () => void {
  let set = listeners.get(source);
  if (!set) {
    set = new Set();
    listeners.set(source, set);
  }
  set.add(fn);
  return () => {
    set!.delete(fn);
  };
}

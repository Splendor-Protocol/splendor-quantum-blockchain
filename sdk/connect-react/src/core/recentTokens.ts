/**
 * Tokens this browser deployed recently, so a token panel on another page can offer them
 * instead of asking for a pasted address. Public data only (address, symbol, network).
 */
export const RECENT_TOKENS_KEY = 'sqc.connect.recentTokens';
export const RECENT_TOKENS_MAX = 5;

export type RecentToken = { address: string; symbol: string; network: string };

const ADDRESS_RE = /^SQ:[0-9a-f]{64}$/;
const SYMBOL_RE = /^[A-Za-z0-9]{1,11}$/;
const NETWORK_RE = /^[A-Za-z0-9_-]{1,64}$/;

/** Used when storage is unavailable (sandboxed iframes, private mode). */
let memory: RecentToken[] = [];
const listeners = new Set<() => void>();
let snapshot: RecentToken[] | null = null;

function store(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

function valid(v: unknown): RecentToken | null {
  if (!v || typeof v !== 'object') return null;
  const { address, symbol, network } = v as Record<string, unknown>;
  if (typeof address !== 'string' || typeof symbol !== 'string' || typeof network !== 'string') return null;
  const addr = address.trim();
  const canonical = /^SQ:/i.test(addr) ? `SQ:${addr.slice(3).toLowerCase()}` : '';
  if (!ADDRESS_RE.test(canonical) || !SYMBOL_RE.test(symbol) || !NETWORK_RE.test(network)) return null;
  return { address: canonical, symbol, network };
}

function read(): RecentToken[] {
  const s = store();
  if (!s) return memory;
  try {
    const parsed: unknown = JSON.parse(s.getItem(RECENT_TOKENS_KEY) ?? '[]');
    const out: RecentToken[] = [];
    for (const item of Array.isArray(parsed) ? parsed : []) {
      const t = valid(item);
      if (t && !out.some((o) => o.address === t.address && o.network === t.network)) out.push(t);
      if (out.length >= RECENT_TOKENS_MAX * 4) break;
    }
    return out;
  } catch {
    return memory;
  }
}

/** Recently deployed tokens, newest first (all networks; filter by `network`). */
export function getRecentTokens(): RecentToken[] {
  snapshot ??= read();
  return snapshot;
}

/** Record a deployed token. Invalid entries are ignored. */
export function rememberDeployedToken(entry: RecentToken): void {
  const t = valid(entry);
  if (!t) return;
  const rest = getRecentTokens().filter((o) => !(o.address === t.address && o.network === t.network));
  const sameNet = [t, ...rest.filter((o) => o.network === t.network)].slice(0, RECENT_TOKENS_MAX);
  const next = [...sameNet, ...rest.filter((o) => o.network !== t.network)];
  memory = next;
  snapshot = next;
  try {
    store()?.setItem(RECENT_TOKENS_KEY, JSON.stringify(next));
  } catch {
    /* quota / private mode: the in-memory list still works for this page */
  }
  for (const l of listeners) l();
}

export function subscribeRecentTokens(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Test helper. */
export function __resetRecentTokens(): void {
  memory = [];
  snapshot = null;
}

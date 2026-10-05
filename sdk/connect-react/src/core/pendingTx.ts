/**
 * Sent-but-unconfirmed transactions, kept in localStorage so a reload can resume
 * watching them. Entries hold only public data (hash, summary, sender, network).
 */
export const PENDING_TX_KEY = 'sqc.connect.pendingTx';
export const PENDING_TX_MAX = 20;
/** Entries older than this are dropped on load (the node will not report them anymore). */
export const PENDING_TX_TTL_MS = 24 * 60 * 60 * 1000;

export type PendingTx = {
  hash: string;
  /** What was sent: `transfer`, `token`, `deploy`, `call`, `module`, … */
  kind: string;
  summary: string;
  from?: string;
  /** Network id (`testnet`, `mainnet`, …) the transaction was sent on. */
  network: string;
  /** Epoch ms when it was sent. */
  ts: number;
};

const HASH_RE = /^(0x)?[0-9a-fA-F]{64}$/;
const SAFE_TEXT_RE = /^[^\u0000-\u001f\u007f]*$/;

function validEntry(v: unknown, now: number): PendingTx | null {
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  const { hash, kind, summary, from, network, ts } = o;
  if (typeof hash !== 'string' || !HASH_RE.test(hash)) return null;
  if (typeof kind !== 'string' || kind.length > 32 || !SAFE_TEXT_RE.test(kind)) return null;
  if (typeof summary !== 'string' || summary.length > 200 || !SAFE_TEXT_RE.test(summary)) return null;
  if (typeof network !== 'string' || !network || network.length > 64 || !SAFE_TEXT_RE.test(network)) return null;
  if (typeof ts !== 'number' || !Number.isFinite(ts) || ts > now + 60_000 || now - ts > PENDING_TX_TTL_MS) {
    return null;
  }
  if (from !== undefined && (typeof from !== 'string' || from.length > 80 || !SAFE_TEXT_RE.test(from))) {
    return null;
  }
  return { hash: hash.toLowerCase(), kind, summary, network, ts, ...(from ? { from } : {}) };
}

function storage(): Storage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null;
  } catch {
    return null;
  }
}

const listeners = new Set<() => void>();
let cache: PendingTx[] | null = null;

function read(): PendingTx[] {
  const store = storage();
  if (!store) return cache ?? [];
  let raw: unknown;
  try {
    raw = JSON.parse(store.getItem(PENDING_TX_KEY) ?? '[]');
  } catch {
    raw = [];
  }
  const now = Date.now();
  const seen = new Set<string>();
  const out: PendingTx[] = [];
  for (const item of Array.isArray(raw) ? raw : []) {
    const e = validEntry(item, now);
    if (!e || seen.has(e.hash)) continue;
    seen.add(e.hash);
    out.push(e);
    if (out.length >= PENDING_TX_MAX) break;
  }
  return out;
}

function write(list: PendingTx[]): void {
  cache = list;
  const store = storage();
  try {
    if (list.length) store?.setItem(PENDING_TX_KEY, JSON.stringify(list));
    else store?.removeItem(PENDING_TX_KEY);
  } catch {
    /* quota / private mode — keep the in-memory list */
  }
  for (const l of listeners) l();
}

/** Valid, de-duplicated pending entries (newest first, at most 20). */
export function loadPendingTxs(): PendingTx[] {
  if (!cache) cache = read();
  return cache;
}

export function addPendingTx(entry: Omit<PendingTx, 'ts'> & { ts?: number }): void {
  const e = validEntry({ ...entry, ts: entry.ts ?? Date.now() }, Date.now());
  if (!e) return;
  const rest = loadPendingTxs().filter((p) => p.hash !== e.hash);
  write([e, ...rest].slice(0, PENDING_TX_MAX));
}

export function removePendingTx(hash: string): void {
  const h = hash.toLowerCase();
  const list = loadPendingTxs();
  if (!list.some((p) => p.hash === h)) return;
  write(list.filter((p) => p.hash !== h));
}

export function clearPendingTxs(): void {
  write([]);
}

/** Notified on every change, including writes from other tabs. */
export function subscribePendingTxs(listener: () => void): () => void {
  listeners.add(listener);
  const onStorage = (e: StorageEvent) => {
    if (e.key !== PENDING_TX_KEY && e.key !== null) return;
    cache = read();
    listener();
  };
  if (typeof window !== 'undefined') window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(listener);
    if (typeof window !== 'undefined') window.removeEventListener('storage', onStorage);
  };
}

/** True when a send result already includes a confirmation (nothing left to watch). */
export function isConfirmedResult(res: unknown): boolean {
  if (!res || typeof res !== 'object') return false;
  const o = res as Record<string, unknown>;
  const c = o.confirmation;
  if (c && typeof c === 'object') {
    const co = c as Record<string, unknown>;
    if (co.ok === true || co.confirmed === true) return true;
  }
  return o.confirmed === true;
}

/** Map a node receipt to success / failed, or null while it is not final. */
export function receiptOutcome(receipt: unknown): 'success' | 'failed' | null {
  if (!receipt || typeof receipt !== 'object') return null;
  const raw = (receipt as Record<string, unknown>).status;
  if (typeof raw === 'number') return raw === 1 ? 'success' : raw === 0 ? 'failed' : null;
  if (typeof raw !== 'string' || !raw) return null;
  const s = raw.toLowerCase();
  if (s === 'success' || s === 'ok' || s === 'confirmed' || s === '1') return 'success';
  if (s === 'failed' || s === 'failure' || s === 'error' || s === 'reverted' || s === '0') return 'failed';
  return null;
}

/** Test helper: forget the in-memory cache so the next read hits storage. */
export function __resetPendingTxCache(): void {
  cache = null;
}

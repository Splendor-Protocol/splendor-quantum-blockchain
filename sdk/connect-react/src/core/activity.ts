/**
 * Token activity from node event logs (`GET /logs`, or an indexer with the same response shape).
 *
 * Topics are SHA3-256 of the event signature (vm/events.go), and an indexed `sqaddress` is the
 * 32 address bytes verbatim. The node serializes byte arrays as JSON number arrays and `data` as
 * base64, so every field is accepted either as bytes, `0x` hex, or (for `data`) base64.
 */

/** SHA3-256("Transfer(sqaddress,sqaddress,uint256)") — QRC-20 transfers, mints and burns. */
export const TOPIC_TRANSFER = '9cd5034ff25ae787380a126c0d6200006418e7f276beda2630a90f2f7e24e748';
/** SHA3-256("TransferSingle(sqaddress,sqaddress,sqaddress,uint256,uint256)") — QRC-1155. */
export const TOPIC_TRANSFER_SINGLE = '0c9b1ec27f09f5e9eee532240e4d6361b9aa897cd69d50f44f43bed3742a3b2e';
/** SHA3-256("TransferBatch(sqaddress,sqaddress,sqaddress,uint256[],uint256[])") — QRC-1155. */
export const TOPIC_TRANSFER_BATCH = 'b999bf18520194310a2dda23b5d67575f6f14e4f0e899638b19548a434890b3f';

/** Max blocks one node `/logs` request may span (rpc/explorer.go maxLogBlockRange). */
export const MAX_LOG_BLOCK_RANGE = 2_000;

const ZERO_ADDRESS = 'SQ:' + '0'.repeat(64);
const HEX_RE = /^[0-9a-f]*$/;

export type ActivityKind = 'token' | 'multi-token';
export type ActivityDirection = 'in' | 'out' | 'self';

export type ActivityItem = {
  /** Stable id: tx hash (or block) plus log index. */
  id: string;
  kind: ActivityKind;
  direction: ActivityDirection;
  /** Contract that emitted the event. */
  contract: string;
  from: string;
  to: string;
  /** Raw amount (base units). For a QRC-1155 batch, the sum over all ids. */
  amount: string;
  /** QRC-1155 token id(s), comma separated. */
  tokenId?: string;
  /** True for a mint (from the zero address). */
  mint: boolean;
  /** True for a burn (to the zero address). */
  burn: boolean;
  blockHeight: number | null;
  txHash: string | null;
  logIndex: number;
};

export type ActivityLogFilter = { topics: string[] };

function bytesToHex(bytes: ArrayLike<number>): string {
  let out = '';
  for (let i = 0; i < bytes.length; i++) out += (bytes[i]! & 0xff).toString(16).padStart(2, '0');
  return out;
}

function isByteArray(v: unknown): v is number[] {
  return Array.isArray(v) && v.every((b) => Number.isInteger(b) && b >= 0 && b <= 255);
}

/** Lowercase hex (no prefix) for a byte array or hex string, or null. */
export function toHex(v: unknown): string | null {
  if (isByteArray(v)) return bytesToHex(v);
  if (typeof v !== 'string') return null;
  const s = v.trim().replace(/^0x/i, '').toLowerCase();
  return HEX_RE.test(s) && s.length % 2 === 0 ? s : null;
}

function base64ToHex(s: string): string | null {
  try {
    const bin = atob(s);
    let out = '';
    for (let i = 0; i < bin.length; i++) out += bin.charCodeAt(i).toString(16).padStart(2, '0');
    return out;
  } catch {
    return null;
  }
}

/** Event data as hex: byte array, `0x` hex, or base64 (the node's encoding for `[]byte`). */
export function dataToHex(v: unknown): string | null {
  if (v == null || v === '') return '';
  if (isByteArray(v)) return bytesToHex(v);
  if (typeof v !== 'string') return null;
  if (/^0x/i.test(v)) return toHex(v);
  return base64ToHex(v);
}

/** `SQ:` address from a 32-byte topic / address field, or null. */
export function toSqAddress(v: unknown): string | null {
  if (typeof v === 'string' && /^SQ:[0-9a-f]{64}$/i.test(v.trim())) return `SQ:${v.trim().slice(3).toLowerCase()}`;
  const hex = toHex(v);
  return hex && hex.length === 64 ? `SQ:${hex}` : null;
}

/** 32-byte topic for an indexed `sqaddress`. */
export function addressTopic(address: string): string {
  const hex = address.trim().replace(/^SQ:/i, '').toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(hex)) throw new Error('addressTopic: expected an SQ: address');
  return hex;
}

/** The four positional-topic filters that find every transfer touching `address`. */
export function activityFilters(address: string): ActivityLogFilter[] {
  const me = addressTopic(address);
  return [
    { topics: [TOPIC_TRANSFER, me] },
    { topics: [TOPIC_TRANSFER, '', me] },
    { topics: [TOPIC_TRANSFER_SINGLE, '', me] },
    { topics: [TOPIC_TRANSFER_SINGLE, '', '', me] },
    { topics: [TOPIC_TRANSFER_BATCH, '', me] },
    { topics: [TOPIC_TRANSFER_BATCH, '', '', me] },
  ];
}

function words(hex: string): bigint[] {
  const out: bigint[] = [];
  for (let i = 0; i + 64 <= hex.length; i += 64) out.push(BigInt('0x' + hex.slice(i, i + 64)));
  return out;
}

function num(v: unknown): number | null {
  if (typeof v === 'number' && Number.isFinite(v)) return v;
  if (typeof v === 'string' && /^\d+$/.test(v)) return Number(v);
  return null;
}

/** One log as an activity row for `me`, or null when it is not a transfer touching `me`. */
export function parseActivityLog(log: unknown, me: string): ActivityItem | null {
  if (!log || typeof log !== 'object') return null;
  const l = log as Record<string, unknown>;
  const topics = Array.isArray(l.topics) ? l.topics.map(toHex) : [];
  const t0 = topics[0];
  const contract = toSqAddress(l.address);
  const data = dataToHex(l.data);
  if (!t0 || !contract || data == null) return null;
  const self = `SQ:${addressTopic(me)}`;
  let kind: ActivityKind;
  let from: string | null;
  let to: string | null;
  let amount: bigint;
  let tokenId: string | undefined;
  const w = words(data);
  if (t0 === TOPIC_TRANSFER) {
    kind = 'token';
    from = toSqAddress(topics[1]);
    to = toSqAddress(topics[2]);
    amount = w[0] ?? 0n;
  } else if (t0 === TOPIC_TRANSFER_SINGLE) {
    kind = 'multi-token';
    from = toSqAddress(topics[2]);
    to = toSqAddress(topics[3]);
    tokenId = (w[0] ?? 0n).toString();
    amount = w[1] ?? 0n;
  } else if (t0 === TOPIC_TRANSFER_BATCH) {
    kind = 'multi-token';
    from = toSqAddress(topics[2]);
    to = toSqAddress(topics[3]);
    const n = Number(w[0] ?? 0n);
    if (!Number.isSafeInteger(n) || n < 0 || w.length < 1 + 2 * n) return null;
    tokenId = w.slice(1, 1 + n).map(String).join(',');
    amount = w.slice(1 + n, 1 + 2 * n).reduce((a, b) => a + b, 0n);
  } else {
    return null;
  }
  if (!from || !to) return null;
  if (from !== self && to !== self) return null;
  const txHash = toHex(l.tx_hash);
  const blockHeight = num(l.block_height);
  const logIndex = num(l.log_index) ?? 0;
  return {
    id: `${txHash ?? `b${blockHeight ?? '?'}`}:${logIndex}`,
    kind,
    direction: from === to ? 'self' : from === self ? 'out' : 'in',
    contract,
    from,
    to,
    amount: amount.toString(),
    tokenId,
    mint: from === ZERO_ADDRESS,
    burn: to === ZERO_ADDRESS,
    blockHeight,
    txHash: txHash && /^[0-9a-f]{64}$/.test(txHash) && !/^0+$/.test(txHash) ? txHash : null,
    logIndex,
  };
}

/** Parse, de-duplicate and sort (newest first) logs from several filter queries. */
export function mergeActivity(logs: readonly unknown[], me: string, limit = 50): ActivityItem[] {
  const byId = new Map<string, ActivityItem>();
  for (const log of logs) {
    const item = parseActivityLog(log, me);
    if (item && !byId.has(item.id)) byId.set(item.id, item);
  }
  return [...byId.values()]
    .sort((a, b) => (b.blockHeight ?? 0) - (a.blockHeight ?? 0) || b.logIndex - a.logIndex)
    .slice(0, Math.max(0, limit));
}

/** Indexer base URL that may be fetched: https, or http only for localhost. */
export function safeIndexerUrl(raw: string | null | undefined): string | null {
  if (!raw) return null;
  try {
    const u = new URL(raw.trim());
    const local = u.hostname === 'localhost' || u.hostname === '127.0.0.1' || u.hostname === '[::1]';
    if (u.protocol !== 'https:' && !(u.protocol === 'http:' && local)) return null;
    if (u.username || u.password) return null;
    return u.href.replace(/\/+$/, '');
  } catch {
    return null;
  }
}

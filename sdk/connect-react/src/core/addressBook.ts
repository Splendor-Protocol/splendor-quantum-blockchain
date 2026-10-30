/**
 * Local address book: labelled `SQ:` contacts per network, kept in this browser only.
 * Entries are validated on read and write, so a tampered store cannot inject bad addresses.
 */
export const ADDRESS_BOOK_KEY = 'sqc.connect.addressBook';
/** Contacts kept per network. */
export const ADDRESS_BOOK_MAX = 100;
export const CONTACT_LABEL_MAX = 40;

export type Contact = { address: string; label: string; network: string };

const ADDRESS_RE = /^SQ:[0-9a-f]{64}$/;
const NETWORK_RE = /^[A-Za-z0-9_-]{1,64}$/;
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u001f\u007f-\u009f\u200b-\u200f\u202a-\u202e\u2066-\u2069]/g;

let memory: Contact[] = [];
let snapshot: Contact[] | null = null;
const listeners = new Set<() => void>();

function store(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

/** Trimmed label without control or bidi-override characters, or null when empty / too long. */
export function cleanContactLabel(raw: unknown): string | null {
  if (typeof raw !== 'string') return null;
  const label = raw.replace(CONTROL_RE, '').replace(/\s+/g, ' ').trim();
  if (!label || label.length > CONTACT_LABEL_MAX) return null;
  return label;
}

function valid(v: unknown): Contact | null {
  if (!v || typeof v !== 'object') return null;
  const { address, label, network } = v as Record<string, unknown>;
  if (typeof address !== 'string' || typeof network !== 'string') return null;
  const addr = address.trim();
  const canonical = /^SQ:/i.test(addr) ? `SQ:${addr.slice(3).toLowerCase()}` : '';
  const clean = cleanContactLabel(label);
  if (!ADDRESS_RE.test(canonical) || !clean || !NETWORK_RE.test(network)) return null;
  return { address: canonical, label: clean, network };
}

function read(): Contact[] {
  const s = store();
  if (!s) return memory;
  try {
    const parsed: unknown = JSON.parse(s.getItem(ADDRESS_BOOK_KEY) ?? '[]');
    const out: Contact[] = [];
    const perNet = new Map<string, number>();
    for (const item of Array.isArray(parsed) ? parsed : []) {
      const c = valid(item);
      if (!c || out.some((o) => o.address === c.address && o.network === c.network)) continue;
      const n = perNet.get(c.network) ?? 0;
      if (n >= ADDRESS_BOOK_MAX) continue;
      perNet.set(c.network, n + 1);
      out.push(c);
    }
    return out;
  } catch {
    return memory;
  }
}

function write(next: Contact[]): void {
  memory = next;
  snapshot = next;
  try {
    store()?.setItem(ADDRESS_BOOK_KEY, JSON.stringify(next));
  } catch {
    /* quota / private mode: the in-memory list still works for this page */
  }
  for (const l of listeners) l();
}

/** Every contact on every network, in insertion order. */
export function getAllContacts(): Contact[] {
  snapshot ??= read();
  return snapshot;
}

/** Contacts on one network, sorted by label. */
export function getContacts(network: string | null | undefined): Contact[] {
  if (!network) return [];
  return getAllContacts()
    .filter((c) => c.network === network)
    .sort((a, b) => a.label.localeCompare(b.label));
}

export type SaveContactResult = { ok: true; contact: Contact } | { ok: false; reason: 'invalid' | 'full' };

/** Add or relabel a contact. Fails for an invalid entry or when the network's book is full. */
export function saveContact(entry: { address: string; label: string; network: string }): SaveContactResult {
  const c = valid(entry);
  if (!c) return { ok: false, reason: 'invalid' };
  const all = getAllContacts();
  const existing = all.findIndex((o) => o.address === c.address && o.network === c.network);
  if (existing >= 0) {
    const next = all.slice();
    next[existing] = c;
    write(next);
    return { ok: true, contact: c };
  }
  if (all.filter((o) => o.network === c.network).length >= ADDRESS_BOOK_MAX) return { ok: false, reason: 'full' };
  write([...all, c]);
  return { ok: true, contact: c };
}

export function removeContact(address: string, network: string): void {
  const addr = /^SQ:/i.test(address.trim()) ? `SQ:${address.trim().slice(3).toLowerCase()}` : address;
  const all = getAllContacts();
  const next = all.filter((o) => !(o.address === addr && o.network === network));
  if (next.length !== all.length) write(next);
}

export function subscribeContacts(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Test helper. */
export function __resetAddressBook(): void {
  memory = [];
  snapshot = null;
}

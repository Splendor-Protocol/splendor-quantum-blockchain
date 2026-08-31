/**
 * KV-backed per-IP cooldown for /api/status polling.
 *
 * Simpler than the faucet's reserve/commit/release machinery on purpose: this
 * endpoint spends nothing, so the only job is to keep one client from turning
 * a 6-second poll loop into a hammer on the upstream RPCs. The page polls
 * every 6s; the cooldown is ~2s, so honest clients never see a 429.
 *
 * Consistency note (same as the faucet): Workers KV is eventually consistent,
 * so two truly simultaneous requests can both pass. That under-blocks by one
 * request — acceptable for a read-only endpoint. And unlike the faucet this
 * check FAILS OPEN at the call site: a broken KV must not take status polling
 * down, because nothing is at stake but upstream load.
 */

export interface LimitStore {
  get(key: string): Promise<string | null>;
  put(key: string, value: string, options?: { expirationTtl?: number }): Promise<void>;
}

/**
 * KV bucket for a client IP — copied from the faucet.
 *
 * IPv4 is used whole. IPv6 is truncated to its /64 prefix: a residential or
 * cloud IPv6 allocation is an entire /64 (usually more), so keying on the full
 * 128-bit address hands one host 2^64 free identities. Anything containing a
 * dot (plain IPv4, or IPv4-mapped `::ffff:a.b.c.d`) is used whole. Anything
 * unparseable is used whole too — a bucket that is too NARROW only
 * under-blocks, while a bad guess that is too WIDE locks out unrelated users.
 */
export function ipBucket(ip: string): string {
  const raw = ip.trim().toLowerCase().split('%')[0];
  if (!raw.includes(':') || raw.includes('.')) return raw;

  let groups: string[];
  if (raw.includes('::')) {
    const halves = raw.split('::');
    if (halves.length !== 2) return raw;
    const head = halves[0] ? halves[0].split(':') : [];
    const tail = halves[1] ? halves[1].split(':') : [];
    const fill = 8 - head.length - tail.length;
    if (fill < 0) return raw;
    groups = [...head, ...new Array<string>(fill).fill('0'), ...tail];
  } else {
    groups = raw.split(':');
  }
  if (groups.length !== 8) return raw;
  const prefix = groups.slice(0, 4);
  if (!prefix.every((g) => /^[0-9a-f]{1,4}$/.test(g))) return raw;
  return prefix.map((g) => g.replace(/^0+(?=.)/, '')).join(':') + '::/64';
}

export const statusKey = (ipBucketValue: string) => `st:ip:${ipBucketValue}`;

export type CooldownDecision =
  | { allowed: true }
  | { allowed: false; retryAfterSeconds: number };

/**
 * Allow at most one request per `cooldownMs` per IP bucket. Writing the stamp
 * on every allowed request is the reservation; there is nothing to release
 * because nothing downstream can fail in a way that costs the user anything.
 */
export async function checkStatusCooldown(
  store: LimitStore,
  ip: string | null,
  nowMs: number,
  cooldownMs: number,
): Promise<CooldownDecision> {
  if (!ip || cooldownMs <= 0) return { allowed: true };
  const key = statusKey(ipBucket(ip));

  const raw = await store.get(key);
  if (raw !== null && /^[0-9]+$/.test(raw)) {
    const last = Number(raw);
    if (Number.isSafeInteger(last)) {
      const elapsed = nowMs - last;
      // A clock that went backwards reads as "still cooling down" — safe side.
      if (elapsed < cooldownMs) {
        return {
          allowed: false,
          retryAfterSeconds: Math.max(1, Math.ceil((cooldownMs - Math.max(elapsed, 0)) / 1000)),
        };
      }
    }
  }

  // KV requires expirationTtl >= 60 even though the cooldown is ~2s.
  await store.put(key, String(nowMs), {
    expirationTtl: Math.max(60, Math.ceil(cooldownMs / 1000)),
  });
  return { allowed: true };
}

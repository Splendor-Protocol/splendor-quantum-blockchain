import type { ApiConfig } from './config.js';
import type { Store } from './store/types.js';

/** One pass: expired nonces and sessions, and audit rows past the retention window. */
export async function runHousekeeping(store: Store, config: Pick<ApiConfig, 'auditRetentionDays'>, now = Date.now()) {
  const auditBefore = new Date(now - config.auditRetentionDays * 24 * 3600 * 1000).toISOString();
  return store.purgeExpired(Math.floor(now / 1000), auditBefore);
}

/**
 * In-process timer. Fine for the single instance this API is built for; several instances would
 * each run it, which is harmless (the deletes are idempotent) but wasteful.
 */
export function startHousekeeping(
  store: Store,
  config: Pick<ApiConfig, 'auditRetentionDays'>,
  log: { info: (o: object, m: string) => void; error: (o: object, m: string) => void },
  intervalMs: number,
): () => void {
  const tick = () => {
    runHousekeeping(store, config)
      .then((removed) => {
        if (removed.nonces || removed.sessions || removed.audit) log.info({ removed }, 'housekeeping');
      })
      .catch((err: unknown) => log.error({ err }, 'housekeeping failed'));
  };
  const timer = setInterval(tick, intervalMs);
  timer.unref();
  tick();
  return () => clearInterval(timer);
}

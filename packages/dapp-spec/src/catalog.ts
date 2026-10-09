import { BLOCK_MANIFESTS } from './blocks.js';
import type { CatalogEntry } from './types.js';

/** Flags an admin can set per catalog entry (stored by the playground API). */
export type CatalogFlags = { hidden?: boolean; status?: CatalogEntry['status'] };

/**
 * Merge the widget manifests from `@splendor/connect-react/manifest` with the dApp blocks and
 * optional admin flags. Widget manifests are structurally compatible with `CatalogEntry`, so this
 * package does not need connect-react at runtime.
 */
export function createCatalog(
  widgetManifests: readonly CatalogEntry[],
  flags: Readonly<Record<string, CatalogFlags>> = {},
): CatalogEntry[] {
  const seen = new Set<string>();
  const out: CatalogEntry[] = [];
  for (const entry of [...widgetManifests, ...BLOCK_MANIFESTS]) {
    if (seen.has(entry.id)) throw new Error(`dapp-spec: duplicate catalog id "${entry.id}"`);
    seen.add(entry.id);
    const f = flags[entry.id];
    if (f?.hidden) continue;
    out.push(f?.status ? { ...entry, status: f.status } : entry);
  }
  return out;
}

/** Default prop values for a new block of this entry. */
export function defaultProps(entry: CatalogEntry): Record<string, string | number | boolean> {
  const out: Record<string, string | number | boolean> = {};
  for (const [k, v] of Object.entries(entry.defaults)) {
    if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') out[k] = v;
  }
  return out;
}

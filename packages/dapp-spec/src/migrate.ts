import { DAPP_SPEC_VERSION, type DappSpec } from './types.js';

type Migration = (spec: Record<string, unknown>) => Record<string, unknown>;

/**
 * `MIGRATIONS[n]` upgrades a version-n spec to n+1. Add one entry per format change and never
 * edit an old one: saved projects and share links stay loadable forever.
 */
const MIGRATIONS: Record<number, Migration> = {
  // v0 (playground preview): single page with top-level `blocks` and a theme name string.
  0: (s) => ({
    ...s,
    version: 1,
    theme:
      typeof s.theme === 'string'
        ? { base: s.theme, overrides: {} }
        : (s.theme as Record<string, unknown> | undefined) ?? { base: 'splendor', overrides: {} },
    pages: Array.isArray(s.pages) ? s.pages : [{ path: '/', title: 'Home', blocks: Array.isArray(s.blocks) ? s.blocks : [] }],
    blocks: undefined,
  }),
};

/** Bring any known older spec up to the current version. Run `validateSpec` on the result. */
export function migrateSpec(input: unknown): DappSpec {
  if (typeof input !== 'object' || input === null) throw new Error('dapp-spec: spec must be an object');
  let spec = { ...(input as Record<string, unknown>) };
  let version = typeof spec.version === 'number' ? spec.version : 0;
  if (version > DAPP_SPEC_VERSION) {
    throw new Error(`dapp-spec: spec version ${version} is newer than this package supports (${DAPP_SPEC_VERSION})`);
  }
  while (version < DAPP_SPEC_VERSION) {
    const step = MIGRATIONS[version];
    if (!step) throw new Error(`dapp-spec: no migration from version ${version}`);
    spec = step(spec);
    version += 1;
  }
  delete spec.blocks;
  return spec as unknown as DappSpec;
}

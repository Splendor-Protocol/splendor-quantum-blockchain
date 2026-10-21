import { DAPP_SPEC_VERSION, type DappBlock, type DappSpec, type LayoutNode, type PropValue } from './types.js';
import { blocksFromTree, wrapBlocks } from './tree.js';

type Migration = (spec: Record<string, unknown>) => Record<string, unknown>;

function asBlocks(raw: unknown): DappBlock[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((b): b is Record<string, unknown> => typeof b === 'object' && b !== null)
    .map((b, i) => ({
      id: typeof b.id === 'string' ? b.id : `block-${i + 1}`,
      widget: typeof b.widget === 'string' ? b.widget : 'hero',
      props: (typeof b.props === 'object' && b.props && !Array.isArray(b.props) ? b.props : {}) as Record<string, PropValue>,
    }));
}

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
  // v1 → v2: each page gains a layout tree; contracts/nav optional.
  1: (s) => {
    const pages = Array.isArray(s.pages)
      ? s.pages.map((p, i) => {
          if (typeof p !== 'object' || p === null) return p;
          const page = p as Record<string, unknown>;
          if (page.root && typeof page.root === 'object') {
            const root = page.root as LayoutNode;
            return { ...page, blocks: blocksFromTree(root) };
          }
          const blocks = asBlocks(page.blocks);
          const root = wrapBlocks(blocks, `page-${i}-root`);
          return { ...page, root, blocks };
        })
      : [];
    const theme =
      typeof s.theme === 'object' && s.theme !== null
        ? { ...(s.theme as Record<string, unknown>), overrides: (s.theme as { overrides?: unknown }).overrides ?? {} }
        : { base: 'splendor', overrides: {} };
    return {
      ...s,
      version: 2,
      theme,
      pages,
      contracts: Array.isArray(s.contracts) ? s.contracts : [],
      nav: typeof s.nav === 'object' && s.nav !== null ? s.nav : { showConnect: true },
    };
  },
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

import { blocksFromTree, isContainer, isSafeCssValue, mapTree, validateSpec, type CatalogEntry, type DappSpec, type LayoutNode, type ValidationIssue } from '@splendor/dapp-spec';

export type PreviewValidation = {
  /** Spec safe to render, or null when an issue blocks rendering. */
  spec: DappSpec | null;
  issues: ValidationIssue[];
};

const relaxedCache = new WeakMap<readonly CatalogEntry[], CatalogEntry[]>();

function withoutRequired(catalog: readonly CatalogEntry[]): CatalogEntry[] {
  let out = relaxedCache.get(catalog);
  if (!out) {
    out = catalog.map((e) => ({ ...e, props: e.props.map((p) => ({ ...p, required: false })) }));
    relaxedCache.set(catalog, out);
  }
  return out;
}

const PROP_ISSUE_RE = /^pages\[(\d+)\]\.blocks\[(\d+)\]\.props\.([\w-]+)$/;
/** Prop issues that mean the shape is wrong, not just the value: never render past these. */
const STRUCTURAL_PROP_MESSAGES = [/^reserved prop name$/, /^unknown prop for /, /^prop values must be /];

/** A known prop with a bad value (half-typed address, bad amount, unsafe link). */
function isValueIssue(issue: ValidationIssue): boolean {
  return issue.message !== 'required' && PROP_ISSUE_RE.test(issue.path) && !STRUCTURAL_PROP_MESSAGES.some((re) => re.test(issue.message));
}

/** Copy of the spec without the props named by value issues, so those blocks fall back to defaults. */
function withoutBadValues(input: unknown, issues: readonly ValidationIssue[]): unknown {
  if (typeof input !== 'object' || input === null || !Array.isArray((input as DappSpec).pages)) return input;
  const spec = input as DappSpec;
  const drop = new Map<string, Set<string>>();
  for (const issue of issues) {
    const m = PROP_ISSUE_RE.exec(issue.path);
    const page = m ? spec.pages[Number(m[1])] : undefined;
    if (!m || !page?.root || !isContainer(page.root)) continue;
    const block = blocksFromTree(page.root)[Number(m[2])];
    if (!block) continue;
    drop.set(block.id, (drop.get(block.id) ?? new Set()).add(m[3]!));
  }
  return {
    ...spec,
    pages: spec.pages.map((p) => {
      if (!p.root || !isContainer(p.root)) return p;
      const root = mapTree(p.root, (n): LayoutNode => {
        const names = n.kind === 'block' ? drop.get(n.block.id) : undefined;
        if (n.kind !== 'block' || !names) return n;
        return { ...n, block: { ...n.block, props: Object.fromEntries(Object.entries(n.block.props).filter(([k]) => !names.has(k))) } };
      });
      return { ...p, root, blocks: blocksFromTree(root) };
    }),
  };
}

const STYLE_VALUE_MSG = 'style values must be plain CSS tokens';
/** A half-typed style value (Width "50", Background "#12"): that one property is left out. */
function isStyleValueIssue(issue: ValidationIssue): boolean {
  return issue.message === STYLE_VALUE_MSG && /\.style\.[A-Za-z]+$/.test(issue.path);
}

const STYLE_TOKEN_KEYS = new Set(['width', 'maxWidth', 'gap', 'padding', 'background']);

/** Copy of the spec with every unsafe style token dropped (the validator rejects the same ones). */
function withoutBadStyles(input: unknown): unknown {
  if (typeof input !== 'object' || input === null || !Array.isArray((input as DappSpec).pages)) return input;
  const spec = input as DappSpec;
  const clean = <T extends { style?: Record<string, unknown> }>(n: T): T => {
    if (!n.style) return n;
    const entries = Object.entries(n.style).filter(([k, v]) => !STYLE_TOKEN_KEYS.has(k) || isSafeCssValue(v));
    return entries.length === Object.keys(n.style).length ? n : { ...n, style: Object.fromEntries(entries) };
  };
  return {
    ...spec,
    pages: spec.pages.map((p) => {
      if (!p.root || !isContainer(p.root)) return p;
      const root = mapTree(p.root, (n): LayoutNode => (n.kind === 'block' ? { ...n, block: clean(n.block as never) } : clean(n as never)));
      return { ...p, root, blocks: blocksFromTree(root) };
    }),
  };
}

/**
 * Validate like the API does, but keep the preview alive while the user is mid-edit:
 * - empty required fields render (those blocks show their own "set this up" state);
 * - a known prop with a bad value (a half-typed address) renders with that prop left out;
 * - a half-typed style value renders without that one property.
 * Everything structural or unsafe (unknown widgets or props, theme tokens, ids, limits) still
 * blocks rendering. `issues` always lists every problem.
 */
export function validateForPreview(input: unknown, catalog: readonly CatalogEntry[]): PreviewValidation {
  const strict = validateSpec(input, { catalog });
  if (strict.ok) return { spec: strict.spec, issues: [] };
  if (strict.issues.some((i) => i.message !== 'required' && !isValueIssue(i) && !isStyleValueIssue(i))) return { spec: null, issues: strict.issues };
  const styled = strict.issues.some(isStyleValueIssue) ? withoutBadStyles(input) : input;
  const cleaned = strict.issues.some(isValueIssue) ? withoutBadValues(styled, strict.issues) : styled;
  const relaxed = validateSpec(cleaned, { catalog: withoutRequired(catalog) });
  return { spec: relaxed.ok ? relaxed.spec : null, issues: strict.issues };
}

import { validateSpec, type CatalogEntry, type DappSpec, type ValidationIssue } from '@splendor/dapp-spec';

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

/**
 * Validate like the API does, but let a spec whose only problems are empty required fields still
 * render: those blocks show their own "set this up" state, and blanking the whole preview while
 * the user fills in a recipient helps nobody. Every other issue (unknown widgets, bad values,
 * unsafe theme tokens) still blocks rendering.
 */
export function validateForPreview(input: unknown, catalog: readonly CatalogEntry[]): PreviewValidation {
  const strict = validateSpec(input, { catalog });
  if (strict.ok) return { spec: strict.spec, issues: [] };
  if (strict.issues.some((i) => i.message !== 'required')) return { spec: null, issues: strict.issues };
  const relaxed = validateSpec(input, { catalog: withoutRequired(catalog) });
  return { spec: relaxed.ok ? relaxed.spec : null, issues: strict.issues };
}

/** Current `DappSpec.version`. Older specs go through `migrate()`. */
export const DAPP_SPEC_VERSION = 1 as const;

export type NetworkId = 'local' | 'devnet' | 'testnet' | 'mainnet';
export const NETWORK_IDS: readonly NetworkId[] = ['local', 'devnet', 'testnet', 'mainnet'];

/** Same keys as `ThemeOverrides` in `@splendor/connect-react`. */
export type ThemeOverrides = {
  accent?: string;
  accentHover?: string;
  bg?: string;
  surface?: string;
  text?: string;
  textMuted?: string;
  border?: string;
  danger?: string;
  success?: string;
  radius?: string;
  widgetPadding?: string;
  fontDisplay?: string;
  fontBody?: string;
  fontMono?: string;
};
export type ThemeName = 'splendor' | 'light' | 'dark';
export const THEME_KEYS: readonly (keyof ThemeOverrides)[] = [
  'accent', 'accentHover', 'bg', 'surface', 'text', 'textMuted', 'border', 'danger', 'success',
  'radius', 'widgetPadding', 'fontDisplay', 'fontBody', 'fontMono',
];

export type PropValue = string | number | boolean;

/** One widget or block on a page. `widget` is a catalog id (`transfer`, `tip-jar`, …). */
export type DappBlock = {
  id: string;
  widget: string;
  props: Record<string, PropValue>;
};

export type DappPage = {
  /** `/` for the home page, otherwise `/segment` (lowercase letters, digits, dashes). */
  path: string;
  title: string;
  blocks: DappBlock[];
};

/** Saved, shareable, declarative description of a dApp. Contains no executable code. */
export type DappSpec = {
  version: typeof DAPP_SPEC_VERSION;
  name: string;
  description?: string;
  network: NetworkId;
  theme: { base: ThemeName; overrides: ThemeOverrides };
  pages: DappPage[];
};

export type PropType = 'string' | 'number' | 'boolean' | 'select';

/** Mirrors `PropSchema` in `@splendor/connect-react/manifest`. */
export type PropSchema = {
  name: string;
  label: string;
  type: PropType;
  description?: string;
  format?: 'address' | 'amount' | 'text' | 'url';
  placeholder?: string;
  required?: boolean;
  min?: number;
  max?: number;
  options?: { value: string; label: string }[];
};

/**
 * What the validator and code generator need to know about a placeable item. Widgets come
 * from `WIDGET_MANIFESTS` (connect-react); blocks from `BLOCK_MANIFESTS` (this package).
 */
export type CatalogEntry = {
  id: string;
  title: string;
  description: string;
  category: string;
  /** `widget`: imported from a package. `block`: source file copied into the project. */
  kind: 'widget' | 'part' | 'block';
  importName: string;
  /** Package to import from (widgets) — blocks are local files. */
  package?: string;
  status: 'stable' | 'beta' | 'experimental';
  placeable: boolean;
  props: PropSchema[];
  defaults: Record<string, unknown>;
  since?: string;
  requires?: { wallet?: boolean; modules?: string[]; networks?: string[] };
  docs?: string;
};

export type ValidationIssue = { path: string; message: string };
export type ValidationResult = { ok: true; spec: DappSpec } | { ok: false; issues: ValidationIssue[] };

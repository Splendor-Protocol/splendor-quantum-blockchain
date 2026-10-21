/** Current `DappSpec.version`. Older specs go through `migrate()`. */
export const DAPP_SPEC_VERSION = 2 as const;

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
  /** Soft background wash behind heroes / CTAs (validated CSS). */
  heroGradient?: string;
};
export type ThemeName = 'splendor' | 'light' | 'dark';
export const THEME_KEYS: readonly (keyof ThemeOverrides)[] = [
  'accent', 'accentHover', 'bg', 'surface', 'text', 'textMuted', 'border', 'danger', 'success',
  'radius', 'widgetPadding', 'fontDisplay', 'fontBody', 'fontMono', 'heroGradient',
];

export type PropValue = string | number | boolean;

/** One placeable widget or block leaf in the layout tree. */
export type DappBlock = {
  id: string;
  widget: string;
  props: Record<string, PropValue>;
  /** Optional visual style overrides for this leaf. */
  style?: NodeStyle;
};

export type ContainerKind = 'section' | 'row' | 'column' | 'card' | 'stack';

/** Layout / spacing tokens. Values are validated as plain CSS tokens. */
export type NodeStyle = {
  width?: string;
  maxWidth?: string;
  gap?: string;
  padding?: string;
  align?: 'start' | 'center' | 'end' | 'stretch' | 'between';
  background?: string;
  hideOn?: 'mobile' | 'desktop';
};

export type ContainerNode = {
  kind: ContainerKind;
  id: string;
  style?: NodeStyle;
  children: LayoutNode[];
};

export type BlockNode = {
  kind: 'block';
  block: DappBlock;
};

export type LayoutNode = ContainerNode | BlockNode;

export type BlockAction =
  | { type: 'page'; path: string }
  | { type: 'url'; href: string }
  | { type: 'contract'; contractId: string; fn: string; argsFrom?: string };

export type ContractParam = { name: string; type: string };
export type ContractFunction = {
  name: string;
  signature: string;
  params: ContractParam[];
  returns?: string;
  kind: 'read' | 'write';
};
export type ContractRef = {
  id: string;
  name: string;
  address: string;
  functions: ContractFunction[];
};

export type DappNav = {
  brand?: string;
  showConnect?: boolean;
  /** Page paths in display order. Missing paths are appended. */
  links?: string[];
  footer?: string;
};

export type DappPage = {
  /** `/` for the home page, otherwise `/segment` (lowercase letters, digits, dashes). */
  path: string;
  title: string;
  /** Layout tree for the page body. */
  root: LayoutNode;
  /**
   * Flat list kept for tooling that still walks blocks (export summaries, issue paths).
   * Always derived from `root` on normalize; may be absent on raw input before migrate.
   */
  blocks?: DappBlock[];
};

/** Saved, shareable, declarative description of a dApp. Contains no executable code. */
export type DappSpec = {
  version: typeof DAPP_SPEC_VERSION;
  name: string;
  description?: string;
  network: NetworkId;
  theme: { base: ThemeName; overrides: ThemeOverrides; preset?: string };
  pages: DappPage[];
  contracts?: ContractRef[];
  nav?: DappNav;
};

export type PropType = 'string' | 'number' | 'boolean' | 'select';

/** Mirrors `PropSchema` in `@splendor/connect-react/manifest`. */
export type PropSchema = {
  name: string;
  label: string;
  type: PropType;
  description?: string;
  format?: 'address' | 'amount' | 'text' | 'url' | 'action' | 'image';
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
  /** SVG thumbnail id under `thumbs/` (catalog UI). */
  thumb?: string;
  /** True when this entry is a layout container shell (section/row/…). */
  container?: boolean;
};

export type ValidationIssue = { path: string; message: string };
export type ValidationResult = { ok: true; spec: DappSpec } | { ok: false; issues: ValidationIssue[] };

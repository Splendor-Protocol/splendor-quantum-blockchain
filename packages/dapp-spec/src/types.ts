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
  /** On a `column` this is the flex-basis (the column does not grow); elsewhere it is `width`. */
  width?: string;
  maxWidth?: string;
  gap?: string;
  padding?: string;
  /**
   * Content alignment. Column-like containers (section, stack, card, column): `start`/`center`/`end`
   * align children horizontally, `between` spreads them vertically. Rows: `start`/`center`/`end`
   * place the children at that edge on both axes, `between` spreads them horizontally.
   */
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
  /** `token`: a QRC-20 the kits read name/symbol/supply from. Default `contract`. */
  kind?: 'token' | 'contract';
  /** Display decimals for `token` contracts (default 18). */
  decimals?: number;
};

/** Who may see a page. `wallet`: a connected wallet. `signin`: a signed sign-in message. */
export type PageAccess = 'public' | 'wallet' | 'signin';
export const PAGE_ACCESS: readonly PageAccess[] = ['public', 'wallet', 'signin'];

export type DappNav = {
  brand?: string;
  showConnect?: boolean;
  /**
   * Pages shown in the navigation, in display order. Pages not listed stay reachable by links but
   * are not in the menu. Absent: every page, in page order.
   */
  links?: string[];
  footer?: string;
};

export type DappPage = {
  /** `/` for the home page, otherwise `/segment` (lowercase letters, digits, dashes). */
  path: string;
  title: string;
  /** Layout tree for the page body. */
  root: LayoutNode;
  /** Who may see the page (default `public`). Gated pages show a connect / sign-in panel first. */
  access?: PageAccess;
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
  /**
   * - `integer`: a non-negative base-10 integer without a sign or decimals (token ids, counts).
   * - `contract`: the id of an entry in `spec.contracts` (studio shows a contract picker). At render
   *   time the contract's address is filled into the block's address prop (see `bindBlockProps`).
   * - `function`: a function signature of the contract named by `contractProp` (picker filtered by
   *   `functionKind`). The function's ABI params are passed to the block as `fnParams`.
   * - `list`: rows of `columns` separated by `|`, one row per line (studio shows a row editor).
   */
  format?: 'address' | 'amount' | 'integer' | 'text' | 'url' | 'action' | 'image' | 'contract' | 'function' | 'list';
  /** With `format: 'url'`: only `https://` is accepted (images, server endpoints). */
  httpsOnly?: boolean;
  /** With `format: 'contract'`: which contracts the picker offers. */
  contractKind?: 'token' | 'contract' | 'any';
  /** With `format: 'contract'`: the block prop that receives the contract's address (default `contract`). */
  addressProp?: string;
  /** With `format: 'function'`: the prop holding the contract id (default `contractId`). */
  contractProp?: string;
  /** With `format: 'function'`: only read or only write functions. */
  functionKind?: 'read' | 'write';
  /** With `format: 'list'`: the columns of one row, in order. */
  columns?: { key: string; label: string; placeholder?: string }[];
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
  /** Still renders and validates (old specs) but is not offered in the palette. */
  hidden?: boolean;
};

export type ValidationIssue = { path: string; message: string };
export type ValidationResult = { ok: true; spec: DappSpec } | { ok: false; issues: ValidationIssue[] };

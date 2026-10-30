import {
  DAPP_SPEC_VERSION,
  NETWORK_IDS,
  PAGE_ACCESS,
  THEME_KEYS,
  type CatalogEntry,
  type PageAccess,
  type PropSchema,
  type ContainerKind,
  type ContractRef,
  type DappBlock,
  type DappNav,
  type DappPage,
  type DappSpec,
  type LayoutNode,
  type NetworkId,
  type NodeStyle,
  type PropValue,
  type ThemeName,
  type ThemeOverrides,
  type ValidationIssue,
  type ValidationResult,
} from './types.js';
import { CONTAINER_KINDS, blocksFromTree, countNodes, isContainer, treeDepth } from './tree.js';

const MAX_PAGES = 20;
const MAX_BLOCKS_PER_PAGE = 40;
const MAX_NODES_PER_PAGE = 200;
const MAX_TREE_DEPTH = 6;
const MAX_CONTRACTS = 40;
const MAX_STRING = 2000;
const MAX_FUNCTIONS_PER_CONTRACT = 64;
const MAX_PARAMS = 16;
const MAX_NAME = 80;
const MAX_NAV_LINKS = 20;
const MAX_NAV_BRAND = 80;
const MAX_NAV_FOOTER = 200;
const PATH_RE = /^\/([a-z0-9][a-z0-9-]{0,39})?$/;
const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const AMOUNT_RE = /^(0|[1-9]\d*)(\.\d{1,18})?$/;
const INTEGER_RE = /^(0|[1-9]\d*)$/;
/** Same cap as connect-react's TokenBalances. */
const MAX_TOKEN_LIST = 20;

/** "1-12, 5" style id lists: whole numbers or ascending ranges, comma / whitespace / newline separated. */
function validIdRanges(value: string): boolean {
  return value
    .split(/[\s,]+/)
    .filter(Boolean)
    .every((part) => {
      if (INTEGER_RE.test(part)) return true;
      const m = /^(0|[1-9]\d*)-(0|[1-9]\d*)$/.exec(part);
      return !!m && Number(m[1]) <= Number(m[2]);
    });
}

/** https anywhere; http only for localhost / 127.0.0.1; never credentials in the URL. */
function isHttpsOrLocalhost(value: string): boolean {
  try {
    const u = new URL(value);
    if (u.username || u.password) return false;
    if (u.protocol === 'https:') return true;
    return u.protocol === 'http:' && (u.hostname === 'localhost' || u.hostname === '127.0.0.1');
  } catch {
    return false;
  }
}
const SIGNATURE_RE = /^[A-Za-z_$][A-Za-z0-9_$]*\([^()]*\)$/;
/**
 * Theme / style values end up in CSS; keep them to plain tokens. `+` and `*` are allowed for
 * calc(); url(), image(), image-set(), src(), expressions and `//` (protocol-relative URLs) are not.
 */
const CSS_VALUE_RE = /^[#(),.%+*\w\s"'/-]{1,200}$/;
const CSS_FORBIDDEN_RE = /url\s*\(|expression|image-set|image\s*\(|src\s*\(|\/\//i;

function isSafeCssValue(v: unknown): v is string {
  return typeof v === 'string' && CSS_VALUE_RE.test(v) && !CSS_FORBIDDEN_RE.test(v);
}

/** Item separator of list props (`items`, `features`, `links`, …): newline when present, else comma. */
function listItems(raw: string): string[] {
  return raw.split(raw.includes('\n') ? /\r?\n/ : ',');
}
const RESERVED_KEYS = new Set(['__proto__', 'constructor', 'prototype']);
/** Props that hold a navigation target (buttons, heroes, CTAs). Mirrors the code generator's action keys. */
const LINK_PROPS = new Set(['href', 'ctaPath', 'buttonPath', 'link', 'action']);
const LINK_MSG = 'must be a page path (/about), an http(s) URL or contract:<id>.<function>';
const PAGE_LINK_RE = /^#?\/?[A-Za-z0-9/_-]{0,80}$/;
const HTTP_LINK_RE = /^https?:\/\/[^\s"'<>\\]{1,1900}$/i;
const CONTRACT_LINK_RE = /^contract:[A-Za-z0-9_-]{1,64}\.[A-Za-z_$][A-Za-z0-9_$]{0,63}$/;

/** Navigation targets: page paths, http(s) URLs, contract calls, or a JSON BlockAction of those. */
export function isSafeLink(value: string): boolean {
  const v = value.trim();
  if (!v) return true;
  if (PAGE_LINK_RE.test(v) || HTTP_LINK_RE.test(v) || CONTRACT_LINK_RE.test(v)) return true;
  if (!v.startsWith('{')) return false;
  try {
    const a = JSON.parse(v) as Record<string, unknown>;
    if (a.type === 'page') return typeof a.path === 'string' && PAGE_LINK_RE.test(a.path);
    if (a.type === 'url') return typeof a.href === 'string' && HTTP_LINK_RE.test(a.href);
    if (a.type === 'contract') {
      return (
        typeof a.contractId === 'string' &&
        ID_RE.test(a.contractId) &&
        typeof a.fn === 'string' &&
        /^[A-Za-z_$][A-Za-z0-9_$]{0,63}$/.test(a.fn) &&
        (a.argsFrom === undefined || (typeof a.argsFrom === 'string' && ID_RE.test(a.argsFrom)))
      );
    }
  } catch {
    /* not JSON */
  }
  return false;
}
const ALIGN = new Set(['start', 'center', 'end', 'stretch', 'between']);

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function normalizeStyle(raw: unknown): NodeStyle | undefined {
  if (!isObj(raw)) return undefined;
  const style: NodeStyle = {};
  if (typeof raw.width === 'string') style.width = raw.width;
  if (typeof raw.maxWidth === 'string') style.maxWidth = raw.maxWidth;
  if (typeof raw.gap === 'string') style.gap = raw.gap;
  if (typeof raw.padding === 'string') style.padding = raw.padding;
  if (typeof raw.background === 'string') style.background = raw.background;
  if (typeof raw.align === 'string' && ALIGN.has(raw.align)) style.align = raw.align as NodeStyle['align'];
  if (raw.hideOn === 'mobile' || raw.hideOn === 'desktop') style.hideOn = raw.hideOn;
  return Object.keys(style).length ? style : undefined;
}

function normalizeTree(node: unknown, issues: ValidationIssue[], path: string): LayoutNode | null {
  if (!isObj(node)) {
    issues.push({ path, message: 'layout node must be an object' });
    return null;
  }
  if (node.kind === 'block') {
    if (!isObj(node.block)) {
      issues.push({ path: `${path}.block`, message: 'block leaf needs a block object' });
      return null;
    }
    const b = node.block;
    const props: Record<string, PropValue> = {};
    if (isObj(b.props)) {
      for (const [k, v] of Object.entries(b.props)) if (!RESERVED_KEYS.has(k)) props[k] = v as PropValue;
    }
    const block: DappBlock = {
      id: typeof b.id === 'string' ? b.id : '',
      widget: typeof b.widget === 'string' ? b.widget : '',
      props,
      ...(normalizeStyle(b.style) ? { style: normalizeStyle(b.style) } : {}),
    };
    return { kind: 'block', block };
  }
  if (!CONTAINER_KINDS.includes(node.kind as ContainerKind)) {
    issues.push({ path: `${path}.kind`, message: `kind must be block or ${CONTAINER_KINDS.join('|')}` });
    return null;
  }
  const childrenRaw = Array.isArray(node.children) ? node.children : [];
  const children: LayoutNode[] = [];
  childrenRaw.forEach((c, i) => {
    const child = normalizeTree(c, issues, `${path}.children[${i}]`);
    if (child) children.push(child);
  });
  return {
    kind: node.kind as ContainerKind,
    id: typeof node.id === 'string' ? node.id : '',
    ...(normalizeStyle(node.style) ? { style: normalizeStyle(node.style)! } : {}),
    children,
  };
}

function normalizeContracts(raw: unknown): ContractRef[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((c): c is Record<string, unknown> => isObj(c))
    .map((c) => ({
      id: String(c.id ?? ''),
      name: String(c.name ?? ''),
      address: String(c.address ?? ''),
      functions: Array.isArray(c.functions)
        ? c.functions
            .filter((f): f is Record<string, unknown> => isObj(f))
            .map((f) => ({
              name: String(f.name ?? ''),
              signature: String(f.signature ?? ''),
              params: Array.isArray(f.params)
                ? f.params
                    .filter((p): p is Record<string, unknown> => isObj(p))
                    .map((p) => ({ name: String(p.name ?? ''), type: String(p.type ?? 'uint256') }))
                : [],
              ...(typeof f.returns === 'string' ? { returns: f.returns } : {}),
              kind: f.kind === 'write' ? ('write' as const) : ('read' as const),
            }))
        : [],
      ...(c.kind === 'token' || c.kind === 'contract' ? { kind: c.kind } : {}),
      ...(typeof c.decimals === 'number' ? { decimals: c.decimals } : {}),
    }));
}

function normalizeNav(raw: unknown): DappNav | undefined {
  if (!isObj(raw)) return undefined;
  const nav: DappNav = {};
  if (typeof raw.brand === 'string') nav.brand = raw.brand;
  if (typeof raw.showConnect === 'boolean') nav.showConnect = raw.showConnect;
  if (Array.isArray(raw.links)) nav.links = raw.links.filter((l): l is string => typeof l === 'string');
  if (typeof raw.footer === 'string') nav.footer = raw.footer;
  return nav;
}

/**
 * A fresh spec holding only the fields DappSpec defines. Callers store and render this copy,
 * so extra keys in the input never reach a database, an export or a component.
 */
function normalize(input: Record<string, unknown>, issues: ValidationIssue[]): DappSpec {
  const theme = input.theme as { base: ThemeName; overrides: Record<string, unknown>; preset?: string };
  const overrides: ThemeOverrides = {};
  for (const k of THEME_KEYS) {
    const v = theme.overrides[k];
    if (typeof v === 'string') overrides[k] = v;
  }
  const pages = (input.pages as unknown[]).map((p, pi): DappPage => {
    const page = p as Record<string, unknown>;
    const root = normalizeTree(page.root, issues, `pages[${pi}].root`) ?? {
      kind: 'section' as const,
      id: `page-${pi}-root`,
      children: [],
    };
    return {
      path: page.path as string,
      title: page.title as string,
      root,
      blocks: blocksFromTree(root),
      ...(page.access === 'wallet' || page.access === 'signin' ? { access: page.access } : {}),
    };
  });
  return {
    version: DAPP_SPEC_VERSION,
    name: input.name as string,
    ...(typeof input.description === 'string' ? { description: input.description } : {}),
    network: input.network as NetworkId,
    theme: {
      base: theme.base,
      overrides,
      ...(typeof theme.preset === 'string' ? { preset: theme.preset } : {}),
    },
    pages,
    contracts: normalizeContracts(input.contracts),
    ...(normalizeNav(input.nav) ? { nav: normalizeNav(input.nav) } : {}),
  };
}

function checkStyle(style: unknown, path: string, add: (p: string, m: string) => void) {
  if (style == null) return;
  if (!isObj(style)) return add(path, 'style must be an object');
  for (const [k, v] of Object.entries(style)) {
    if (k === 'hideOn') {
      if (v !== 'mobile' && v !== 'desktop') add(`${path}.hideOn`, 'must be mobile or desktop');
      continue;
    }
    if (k === 'align') {
      if (typeof v !== 'string' || !ALIGN.has(v)) add(`${path}.align`, 'invalid align');
      continue;
    }
    if (!isSafeCssValue(v)) {
      add(`${path}.${k}`, 'style values must be plain CSS tokens');
    }
  }
}

/** Contract id → its functions, for `format: 'contract' | 'function'` props. */
type ContractIndex = ReadonlyMap<string, { kind?: unknown; functions: { signature: string; kind: string }[] }>;

function indexContracts(raw: unknown): ContractIndex {
  const out = new Map<string, { kind?: unknown; functions: { signature: string; kind: string }[] }>();
  if (!Array.isArray(raw)) return out;
  for (const c of raw) {
    if (!isObj(c) || typeof c.id !== 'string') continue;
    const functions = Array.isArray(c.functions)
      ? c.functions.filter(isObj).map((f) => ({ signature: String(f.signature ?? ''), kind: String(f.kind ?? '') }))
      : [];
    out.set(c.id, { kind: c.kind, functions });
  }
  return out;
}

function checkBinding(
  block: Record<string, unknown>,
  schema: PropSchema,
  value: string,
  p: string,
  contracts: ContractIndex,
  add: (p: string, m: string) => void,
) {
  if (schema.format === 'contract') {
    const c = contracts.get(value);
    if (!c) return add(p, 'pick a contract from the Contracts panel');
    if (schema.contractKind === 'token' && c.kind !== 'token') add(p, 'pick a token contract');
    return;
  }
  if (schema.format !== 'function') return;
  if (!SIGNATURE_RE.test(value)) return add(p, 'signature must look like name(type,type)');
  const owner = (block.props as Record<string, unknown>)[schema.contractProp ?? 'contractId'];
  const c = typeof owner === 'string' && owner ? contracts.get(owner) : undefined;
  if (!c || !c.functions.length) return;
  const fn = c.functions.find((f) => f.signature === value);
  if (!fn) add(p, 'not a function of the chosen contract');
  else if (schema.functionKind && fn.kind !== schema.functionKind) add(p, `pick a ${schema.functionKind} function`);
}

function checkProps(
  block: Record<string, unknown>,
  bat: string,
  entry: CatalogEntry | undefined,
  catalog: boolean,
  add: (p: string, m: string) => void,
  ids: Set<string>,
  contracts: ContractIndex = new Map(),
) {
  if (typeof block.id !== 'string' || !ID_RE.test(block.id)) add(`${bat}.id`, 'id must be 1-64 letters, digits, _ or -');
  else if (ids.has(block.id)) add(`${bat}.id`, 'duplicate block id');
  else ids.add(block.id);
  if (typeof block.widget !== 'string' || !block.widget) return add(`${bat}.widget`, 'widget is required');
  if (!isObj(block.props)) return add(`${bat}.props`, 'props must be an object');
  checkStyle(block.style, `${bat}.style`, add);
  if (catalog && !entry) return add(`${bat}.widget`, `unknown widget "${block.widget}"`);
  if (entry && !entry.placeable) return add(`${bat}.widget`, `"${block.widget}" cannot be placed on a page`);
  for (const [name, value] of Object.entries(block.props)) {
    const p = `${bat}.props.${name}`;
    if (RESERVED_KEYS.has(name)) {
      add(p, 'reserved prop name');
      continue;
    }
    if (!['string', 'number', 'boolean'].includes(typeof value)) {
      add(p, 'prop values must be strings, numbers or booleans');
      continue;
    }
    if (typeof value === 'string' && value.length > MAX_STRING) add(p, `max ${MAX_STRING} chars`);
    if (!entry) continue;
    const schema = entry.props.find((s) => s.name === name);
    if (!schema) {
      add(p, `unknown prop for ${entry.id}`);
      continue;
    }
    if (schema.type === 'select') {
      if (!schema.options?.some((o) => o.value === value)) add(p, 'not one of the allowed options');
    } else if (typeof value !== schema.type) {
      add(p, `expected ${schema.type}`);
    } else if (typeof value === 'number') {
      if (!Number.isFinite(value)) add(p, 'must be a finite number');
      if (schema.min != null && value < schema.min) add(p, `min ${schema.min}`);
      if (schema.max != null && value > schema.max) add(p, `max ${schema.max}`);
    } else if (typeof value === 'string' && value !== '') {
      if (schema.format === 'address' && !ADDRESS_RE.test(value)) add(p, 'must be an SQ: address');
      if (schema.format === 'amount' && !AMOUNT_RE.test(value)) add(p, 'must be a decimal amount');
      if (schema.format === 'integer' && !INTEGER_RE.test(value)) add(p, 'must be a whole number (digits only)');
      if (schema.format === 'contract' || schema.format === 'function') checkBinding(block, schema, value, p, contracts, add);
      if (schema.format === 'url') {
        if (schema.httpsOnly) {
          if (!/^https:\/\/[^\s"'<>]+$/i.test(value)) add(p, 'must be an https URL');
        } else if (!/^https?:\/\/[^\s"'<>]+$/.test(value)) add(p, 'must be an http(s) URL');
      }
      if ((LINK_PROPS.has(name) || schema.format === 'action') && !isSafeLink(value)) add(p, LINK_MSG);
      if (name === 'links' && listItems(value).some((item) => !isSafeLink(item.split('|').slice(1).join('|')))) {
        add(p, `each link ${LINK_MSG}`);
      }
      if (entry.id === 'nft-gallery' && name === 'tokenIds') {
        const parts = listItems(value).map((s) => s.trim()).filter(Boolean);
        if (parts.some((s) => !INTEGER_RE.test(s))) add(p, 'token ids must be whole numbers, comma or newline separated');
      }
      if ((entry.id === 'nft721-gallery' || entry.id === 'nft-panel') && name === 'tokenIds' && !validIdRanges(value)) {
        add(p, 'token ids must be whole numbers or ranges like 1-12, comma separated');
      }
      if ((entry.id === 'x402-paywall' && name === 'resourceUrl') || (entry.id === 'activity-feed' && name === 'indexerUrl')) {
        if (!isHttpsOrLocalhost(value)) add(p, 'must be an https URL (http is allowed only for localhost)');
      }
      if (entry.id === 'token-balances' && name === 'tokens') {
        const parts = value.split(/[\s,]+/).filter(Boolean);
        if (parts.some((s) => !ADDRESS_RE.test(s))) add(p, 'each token must be an SQ: address, comma separated');
        else if (parts.length > MAX_TOKEN_LIST) add(p, `at most ${MAX_TOKEN_LIST} tokens`);
      }
    }
  }
  if (entry) {
    const props = block.props as Record<string, unknown>;
    // A required address is also satisfied by a contract pick that fills it (`addressProp`).
    const boundAddress = new Set(
      entry.props.filter((s) => s.format === 'contract' && typeof props[s.name] === 'string' && props[s.name] !== '').map((s) => s.addressProp ?? 'contract'),
    );
    for (const s of entry.props) {
      if (s.required && !boundAddress.has(s.name) && (props[s.name] == null || props[s.name] === '')) add(`${bat}.props.${s.name}`, 'required');
    }
  }
}

function checkTreeStructure(
  node: unknown,
  path: string,
  add: (p: string, m: string) => void,
  ids: Set<string>,
  leafBlocks: Record<string, unknown>[],
) {
  if (!isObj(node)) return add(path, 'layout node must be an object');
  if (node.kind === 'block') {
    if (!isObj(node.block)) return add(`${path}.block`, 'missing block');
    leafBlocks.push(node.block);
    return;
  }
  if (!CONTAINER_KINDS.includes(node.kind as ContainerKind)) {
    return add(`${path}.kind`, `kind must be block or ${CONTAINER_KINDS.join('|')}`);
  }
  if (typeof node.id !== 'string' || !ID_RE.test(node.id)) add(`${path}.id`, 'id must be 1-64 letters, digits, _ or -');
  else if (ids.has(node.id)) add(`${path}.id`, 'duplicate node id');
  else ids.add(node.id);
  checkStyle(node.style, `${path}.style`, add);
  if (!Array.isArray(node.children)) return add(`${path}.children`, 'children must be an array');
  node.children.forEach((c, i) => checkTreeStructure(c, `${path}.children[${i}]`, add, ids, leafBlocks));
}

function checkNav(nav: unknown, pagePaths: ReadonlySet<string>, add: (p: string, m: string) => void) {
  if (!isObj(nav)) return add('nav', 'nav must be an object');
  if (nav.brand != null && (typeof nav.brand !== 'string' || nav.brand.length > MAX_NAV_BRAND)) {
    add('nav.brand', `brand must be a string (max ${MAX_NAV_BRAND} chars)`);
  }
  if (nav.footer != null && (typeof nav.footer !== 'string' || nav.footer.length > MAX_NAV_FOOTER)) {
    add('nav.footer', `footer must be a string (max ${MAX_NAV_FOOTER} chars)`);
  }
  if (nav.showConnect != null && typeof nav.showConnect !== 'boolean') add('nav.showConnect', 'showConnect must be true or false');
  if (nav.links != null) {
    if (!Array.isArray(nav.links) || nav.links.length > MAX_NAV_LINKS) {
      add('nav.links', `links must be an array of page paths (max ${MAX_NAV_LINKS})`);
    } else {
      nav.links.forEach((l, i) => {
        if (typeof l !== 'string' || !pagePaths.has(l)) add(`nav.links[${i}]`, 'must be the path of an existing page');
      });
    }
  }
}

export type ValidateOptions = {
  /** Known placeable items. When given, unknown widgets and props are errors. */
  catalog?: readonly CatalogEntry[];
};

/**
 * Check an untrusted spec (from storage, the API or a share link). Returns the spec typed on
 * success; never throws. Specs are data only, so this is the trust boundary for hosting too.
 */
export function validateSpec(input: unknown, opts: ValidateOptions = {}): ValidationResult {
  const issues: ValidationIssue[] = [];
  const add = (path: string, message: string) => issues.push({ path, message });
  if (!isObj(input)) return { ok: false, issues: [{ path: '', message: 'spec must be an object' }] };

  if (input.version !== DAPP_SPEC_VERSION) add('version', `expected version ${DAPP_SPEC_VERSION} (run migrate first)`);
  if (typeof input.name !== 'string' || !input.name.trim() || input.name.length > 80) add('name', 'name is required (max 80 chars)');
  if (input.description != null && (typeof input.description !== 'string' || input.description.length > 500)) {
    add('description', 'description must be a string (max 500 chars)');
  }
  if (!NETWORK_IDS.includes(input.network as never)) add('network', `network must be one of ${NETWORK_IDS.join(', ')}`);

  const theme = input.theme;
  if (!isObj(theme) || !['splendor', 'light', 'dark'].includes(theme.base as string) || !isObj(theme.overrides)) {
    add('theme', 'theme must be { base: splendor|light|dark, overrides: {} }');
  } else {
    for (const [k, v] of Object.entries(theme.overrides)) {
      if (!THEME_KEYS.includes(k as never)) add(`theme.overrides.${k}`, 'unknown theme key');
      else if (!isSafeCssValue(v)) {
        add(`theme.overrides.${k}`, 'theme values must be plain CSS tokens');
      }
    }
    if (theme.preset != null && typeof theme.preset !== 'string') add('theme.preset', 'preset must be a string');
  }

  if (input.contracts != null) {
    if (!Array.isArray(input.contracts) || input.contracts.length > MAX_CONTRACTS) {
      add('contracts', `contracts must be an array (max ${MAX_CONTRACTS})`);
    } else {
      const cids = new Set<string>();
      input.contracts.forEach((c, i) => {
        const at = `contracts[${i}]`;
        if (!isObj(c)) return add(at, 'contract must be an object');
        if (typeof c.id !== 'string' || !ID_RE.test(c.id)) add(`${at}.id`, 'id required');
        else if (cids.has(c.id)) add(`${at}.id`, 'duplicate contract id');
        else cids.add(c.id);
        if (typeof c.name !== 'string' || !c.name.trim() || c.name.length > MAX_NAME) {
          add(`${at}.name`, `name required (max ${MAX_NAME} chars)`);
        }
        if (typeof c.address !== 'string' || (c.address !== '' && !ADDRESS_RE.test(c.address))) {
          add(`${at}.address`, 'must be an SQ: address (or empty while drafting)');
        }
        if (c.kind != null && c.kind !== 'token' && c.kind !== 'contract') add(`${at}.kind`, 'kind must be token or contract');
        if (c.decimals != null && (typeof c.decimals !== 'number' || !Number.isInteger(c.decimals) || c.decimals < 0 || c.decimals > 36)) {
          add(`${at}.decimals`, 'decimals must be a whole number from 0 to 36');
        }
        if (!Array.isArray(c.functions)) add(`${at}.functions`, 'functions must be an array');
        else if (c.functions.length > MAX_FUNCTIONS_PER_CONTRACT) {
          add(`${at}.functions`, `max ${MAX_FUNCTIONS_PER_CONTRACT} functions per contract`);
        } else {
          const sigs = new Set<string>();
          c.functions.forEach((f, fi) => {
            const fat = `${at}.functions[${fi}]`;
            if (!isObj(f)) return add(fat, 'function must be an object');
            if (typeof f.name !== 'string' || !f.name || f.name.length > MAX_NAME) {
              add(`${fat}.name`, `name required (max ${MAX_NAME} chars)`);
            }
            if (typeof f.signature !== 'string' || !f.signature) add(`${fat}.signature`, 'signature required');
            else if (f.signature.length > MAX_STRING || !SIGNATURE_RE.test(f.signature)) {
              add(`${fat}.signature`, 'signature must look like name(type,type)');
            } else if (sigs.has(f.signature)) add(`${fat}.signature`, 'duplicate signature');
            else sigs.add(f.signature);
            if (f.kind !== 'read' && f.kind !== 'write') add(`${fat}.kind`, 'kind must be read or write');
            if (f.returns != null && (typeof f.returns !== 'string' || f.returns.length > MAX_NAME)) {
              add(`${fat}.returns`, `returns must be a string (max ${MAX_NAME} chars)`);
            }
            if (f.params != null) {
              if (!Array.isArray(f.params) || f.params.length > MAX_PARAMS) {
                add(`${fat}.params`, `params must be an array (max ${MAX_PARAMS})`);
              } else {
                f.params.forEach((prm, pi) => {
                  const pat = `${fat}.params[${pi}]`;
                  if (!isObj(prm)) return add(pat, 'param must be { name, type }');
                  if (typeof prm.name !== 'string' || prm.name.length > MAX_NAME) add(`${pat}.name`, `name must be a string (max ${MAX_NAME} chars)`);
                  if (typeof prm.type !== 'string' || !prm.type || prm.type.length > MAX_NAME) add(`${pat}.type`, `type required (max ${MAX_NAME} chars)`);
                });
              }
            }
          });
        }
      });
    }
  }

  const byId = new Map((opts.catalog ?? []).map((c) => [c.id, c]));
  const contractIndex = indexContracts(input.contracts);
  const paths = new Set<string>();
  const pages = input.pages;
  if (!Array.isArray(pages) || pages.length === 0 || pages.length > MAX_PAGES) {
    add('pages', `1 to ${MAX_PAGES} pages required`);
  } else {
    // Container ids and block ids share one namespace (`data-node-id`, selection, tree ops).
    const ids = new Set<string>();
    pages.forEach((page, pi) => {
      const at = `pages[${pi}]`;
      if (!isObj(page)) return add(at, 'page must be an object');
      if (typeof page.path !== 'string' || !PATH_RE.test(page.path)) add(`${at}.path`, 'path must be / or /lowercase-segment');
      else if (paths.has(page.path)) add(`${at}.path`, 'duplicate path');
      else paths.add(page.path);
      if (typeof page.title !== 'string' || !page.title.trim() || page.title.length > 60) add(`${at}.title`, 'title is required (max 60 chars)');
      if (page.access != null && !PAGE_ACCESS.includes(page.access as PageAccess)) add(`${at}.access`, `access must be one of ${PAGE_ACCESS.join(', ')}`);
      if (page.path === '/' && page.access != null && page.access !== 'public') add(`${at}.access`, 'the home page must stay public (visitors land there)');

      if (page.root != null) {
        const leafBlocks: Record<string, unknown>[] = [];
        if (isObj(page.root) && page.root.kind === 'block') {
          add(`${at}.root.kind`, `page root must be a container (${CONTAINER_KINDS.join('|')}), not a block`);
        }
        checkTreeStructure(page.root, `${at}.root`, add, ids, leafBlocks);
        const probe = normalizeTree(page.root, [], `${at}.root`);
        if (probe) {
          if (countNodes(probe) > MAX_NODES_PER_PAGE) add(`${at}.root`, `max ${MAX_NODES_PER_PAGE} nodes per page`);
          if (treeDepth(probe) > MAX_TREE_DEPTH) add(`${at}.root`, `max tree depth ${MAX_TREE_DEPTH}`);
        }
        if (leafBlocks.length > MAX_BLOCKS_PER_PAGE) add(`${at}.blocks`, `max ${MAX_BLOCKS_PER_PAGE} blocks per page`);
        // Prop issues use flat `blocks[i]` paths so builders and describeIssue keep working.
        leafBlocks.forEach((block, bi) => {
          checkProps(block, `${at}.blocks[${bi}]`, byId.get(String(block.widget)), !!opts.catalog, add, ids, contractIndex);
        });
      } else if (Array.isArray(page.blocks)) {
        if (page.blocks.length > MAX_BLOCKS_PER_PAGE) add(`${at}.blocks`, `max ${MAX_BLOCKS_PER_PAGE} blocks per page`);
        page.blocks.forEach((block: unknown, bi: number) => {
          if (!isObj(block)) return add(`${at}.blocks[${bi}]`, 'block must be an object');
          checkProps(block, `${at}.blocks[${bi}]`, byId.get(String(block.widget)), !!opts.catalog, add, ids, contractIndex);
        });
        add(`${at}.root`, 'root layout tree is required (run migrate)');
      } else {
        add(`${at}.root`, 'root layout tree is required');
      }
    });
    if (!paths.has('/')) add('pages', 'a home page with path "/" is required');
  }

  if (input.nav != null) checkNav(input.nav, paths, add);

  return issues.length ? { ok: false, issues } : { ok: true, spec: normalize(input, issues) };
}

export { isContainer, blocksFromTree };

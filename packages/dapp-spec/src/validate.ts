import {
  DAPP_SPEC_VERSION,
  NETWORK_IDS,
  THEME_KEYS,
  type CatalogEntry,
  type DappSpec,
  type ValidationIssue,
  type ValidationResult,
} from './types.js';

const MAX_PAGES = 20;
const MAX_BLOCKS_PER_PAGE = 40;
const MAX_STRING = 2000;
const PATH_RE = /^\/([a-z0-9][a-z0-9-]{0,39})?$/;
const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const AMOUNT_RE = /^(0|[1-9]\d*)(\.\d{1,18})?$/;
/** Theme values end up in CSS; keep them to plain tokens (no url(), no expressions, no ; or braces). */
const CSS_VALUE_RE = /^[#(),.%\w\s"'-]{1,120}$/;

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
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
      else if (typeof v !== 'string' || !CSS_VALUE_RE.test(v) || /url\s*\(|expression/i.test(v)) {
        add(`theme.overrides.${k}`, 'theme values must be plain CSS tokens');
      }
    }
  }

  const byId = new Map((opts.catalog ?? []).map((c) => [c.id, c]));
  const pages = input.pages;
  if (!Array.isArray(pages) || pages.length === 0 || pages.length > MAX_PAGES) {
    add('pages', `1 to ${MAX_PAGES} pages required`);
  } else {
    const paths = new Set<string>();
    const blockIds = new Set<string>();
    pages.forEach((page, pi) => {
      const at = `pages[${pi}]`;
      if (!isObj(page)) return add(at, 'page must be an object');
      if (typeof page.path !== 'string' || !PATH_RE.test(page.path)) add(`${at}.path`, 'path must be / or /lowercase-segment');
      else if (paths.has(page.path)) add(`${at}.path`, 'duplicate path');
      else paths.add(page.path);
      if (typeof page.title !== 'string' || !page.title.trim() || page.title.length > 60) add(`${at}.title`, 'title is required (max 60 chars)');
      if (!Array.isArray(page.blocks) || page.blocks.length > MAX_BLOCKS_PER_PAGE) {
        return add(`${at}.blocks`, `blocks must be an array (max ${MAX_BLOCKS_PER_PAGE})`);
      }
      page.blocks.forEach((block: unknown, bi: number) => {
        const bat = `${at}.blocks[${bi}]`;
        if (!isObj(block)) return add(bat, 'block must be an object');
        if (typeof block.id !== 'string' || !ID_RE.test(block.id)) add(`${bat}.id`, 'id must be 1-64 letters, digits, _ or -');
        else if (blockIds.has(block.id)) add(`${bat}.id`, 'duplicate block id');
        else blockIds.add(block.id);
        if (typeof block.widget !== 'string' || !block.widget) return add(`${bat}.widget`, 'widget is required');
        if (!isObj(block.props)) return add(`${bat}.props`, 'props must be an object');
        const entry = byId.get(block.widget);
        if (opts.catalog && !entry) return add(`${bat}.widget`, `unknown widget "${block.widget}"`);
        if (entry && !entry.placeable) return add(`${bat}.widget`, `"${block.widget}" cannot be placed on a page`);
        for (const [name, value] of Object.entries(block.props)) {
          const p = `${bat}.props.${name}`;
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
            if (schema.format === 'url' && !/^https?:\/\/[^\s"'<>]+$/.test(value)) add(p, 'must be an http(s) URL');
          }
        }
        if (entry) {
          for (const s of entry.props) {
            if (s.required && (block.props[s.name] == null || block.props[s.name] === '')) add(`${bat}.props.${s.name}`, 'required');
          }
        }
      });
    });
    if (!paths.has('/')) add('pages', 'a home page with path "/" is required');
  }

  return issues.length ? { ok: false, issues } : { ok: true, spec: input as unknown as DappSpec };
}

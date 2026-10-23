import { blocksFromTree, isContainer, type CatalogEntry, type DappSpec, type LayoutNode, type ValidationIssue } from '@splendor/dapp-spec';

export type DescribedIssue = {
  /** Where, in the words the builder UI uses: "Tip → Tip jar → Recipient". */
  where: string;
  message: string;
  /** Set when the issue belongs to a block or container, so the UI can jump to it. */
  target: { pagePath: string; blockId: string; prop: string | null; entryId: string | null } | null;
};

const BLOCK_PATH_RE = /^pages\[(\d+)\](?:\.blocks\[(\d+)\](?:\.props\.([\w-]+)|\.style\.(\w+)|\.(\w+))?|\.(\w+))?$/;
const ROOT_PATH_RE = /^pages\[(\d+)\]\.root((?:\.children\[\d+\])*)(?:\.block)?(?:\.(style)\.(\w+)|\.(\w+))?$/;
const CONTRACT_PATH_RE = /^contracts\[(\d+)\](?:\.functions\[(\d+)\])?(?:\.(\w+))?$/;

const MESSAGES: Record<string, string> = {
  required: 'needs a value',
  'must be an SQ: address': 'must be an SQ: address (SQ: followed by 64 hex characters)',
};

const KIND_NAMES: Record<string, string> = { section: 'Section', row: 'Row', column: 'Column', card: 'Card', stack: 'Stack' };

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** Follow `.children[i]` steps from a page root. */
function nodeAt(root: LayoutNode | undefined, steps: string): LayoutNode | undefined {
  let node = root;
  for (const m of steps.matchAll(/\[(\d+)\]/g)) {
    if (!node || !isContainer(node)) return undefined;
    node = node.children[Number(m[1])];
  }
  return node;
}

export type SetupItem =
  | { kind: 'contracts'; key: string; message: string }
  | { kind: 'component'; key: string; message: string; target: NonNullable<DescribedIssue['target']> };

const isBlank = (v: unknown) => v === undefined || v === null || (typeof v === 'string' && v.trim() === '');

/**
 * Data the dApp still needs before it works, in plain words: a contract or token with no address, and
 * components that need a contract or token but have none picked (and no address typed in either).
 */
export function setupItems(spec: DappSpec, catalog: readonly CatalogEntry[]): SetupItem[] {
  const items: SetupItem[] = [];
  const contracts = spec.contracts ?? [];
  contracts.forEach((c, i) => {
    if (!isBlank(c.address)) return;
    items.push({ kind: 'contracts', key: `contract:${c.id}:${i}`, message: c.kind === 'token' ? 'Add your token address' : `Add an address for ${c.name.trim() || `contract ${i + 1}`}` });
  });
  for (const page of spec.pages) {
    for (const block of page.blocks ?? blocksFromTree(page.root)) {
      const entry = catalog.find((e) => e.id === block.widget);
      if (!entry) continue;
      for (const p of entry.props) {
        if (p.format !== 'contract' || !isBlank(block.props[p.name])) continue;
        const addressProp = entry.props.find((a) => a.name === (p.addressProp ?? 'contract'));
        if (!addressProp || !isBlank(block.props[addressProp.name])) continue;
        if (!p.required && !addressProp.required) continue;
        const what = p.contractKind === 'token' ? 'a token' : 'a contract';
        items.push({
          kind: 'component',
          key: `block:${block.id}:${p.name}`,
          message: `${entry.title} needs ${what}.`,
          target: { pagePath: page.path, blockId: block.id, prop: p.name, entryId: entry.id },
        });
      }
    }
  }
  return items;
}

/** Turn a validator issue path into names the user sees in the builder. */
export function describeIssue(spec: DappSpec, catalog: readonly CatalogEntry[], issue: ValidationIssue): DescribedIssue {
  const message = MESSAGES[issue.message] ?? issue.message;
  const pageName = (i: number) => spec.pages[i]?.title?.trim() || `Page ${i + 1}`;
  const entryOf = (widget: string) => catalog.find((e) => e.id === widget);

  const b = BLOCK_PATH_RE.exec(issue.path);
  if (b) {
    const pi = Number(b[1]);
    const page = spec.pages[pi];
    if (b[2] == null) return { where: b[6] ? `${pageName(pi)} → ${b[6]}` : pageName(pi), message, target: null };
    const block = page ? (page.blocks ?? blocksFromTree(page.root))[Number(b[2])] : undefined;
    const entry = block ? entryOf(block.widget) : undefined;
    const blockName = entry?.title ?? block?.widget ?? `Block ${Number(b[2]) + 1}`;
    const prop = b[3] ?? null;
    const detail = prop ? (entry?.props.find((p) => p.name === prop)?.label ?? prop) : b[4] ? `Style → ${b[4]}` : b[5];
    return {
      where: [pageName(pi), blockName, detail].filter(Boolean).join(' → '),
      message,
      target: page && block ? { pagePath: page.path, blockId: block.id, prop, entryId: entry?.id ?? null } : null,
    };
  }

  const r = ROOT_PATH_RE.exec(issue.path);
  if (r) {
    const pi = Number(r[1]);
    const page = spec.pages[pi];
    const node = nodeAt(page?.root, r[2] ?? '');
    const name = !node
      ? 'Layout'
      : node.kind === 'block'
        ? (entryOf(node.block.widget)?.title ?? node.block.widget)
        : (KIND_NAMES[node.kind] ?? cap(node.kind));
    const detail = r[3] ? `Style → ${r[4]}` : r[5];
    const id = node ? (node.kind === 'block' ? node.block.id : node.id) : null;
    return {
      where: [pageName(pi), name, detail].filter(Boolean).join(' → '),
      message,
      target: page && id ? { pagePath: page.path, blockId: id, prop: null, entryId: node?.kind === 'block' ? (entryOf(node.block.widget)?.id ?? null) : null } : null,
    };
  }

  const c = CONTRACT_PATH_RE.exec(issue.path);
  if (c) {
    const contract = spec.contracts?.[Number(c[1])];
    const fn = c[2] != null ? contract?.functions?.[Number(c[2])] : undefined;
    const parts = ['Contracts', contract?.name || `Contract ${Number(c[1]) + 1}`, c[2] != null ? (fn?.name || `Function ${Number(c[2]) + 1}`) : null, c[3] ?? null];
    return { where: parts.filter(Boolean).join(' → '), message, target: null };
  }

  if (issue.path.startsWith('nav')) {
    const rest = issue.path.slice(3).replace(/^\./, '');
    return { where: `Navigation${rest ? ` → ${rest}` : ''}`, message, target: null };
  }
  const where = issue.path.startsWith('theme') ? `Theme${issue.path.split('.').length > 2 ? ` → ${issue.path.split('.').pop()}` : ''}` : issue.path || 'dApp';
  return { where: cap(where), message, target: null };
}

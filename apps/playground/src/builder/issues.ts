import type { CatalogEntry, DappSpec, ValidationIssue } from '@splendor/dapp-spec';

export type DescribedIssue = {
  /** Where, in the words the builder UI uses: "Tip → Tip jar → Recipient". */
  where: string;
  message: string;
  /** Set when the issue belongs to a block, so the UI can jump to it. */
  target: { pagePath: string; blockId: string; prop: string | null; entryId: string | null } | null;
};

const PATH_RE = /^pages\[(\d+)\](?:\.blocks\[(\d+)\](?:\.props\.([\w-]+)|\.(\w+))?|\.(\w+))?$/;

const MESSAGES: Record<string, string> = {
  required: 'needs a value',
  'must be an SQ: address': 'must be an SQ: address (SQ: followed by 64 hex characters)',
};

/** Turn a validator issue path into names the user sees in the builder. */
export function describeIssue(spec: DappSpec, catalog: readonly CatalogEntry[], issue: ValidationIssue): DescribedIssue {
  const message = MESSAGES[issue.message] ?? issue.message;
  const m = PATH_RE.exec(issue.path);
  if (!m) {
    const where = issue.path.startsWith('theme') ? `Theme${issue.path.split('.').length > 2 ? ` → ${issue.path.split('.').pop()}` : ''}` : issue.path || 'dApp';
    return { where: where.charAt(0).toUpperCase() + where.slice(1), message, target: null };
  }
  const page = spec.pages[Number(m[1])];
  const pageName = page?.title?.trim() || `Page ${Number(m[1]) + 1}`;
  if (m[2] == null) return { where: m[5] ? `${pageName} → ${m[5]}` : pageName, message, target: null };
  const block = page?.blocks[Number(m[2])];
  const entry = block ? catalog.find((e) => e.id === block.widget) : undefined;
  const blockName = entry?.title ?? block?.widget ?? `Block ${Number(m[2]) + 1}`;
  const prop = m[3] ?? null;
  const propLabel = prop ? (entry?.props.find((p) => p.name === prop)?.label ?? prop) : m[4];
  return {
    where: [pageName, blockName, propLabel].filter(Boolean).join(' → '),
    message,
    target: page && block ? { pagePath: page.path, blockId: block.id, prop, entryId: entry?.id ?? null } : null,
  };
}

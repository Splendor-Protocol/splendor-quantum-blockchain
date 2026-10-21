import type { ContainerKind, ContainerNode, DappBlock, LayoutNode, NodeStyle } from './types.js';

export const CONTAINER_KINDS: readonly ContainerKind[] = ['section', 'row', 'column', 'card', 'stack'];

export function isContainer(node: LayoutNode): node is ContainerNode {
  return node.kind !== 'block';
}

export function blocksFromTree(root: LayoutNode): DappBlock[] {
  const out: DappBlock[] = [];
  walk(root, (n) => {
    if (n.kind === 'block') out.push(n.block);
  });
  return out;
}

export function walk(node: LayoutNode, visit: (n: LayoutNode, path: string[]) => void, path: string[] = []): void {
  visit(node, path);
  if (isContainer(node)) {
    node.children.forEach((c, i) => walk(c, visit, [...path, String(i)]));
  }
}

export function findNode(root: LayoutNode, id: string): LayoutNode | null {
  let found: LayoutNode | null = null;
  walk(root, (n) => {
    if (found) return;
    if (n.kind === 'block' ? n.block.id === id : n.id === id) found = n;
  });
  return found;
}

export function nodeId(node: LayoutNode): string {
  return node.kind === 'block' ? node.block.id : node.id;
}

export function countNodes(root: LayoutNode): number {
  let n = 0;
  walk(root, () => {
    n += 1;
  });
  return n;
}

export function treeDepth(root: LayoutNode): number {
  if (!isContainer(root) || root.children.length === 0) return 1;
  return 1 + Math.max(...root.children.map(treeDepth));
}

/** Wrap a flat block list into a section > stack tree (v1 → v2 migrate). */
export function wrapBlocks(blocks: DappBlock[], sectionId = 'root'): ContainerNode {
  return {
    kind: 'section',
    id: sectionId,
    style: { maxWidth: '1120px', padding: '1.25rem', gap: '1rem' },
    children: [
      {
        kind: 'stack',
        id: `${sectionId}-stack`,
        style: { gap: '1rem' },
        children: blocks.map((block) => ({ kind: 'block' as const, block })),
      },
    ],
  };
}

export function emptySection(id = 'root'): ContainerNode {
  return {
    kind: 'section',
    id,
    style: { maxWidth: '1120px', padding: '1.25rem', gap: '1rem' },
    children: [],
  };
}

export function mapTree(root: LayoutNode, fn: (n: LayoutNode) => LayoutNode): LayoutNode {
  const next = fn(root);
  if (!isContainer(next)) return next;
  return { ...next, children: next.children.map((c) => mapTree(c, fn)) };
}

/** Insert `node` into the container with `parentId` at `index`. Returns null if parent missing. */
export function insertChild(root: LayoutNode, parentId: string, index: number, node: LayoutNode): LayoutNode | null {
  let ok = false;
  const next = mapTree(root, (n) => {
    if (!isContainer(n) || n.id !== parentId) return n;
    ok = true;
    const children = [...n.children];
    const i = Math.max(0, Math.min(index, children.length));
    children.splice(i, 0, node);
    return { ...n, children };
  });
  return ok ? next : null;
}

/** Remove the node with `id` (block id or container id). Never removes the outermost root — empties it instead. */
export function removeNode(root: LayoutNode, id: string): LayoutNode {
  if (nodeId(root) === id) return isContainer(root) ? { ...root, children: [] } : emptySection('root');
  if (!isContainer(root)) return root;
  return {
    ...root,
    children: root.children.filter((c) => nodeId(c) !== id).map((c) => removeNode(c, id)),
  };
}

/** Reorder a direct child within its parent. */
export function moveChild(root: LayoutNode, parentId: string, from: number, to: number): LayoutNode | null {
  let ok = false;
  const next = mapTree(root, (n) => {
    if (!isContainer(n) || n.id !== parentId) return n;
    if (from < 0 || from >= n.children.length) return n;
    ok = true;
    const children = [...n.children];
    const [item] = children.splice(from, 1);
    const dest = Math.max(0, Math.min(to, children.length));
    children.splice(dest, 0, item!);
    return { ...n, children };
  });
  return ok ? next : null;
}

export function updateNodeStyle(root: LayoutNode, id: string, style: NodeStyle): LayoutNode {
  return mapTree(root, (n) => {
    if (n.kind === 'block' && n.block.id === id) return { ...n, block: { ...n.block, style: { ...n.block.style, ...style } } };
    if (isContainer(n) && n.id === id) return { ...n, style: { ...n.style, ...style } };
    return n;
  });
}

export function updateBlockProps(root: LayoutNode, id: string, props: DappBlock['props']): LayoutNode {
  return mapTree(root, (n) => {
    if (n.kind === 'block' && n.block.id === id) return { ...n, block: { ...n.block, props } };
    return n;
  });
}

export function styleToCss(style?: NodeStyle): Record<string, string> {
  if (!style) return {};
  const out: Record<string, string> = {};
  if (style.width) out.width = style.width;
  if (style.maxWidth) out.maxWidth = style.maxWidth;
  if (style.gap) out.gap = style.gap;
  if (style.padding) out.padding = style.padding;
  if (style.background) out.background = style.background;
  if (style.align === 'center') out.alignItems = 'center';
  else if (style.align === 'end') out.alignItems = 'flex-end';
  else if (style.align === 'stretch') out.alignItems = 'stretch';
  else if (style.align === 'between') out.justifyContent = 'space-between';
  else if (style.align === 'start') out.alignItems = 'flex-start';
  return out;
}

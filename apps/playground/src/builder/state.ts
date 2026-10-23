import {
  blocksFromTree,
  countNodes,
  findNode,
  getPageTemplate,
  insertChild,
  isContainer,
  mapTree,
  moveChild,
  nodeId,
  removeNode,
  slugify,
  treeDepth,
  updateNodeStyle,
  walk,
  type DappBlock,
  type ContainerNode,
  type DappPage,
  type DappSpec,
  type LayoutNode,
  type NetworkId,
  type NodeStyle,
  type PageAccess,
  type PropValue,
  type ThemeName,
  type ThemeOverrides,
} from '@splendor/dapp-spec';

export type BuilderState = {
  spec: DappSpec;
  pagePath: string;
  selectedBlockId: string | null;
  past: DappSpec[];
  future: DappSpec[];
  /** Edits with the same key within COALESCE_MS share one undo step (typing in a field). */
  lastKey: string | null;
  lastAt: number;
};

export type BuilderAction =
  | { type: 'load'; spec: DappSpec }
  | { type: 'setName'; name: string }
  | { type: 'setDescription'; description: string }
  | { type: 'setNetwork'; network: NetworkId }
  | { type: 'setTheme'; theme: { base: ThemeName; overrides: ThemeOverrides; preset?: string } }
  | { type: 'addPage'; title: string }
  | { type: 'addPageFromTemplate'; title: string; templateId: string }
  | { type: 'removePage'; path: string }
  /** Copy a page (layout tree with fresh ids) to a new path, titled "<title> copy", and select it. */
  | { type: 'duplicatePage'; path: string }
  /** Make `path` the home page (`/`). The old home moves to a path derived from its title; links follow. */
  | { type: 'setHomePage'; path: string }
  | { type: 'renamePage'; path: string; title: string }
  | { type: 'selectPage'; path: string }
  | { type: 'addBlock'; widget: string; props: Record<string, PropValue> }
  | { type: 'insertBlock'; widget: string; props: Record<string, PropValue>; index: number }
  | { type: 'reorderBlock'; id: string; index: number }
  | { type: 'removeBlock'; id: string }
  | { type: 'duplicateBlock'; id: string }
  | { type: 'moveBlock'; id: string; delta: -1 | 1 }
  /** Tree edits (in-canvas drag/drop). `index` is the insertion slot among the parent's children. */
  | { type: 'insertIntoTree'; parentId: string; index: number; widget: string; props: Record<string, PropValue> }
  | { type: 'moveNode'; id: string; parentId: string; index: number }
  | { type: 'duplicateNode'; id: string }
  | { type: 'removeNodeFromTree'; id: string }
  | { type: 'movePage'; path: string; delta: -1 | 1 }
  /** Who may see a page. `public` clears the field. The home page (`/`) is always public (ignored). */
  | { type: 'setPageAccess'; path: string; access: PageAccess }
  /** Show or hide a page in the dApp's navigation (keeps `spec.nav.links` as the ordered list of shown pages). */
  | { type: 'setNavVisible'; path: string; visible: boolean }
  | { type: 'setProp'; id: string; name: string; value: PropValue | undefined }
  | { type: 'setBlockStyle'; id: string; style: NodeStyle }
  | { type: 'setContracts'; contracts: NonNullable<DappSpec['contracts']> }
  /**
   * Change one contract's address. Components bound to it follow automatically; raw props that still
   * hold the old address (widgets such as the token panel take an address directly) are updated too.
   */
  | { type: 'setContractAddress'; id: string; address: string }
  | { type: 'selectBlock'; id: string | null }
  | { type: 'undo' }
  | { type: 'redo' };

const HISTORY_LIMIT = 100;
/** Same cap as the spec validator. */
export const MAX_BLOCKS_PER_PAGE = 40;
export const MAX_PAGES = 20;
/** Same caps as the spec validator. */
const MAX_NODES_PER_PAGE = 200;
const MAX_TREE_DEPTH = 6;
const COALESCE_MS = 1000;

const VALID_ID = /^[A-Za-z0-9_-]{1,64}$/;

/**
 * Rewrite ids the validator would reject (e.g. `/dashboard-hero` from older page templates) and
 * de-duplicate them, so drafts saved before a fix still open with a working preview.
 */
function repairIds(pages: DappPage[]): DappPage[] {
  const taken = new Set<string>();
  const fix = (id: string, fallback: string): string => {
    let base = VALID_ID.test(id) ? id : id.replace(/[^A-Za-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 56) || fallback;
    if (!taken.has(base)) {
      taken.add(base);
      return base;
    }
    base = base.slice(0, 56);
    return freshId(base, taken);
  };
  return pages.map((p) => {
    if (!p.root || !isContainer(p.root)) {
      return { ...p, blocks: pageBlocks(p).map((b) => ({ ...b, id: fix(b.id, b.widget) })) };
    }
    const root = mapTree(p.root, (n) =>
      n.kind === 'block' ? { ...n, block: { ...n.block, id: fix(n.block.id, n.block.widget) } } : { ...n, id: fix(n.id, n.kind) },
    );
    return { ...p, root };
  });
}

export function initBuilder(spec: DappSpec): BuilderState {
  const synced: DappSpec = {
    ...spec,
    version: 2,
    // The layout tree is the source of truth when present (nested containers survive); the flat list follows it.
    pages: repairIds(spec.pages).map((p) => (p.root && isContainer(p.root) ? withTree(p, p.root) : withBlocks(p, pageBlocks(p)))),
    contracts: spec.contracts ?? [],
    nav: spec.nav ?? { showConnect: true, brand: spec.name },
  };
  return { spec: synced, pagePath: synced.pages[0]?.path ?? '/', selectedBlockId: null, past: [], future: [], lastKey: null, lastAt: 0 };
}

function uniqueBlockId(spec: DappSpec, widget: string): string {
  const taken = new Set([...allIds(spec), ...spec.pages.flatMap((p) => pageBlocks(p).map((b) => b.id))]);
  const base = widget.replace(/[^A-Za-z0-9_-]/g, '-').slice(0, 50) || 'block';
  for (let n = 1; ; n++) {
    const id = `${base}-${n}`;
    if (!taken.has(id)) return id;
  }
}

function uniquePath(spec: DappSpec, title: string): string {
  const taken = new Set(spec.pages.map((p) => p.path));
  const base = slugify(title).slice(0, 34).replace(/-+$/, '') || 'page';
  const clean = /^[a-z0-9]/.test(base) ? base : `p-${base}`;
  for (let n = 1; ; n++) {
    const path = `/${n === 1 ? clean : `${clean}-${n}`}`;
    if (!taken.has(path)) return path;
  }
}

function pageBlocks(page: DappPage): DappBlock[] {
  if (Array.isArray(page.blocks)) return page.blocks;
  if (page.root) return blocksFromTree(page.root);
  return [];
}

/** Id-safe segment for a page path (`/` → `home`). */
function pathSegment(path: string): string {
  return path === '/' ? 'home' : path.replace(/^\//, '').replace(/[^a-z0-9-]+/gi, '-') || 'page';
}

function pageRootId(page: DappPage): string {
  if (page.root && page.root.kind !== 'block') return page.root.id;
  return `${pathSegment(page.path)}-root`;
}

/** Rename only the ids in `root` that are already taken elsewhere in the spec. */
function uniqueIds(root: ContainerNode, taken: Set<string>): ContainerNode {
  const fix = (id: string) => {
    if (!taken.has(id)) {
      taken.add(id);
      return id;
    }
    return freshId(id.replace(/-\d+$/, '').slice(0, 56), taken);
  };
  return mapTree(root, (n) => (n.kind === 'block' ? { ...n, block: { ...n.block, id: fix(n.block.id) } } : { ...n, id: fix(n.id) })) as ContainerNode;
}

/** Keep flat block edits and layout tree in sync (mirrors dapp-spec wrapBlocks). */
function wrapBlocksLocal(blocks: DappBlock[], sectionId: string): ContainerNode {
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

/** Replace the layout tree; the flat block list is always derived from it. */
function withTree(page: DappPage, root: LayoutNode): DappPage {
  return { ...page, root, blocks: blocksFromTree(root) };
}

/** Position of `id` among its parent's children. */
function locate(root: LayoutNode, id: string): { parent: ContainerNode; index: number } | null {
  let hit: { parent: ContainerNode; index: number } | null = null;
  walk(root, (n) => {
    if (!isContainer(n) || hit) return;
    const index = n.children.findIndex((c) => nodeId(c) === id);
    if (index >= 0) hit = { parent: n, index };
  });
  return hit;
}

/** If every block sits directly under one container, that container (flat pages). */
function sharedBlockParent(root: LayoutNode): ContainerNode | null {
  let parent: ContainerNode | null = null;
  let mixed = false;
  walk(root, (n) => {
    if (!isContainer(n)) return;
    if (n.children.some((c) => c.kind === 'block')) {
      if (parent) mixed = true;
      parent = n;
    }
  });
  return mixed ? null : parent;
}

/**
 * Apply a flat block-list edit (add / remove / reorder / props) to an existing layout tree without
 * flattening it: removed blocks leave, changed blocks update in place, new blocks land next to the
 * block that precedes (or follows) them in the list.
 */
function reconcileTree(root: ContainerNode, blocks: DappBlock[]): ContainerNode {
  const next = new Map(blocks.map((b) => [b.id, b]));
  const prune = (n: LayoutNode): LayoutNode | null => {
    if (n.kind === 'block') {
      const b = next.get(n.block.id);
      return b ? { kind: 'block', block: b } : null;
    }
    return { ...n, children: n.children.map(prune).filter((c): c is LayoutNode => c !== null) };
  };
  let tree = prune(root) as ContainerNode;
  const present = new Set(blocksFromTree(tree).map((b) => b.id));

  blocks.forEach((block, i) => {
    if (present.has(block.id)) return;
    const node: LayoutNode = { kind: 'block', block };
    let placed: LayoutNode | null = null;
    for (let j = i - 1; j >= 0 && !placed; j--) {
      const at = present.has(blocks[j]!.id) ? locate(tree, blocks[j]!.id) : null;
      if (at) placed = insertChild(tree, at.parent.id, at.index + 1, node);
    }
    for (let j = i + 1; j < blocks.length && !placed; j++) {
      const at = present.has(blocks[j]!.id) ? locate(tree, blocks[j]!.id) : null;
      if (at) placed = insertChild(tree, at.parent.id, at.index, node);
    }
    if (!placed) placed = insertChild(tree, tree.id, tree.children.length, node);
    tree = (placed ?? tree) as ContainerNode;
    present.add(block.id);
  });

  // Flat pages: the list order is the order of the blocks under their shared parent.
  const parent = sharedBlockParent(tree);
  if (parent) {
    const rank = new Map(blocks.map((b, i) => [b.id, i]));
    const sorted = parent.children
      .filter((c): c is Extract<LayoutNode, { kind: 'block' }> => c.kind === 'block')
      .sort((a, b) => (rank.get(a.block.id) ?? 0) - (rank.get(b.block.id) ?? 0));
    let k = 0;
    const children = parent.children.map((c) => (c.kind === 'block' ? sorted[k++]! : c));
    tree = mapTree(tree, (n) => (isContainer(n) && n.id === parent.id ? { ...n, children } : n)) as ContainerNode;
  }
  return tree;
}

function withBlocks(page: DappPage, blocks: DappBlock[]): DappPage {
  if (page.root && isContainer(page.root)) return withTree(page, reconcileTree(page.root, blocks));
  return { ...page, blocks, root: wrapBlocksLocal(blocks, pageRootId(page)) };
}

function allIds(spec: DappSpec): Set<string> {
  const ids = new Set<string>();
  for (const p of spec.pages) if (p.root) walk(p.root, (n) => ids.add(nodeId(n)));
  return ids;
}

function freshId(base: string, taken: Set<string>): string {
  for (let n = 1; ; n++) {
    const id = `${base}-${n}`;
    if (!taken.has(id)) {
      taken.add(id);
      return id;
    }
  }
}

/** Palette ids that insert a real layout container (not a leaf shell block). */
const CONTAINER_WIDGETS: Record<string, ContainerNode['kind']> = {
  section: 'section',
  row: 'row',
  card: 'card',
  stack: 'stack',
};

/** Build a layout node for a palette drop: containers become empty ContainerNodes. */
function layoutNodeForWidget(id: string, widget: string, props: Record<string, PropValue>): LayoutNode {
  if (widget === 'columns') {
    const n = Math.max(2, Math.min(4, Number(props.cols) || 3));
    return {
      kind: 'row',
      id,
      style: { gap: '1rem', align: 'stretch' },
      children: Array.from({ length: n }, (_, i) => ({
        kind: 'column' as const,
        id: `${id}-col-${i + 1}`,
        style: { gap: '0.75rem', padding: '0.25rem', width: `${Math.floor(100 / n)}%` },
        children: [],
      })),
    };
  }
  const kind = CONTAINER_WIDGETS[widget];
  if (kind) {
    return {
      kind,
      id,
      style:
        kind === 'section'
          ? { maxWidth: '1120px', padding: '1.25rem', gap: '1rem' }
          : kind === 'row'
            ? { gap: '1rem', align: 'stretch' }
            : kind === 'card'
              ? { padding: '1rem', gap: '0.75rem', background: 'var(--sqc-surface)' }
              : { gap: '0.75rem' },
      children: [],
    };
  }
  return { kind: 'block', block: { id, widget, props } };
}

/** Deep copy with new ids for every container and block (so ids stay unique across the spec). */
function cloneNode(node: LayoutNode, taken: Set<string>): LayoutNode {
  if (node.kind === 'block') {
    const base = node.block.widget.replace(/[^A-Za-z0-9_-]/g, '-').slice(0, 50) || 'block';
    return { kind: 'block', block: { ...node.block, id: freshId(base, taken), props: { ...node.block.props } } };
  }
  return { ...node, id: freshId(node.kind, taken), children: node.children.map((c) => cloneNode(c, taken)) };
}

/**
 * Move `id` so it becomes child number `index` of `parentId` (the index is a slot in the parent's
 * children as they are *before* the move). Null when it would be a no-op or invalid.
 */
function relocate(root: LayoutNode, id: string, parentId: string, index: number): LayoutNode | null {
  if (!isContainer(root) || nodeId(root) === id) return null;
  const node = findNode(root, id);
  const at = locate(root, id);
  const parent = findNode(root, parentId);
  if (!node || !at || !parent || !isContainer(parent)) return null;
  if (isContainer(node) && findNode(node, parentId)) return null; // not into itself
  const slot = Math.max(0, Math.min(index, parent.children.length));
  let next: LayoutNode | null;
  if (at.parent.id === parentId) {
    const to = slot > at.index ? slot - 1 : slot;
    if (to === at.index) return null;
    next = moveChild(root, parentId, at.index, to);
  } else {
    next = insertChild(removeNode(root, id), parentId, slot, node);
  }
  if (!next || treeDepth(next) > MAX_TREE_DEPTH) return null;
  return next;
}

/** Props that hold a page path or URL (buttons, CTAs). Mirrors the keys the code generator reads. */
const LINK_PROPS = ['href', 'ctaPath', 'buttonPath', 'link'] as const;

/** New value for one link (`undefined` = untouched). A target mapped to '' was deleted: the link is cleared. */
function remapLink(value: string, map: Map<string, string>): string | undefined {
  const hash = value.startsWith('#');
  const raw = (hash ? value.slice(1) : value).trim();
  if (!raw) return undefined;
  const next = map.get(raw.startsWith('/') ? raw : `/${raw}`);
  if (next === undefined) return undefined;
  return next === '' ? '' : hash ? `#${next}` : next;
}

/**
 * Rewrite page-path links (`/about` -> `/` etc.) in button-like props, navbar `links` lists and
 * JSON page actions. External URLs and contract actions are left alone.
 */
function remapLinks(root: LayoutNode, map: Map<string, string>): LayoutNode {
  return mapTree(root, (n) => {
    if (n.kind !== 'block') return n;
    let props = n.block.props;
    const set = (key: string, value: string) => {
      props = { ...props, [key]: value };
    };
    for (const key of LINK_PROPS) {
      const v = props[key];
      if (typeof v !== 'string' || /^[a-z]+:/i.test(v)) continue;
      const next = remapLink(v, map);
      if (next !== undefined) set(key, next);
    }
    const action = props.action;
    if (typeof action === 'string' && action.trim().startsWith('{')) {
      try {
        const a = JSON.parse(action) as { type?: unknown; path?: unknown };
        if (a.type === 'page' && typeof a.path === 'string') {
          const next = remapLink(a.path, map);
          if (next !== undefined) set('action', next === '' ? '' : JSON.stringify({ ...a, path: next }));
        }
      } catch {
        /* not JSON: validation reports it */
      }
    }
    const links = props.links;
    if (typeof links === 'string' && links.includes('|')) {
      const items = links.split(',').flatMap((item) => {
        const [label, ...rest] = item.split('|');
        const target = rest.join('|').trim();
        const next = target && !/^[a-z]+:/i.test(target) ? remapLink(target, map) : undefined;
        if (next === undefined) return [item];
        return next === '' ? [] : [`${label}|${next}`];
      });
      const joined = items.join(',');
      if (joined !== links) set('links', joined);
    }
    return props === n.block.props ? n : { kind: 'block', block: { ...n.block, props } };
  });
}

/** With an explicit nav order, a new page joins it (right after `after` when that page is listed). */
function navWithPage(nav: DappSpec['nav'], path: string, after?: string): DappSpec['nav'] {
  if (!nav?.links) return nav;
  const at = after ? nav.links.indexOf(after) : -1;
  const links = at >= 0 ? [...nav.links.slice(0, at + 1), path, ...nav.links.slice(at + 1)] : [...nav.links, path];
  return { ...nav, links };
}

function pageHolding(spec: DappSpec, id: string): DappPage | undefined {
  return spec.pages.find((p) => p.root && findNode(p.root, id));
}

function replacePage(spec: DappSpec, path: string, page: DappPage): DappSpec {
  return { ...spec, pages: spec.pages.map((p) => (p.path === path ? page : p)) };
}

function mapPage(spec: DappSpec, path: string, fn: (blocks: DappBlock[]) => DappBlock[]): DappSpec {
  return { ...spec, pages: spec.pages.map((p) => (p.path === path ? withBlocks(p, fn(pageBlocks(p))) : p)) };
}

function mapBlock(spec: DappSpec, id: string, fn: (b: DappBlock) => DappBlock): DappSpec {
  return {
    ...spec,
    pages: spec.pages.map((p) => {
      let hit = false;
      const next = pageBlocks(p).map((b) => {
        if (b.id !== id) return b;
        hit = true;
        return fn(b);
      });
      return hit ? withBlocks(p, next) : p;
    }),
  };
}

/** Apply an edit, recording undo history. `key` groups rapid edits of the same field. */
function commit(state: BuilderState, spec: DappSpec, key: string | null, extra: Partial<BuilderState> = {}): BuilderState {
  const now = Date.now();
  const coalesce = key !== null && key === state.lastKey && now - state.lastAt < COALESCE_MS;
  return {
    ...state,
    ...extra,
    spec,
    past: coalesce ? state.past : [...state.past, state.spec].slice(-HISTORY_LIMIT),
    future: [],
    lastKey: key,
    lastAt: now,
  };
}

/** Keep the selection and current page pointing at things that still exist. */
function reconcile(state: BuilderState): BuilderState {
  const page = state.spec.pages.find((p) => p.path === state.pagePath) ?? state.spec.pages[0];
  const pagePath = page?.path ?? '/';
  // The selection may be a block or a container of the current page.
  const selectedBlockId =
    page && state.selectedBlockId && (pageBlocks(page).some((b) => b.id === state.selectedBlockId) || (page.root && findNode(page.root, state.selectedBlockId)))
      ? state.selectedBlockId
      : null;
  return pagePath === state.pagePath && selectedBlockId === state.selectedBlockId ? state : { ...state, pagePath, selectedBlockId };
}

export function builderReducer(state: BuilderState, action: BuilderAction): BuilderState {
  const s = state.spec;
  switch (action.type) {
    case 'load':
      return initBuilder(action.spec);
    case 'setName':
      return commit(state, { ...s, name: action.name }, 'name');
    case 'setDescription':
      return commit(state, { ...s, description: action.description }, 'description');
    case 'setNetwork':
      return commit(state, { ...s, network: action.network }, null);
    case 'setTheme':
      return commit(state, { ...s, theme: action.theme }, 'theme');
    case 'addPage': {
      if (s.pages.length >= MAX_PAGES) return state;
      const title = action.title.slice(0, 60) || 'Page';
      const path = uniquePath(s, title);
      const root = uniqueIds(wrapBlocksLocal([], `${pathSegment(path)}-root`), allIds(s));
      const nav = navWithPage(s.nav, path);
      return commit(state, { ...s, pages: [...s.pages, withTree({ path, title, root }, root)], ...(nav ? { nav } : {}) }, null, {
        pagePath: path,
        selectedBlockId: null,
      });
    }
    case 'addPageFromTemplate': {
      const tpl = getPageTemplate(action.templateId);
      if (!tpl || s.pages.length >= MAX_PAGES) return state;
      const title = action.title.slice(0, 60) || tpl.title;
      const path = uniquePath(s, title);
      const built = tpl.build(path, title);
      if (!isContainer(built.root)) return state;
      const root = uniqueIds(built.root, allIds(s));
      const nav = navWithPage(s.nav, path);
      return commit(state, { ...s, pages: [...s.pages, withTree({ ...built, root }, root)], ...(nav ? { nav } : {}) }, null, { pagePath: path, selectedBlockId: null });
    }
    case 'removePage': {
      if (action.path === '/' || !s.pages.some((p) => p.path === action.path)) return state;
      // Links to the removed page would point nowhere: clear them, and drop it from the nav order.
      const gone = new Map([[action.path, '']]);
      const pages = s.pages.filter((p) => p.path !== action.path).map((p) => withTree(p, remapLinks(p.root, gone)));
      const nav = s.nav?.links ? { ...s.nav, links: s.nav.links.filter((l) => l !== action.path) } : s.nav;
      return reconcile(commit(state, { ...s, pages, ...(nav ? { nav } : {}) }, null));
    }
    case 'duplicatePage': {
      const src = s.pages.find((p) => p.path === action.path);
      if (!src || s.pages.length >= MAX_PAGES) return state;
      const title = `${src.title} copy`.slice(0, 60);
      const path = uniquePath(s, title);
      const root = cloneNode(src.root, allIds(s)) as ContainerNode;
      const copy = withTree({ path, title, root }, root);
      const at = s.pages.indexOf(src);
      const pages = [...s.pages.slice(0, at + 1), copy, ...s.pages.slice(at + 1)];
      const nav = navWithPage(s.nav, path, src.path);
      return commit(state, { ...s, pages, ...(nav ? { nav } : {}) }, null, { pagePath: path, selectedBlockId: null });
    }
    case 'setHomePage': {
      if (action.path === '/') return state;
      const target = s.pages.find((p) => p.path === action.path);
      const oldHome = s.pages.find((p) => p.path === '/');
      if (!target || !oldHome) return state;
      const movedTo = uniquePath(s, oldHome.title);
      const remap = new Map([
        [target.path, '/'],
        ['/', movedTo],
      ]);
      const pages = [
        withTree({ ...target, path: '/' }, remapLinks(target.root, remap)),
        ...s.pages.filter((p) => p !== target).map((p) => {
          const root = remapLinks(p.root, remap);
          return withTree(p.path === '/' ? { ...p, path: movedTo } : p, root);
        }),
      ];
      const links = s.nav?.links?.map((l) => remap.get(l) ?? l);
      const nav = s.nav ? { ...s.nav, ...(links ? { links } : {}) } : s.nav;
      return commit(state, { ...s, pages, ...(nav ? { nav } : {}) }, null, { pagePath: '/', selectedBlockId: null });
    }
    case 'renamePage':
      return commit(state, { ...s, pages: s.pages.map((p) => (p.path === action.path ? { ...p, title: action.title } : p)) }, `page:${action.path}`);
    case 'selectPage':
      return reconcile({ ...state, pagePath: action.path, selectedBlockId: null });
    case 'addBlock': {
      const page = s.pages.find((p) => p.path === state.pagePath);
      if (!page || !isContainer(page.root) || pageBlocks(page).length >= MAX_BLOCKS_PER_PAGE) return state;
      const id = uniqueBlockId(s, action.widget);
      const node = layoutNodeForWidget(id, action.widget, action.props);
      // Append into the deepest shared stack / page root so containers nest correctly.
      const parent = sharedBlockParent(page.root) ?? (isContainer(page.root) ? page.root : null);
      if (!parent) return state;
      const root = insertChild(page.root, parent.id, parent.children.length, node);
      if (!root || treeDepth(root) > MAX_TREE_DEPTH || countNodes(root) > MAX_NODES_PER_PAGE) return state;
      return commit(state, replacePage(s, page.path, withTree(page, root)), null, { selectedBlockId: id });
    }
    case 'insertBlock': {
      const page = s.pages.find((p) => p.path === state.pagePath);
      if (!page || !isContainer(page.root) || pageBlocks(page).length >= MAX_BLOCKS_PER_PAGE) return state;
      const id = uniqueBlockId(s, action.widget);
      const node = layoutNodeForWidget(id, action.widget, action.props);
      const blocks = pageBlocks(page);
      const index = Math.max(0, Math.min(action.index, blocks.length));
      const parent = sharedBlockParent(page.root);
      if (parent) {
        const root = insertChild(page.root, parent.id, Math.min(index, parent.children.length), node);
        if (!root || treeDepth(root) > MAX_TREE_DEPTH || countNodes(root) > MAX_NODES_PER_PAGE) return state;
        return commit(state, replacePage(s, page.path, withTree(page, root)), null, { selectedBlockId: id });
      }
      // Nested tree without a shared parent: place next to the block at `index` (or at end).
      const neighbour = blocks[Math.min(index, Math.max(0, blocks.length - 1))];
      const at = neighbour ? locate(page.root, neighbour.id) : null;
      const parentId = at?.parent.id ?? page.root.id;
      const slot = at ? (index >= blocks.length ? at.index + 1 : at.index) : page.root.children.length;
      const root = insertChild(page.root, parentId, slot, node);
      if (!root || treeDepth(root) > MAX_TREE_DEPTH || countNodes(root) > MAX_NODES_PER_PAGE) return state;
      return commit(state, replacePage(s, page.path, withTree(page, root)), null, { selectedBlockId: id });
    }
    case 'reorderBlock': {
      const page = s.pages.find((p) => p.path === state.pagePath);
      if (!page) return state;
      const blocks = pageBlocks(page);
      const from = blocks.findIndex((b) => b.id === action.id);
      if (from < 0) return state;
      if (!sharedBlockParent(page.root)) {
        // Blocks live in different containers: move next to the block currently at that slot.
        const last = action.index >= blocks.length;
        const target = last ? blocks[blocks.length - 1]! : blocks[action.index]!;
        const at = target.id === action.id ? null : locate(page.root, target.id);
        const next = at ? relocate(page.root, action.id, at.parent.id, last ? at.index + 1 : at.index) : null;
        return next ? commit(state, replacePage(s, page.path, withTree(page, next)), null) : state;
      }
      const to = Math.max(0, Math.min(action.index, blocks.length));
      const dest = to > from ? to - 1 : to;
      if (dest === from) return state;
      return commit(
        state,
        mapPage(s, state.pagePath, (blocks) => {
          const next = [...blocks];
          const [item] = next.splice(from, 1);
          next.splice(dest, 0, item!);
          return next;
        }),
        null,
      );
    }
    case 'removeBlock':
      return reconcile(
        commit(state, { ...s, pages: s.pages.map((p) => withBlocks(p, pageBlocks(p).filter((b) => b.id !== action.id))) }, null),
      );
    case 'duplicateBlock': {
      const page = s.pages.find((p) => pageBlocks(p).some((b) => b.id === action.id));
      const blocks = page ? pageBlocks(page) : [];
      const source = blocks.find((b) => b.id === action.id);
      if (!page || !source || blocks.length >= MAX_BLOCKS_PER_PAGE) return state;
      const id = uniqueBlockId(s, source.widget);
      const copy: DappBlock = { ...source, id, props: { ...source.props }, ...(source.style ? { style: { ...source.style } } : {}) };
      return commit(
        state,
        mapPage(s, page.path, (blocks) => {
          const i = blocks.indexOf(source);
          return [...blocks.slice(0, i + 1), copy, ...blocks.slice(i + 1)];
        }),
        null,
        { pagePath: page.path, selectedBlockId: id },
      );
    }
    case 'movePage': {
      const i = s.pages.findIndex((p) => p.path === action.path);
      const j = i + action.delta;
      if (i < 0 || j < 0 || j >= s.pages.length) return state;
      const pages = [...s.pages];
      [pages[i], pages[j]] = [pages[j]!, pages[i]!];
      // An explicit nav order follows the move: the two pages swap places when both are listed.
      const links = s.nav?.links;
      const a = links?.indexOf(action.path) ?? -1;
      const b = links?.indexOf(s.pages[j]!.path) ?? -1;
      let nav = s.nav;
      if (links && a >= 0 && b >= 0) {
        const next = [...links];
        [next[a], next[b]] = [next[b]!, next[a]!];
        nav = { ...s.nav, links: next };
      }
      return commit(state, { ...s, pages, ...(nav ? { nav } : {}) }, null);
    }
    case 'setPageAccess': {
      const page = s.pages.find((p) => p.path === action.path);
      if (!page || action.path === '/') return state;
      const current = page.access ?? 'public';
      if (current === action.access) return state;
      const next: DappPage = { ...page };
      if (action.access === 'public') delete next.access;
      else next.access = action.access;
      return commit(state, replacePage(s, page.path, next), null);
    }
    case 'setNavVisible': {
      if (!s.pages.some((p) => p.path === action.path)) return state;
      const order = s.pages.map((p) => p.path);
      const shown = s.nav?.links ? s.nav.links.filter((l) => order.includes(l)) : order;
      const isShown = shown.includes(action.path);
      if (isShown === action.visible) return state;
      let links: string[];
      if (action.visible) {
        // Back in at the place its page order suggests: before the first shown page that follows it.
        const rank = order.indexOf(action.path);
        const at = shown.findIndex((l) => order.indexOf(l) > rank);
        links = at < 0 ? [...shown, action.path] : [...shown.slice(0, at), action.path, ...shown.slice(at)];
      } else {
        links = shown.filter((l) => l !== action.path);
      }
      const nav = { ...(s.nav ?? {}) };
      // Everything shown in page order is the default: no explicit list needed.
      if (links.length === order.length && links.every((l, i) => l === order[i])) delete nav.links;
      else nav.links = links;
      return commit(state, { ...s, nav }, null);
    }
    case 'moveBlock': {
      const page = s.pages.find((p) => p.path === state.pagePath);
      if (page && !sharedBlockParent(page.root)) {
        const blocks = pageBlocks(page);
        const i = blocks.findIndex((b) => b.id === action.id);
        const neighbour = blocks[i + action.delta];
        const at = i >= 0 && neighbour ? locate(page.root, neighbour.id) : null;
        const next = at ? relocate(page.root, action.id, at.parent.id, action.delta < 0 ? at.index : at.index + 1) : null;
        return next ? commit(state, replacePage(s, page.path, withTree(page, next)), null) : state;
      }
      return commit(
        state,
        mapPage(s, state.pagePath, (blocks) => {
          const i = blocks.findIndex((b) => b.id === action.id);
          const j = i + action.delta;
          if (i < 0 || j < 0 || j >= blocks.length) return blocks;
          const next = [...blocks];
          [next[i], next[j]] = [next[j]!, next[i]!];
          return next;
        }),
        null,
      );
    }
    case 'insertIntoTree': {
      const page = s.pages.find((p) => p.path === state.pagePath);
      if (!page || !isContainer(page.root)) return state;
      if (pageBlocks(page).length >= MAX_BLOCKS_PER_PAGE || countNodes(page.root) >= MAX_NODES_PER_PAGE) return state;
      const id = uniqueBlockId(s, action.widget);
      const node = layoutNodeForWidget(id, action.widget, action.props);
      const root = insertChild(page.root, action.parentId, action.index, node);
      if (!root || treeDepth(root) > MAX_TREE_DEPTH || countNodes(root) > MAX_NODES_PER_PAGE) return state;
      return commit(state, replacePage(s, page.path, withTree(page, root)), null, { selectedBlockId: id });
    }
    case 'moveNode': {
      const page = pageHolding(s, action.id);
      const root = page ? relocate(page.root, action.id, action.parentId, action.index) : null;
      if (!page || !root) return state;
      return commit(state, replacePage(s, page.path, withTree(page, root)), null, { pagePath: page.path, selectedBlockId: action.id });
    }
    case 'duplicateNode': {
      const page = pageHolding(s, action.id);
      const node = page ? findNode(page.root, action.id) : null;
      const at = page ? locate(page.root, action.id) : null; // null for the page root: it can't be duplicated
      if (!page || !node || !at) return state;
      const copy = cloneNode(node, allIds(s));
      const root = insertChild(page.root, at.parent.id, at.index + 1, copy);
      if (!root) return state;
      if (blocksFromTree(root).length > MAX_BLOCKS_PER_PAGE || countNodes(root) > MAX_NODES_PER_PAGE || treeDepth(root) > MAX_TREE_DEPTH) return state;
      return commit(state, replacePage(s, page.path, withTree(page, root)), null, { pagePath: page.path, selectedBlockId: nodeId(copy) });
    }
    case 'removeNodeFromTree': {
      const page = pageHolding(s, action.id);
      if (!page || !locate(page.root, action.id)) return state; // unknown id, or the page root
      return reconcile(commit(state, replacePage(s, page.path, withTree(page, removeNode(page.root, action.id))), null));
    }
    case 'setProp':
      return commit(
        state,
        mapBlock(s, action.id, (b) => {
          const props = { ...b.props };
          if (action.value === undefined) delete props[action.name];
          else props[action.name] = action.value;
          return { ...b, props };
        }),
        `prop:${action.id}:${action.name}`,
      );
    case 'setBlockStyle':
      return commit(
        state,
        {
          ...s,
          pages: s.pages.map((p) => {
            const root = updateNodeStyle(p.root, action.id, action.style);
            return { ...p, root, blocks: blocksFromTree(root) };
          }),
        },
        `style:${action.id}`,
      );
    case 'setContracts':
      return commit(state, { ...s, contracts: action.contracts }, null);
    case 'setContractAddress': {
      const target = s.contracts?.find((c) => c.id === action.id);
      if (!target || target.address === action.address) return state;
      const old = target.address;
      const contracts = (s.contracts ?? []).map((c) => (c.id === action.id ? { ...c, address: action.address } : c));
      const pages = old
        ? s.pages.map((p) => {
            const root = mapTree(p.root, (n) => {
              if (n.kind !== 'block' || !Object.values(n.block.props).includes(old)) return n;
              const props = Object.fromEntries(Object.entries(n.block.props).map(([k, v]) => [k, v === old ? action.address : v]));
              return { ...n, block: { ...n.block, props } };
            });
            return withTree(p, root);
          })
        : s.pages;
      return commit(state, { ...s, contracts, pages }, null);
    }
    case 'selectBlock':
      return { ...state, selectedBlockId: action.id };
    case 'undo': {
      const prev = state.past[state.past.length - 1];
      if (!prev) return state;
      return reconcile({ ...state, spec: prev, past: state.past.slice(0, -1), future: [state.spec, ...state.future], lastKey: null });
    }
    case 'redo': {
      const next = state.future[0];
      if (!next) return state;
      return reconcile({ ...state, spec: next, past: [...state.past, state.spec], future: state.future.slice(1), lastKey: null });
    }
  }
}

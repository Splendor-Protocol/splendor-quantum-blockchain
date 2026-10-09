import { slugify, type DappBlock, type DappSpec, type NetworkId, type PropValue, type ThemeName, type ThemeOverrides } from '@splendor/dapp-spec';

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
  | { type: 'setTheme'; theme: { base: ThemeName; overrides: ThemeOverrides } }
  | { type: 'addPage'; title: string }
  | { type: 'removePage'; path: string }
  | { type: 'renamePage'; path: string; title: string }
  | { type: 'selectPage'; path: string }
  | { type: 'addBlock'; widget: string; props: Record<string, PropValue> }
  | { type: 'removeBlock'; id: string }
  | { type: 'moveBlock'; id: string; delta: -1 | 1 }
  | { type: 'setProp'; id: string; name: string; value: PropValue | undefined }
  | { type: 'selectBlock'; id: string | null }
  | { type: 'undo' }
  | { type: 'redo' };

const HISTORY_LIMIT = 100;
const COALESCE_MS = 1000;

export function initBuilder(spec: DappSpec): BuilderState {
  return { spec, pagePath: spec.pages[0]?.path ?? '/', selectedBlockId: null, past: [], future: [], lastKey: null, lastAt: 0 };
}

function uniqueBlockId(spec: DappSpec, widget: string): string {
  const taken = new Set(spec.pages.flatMap((p) => p.blocks.map((b) => b.id)));
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

function mapPage(spec: DappSpec, path: string, fn: (blocks: DappBlock[]) => DappBlock[]): DappSpec {
  return { ...spec, pages: spec.pages.map((p) => (p.path === path ? { ...p, blocks: fn(p.blocks) } : p)) };
}

function mapBlock(spec: DappSpec, id: string, fn: (b: DappBlock) => DappBlock): DappSpec {
  return { ...spec, pages: spec.pages.map((p) => ({ ...p, blocks: p.blocks.map((b) => (b.id === id ? fn(b) : b)) })) };
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
  const selectedBlockId = page?.blocks.some((b) => b.id === state.selectedBlockId) ? state.selectedBlockId : null;
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
      const path = uniquePath(s, action.title);
      return commit(state, { ...s, pages: [...s.pages, { path, title: action.title.slice(0, 60) || 'Page', blocks: [] }] }, null, {
        pagePath: path,
        selectedBlockId: null,
      });
    }
    case 'removePage':
      if (action.path === '/') return state;
      return reconcile(commit(state, { ...s, pages: s.pages.filter((p) => p.path !== action.path) }, null));
    case 'renamePage':
      return commit(state, { ...s, pages: s.pages.map((p) => (p.path === action.path ? { ...p, title: action.title } : p)) }, `page:${action.path}`);
    case 'selectPage':
      return reconcile({ ...state, pagePath: action.path, selectedBlockId: null });
    case 'addBlock': {
      const id = uniqueBlockId(s, action.widget);
      return commit(state, mapPage(s, state.pagePath, (blocks) => [...blocks, { id, widget: action.widget, props: action.props }]), null, {
        selectedBlockId: id,
      });
    }
    case 'removeBlock':
      return reconcile(commit(state, { ...s, pages: s.pages.map((p) => ({ ...p, blocks: p.blocks.filter((b) => b.id !== action.id) })) }, null));
    case 'moveBlock':
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

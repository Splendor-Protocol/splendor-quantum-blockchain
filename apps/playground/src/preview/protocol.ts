import type { DappSpec, NetworkId } from '@splendor/dapp-spec';
import type { MockWalletState } from './mock';

export const PREVIEW_SOURCE = 'splendor-playground-preview';

/** The preview's own entry (preview.html), under the app's base path. */
export const PREVIEW_SRC = `${import.meta.env.BASE_URL.replace(/\/?$/, '/')}preview.html`;

/**
 * Parent messages carry the same `token` as the frame URL's fragment (`#t=…`). The frame drops
 * messages without it, so only the PreviewFrame that opened this document can drive it.
 */
type ToFrame = { source: typeof PREVIEW_SOURCE; token?: string };

export type PreviewRender = ToFrame & {
  type: 'render';
  spec: unknown;
  pagePath: string;
  network: NetworkId;
  walletState: MockWalletState;
  selectedBlockId: string | null;
  /**
   * `edit` (default): builder mode, with selection outlines, measured rects and drop targets.
   * `play`: the app as a visitor sees it: page gates are enforced, nothing is selectable or measured.
   * Links still post `navigate` so the parent can follow them.
   */
  mode?: PreviewMode;
  /**
   * Bump to restart the app: the frame clears its signed-in state (sessionStorage
   * `sqc-dapp-signed-in`) and remounts the page. The first value a frame sees changes nothing.
   */
  resetKey?: number;
};

export type PreviewMode = 'edit' | 'play';

export type ParentToPreview =
  | PreviewRender
  /** Only the selection changed: the frame keeps the spec it has. */
  | (ToFrame & { type: 'select'; selectedBlockId: string | null })
  /** Ask the frame to re-measure and post `rects` (e.g. after the frame was resized). */
  | (ToFrame & { type: 'requestRects' });

/** Rect in the preview frame's viewport coordinates (CSS px). */
export type PreviewRect = { top: number; left: number; width: number; height: number };

export type PreviewNodeRect = {
  /** Block id or container id. */
  id: string;
  /** `block` or a container kind (section, row, column, card, stack). */
  kind: string;
  /** Id of the enclosing container, or null for the page root. */
  parentId: string | null;
  rect: PreviewRect;
};

export type PreviewDropTarget = {
  parentId: string;
  /** Insertion index among the parent's children. */
  index: number;
  /** Indicator rect for the insertion point (a thin line, or the whole empty container). */
  rect: PreviewRect;
};

export type PreviewRects = { nodes: PreviewNodeRect[]; dropTargets: PreviewDropTarget[] };

/**
 * Every frame message echoes `token`, the value the parent put in the frame URL's fragment
 * (`#t=…`). The parent drops messages without it.
 */
type FromFrame = { source: typeof PREVIEW_SOURCE; token?: string };

export type PreviewToParent = FromFrame &
  (
    | { type: 'ready' }
    | { type: 'navigate'; pagePath: string }
    | { type: 'select'; blockId: string }
    | { type: 'invalid'; issues: { path: string; message: string }[] }
    | { type: 'calls'; calls: { method: string; args: string }[] }
    | ({ type: 'rects' } & PreviewRects)
    /** Pointer is over this node (block or container id), or null when it left the page. */
    | { type: 'hover'; id: string | null }
    /** A native drag was dropped inside the frame. `contractFn` is the raw contract-function MIME payload, if any. */
    | { type: 'drop'; parentId: string; index: number; contractFn?: string }
  );

/** The token this frame document was opened with (`#t=…`), or an empty string. */
export function frameTokenFrom(hash: string): string {
  const t = new URLSearchParams(hash.replace(/^#/, '')).get('t') ?? '';
  return /^[0-9a-f]{32}$/.test(t) ? t : '';
}

export function isPreviewMessage<T extends { source: string }>(data: unknown): data is T {
  return typeof data === 'object' && data !== null && (data as { source?: unknown }).source === PREVIEW_SOURCE;
}

export type RenderRequest = Omit<PreviewRender, 'source' | 'type' | 'spec'> & { spec: DappSpec };

export const MAX_RECT_NODES = 600;
export const MAX_DROP_TARGETS = 1200;

const finite = (n: unknown): n is number => typeof n === 'number' && Number.isFinite(n) && Math.abs(n) < 1e6;

function cleanRect(r: unknown): PreviewRect | null {
  if (typeof r !== 'object' || r === null) return null;
  const { top, left, width, height } = r as Record<string, unknown>;
  if (!finite(top) || !finite(left) || !finite(width) || !finite(height) || width < 0 || height < 0) return null;
  return { top, left, width, height };
}

const cleanId = (v: unknown): string | null => (typeof v === 'string' && v.length > 0 && v.length <= 120 ? v : null);

/** The preview frame can render untrusted specs; treat everything it reports as untrusted too. */
export function sanitizeRects(data: { nodes?: unknown; dropTargets?: unknown }): PreviewRects {
  const nodes: PreviewNodeRect[] = [];
  if (Array.isArray(data.nodes)) {
    for (const n of data.nodes.slice(0, MAX_RECT_NODES)) {
      if (typeof n !== 'object' || n === null) continue;
      const o = n as Record<string, unknown>;
      const id = cleanId(o.id);
      const kind = typeof o.kind === 'string' ? o.kind.slice(0, 24) : null;
      const rect = cleanRect(o.rect);
      const parentId = o.parentId === null ? null : cleanId(o.parentId);
      if (!id || !kind || !rect || (o.parentId !== null && !parentId)) continue;
      nodes.push({ id, kind, parentId, rect });
    }
  }
  const dropTargets: PreviewDropTarget[] = [];
  if (Array.isArray(data.dropTargets)) {
    for (const t of data.dropTargets.slice(0, MAX_DROP_TARGETS)) {
      if (typeof t !== 'object' || t === null) continue;
      const o = t as Record<string, unknown>;
      const parentId = cleanId(o.parentId);
      const rect = cleanRect(o.rect);
      if (!parentId || !rect || typeof o.index !== 'number' || !Number.isInteger(o.index) || o.index < 0 || o.index > 1000) continue;
      dropTargets.push({ parentId, index: o.index, rect });
    }
  }
  return { nodes, dropTargets };
}

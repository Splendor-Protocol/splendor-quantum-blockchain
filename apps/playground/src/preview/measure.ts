import type { PreviewDropTarget, PreviewNodeRect, PreviewRect, PreviewRects } from './protocol';

const NODE_SELECTOR = '[data-node-id],[data-block-id]';

const round = (n: number) => Math.round(n * 10) / 10;

function rectOf(el: Element): PreviewRect | null {
  if (el.getClientRects().length === 0) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  return { top: round(r.top), left: round(r.left), width: round(r.width), height: round(r.height) };
}

const idOf = (el: Element): string | null => el.getAttribute('data-node-id') ?? el.getAttribute('data-block-id');

/** Nearest layout node (block or container) at or above `el`. */
export function nodeAt(el: EventTarget | null): HTMLElement | null {
  return el instanceof Element ? (el.closest(NODE_SELECTOR) as HTMLElement | null) : null;
}

export function nodeIdAt(el: EventTarget | null): string | null {
  const n = nodeAt(el);
  return n ? idOf(n) : null;
}

/**
 * Measures every `[data-node-id]` / `[data-block-id]` element and derives the insertion points of
 * each container. Rects are in the frame's viewport coordinates.
 */
export function measureRects(doc: Document = document): PreviewRects {
  const nodes: PreviewNodeRect[] = [];
  const dropTargets: PreviewDropTarget[] = [];

  for (const el of Array.from(doc.querySelectorAll(NODE_SELECTOR))) {
    const id = idOf(el);
    const rect = rectOf(el);
    if (!id || !rect) continue;
    const isBlock = el.hasAttribute('data-block-id');
    const parentEl = el.parentElement?.closest('[data-node-id]') ?? null;
    nodes.push({ id, kind: isBlock ? 'block' : (el.getAttribute('data-node-kind') ?? 'container'), parentId: parentEl ? idOf(parentEl) : null, rect });
    if (isBlock) continue;

    // `at` is the child's position among all node children (hidden ones keep their slot).
    const kids = Array.from(el.children)
      .filter((c) => c.matches(NODE_SELECTOR))
      .map((c, at) => ({ rect: rectOf(c), at }))
      .filter((c): c is { rect: PreviewRect; at: number } => c.rect !== null);

    if (kids.length === 0) {
      const placeholder = el.querySelector(':scope > [data-drop-placeholder]');
      dropTargets.push({ parentId: id, index: 0, rect: (placeholder && rectOf(placeholder)) || rect });
      continue;
    }

    let horizontal: boolean;
    if (kids.length >= 2) {
      const a = kids[0]!.rect;
      const b = kids[1]!.rect;
      horizontal = b.left >= a.left + a.width - 1 && b.top < a.top + a.height && a.top < b.top + b.height;
    } else {
      const cs = getComputedStyle(el);
      horizontal = cs.display.includes('flex') && cs.flexDirection.startsWith('row');
    }

    for (let i = 0; i <= kids.length; i++) {
      const prev = kids[i - 1]?.rect;
      const next = kids[i]?.rect;
      const index = i === kids.length ? kids[i - 1]!.at + 1 : kids[i]!.at;
      if (horizontal) {
        const x = !prev ? next!.left : !next ? prev.left + prev.width : (prev.left + prev.width + next.left) / 2;
        dropTargets.push({ parentId: id, index, rect: { top: rect.top, left: round(x - 2), width: 4, height: rect.height } });
      } else {
        const y = !prev ? next!.top : !next ? prev.top + prev.height : (prev.top + prev.height + next.top) / 2;
        dropTargets.push({ parentId: id, index, rect: { top: round(y - 2), left: rect.left, width: rect.width, height: 4 } });
      }
    }
  }
  return { nodes, dropTargets };
}

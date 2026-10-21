import type { PreviewDropTarget, PreviewRect, PreviewRects } from './protocol';

function distanceToRect(r: PreviewRect, x: number, y: number): number {
  const dx = Math.max(r.left - x, 0, x - (r.left + r.width));
  const dy = Math.max(r.top - y, 0, y - (r.top + r.height));
  return Math.hypot(dx, dy);
}

export function rectContains(r: PreviewRect, x: number, y: number): boolean {
  return x >= r.left && x <= r.left + r.width && y >= r.top && y <= r.top + r.height;
}

/** True when `id` is `ancestorId` or lives somewhere under it. */
export function isWithin(rects: PreviewRects, id: string, ancestorId: string): boolean {
  const byId = new Map(rects.nodes.map((n) => [n.id, n]));
  let cur: string | null = id;
  for (let guard = 0; cur && guard < 64; guard++) {
    if (cur === ancestorId) return true;
    cur = byId.get(cur)?.parentId ?? null;
  }
  return false;
}

/**
 * Pick the insertion point nearest to a pointer position (in the frame's viewport coordinates):
 * the deepest container under the pointer wins, then its closest insertion line.
 * `excludeId` hides that node (and everything inside it) so a node can't be dropped into itself.
 */
export function pickDropTarget(rects: PreviewRects, x: number, y: number, excludeId?: string | null): PreviewDropTarget | null {
  const byId = new Map(rects.nodes.map((n) => [n.id, n]));
  const depth = (id: string): number => {
    let d = 0;
    let cur = byId.get(id)?.parentId ?? null;
    while (cur && d < 64) {
      d += 1;
      cur = byId.get(cur)?.parentId ?? null;
    }
    return d;
  };
  const usable = rects.dropTargets.filter((t) => !excludeId || !isWithin(rects, t.parentId, excludeId));
  const parents = [...new Set(usable.map((t) => t.parentId))];
  let best: { id: string; depth: number; dist: number } | null = null;
  for (const id of parents) {
    const rect = byId.get(id)?.rect;
    if (!rect) continue;
    const cand = { id, depth: depth(id), dist: distanceToRect(rect, x, y) };
    if (!best) best = cand;
    else if (cand.dist === 0 && best.dist === 0) {
      if (cand.depth > best.depth) best = cand;
    } else if (cand.dist < best.dist) best = cand;
  }
  if (!best) return null;
  let pick: PreviewDropTarget | null = null;
  let pickDist = Infinity;
  for (const t of usable) {
    if (t.parentId !== best.id) continue;
    const d = distanceToRect(t.rect, x, y);
    if (d < pickDist) {
      pick = t;
      pickDist = d;
    }
  }
  return pick;
}

import { describe, expect, it } from 'vitest';
import { pickDropTarget } from './dropTarget';
import { sanitizeRects, type PreviewRects } from './protocol';

const r = (top: number, left: number, width: number, height: number) => ({ top, left, width, height });

const rects: PreviewRects = {
  nodes: [
    { id: 'root', kind: 'section', parentId: null, rect: r(0, 0, 400, 400) },
    { id: 'col', kind: 'column', parentId: 'root', rect: r(0, 0, 400, 200) },
    { id: 'a', kind: 'block', parentId: 'col', rect: r(0, 0, 400, 100) },
    { id: 'b', kind: 'block', parentId: 'col', rect: r(100, 0, 400, 100) },
    { id: 'empty', kind: 'row', parentId: 'root', rect: r(250, 0, 400, 60) },
  ],
  dropTargets: [
    { parentId: 'root', index: 0, rect: r(0, 0, 400, 4) },
    { parentId: 'root', index: 2, rect: r(310, 0, 400, 4) },
    { parentId: 'col', index: 0, rect: r(-2, 0, 400, 4) },
    { parentId: 'col', index: 1, rect: r(98, 0, 400, 4) },
    { parentId: 'col', index: 2, rect: r(198, 0, 400, 4) },
    { parentId: 'empty', index: 0, rect: r(250, 0, 400, 60) },
  ],
};

describe('pickDropTarget', () => {
  it('prefers the deepest container under the pointer, then the nearest insertion line', () => {
    expect(pickDropTarget(rects, 50, 90)).toMatchObject({ parentId: 'col', index: 1 });
    expect(pickDropTarget(rects, 50, 10)).toMatchObject({ parentId: 'col', index: 0 });
  });

  it('targets an empty container', () => {
    expect(pickDropTarget(rects, 50, 270)).toMatchObject({ parentId: 'empty', index: 0 });
  });

  it('never offers the dragged node or its descendants', () => {
    expect(pickDropTarget(rects, 50, 90, 'col')?.parentId).toBe('root');
  });

  it('returns null without drop targets', () => {
    expect(pickDropTarget({ nodes: [], dropTargets: [] }, 1, 1)).toBeNull();
  });
});

describe('sanitizeRects', () => {
  it('drops malformed entries from an untrusted frame', () => {
    const out = sanitizeRects({
      nodes: [
        { id: 'a', kind: 'block', parentId: null, rect: r(0, 0, 10, 10) },
        { id: '', kind: 'block', parentId: null, rect: r(0, 0, 10, 10) },
        { id: 'b', kind: 'block', parentId: 'a', rect: r(0, 0, Number.NaN, 10) },
        'nope',
      ],
      dropTargets: [
        { parentId: 'a', index: 0, rect: r(0, 0, 1, 1) },
        { parentId: 'a', index: -1, rect: r(0, 0, 1, 1) },
        { parentId: 'a', index: 1.5, rect: r(0, 0, 1, 1) },
      ],
    });
    expect(out.nodes.map((n) => n.id)).toEqual(['a']);
    expect(out.dropTargets).toHaveLength(1);
  });
});

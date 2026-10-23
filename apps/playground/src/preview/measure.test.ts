import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { measureRects, nodeAt, nodeIdAt } from './measure';

/** jsdom does no layout: each element's box comes from `data-rect="top,left,width,height"`. */
function stubLayout() {
  const box = (el: Element) => {
    const raw = el.getAttribute('data-rect');
    const [top, left, width, height] = raw ? raw.split(',').map(Number) : [0, 0, 0, 0];
    return { top, left, width, height, right: left! + width!, bottom: top! + height!, x: left, y: top, toJSON: () => ({}) } as DOMRect;
  };
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    return box(this);
  });
  vi.spyOn(Element.prototype, 'getClientRects').mockImplementation(function (this: Element) {
    const hidden = this.getAttribute('data-rect') === null || this.hasAttribute('data-hidden');
    return (hidden ? [] : [box(this)]) as unknown as DOMRectList;
  });
}

beforeEach(() => stubLayout());
afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe('nodeAt / nodeIdAt', () => {
  it('walks up to the nearest block or container', () => {
    document.body.innerHTML = `
      <section data-node-id="root">
        <div data-block-id="hero"><h1><span id="deep">Hi</span></h1></div>
        <p id="loose">text</p>
      </section>
      <p id="outside">x</p>`;
    expect(nodeIdAt(document.getElementById('deep'))).toBe('hero');
    expect(nodeIdAt(document.getElementById('loose'))).toBe('root');
    expect(nodeIdAt(document.getElementById('outside'))).toBeNull();
    expect(nodeAt(null)).toBeNull();
    expect(nodeAt(window)).toBeNull();
  });
});

describe('measureRects', () => {
  it('emits before, between and after drop targets for a vertical container', () => {
    document.body.innerHTML = `
      <section data-node-id="root" data-node-kind="section" data-rect="0,0,400,300">
        <div data-block-id="a" data-rect="10,0,400,100"></div>
        <div data-block-id="b" data-rect="130,0,400,100"></div>
      </section>`;
    const { nodes, dropTargets } = measureRects(document);
    expect(nodes).toEqual([
      { id: 'root', kind: 'section', parentId: null, rect: { top: 0, left: 0, width: 400, height: 300 } },
      { id: 'a', kind: 'block', parentId: 'root', rect: { top: 10, left: 0, width: 400, height: 100 } },
      { id: 'b', kind: 'block', parentId: 'root', rect: { top: 130, left: 0, width: 400, height: 100 } },
    ]);
    expect(dropTargets).toEqual([
      { parentId: 'root', index: 0, rect: { top: 8, left: 0, width: 400, height: 4 } },
      { parentId: 'root', index: 1, rect: { top: 118, left: 0, width: 400, height: 4 } },
      { parentId: 'root', index: 2, rect: { top: 228, left: 0, width: 400, height: 4 } },
    ]);
  });

  it('uses vertical bars for side-by-side children', () => {
    document.body.innerHTML = `
      <div data-node-id="row" data-node-kind="row" data-rect="0,0,300,100">
        <div data-block-id="a" data-rect="0,0,100,100"></div>
        <div data-block-id="b" data-rect="0,150,100,100"></div>
      </div>`;
    const { dropTargets } = measureRects(document);
    expect(dropTargets.map((t) => t.rect.left)).toEqual([-2, 123, 248]);
    expect(dropTargets.every((t) => t.rect.width === 4 && t.rect.height === 100)).toBe(true);
  });

  it('skips hidden and zero-size nodes but keeps their child slot', () => {
    document.body.innerHTML = `
      <section data-node-id="root" data-rect="0,0,400,300">
        <div data-block-id="gone" data-hidden data-rect="0,0,400,50"></div>
        <div data-block-id="empty" data-rect="0,0,0,0"></div>
        <div data-block-id="shown" data-rect="100,0,400,50"></div>
      </section>`;
    const { nodes, dropTargets } = measureRects(document);
    expect(nodes.map((n) => n.id)).toEqual(['root', 'shown']);
    expect(dropTargets.map((t) => t.index)).toEqual([2, 3]);
  });

  it('targets the placeholder of an empty container', () => {
    document.body.innerHTML = `
      <section data-node-id="root" data-rect="0,0,400,300">
        <div data-drop-placeholder data-rect="20,20,360,40"></div>
      </section>`;
    expect(measureRects(document).dropTargets).toEqual([{ parentId: 'root', index: 0, rect: { top: 20, left: 20, width: 360, height: 40 } }]);
  });
});

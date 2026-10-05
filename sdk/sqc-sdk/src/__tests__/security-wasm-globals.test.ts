import { describe, expect, it } from 'vitest';
import { WASM_EXPORTS, detachWasmExports } from '../wasm-types.js';

describe('detachWasmExports', () => {
  it('captures every Go export and removes it from the global object', () => {
    const g: Record<string, unknown> = { unrelated: 1 };
    for (const name of WASM_EXPORTS) g[name] = () => name;

    const captured = detachWasmExports(g);

    for (const name of WASM_EXPORTS) {
      expect(typeof captured[name]).toBe('function');
      expect(name in g).toBe(false);
    }
    expect(g.unrelated).toBe(1);
  });

  it('blanks non-configurable globals it cannot delete', () => {
    const g: Record<string, unknown> = {};
    const [first] = WASM_EXPORTS;
    Object.defineProperty(g, first, { value: () => 1, writable: true, configurable: false });
    const captured = detachWasmExports(g);
    expect(typeof captured[first]).toBe('function');
    expect(g[first]).toBeUndefined();
  });
});

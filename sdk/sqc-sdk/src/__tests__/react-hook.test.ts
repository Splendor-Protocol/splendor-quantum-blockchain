/** Smoke test for the deprecated `@splendor/sqc-sdk/react` entry: it must stay importable and SSR-safe. */
import { describe, expect, it } from 'vitest';
import { createElement } from 'react';
import { renderToString } from 'react-dom/server';
import { useSplendorPQ } from '../react.js';

describe('@splendor/sqc-sdk/react', () => {
  it('renders on the server without touching WASM or the network', () => {
    let seen: ReturnType<typeof useSplendorPQ> | null = null;
    function Probe() {
      seen = useSplendorPQ({ network: 'local' });
      return createElement('span', null, String(seen.state));
    }
    expect(renderToString(createElement(Probe))).toContain('disconnected');
    expect(seen).not.toBeNull();
    expect(typeof seen!.connect).toBe('function');
  });
});

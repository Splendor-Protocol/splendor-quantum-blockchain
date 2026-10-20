import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../theme/tokens.css', import.meta.url), 'utf8');

describe('theme tokens', () => {
  it('every variable used without a fallback is defined', () => {
    const defined = new Set([...css.matchAll(/(--sqc-[\w-]+)\s*:/g)].map((m) => m[1]));
    const bare = [...css.matchAll(/var\((--sqc-[\w-]+)\s*\)/g)].map((m) => m[1]);
    expect([...new Set(bare.filter((v) => !defined.has(v)))]).toEqual([]);
  });

  it('message text never splits ordinary words', () => {
    const msg = /\.sqc-widget__msg \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(msg).not.toContain('break-all');
  });
});

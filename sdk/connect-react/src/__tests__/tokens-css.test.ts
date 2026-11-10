import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../theme/tokens.css', import.meta.url), 'utf8');

/** `--sqc-*: value` pairs declared in the first rule whose selector list contains `selector`. */
function tokens(selector: string, source = css): Record<string, string> {
  const start = source.indexOf(selector);
  const body = source.slice(source.indexOf('{', start) + 1, source.indexOf('}', start));
  return Object.fromEntries([...body.matchAll(/(--sqc-[\w-]+)\s*:\s*([^;]+);/g)].map((m) => [m[1]!, m[2]!.trim()]));
}

function luminance(hex: string): number {
  const n = hex.replace('#', '');
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(n.slice(i, i + 2), 16) / 255);
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(r!) + 0.7152 * lin(g!) + 0.0722 * lin(b!);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}

describe('theme tokens', () => {
  it('every variable used without a fallback is defined', () => {
    const defined = new Set([...css.matchAll(/(--sqc-[\w-]+)\s*:/g)].map((m) => m[1]));
    const bare = [...css.matchAll(/var\((--sqc-[\w-]+)\s*\)/g)].map((m) => m[1]);
    expect([...new Set(bare.filter((v) => !defined.has(v)))]).toEqual([]);
  });

  it('message text never splits ordinary words', () => {
    const msg = /\.sqc-widget__msg \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(msg).not.toContain('break-all');
    const review = /\.sqc-review \.sqc-account-row__value span \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(review).not.toContain('break-all');
  });

  for (const [name, selector] of [
    ['dark', ":root,"],
    ['light', "[data-sqc-theme='light'] {"],
  ] as const) {
    it(`${name}: text colours meet WCAG AA (4.5:1) on the widget surface`, () => {
      const t = tokens(selector);
      const accentText = t['--sqc-accent-text'] === 'var(--sqc-accent)' ? t['--sqc-accent']! : t['--sqc-accent-text']!;
      for (const [label, fg] of [
        ['text', t['--sqc-text']!],
        ['muted text', t['--sqc-text-muted']!],
        ['accent text', accentText],
        ['danger', t['--sqc-danger']!],
        ['success', t['--sqc-success']!],
      ] as const) {
        for (const bg of [t['--sqc-surface']!, t['--sqc-bg']!]) {
          expect(contrast(fg, bg), `${name} ${label} ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
        }
      }
      // Primary buttons: dark ink on the accent fill.
      expect(contrast('#04201c', t['--sqc-accent']!), `${name} primary button`).toBeGreaterThanOrEqual(4.5);
    });
  }

  it('theme="auto" uses exactly the light tokens when the OS asks for light', () => {
    const media = css.slice(css.indexOf('@media (prefers-color-scheme: light)'));
    expect(tokens("[data-sqc-theme='auto'] {", media)).toEqual(tokens("[data-sqc-theme='light'] {"));
    expect(css).toMatch(/\[data-sqc-theme='dark'\],\s*\[data-sqc-theme='auto'\] \{/);
  });

  it('touch screens get 44px targets for buttons, Copy and menu rows', () => {
    const coarse = /@media \(pointer: coarse\) \{([\s\S]*?)\n\}/.exec(css)?.[1] ?? '';
    for (const cls of ['.sqc-btn', '.sqc-copy', '.sqc-connector-item', '.sqc-network-badge']) expect(coarse).toContain(cls);
    expect(coarse).toContain('min-height: 2.75rem');
  });
});

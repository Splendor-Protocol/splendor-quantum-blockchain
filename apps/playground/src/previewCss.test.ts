/**
 * @vitest-environment node
 */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { APP_CSS } from '@splendor/dapp-spec';

// Read from disk: Vitest replaces CSS imports, `?raw` included, with an empty module.
const styles = readFileSync(new URL('./styles.css', import.meta.url), 'utf8').replace(/\r\n/g, '\n');

/** `selector { body }` with whitespace collapsed, or null when the rule is missing. */
function rule(css: string, selector: string): string | null {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = new RegExp(`(?:^|\\n)${escaped} \\{([^}]*)\\}`).exec(css);
  return m ? m[1]!.replace(/\s+/g, ' ').trim() : null;
}

describe('preview layout matches exported apps', () => {
  const shared = [...APP_CSS.matchAll(/^(\.dapp-[^{\n]+?) \{/gm)].map((m) => m[1]!).filter((s) => s !== '.dapp-root');

  it('finds the shared rules', () => {
    expect(shared).toEqual(expect.arrayContaining(['.dapp-shell', '.dapp-grid', '.dapp-nav a']));
  });

  it.each(shared)('%s is the same in the preview', (selector) => {
    expect(rule(styles, selector)).toBe(rule(APP_CSS, selector));
  });
});

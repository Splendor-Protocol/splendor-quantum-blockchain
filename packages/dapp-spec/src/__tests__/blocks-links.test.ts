import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const BLOCKS = new URL('../../blocks/', import.meta.url);

describe('exported block sources', () => {
  it('every target="_blank" link sets rel="noopener noreferrer"', () => {
    const offenders: string[] = [];
    for (const name of readdirSync(BLOCKS).filter((n) => n.endsWith('.tsx'))) {
      const src = readFileSync(new URL(name, BLOCKS), 'utf8');
      for (const tag of src.match(/<a\b[^>]*target="_blank"[^>]*>/g) ?? []) {
        if (!/rel="noopener noreferrer"/.test(tag)) offenders.push(`${name}: ${tag}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

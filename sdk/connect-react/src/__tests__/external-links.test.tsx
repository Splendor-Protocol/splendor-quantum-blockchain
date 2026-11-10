/**
 * @vitest-environment jsdom
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { AddressDisplay } from '../widgets/parts/AddressDisplay.js';
import { TxResult } from '../widgets/parts/TxResult.js';

// A string, not `new URL(...)`: under jsdom the global URL is not Node's.
const SRC = dirname(dirname(fileURLToPath(import.meta.url)));

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) return name === '__tests__' ? [] : sourceFiles(full);
    return /\.tsx?$/.test(name) ? [full] : [];
  });
}

afterEach(cleanup);

describe('external links', () => {
  it('every target="_blank" link in the library sets rel="noopener noreferrer"', () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(SRC)) {
      for (const tag of readFileSync(file, 'utf8').match(/<a\b[^>]*target="_blank"[^>]*>/g) ?? []) {
        if (!/rel="noopener noreferrer"/.test(tag)) offenders.push(`${file}: ${tag}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it('AddressDisplay drops an unsafe href instead of linking to it', () => {
    const { rerender } = render(<AddressDisplay value="SQ:abc" href="javascript:alert(1)" copy={false} />);
    expect(screen.queryByRole('link')).toBeNull();
    rerender(<AddressDisplay value="SQ:abc" href="https://explorer.example/address/SQ:abc" copy={false} />);
    const link = screen.getByRole('link');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('TxResult drops an unsafe explorer URL', () => {
    render(<TxResult hash={'ab'.repeat(32)} explorerUrl="data:text/html,<script>1</script>" />);
    expect(screen.queryByRole('link')).toBeNull();
  });
});

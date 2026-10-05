import { describe, expect, it } from 'vitest';
import { addressExplorerUrl } from '../widgets/utils.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);

describe('addressExplorerUrl', () => {
  it('builds a link for https explorers and loopback http', () => {
    expect(addressExplorerUrl('https://explorer.example/', ADDR)).toBe(`https://explorer.example/address/${ADDR}`);
    expect(addressExplorerUrl('http://localhost:3000', ADDR)).toBe(`http://localhost:3000/address/${ADDR}`);
  });

  it.each([
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)//',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'http://explorer.example',
    '//evil.example',
    '   ',
  ])('refuses %s', (base) => {
    expect(addressExplorerUrl(base, ADDR)).toBeNull();
  });

  it('refuses addresses that are not canonical SQ addresses', () => {
    expect(addressExplorerUrl('https://explorer.example', 'SQ:../../admin')).toBeNull();
    expect(addressExplorerUrl('https://explorer.example', '0x' + 'ab'.repeat(20))).toBeNull();
    expect(addressExplorerUrl('https://explorer.example', null)).toBeNull();
  });
});

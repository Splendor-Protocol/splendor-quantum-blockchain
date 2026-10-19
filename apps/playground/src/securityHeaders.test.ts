/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { ASSET_HEADERS, CONTENT_SECURITY_POLICY, SECURITY_HEADERS } from './securityHeaders';

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8');

/** `_headers` → { path: { header: value } } */
function parseNetlify(text: string): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {};
  let current: Record<string, string> | null = null;
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim() || line.startsWith('#')) continue;
    if (!/^\s/.test(line)) out[line.trim()] = current = {};
    else if (current) {
      const i = line.indexOf(':');
      current[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
  }
  return out;
}

describe('deploy configs carry the same security headers', () => {
  it('public/_headers (Netlify)', () => {
    const h = parseNetlify(read('../public/_headers'));
    expect(h['/*']).toEqual(SECURITY_HEADERS);
    expect(h['/assets/*']).toEqual(ASSET_HEADERS);
  });

  it('vercel.json', () => {
    const v = JSON.parse(read('../vercel.json')) as { headers: { source: string; headers: { key: string; value: string }[] }[] };
    const as = (source: string) => Object.fromEntries(v.headers.find((h) => h.source === source)!.headers.map((x) => [x.key, x.value]));
    expect(as('/(.*)')).toEqual(SECURITY_HEADERS);
    expect(as('/assets/(.*)')).toEqual(ASSET_HEADERS);
  });

  it('the CSP never allows eval or inline scripts', () => {
    const script = CONTENT_SECURITY_POLICY.split('; ').find((d) => d.startsWith('script-src'))!;
    expect(script).not.toMatch(/'unsafe-inline'|'unsafe-eval'/);
    expect(CONTENT_SECURITY_POLICY).toContain("object-src 'none'");
  });
});

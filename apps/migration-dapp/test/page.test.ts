/**
 * The page: served with a per-response nonce that appears in BOTH the CSP and
 * the inline script tag, contains the pinned contract selectors verbatim, and
 * hardcodes no addresses — those must come from /api/info at runtime.
 */
import { describe, expect, it } from 'vitest';
import worker from '../src/index';
import { renderPage } from '../src/page';
import {
  SEL_BURN_SPLD,
  SEL_BURN_SPLV,
  SEL_APPROVE,
  SEL_ALLOWANCE,
  SEL_BALANCE_OF,
  BURNED_TOPIC0,
} from '../src/selectors';
import { DEFAULT_SPLV_TOKEN, type Env } from '../src/config';

// The page renders even on a completely unconfigured deployment — the API
// answers 503 and the page explains itself; a blank screen never does.
const emptyEnv = { MIGRATION_KV: undefined } as unknown as Env;

async function servePage(): Promise<{ res: Response; html: string }> {
  const res = await worker.fetch(new Request('https://quantum-migration.splendor.org/'), emptyEnv);
  return { res, html: await res.text() };
}

describe('GET /', () => {
  it('serves HTML with a CSP whose nonce matches the script tag', async () => {
    const { res, html } = await servePage();
    expect(res.status).toBe(200);
    const csp = res.headers.get('content-security-policy') ?? '';
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("connect-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");
    const m = /script-src 'nonce-([^']+)'/.exec(csp);
    expect(m).not.toBeNull();
    expect(html).toContain(`<script nonce="${m![1]}">`);
  });

  it('issues a fresh nonce per response', async () => {
    const a = (await servePage()).res.headers.get('content-security-policy');
    const b = (await servePage()).res.headers.get('content-security-policy');
    expect(a).not.toBe(b);
  });

  it('ships the literal selector constants with their signatures', async () => {
    const { html } = await servePage();
    for (const sel of [SEL_BURN_SPLD, SEL_BURN_SPLV, SEL_APPROVE, SEL_ALLOWANCE, SEL_BALANCE_OF, BURNED_TOPIC0]) {
      expect(html).toContain(`'${sel}'`);
    }
    // The comments naming the full signatures, so a reader can re-derive them.
    expect(html).toContain('burnSpld(bytes32)');
    expect(html).toContain('burnSplv(uint256,bytes32)');
    expect(html).toContain('Burned(uint256,address,uint8,uint256,bytes32)');
  });

  it('hardcodes no contract addresses — they come from /api/info', async () => {
    const { html } = await servePage();
    expect(html).not.toContain(DEFAULT_SPLV_TOKEN);
    expect(html.toLowerCase()).not.toContain(DEFAULT_SPLV_TOKEN.toLowerCase().slice(2));
  });

  it('references no external origins (self-contained under the CSP)', async () => {
    const { html } = await servePage();
    // The only allowed URL schemes in the document: same-origin paths and the
    // inline data: favicon. Nothing may load from another host.
    expect(html).not.toMatch(/src="https?:\/\//);
    expect(html).not.toMatch(/href="https?:\/\//);
    expect(html).not.toMatch(/@import/);
  });

  it('supports HEAD with an empty body', async () => {
    const res = await worker.fetch(
      new Request('https://quantum-migration.splendor.org/', { method: 'HEAD' }),
      emptyEnv,
    );
    expect(res.status).toBe(200);
    expect(await res.text()).toBe('');
  });
});

describe('renderPage', () => {
  it('injects exactly the nonce it was given', () => {
    const html = renderPage({ nonce: 'test-nonce-123' });
    expect(html).toContain('<script nonce="test-nonce-123">');
    // Exactly one script on the page.
    expect(html.match(/<script/g)).toHaveLength(1);
  });

  it('carries the user-facing safety copy', () => {
    const html = renderPage({ nonce: 'n' });
    expect(html).toContain('irreversible');
    expect(html).toContain('Quantum tab');
    expect(html).toContain('plain-transfer SPLV');
  });
});

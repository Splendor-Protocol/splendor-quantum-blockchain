import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { TEMPLATES, buildProject, parseArgs, pinnedVersions, writeProject } from '../bin/lib.mjs';

const RECIPIENT = 'SQ:' + 'ab'.repeat(32);
let dirs = [];
afterEach(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true });
  dirs = [];
});

describe('create-splendor-dapp', () => {
  it('pins the SDK versions from its own package.json', () => {
    const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
    const v = pinnedVersions();
    expect(v.connectReact).toBe(pkg.devDependencies['@splendor/connect-react'].replace(/^[^\d]*/, ''));
    const project = buildProject({ name: 'demo', template: 'wallet-dashboard', framework: 'vite', network: 'testnet' });
    const out = JSON.parse(project.files['package.json']);
    expect(out.dependencies['@splendor/connect-react']).toBe(`^${v.connectReact}`);
    expect(out.dependencies['@splendor/sqc-sdk']).toBe(`^${v.sqcSdk}`);
    expect(out.name).toBe('demo');
  });

  it('parses flags in both forms', () => {
    expect(parseArgs(['app', '--template', 'nft-drop', '--framework=next', '--set', 'a.b=c', '-y'])).toMatchObject({
      name: 'app',
      template: 'nft-drop',
      framework: 'next',
      set: ['a.b=c'],
      yes: true,
    });
    expect(() => parseArgs(['--bogus'])).toThrow(/Unknown option/);
  });

  it('builds every template for both frameworks', () => {
    for (const t of TEMPLATES) {
      for (const framework of ['vite', 'next']) {
        const set = t.setup.map((s) => `${s.blockId}.${s.prop}=${RECIPIENT}`);
        const p = buildProject({ name: `x-${t.id}`, template: t.id, framework, network: 'devnet', set });
        expect(Object.keys(p.files)).toContain('README.md');
        expect(p.files[framework === 'vite' ? 'src/App.tsx' : 'app/providers.tsx']).toContain('"devnet"');
      }
    }
  });

  it('explains missing setup fields', () => {
    expect(() => buildProject({ name: 'tips', template: 'tip-jar', framework: 'vite', network: 'testnet' })).toThrow(/--set tips.recipient=/);
  });

  it('writes files and refuses non-empty directories', () => {
    const root = mkdtempSync(join(tmpdir(), 'csd-'));
    dirs.push(root);
    const p = buildProject({ name: 'w', template: 'wallet-dashboard', framework: 'vite', network: 'testnet' });
    const dir = writeProject(join(root, 'w'), p.files);
    expect(readFileSync(join(dir, 'src/App.tsx'), 'utf8')).toContain('SplendorConnectProvider');
    expect(() => writeProject(dir, p.files)).toThrow(/not empty/);
  });
});

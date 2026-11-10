import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';

const BIN = fileURLToPath(new URL('../bin/index.mjs', import.meta.url));
const RECIPIENT = 'SQ:' + 'ab'.repeat(32);
let dirs = [];
afterEach(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true });
  dirs = [];
});

/** Runs the real CLI the way `npm create` does, with no TTY so it never prompts. */
function run(args) {
  const cwd = mkdtempSync(join(tmpdir(), 'csd-bin-'));
  dirs.push(cwd);
  const r = spawnSync(process.execPath, [BIN, ...args], { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  return { cwd, code: r.status, out: r.stdout, err: r.stderr };
}

describe('create-splendor-dapp CLI (subprocess)', () => {
  it('--help lists every template and the pinned versions', () => {
    const r = run(['--help']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('--template');
    expect(r.out).toContain('wallet-dashboard');
    expect(r.out).toMatch(/@splendor\/connect-react@\^\d/);
  });

  it('creates a Vite project with --yes and prints the next steps', () => {
    const r = run(['my-app', '--yes']);
    expect(r.err).toBe('');
    expect(r.code).toBe(0);
    expect(r.out).toContain('Created my-app/');
    expect(r.out).toContain('npm run dev');
    const pkg = JSON.parse(readFileSync(join(r.cwd, 'my-app', 'package.json'), 'utf8'));
    expect(pkg.name).toBe('my-app');
    expect(existsSync(join(r.cwd, 'my-app', 'src', 'App.tsx'))).toBe(true);
  });

  it('creates a Next.js kit project on the requested network', () => {
    const r = run(['kit-app', '--template', 'token-project', '--framework', 'next', '--network', 'devnet']);
    expect(r.code).toBe(0);
    expect(readFileSync(join(r.cwd, 'kit-app', 'app', 'providers.tsx'), 'utf8')).toContain('"devnet"');
  });

  it('still accepts the retired "landing" template id from old docs', () => {
    const r = run(['old-link', '--template', 'landing', '--yes']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('Created old-link/ (Token project');
  });

  it('fills a required setup field from --set', () => {
    const r = run(['tips', '--template', 'tip-jar', '--set', `tips.recipient=${RECIPIENT}`]);
    expect(r.code).toBe(0);
    expect(readFileSync(join(r.cwd, 'tips', 'src', 'pages', 'HomePage.tsx'), 'utf8')).toContain(RECIPIENT);
  });

  it('exits 1 with a readable message on bad input', () => {
    for (const [args, msg] of [
      [['bad name!', '--yes'], /Project name/],
      [['app', '--template', 'nope'], /template/i],
      [['app', '--bogus'], /Unknown option/],
      [['tips', '--template', 'tip-jar'], /--set tips.recipient=/],
    ]) {
      const r = run(args);
      expect(r.code, args.join(' ')).toBe(1);
      expect(r.err, args.join(' ')).toMatch(msg);
    }
  });
});

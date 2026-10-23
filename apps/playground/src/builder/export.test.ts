import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { GeneratedProject } from '@splendor/dapp-spec';
import { deployLinks, downloadZip, openInStackBlitz } from './export';

const { zipped, generateAsync, openProject } = vi.hoisted(() => ({
  zipped: new Map<string, unknown>(),
  generateAsync: vi.fn(async () => new Blob(['zip'])),
  openProject: vi.fn(),
}));

vi.mock('jszip', () => ({
  default: class {
    folder(name: string) {
      return { file: (path: string, contents: unknown) => zipped.set(`${name}/${path}`, contents) };
    }
    generateAsync = generateAsync;
  },
}));

vi.mock('@stackblitz/sdk', () => ({ default: { openProject } }));

const project = (files: Record<string, string>, framework: GeneratedProject['framework'] = 'vite'): GeneratedProject => ({
  name: 'my-dapp',
  framework,
  files,
});

beforeEach(() => {
  zipped.clear();
  generateAsync.mockClear();
  openProject.mockClear();
  // jsdom has no object URLs.
  URL.createObjectURL = vi.fn(() => 'blob:x');
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

describe('deployLinks', () => {
  it('builds Vercel and Netlify clone links for GitHub, GitLab and Bitbucket repos', () => {
    for (const repo of ['https://github.com/org/repo', 'https://gitlab.com/org/repo.js', 'https://bitbucket.org/org/my_repo/']) {
      const links = deployLinks(`  ${repo}  `)!;
      expect(links.vercel).toBe(`https://vercel.com/new/clone?repository-url=${encodeURIComponent(repo)}`);
      expect(links.netlify).toBe(`https://app.netlify.com/start/deploy?repository=${encodeURIComponent(repo)}`);
    }
  });

  it('rejects anything that is not an https repo root on a known host', () => {
    for (const bad of [
      '',
      'http://github.com/org/repo',
      'https://github.com/org',
      'https://github.com/org/repo/tree/main',
      'https://evil.example/org/repo',
      'https://github.com/org/repo?x=1',
      'javascript:alert(1)',
    ]) {
      expect(deployLinks(bad)).toBeNull();
    }
  });
});

describe('downloadZip', () => {
  it('puts files under the project folder and decodes .wasm.b64 to bytes', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    await downloadZip(project({ 'package.json': '{}', 'public/signer.wasm.b64': btoa('\x00\x01\x02') }));
    expect(zipped.get('my-dapp/package.json')).toBe('{}');
    expect(zipped.has('my-dapp/public/signer.wasm.b64')).toBe(false);
    expect(Array.from(zipped.get('my-dapp/public/signer.wasm') as Uint8Array)).toEqual([0, 1, 2]);
    expect(click).toHaveBeenCalledTimes(1);
    expect(document.querySelector('a[download]')).toBeNull();
  });

  it('refuses an unsafe path before building the archive', async () => {
    await expect(downloadZip(project({ 'ok.txt': 'x', '../escape.txt': 'y' }))).rejects.toThrow(/unsafe file path: \.\.\/escape\.txt/);
    expect(generateAsync).not.toHaveBeenCalled();
  });
});

describe('openInStackBlitz', () => {
  it('opens the entry file for the framework', async () => {
    await openInStackBlitz(project({ 'src/App.tsx': '' }), 'Vite app');
    expect(openProject).toHaveBeenLastCalledWith(expect.objectContaining({ title: 'Vite app', template: 'node' }), expect.objectContaining({ openFile: 'src/App.tsx' }));
    await openInStackBlitz(project({ 'app/providers.tsx': '' }, 'next'), 'Next app');
    expect(openProject).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ openFile: 'app/providers.tsx' }));
  });

  it('refuses an unsafe path without loading StackBlitz', async () => {
    await expect(openInStackBlitz(project({ '/etc/passwd': 'x' }), 'Bad')).rejects.toThrow(/unsafe file path/);
    expect(openProject).not.toHaveBeenCalled();
  });
});

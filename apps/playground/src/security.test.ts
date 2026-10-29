import { describe, expect, it } from 'vitest';
import type { ContractRef } from '@splendor/dapp-spec';
import { isGitHubRunUrl } from './admin/AdminPage';
import { wasmPinsFromEnv } from './config';
import { frameTokenFrom } from './preview/protocol';
import { frameLinkAction } from './preview/PreviewRuntime';
import { parseContractDrop } from './studio/abi';

const ADDR = `SQ:${'ab'.repeat(32)}`;
const token: ContractRef = { id: 'token', name: 'Token', address: ADDR, functions: [] };
const drop = (payload: unknown) => parseContractDrop(JSON.stringify(payload), [token]);

describe('contract-function drops from the preview frame', () => {
  it('accepts the payload the Contracts panel sends, and uses the spec’s own address', () => {
    const out = drop({ contractId: 'token', address: `SQ:${'00'.repeat(32)}`, fn: { name: 'transfer', signature: 'transfer(address,uint256)', kind: 'write' } });
    expect(out).toEqual({ contract: token, address: ADDR, fn: { name: 'transfer', signature: 'transfer(address,uint256)', kind: 'write' } });
  });

  it('accepts an unknown contract only with a valid address', () => {
    expect(drop({ address: ADDR, fn: { name: 'balanceOf', signature: 'balanceOf(address)' } })?.fn.kind).toBe('read');
    expect(drop({ address: 'SQ:12', fn: { name: 'balanceOf', signature: 'balanceOf(address)' } })).toBeNull();
    expect(drop({ contractId: 'nope', fn: { name: 'balanceOf', signature: 'balanceOf(address)' } })).toBeNull();
  });

  it('takes the name and kind of a known contract function from the spec, not the payload', () => {
    const counter: ContractRef = {
      id: 'counter',
      name: 'Counter',
      address: ADDR,
      functions: [{ name: 'Get count', signature: 'get()', params: [], kind: 'read' }, { name: 'Bump', signature: 'inc()', params: [], kind: 'write' }],
    };
    const payload = (fn: object) => JSON.stringify({ contractId: 'counter', address: ADDR, fn });
    expect(parseContractDrop(payload({ name: 'Get count', signature: 'get()', kind: 'read' }), [counter])).toEqual({
      contract: counter,
      address: ADDR,
      fn: { name: 'Get count', signature: 'get()', kind: 'read' },
    });
    // A payload cannot turn the spec's write into a read, or rename it.
    expect(parseContractDrop(payload({ name: 'inc', signature: 'inc()', kind: 'read' }), [counter])?.fn).toEqual({ name: 'Bump', signature: 'inc()', kind: 'write' });
  });

  it('refuses bad signatures, mismatched names and junk', () => {
    expect(drop({ contractId: 'token', fn: { name: 'x', signature: 'x(uint256' } })).toBeNull();
    expect(drop({ contractId: 'token', fn: { name: 'mint', signature: 'transfer(address,uint256)' } })).toBeNull();
    expect(drop({ contractId: 'token', fn: { name: 'a', signature: '<img src=x>(a)' } })).toBeNull();
    expect(drop({ contractId: 'token' })).toBeNull();
    expect(parseContractDrop('{not json', [token])).toBeNull();
    expect(parseContractDrop('[]', [token])).toBeNull();
  });
});

describe('links inside the preview frame', () => {
  const pages = ['/', '/manage'];
  it('follows in-app page links', () => {
    expect(frameLinkAction('#/manage', pages)).toBe('app');
    expect(frameLinkAction('/manage', pages)).toBe('app');
  });

  it('opens web links in a new window instead of navigating the frame', () => {
    expect(frameLinkAction('https://example.com/manage', pages)).toBe('popup');
    expect(frameLinkAction('HTTP://example.com', pages)).toBe('popup');
  });

  it('does nothing for anything else', () => {
    for (const href of ['/not-a-page', '#/missing', '//evil.example/x', 'javascript:alert(1)', 'data:text/html,x', 'mailto:a@b.c', '']) {
      expect(frameLinkAction(href, pages), href).toBe('none');
    }
  });

  it('reads only a well-formed token from the frame URL', () => {
    const t = 'a1'.repeat(16);
    expect(frameTokenFrom(`#t=${t}`)).toBe(t);
    expect(frameTokenFrom(`t=${t}`)).toBe(t);
    expect(frameTokenFrom('#t=short')).toBe('');
    expect(frameTokenFrom('')).toBe('');
  });
});

describe('signer pins pasted into copied code', () => {
  it('accepts only sha256 hex digests', () => {
    const hex = 'ab'.repeat(32);
    expect(wasmPinsFromEnv({ VITE_SQC_WASM_SHA256: hex, VITE_SQC_EXEC_SHA256: ` ${hex} ` })).toEqual({ wasmSha256: hex, execSha256: hex });
    expect(wasmPinsFromEnv({ VITE_SQC_WASM_SHA256: hex })).toBeNull();
    expect(wasmPinsFromEnv({ VITE_SQC_WASM_SHA256: `${hex}" onload="x`, VITE_SQC_EXEC_SHA256: hex })).toBeNull();
    expect(wasmPinsFromEnv({ VITE_SQC_WASM_SHA256: 'abc', VITE_SQC_EXEC_SHA256: hex })).toBeNull();
  });
});

describe('release run links on the admin page', () => {
  it('links only https GitHub run pages', () => {
    expect(isGitHubRunUrl('https://github.com/x/y/actions/runs/1')).toBe(true);
    expect(isGitHubRunUrl('https://ghe.example.com/x/y/actions/runs/42', 'ghe.example.com')).toBe(true);
    // Any other host is refused, even with a run-shaped path.
    expect(isGitHubRunUrl('https://ghe.example.com/x/y/actions/runs/42')).toBe(false);
    expect(isGitHubRunUrl('https://evil.example/a/b/actions/runs/1', 'ghe.example.com')).toBe(false);
    expect(isGitHubRunUrl('https://github.com/login')).toBe(false);
    expect(isGitHubRunUrl('http://github.com/x/y/actions/runs/1')).toBe(false);
    expect(isGitHubRunUrl('javascript:alert(1)')).toBe(false);
    expect(isGitHubRunUrl('https://evil.example/login')).toBe(false);
    expect(isGitHubRunUrl('not a url')).toBe(false);
  });
});

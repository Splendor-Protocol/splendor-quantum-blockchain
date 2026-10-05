import { afterEach, describe, expect, it, vi } from 'vitest';
import { SqRpc, assertSafeRpcUrl, redactRpcUrl } from '../rpc.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('assertSafeRpcUrl', () => {
  it.each([
    'http://169.254.169.254/latest/meta-data',
    'http://[::ffff:169.254.169.254]/',
    'http://[::ffff:a9fe:a9fe]/',
    'http://[fd00:ec2::254]/',
    'http://100.100.100.200/',
    'http://metadata.google.internal/',
    'http://anything.internal/',
    'http://[fe80::1]/',
    'http://0xa9fea9fe/',
  ])('blocks %s', (url) => {
    expect(() => assertSafeRpcUrl(url)).toThrow(/blocked/);
  });

  it('blocks non-http schemes', () => {
    expect(() => assertSafeRpcUrl('file:///etc/passwd')).toThrow(/scheme/);
    expect(() => assertSafeRpcUrl('not a url')).toThrow(/Invalid RPC URL/);
  });

  it('allows public and loopback hosts, and private hosts when opted in', () => {
    expect(() => assertSafeRpcUrl('https://rpc.splendor.org')).not.toThrow();
    expect(() => assertSafeRpcUrl('http://127.0.0.1:8545')).not.toThrow();
    expect(() => assertSafeRpcUrl('http://169.254.169.254/', { allowPrivateRpcHosts: true })).not.toThrow();
  });
});

describe('redactRpcUrl', () => {
  it('keeps only the origin', () => {
    expect(redactRpcUrl('https://user:pw@rpc.example.com/v1/SECRETKEY?apikey=abc')).toBe('https://rpc.example.com');
    expect(redactRpcUrl('::')).toBe('[invalid url]');
  });
});

describe('SqRpc transport policy', () => {
  it('requires https for non-loopback hosts unless allowInsecureHttp', () => {
    expect(() => new SqRpc({ rpcUrl: 'http://rpc.example.com', chainId: 'splendor-quantum-1' })).toThrow(/https/);
    expect(() => new SqRpc({ rpcUrl: 'http://rpc.example.com', chainId: 'splendor-quantum-1', allowInsecureHttp: true })).not.toThrow();
    expect(() => new SqRpc({ rpcUrl: 'http://127.0.0.1:8545', chainId: 'splendor-quantum-1' })).not.toThrow();
  });

  it('does not leak the RPC path or key in the https error', () => {
    try {
      new SqRpc({ rpcUrl: 'http://rpc.example.com/v1/SECRETKEY', chainId: 'splendor-quantum-1' });
      expect.fail('expected throw');
    } catch (e) {
      expect(String((e as Error).message)).not.toContain('SECRETKEY');
    }
  });

  it('refuses to follow redirects', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ chain_id: 'splendor-quantum-1' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const rpc = new SqRpc({ rpcUrl: 'https://rpc.example.com', chainId: 'splendor-quantum-1', retries: 0 });
    await rpc.getStatus().catch(() => undefined);
    expect(fetchMock).toHaveBeenCalled();
    const init = fetchMock.mock.calls[0]![1] as RequestInit | undefined;
    expect(init?.redirect).toBe('error');
  });
});

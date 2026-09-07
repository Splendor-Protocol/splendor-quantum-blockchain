import { describe, expect, it } from 'vitest';
import {
  createSplendorClient,
  connectSplendor,
  resolveNetwork,
  getDefaultNetwork,
  listNetworks,
  getNetworksFile,
  PROTOCOL,
  BRIDGE_CLIENT_VERSION,
  MIN_FEE,
  SqRpc,
} from '../index.js';

describe('sqc.networks.json single source', () => {
  it('loads protocol dynamics from config file', () => {
    const file = getNetworksFile();
    expect(file.sqdpVersion).toBe(PROTOCOL.version);
    expect(file.protocol.minFee).toBe(MIN_FEE);
    expect(file.protocol.bridgeClientVersion).toBe(BRIDGE_CLIENT_VERSION);
    expect(file.defaultNetwork).toBe(PROTOCOL.defaultNetwork);
  });

  it('resolves local / testnet / mainnet', () => {
    const ids = listNetworks().map((n) => n.id);
    expect(ids).toContain('local');
    expect(ids).toContain('testnet');
    expect(ids).toContain('mainnet');
    expect(resolveNetwork('local').rpcUrls.length).toBeGreaterThan(0);
    expect(getDefaultNetwork().id).toBe(fileDefault());
  });

  it('SqRpc defaults come from default network', () => {
    const net = getDefaultNetwork();
    const rpc = new SqRpc();
    expect(rpc.rpcUrls).toEqual(net.rpcUrls.map((u) => u.replace(/\/$/, '')));
    expect(rpc.chainId).toBe(net.chainId);
    expect(rpc.timeoutMs).toBe(net.timeoutMs);
    expect(rpc.retries).toBe(net.retries);
  });
});

describe('one-touch connect', () => {
  it('createSplendorClient wires network config', () => {
    const { provider, wallet, network, info } = createSplendorClient({ network: 'local' });
    expect(network.id).toBe('local');
    expect(provider.rpcUrls[0]).toContain('8080');
    expect(network.wasmUrl).toBeTruthy();
    expect(network.execUrl).toBeTruthy();
    expect(network.wasmSha256).toMatch(/^[0-9a-f]{64}$/i);
    expect(network.execSha256).toMatch(/^[0-9a-f]{64}$/i);
    expect(info).toBeNull();
    expect(wallet.getConnectionState()).toBe('disconnected');
    wallet.disconnect();
  });

  it('connectSplendor with seed unlocks wallet', async () => {
    const { wallet, info, network } = await connectSplendor({
      network: 'local',
      connect: 'seed',
      seed: '01'.repeat(32),
    });
    expect(network.id).toBe('local');
    expect(info?.address.startsWith('SQ:')).toBe(true);
    expect(wallet.getAddress()).toBe(info!.address);
    wallet.disconnect();
  });

  it('rejects unknown network', () => {
    expect(() => createSplendorClient({ network: 'nope' })).toThrow();
  });
});

function fileDefault() {
  return getNetworksFile().defaultNetwork;
}

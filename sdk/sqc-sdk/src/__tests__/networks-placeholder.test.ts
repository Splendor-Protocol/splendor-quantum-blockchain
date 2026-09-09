import { afterEach, describe, expect, it } from 'vitest';
import {
  clearRegisteredNetworks,
  isPlaceholderNetwork,
  listSelectableNetworks,
  getNetwork,
  listNetworks,
  registerNetwork,
  resolveNetwork,
} from '../networks.js';

afterEach(() => clearRegisteredNetworks());

/** A network whose baked-in RPC is still a documentation placeholder. */
function registerPlaceholder(): void {
  registerNetwork({
    id: 'placeholder-net',
    name: 'Placeholder',
    networkId: 'splendor-placeholder',
    chainId: 'splendor-quantum-1',
    rpcUrls: ['https://rpc.splendor.example'],
    gatewayUrl: '',
    gatewayPort: 443,
    wasmUrl: '/sqc_signer.wasm',
    execUrl: '/wasm_exec.js',
    wasmSha256: '',
    execSha256: '',
    timeoutMs: 20_000,
    retries: 3,
  });
}

describe('baked-in networks point at the live tiers', () => {
  // The shipped config used to carry `https://rpc.splendor.example` for mainnet,
  // so every default-configured client refused to resolve it. Each public tier
  // must now name its real RPC, its real chain id, and be selectable.
  const tiers: Record<string, { rpc: string; chainId: string; explorer: string }> = {
    devnet: {
      rpc: 'https://quantum-rpc-devnet.splendor.org',
      chainId: 'splendor-quantum-devnet-1',
      explorer: 'https://quantum-devnet-explorer.splendor.org',
    },
    testnet: {
      rpc: 'https://quantum-rpc-testnet.splendor.org',
      chainId: 'splendor-quantum-testnet-1',
      explorer: 'https://quantum-testnet-explorer.splendor.org',
    },
    mainnet: {
      rpc: 'https://quantum-rpc.splendor.org',
      chainId: 'splendor-quantum-1',
      explorer: 'https://quantum-explorer.splendor.org',
    },
  };

  for (const [id, want] of Object.entries(tiers)) {
    it(`${id} resolves without overrides`, () => {
      const net = resolveNetwork(id);
      expect(net.rpcUrls).toEqual([want.rpc]);
      expect(net.chainId).toBe(want.chainId);
      expect(net.explorerUrl).toBe(want.explorer);
      expect(isPlaceholderNetwork(id)).toBe(false);
    });
  }

  it('no baked-in network carries a placeholder RPC', () => {
    for (const n of listNetworks()) {
      expect(isPlaceholderNetwork(n), `${n.id} still uses a placeholder RPC`).toBe(false);
    }
  });

  it('public tiers ship no gateway URL until one is verified', () => {
    // The report's rule: do not assume the RPC host also serves the gateway.
    for (const id of Object.keys(tiers)) {
      expect(getNetwork(id)?.gatewayUrl).toBe('');
    }
  });

  it('every network keeps a distinct chain id per tier', () => {
    const chainIds = ['devnet', 'testnet', 'mainnet'].map((id) => getNetwork(id)!.chainId);
    expect(new Set(chainIds).size).toBe(chainIds.length);
  });
});

describe('isPlaceholderNetwork / listSelectableNetworks', () => {
  it('detects placeholder URLs on a network that still has them', () => {
    registerPlaceholder();
    const net = getNetwork('placeholder-net');
    expect(net).toBeTruthy();
    expect(isPlaceholderNetwork(net!)).toBe(true);
    expect(isPlaceholderNetwork('placeholder-net')).toBe(true);
  });

  it('local is not treated as placeholder for picker', () => {
    const local = getNetwork('local');
    expect(local).toBeTruthy();
    expect(isPlaceholderNetwork(local!)).toBe(false);
  });

  it('listSelectableNetworks excludes placeholders by default', () => {
    registerPlaceholder();
    const selectable = listSelectableNetworks();
    expect(selectable.every((n) => n.id === 'local' || !isPlaceholderNetwork(n))).toBe(true);
    expect(selectable.some((n) => n.id === 'local')).toBe(true);
    expect(selectable.some((n) => n.id === 'mainnet')).toBe(true);
    expect(selectable.some((n) => n.id === 'placeholder-net')).toBe(false);
  });

  it('listSelectableNetworks can include placeholders', () => {
    registerPlaceholder();
    const all = listSelectableNetworks({ allowPlaceholder: true });
    expect(all.length).toBe(listNetworks().length);
  });
});

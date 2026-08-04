/**
 * Browser MetaMask helpers for PQ bridge derivation.
 */
import { DERIVATION_DOMAIN_MESSAGE, strip0x } from './derive.js';

export type EthereumProvider = {
  request: (args: { method: string; params?: unknown[] }) => Promise<unknown>;
  on?: (event: string, handler: (...args: unknown[]) => void) => void;
  removeListener?: (event: string, handler: (...args: unknown[]) => void) => void;
};

function getProvider(): EthereumProvider {
  const eth = (globalThis as any).ethereum as EthereumProvider | undefined;
  if (!eth?.request) {
    throw new Error('MetaMask (window.ethereum) not found');
  }
  return eth;
}

/** Request accounts and return the first address. */
export async function connectMetaMask(): Promise<string> {
  const eth = getProvider();
  const accounts = (await eth.request({ method: 'eth_requestAccounts' })) as string[];
  if (!accounts?.length) throw new Error('No MetaMask accounts');
  return accounts[0];
}

/**
 * personal_sign the PQ derivation domain message.
 * Returns hex signature without requiring a specific chain.
 */
export async function signDerivationWithMetaMask(address?: string): Promise<{
  evmAddress: string;
  signatureHex: string;
}> {
  const eth = getProvider();
  const evmAddress = address || (await connectMetaMask());
  const msgHex =
    '0x' +
    Array.from(new TextEncoder().encode(DERIVATION_DOMAIN_MESSAGE))
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('');
  const signature = (await eth.request({
    method: 'personal_sign',
    params: [msgHex, evmAddress],
  })) as string;
  return { evmAddress, signatureHex: strip0x(signature) };
}

/**
 * Subscribe to MetaMask account switches. Returns an unsubscribe function.
 */
export function onMetaMaskAccountsChanged(
  handler: (accounts: string[]) => void,
): () => void {
  const eth = getProvider();
  if (!eth.on) {
    return () => undefined;
  }
  const listener = (accounts: unknown) => {
    handler(Array.isArray(accounts) ? (accounts as string[]) : []);
  };
  eth.on('accountsChanged', listener);
  return () => {
    eth.removeListener?.('accountsChanged', listener);
  };
}

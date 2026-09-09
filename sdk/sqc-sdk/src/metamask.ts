/**
 * Browser MetaMask / EIP-1193 helpers for PQ bridge derivation.
 *
 * The derivation signature is ML-DSA-65 key material (SQDP-1) — never log it
 * or post it to a public gateway.
 */
import {
  DERIVATION_DOMAIN_MESSAGE,
  eip712DerivationTypedData,
  strip0x,
  type DerivationScheme,
} from './derive.js';
import { BridgeError } from './errors.js';

export type EthereumProvider = {
  request: (args: { method: string; params?: unknown[] }) => Promise<unknown>;
  on?: (event: string, handler: (...args: unknown[]) => void) => void;
  removeListener?: (event: string, handler: (...args: unknown[]) => void) => void;
};

export type SignDerivationOpts = {
  address?: string;
  /** Injected EIP-1193 provider; defaults to window.ethereum */
  provider?: EthereumProvider;
  /** Default eip191-v1 (SQDP-1). eip712-v1 is optional SQDP-1.1 path. */
  scheme?: DerivationScheme;
};

function getProvider(explicit?: EthereumProvider): EthereumProvider {
  if (explicit?.request) return explicit;
  const eth = (globalThis as { ethereum?: EthereumProvider }).ethereum;
  if (!eth?.request) {
    throw BridgeError.noProvider();
  }
  return eth;
}

/** Request accounts and return the first address. */
export async function connectMetaMask(provider?: EthereumProvider): Promise<string> {
  const eth = getProvider(provider);
  const accounts = (await eth.request({ method: 'eth_requestAccounts' })) as string[];
  if (!accounts?.length) throw BridgeError.noProvider('No MetaMask accounts');
  return accounts[0];
}

/**
 * Sign the PQ derivation domain (EIP-191 personal_sign or EIP-712).
 * Returns hex signature without requiring a specific EVM chain.
 */
export async function signDerivationWithMetaMask(
  addressOrOpts?: string | SignDerivationOpts,
): Promise<{
  evmAddress: string;
  signatureHex: string;
  scheme: DerivationScheme;
}> {
  const opts: SignDerivationOpts =
    typeof addressOrOpts === 'string' ? { address: addressOrOpts } : addressOrOpts || {};
  const scheme: DerivationScheme = opts.scheme || 'eip191-v1';
  const eth = getProvider(opts.provider);
  const evmAddress = opts.address || (await connectMetaMask(eth));

  let signature: string;
  if (scheme === 'eip712-v1') {
    const typed = eip712DerivationTypedData();
    signature = (await eth.request({
      method: 'eth_signTypedData_v4',
      params: [evmAddress, JSON.stringify(typed)],
    })) as string;
  } else {
    const msgHex =
      '0x' +
      Array.from(new TextEncoder().encode(DERIVATION_DOMAIN_MESSAGE))
        .map((b) => b.toString(16).padStart(2, '0'))
        .join('');
    signature = (await eth.request({
      method: 'personal_sign',
      params: [msgHex, evmAddress],
    })) as string;
  }

  return { evmAddress, signatureHex: strip0x(signature), scheme };
}

/**
 * Subscribe to MetaMask account switches. Returns an unsubscribe function.
 */
export function onMetaMaskAccountsChanged(
  handler: (accounts: string[]) => void,
  provider?: EthereumProvider,
): () => void {
  const eth = getProvider(provider);
  if (!eth.on) {
    throw BridgeError.noProvider(
      'EIP-1193 provider lacks accountsChanged events — cannot safely watch account switches',
    );
  }
  const listener = (accounts: unknown) => {
    handler(Array.isArray(accounts) ? (accounts as string[]) : []);
  };
  eth.on('accountsChanged', listener);
  return () => {
    eth.removeListener?.('accountsChanged', listener);
  };
}

/**
 * Subscribe to EIP-1193 provider disconnect. Returns an unsubscribe function.
 */
export function onMetaMaskDisconnect(
  handler: () => void,
  provider?: EthereumProvider,
): () => void {
  const eth = getProvider(provider);
  if (!eth.on) {
    throw BridgeError.noProvider(
      'EIP-1193 provider lacks disconnect events — cannot safely watch provider lifecycle',
    );
  }
  const listener = () => {
    handler();
  };
  eth.on('disconnect', listener);
  return () => {
    eth.removeListener?.('disconnect', listener);
  };
}

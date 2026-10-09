import type { NetworkId } from '@splendor/dapp-spec';

/** Base URL of apps/playground-api. Same-origin `/api` by default (proxied in dev). */
export const API_URL = (import.meta.env.VITE_PLAYGROUND_API_URL ?? '/api').replace(/\/+$/, '');

/**
 * Versions pinned in exported projects. They are the versions bundled into this playground, so
 * an exported dApp behaves exactly like its preview.
 */
export const PACKAGE_VERSIONS = {
  sqcSdk: import.meta.env.VITE_SQC_SDK_VERSION,
  connectReact: import.meta.env.VITE_CONNECT_REACT_VERSION,
};

export const DEFAULT_NETWORK: NetworkId =
  (import.meta.env.VITE_PLAYGROUND_DEFAULT_NETWORK as NetworkId | undefined) ?? 'testnet';

/** Networks offered in the playground UI. Mainnet is allowed for live mode but never the default. */
export const PLAYGROUND_NETWORKS: { id: NetworkId; label: string }[] = [
  { id: 'testnet', label: 'Testnet' },
  { id: 'devnet', label: 'Devnet' },
  { id: 'mainnet', label: 'Mainnet (real SPLD)' },
  { id: 'local', label: 'Local node' },
];

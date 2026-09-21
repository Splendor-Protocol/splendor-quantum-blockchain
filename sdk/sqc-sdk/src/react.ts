/**
 * React hook for Splendor PQ bridge (SQDP-1) — lower-level, no UI.
 *
 * @deprecated Prefer `@splendor/connect-react` (`SplendorConnectProvider` / ConnectKit)
 * for dApps. Peer-depend on react: `import { useSplendorPQ } from '@splendor/sqc-sdk/react'`
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { createSplendorClient } from './connect.js';
import type { ClientSession, CreateClientOptions } from './connect.js';
import { BridgeError } from './errors.js';
import type { BridgeErrorCode } from './errors.js';
import type { ConnectionState, DeriveInfo } from './types.js';

export type UseSplendorPQOpts = CreateClientOptions & {
  /** Optional seed for connect(); when omitted, connect() requires an explicit seed arg. */
  seed?: string;
};

/** @deprecated Prefer `@splendor/connect-react` ConnectKit hooks. */
export function useSplendorPQ(opts: UseSplendorPQOpts = {}) {
  const [client, setClient] = useState<ClientSession | null>(null);
  const [state, setState] = useState<ConnectionState | 'locked'>('disconnected');
  const [info, setInfo] = useState<DeriveInfo | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const optsKey = JSON.stringify({
    network: opts.network || 'local',
    networkConfigId: opts.networkConfig?.id,
    wasmUrl: opts.wasmUrl,
    execUrl: opts.execUrl,
    wasmSha256: opts.wasmSha256,
    execSha256: opts.execSha256,
    rpcUrls: opts.rpcUrls,
    gatewayUrl: opts.gatewayUrl,
    chainId: opts.chainId,
    timeoutMs: opts.timeoutMs,
    retries: opts.retries,
    allowUnpinnedWasm: opts.allowUnpinnedWasm === true,
    allowInsecureHttp: opts.allowInsecureHttp === true,
    sessionIdleMs: opts.sessionIdleMs,
    lockOnHidden: opts.lockOnHidden === true,
  });
  const optsRef = useRef(opts);
  optsRef.current = opts;

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const next = createSplendorClient(optsRef.current);
    setClient(next);
    setInfo(null);
    setState('disconnected');
    return () => {
      next.wallet.disconnect();
    };
  }, [optsKey]);

  useEffect(() => {
    if (!client) return;
    const { wallet } = client;
    const offConnect = wallet.on('connect', () => {
      setInfo(wallet.getDeriveInfo());
      setState('connected');
    });
    const offChanged = wallet.on('accountsChanged', () => {
      setInfo(wallet.getDeriveInfo());
      setState(wallet.getConnectionState());
    });
    const offLock = wallet.on('lock', () => {
      setInfo(null);
      setState('locked');
    });
    const offDisc = wallet.on('disconnect', () => {
      setInfo(null);
      setState('disconnected');
    });
    const offErr = wallet.on('error', (payload) => {
      setError(new BridgeError((payload.code as BridgeErrorCode) || 'UNKNOWN', payload.message));
      setInfo(null);
      setState('disconnected');
    });
    return () => {
      offConnect();
      offChanged();
      offLock();
      offDisc();
      offErr();
    };
  }, [client]);

  const connect = useCallback(
    async (seed?: string) => {
      if (!client) throw new Error('Splendor client not ready (SSR or initializing)');
      const seedHex = seed ?? optsRef.current.seed;
      if (!seedHex) throw BridgeError.notConnected('useSplendorPQ.connect requires a seed');
      setError(null);
      try {
        const derived = await (client.wallet as import('./splendor-wallet.js').SplendorPQBridge).connectFromSeed(
          seedHex,
        );
        setInfo(derived);
        setState('connected');
        return derived;
      } catch (e) {
        setError(e instanceof Error ? e : new Error(String(e)));
        throw e;
      }
    },
    [client],
  );

  const disconnect = useCallback(() => {
    client?.wallet.disconnect();
    setInfo(null);
    setState('disconnected');
  }, [client]);

  return {
    network: client?.network ?? null,
    provider: client?.provider ?? null,
    wallet: client?.wallet ?? null,
    connect,
    disconnect,
    state,
    info,
    address: info?.address ?? null,
    evmAddress: info?.evmAddress ?? null,
    error,
    ready: !!client,
  };
}

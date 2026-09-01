/**
 * React hook for Splendor PQ bridge (SQDP-1) — lower-level, no UI.
 * Prefer `@splendor/connect-react` (ConnectKit / SplendorConnectProvider) for dApps.
 * Peer-depend on react. Import: `import { useSplendorPQ } from '@splendor/pq-bridge/react'`
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { createSplendorClient } from './connect.js';
import type { SplendorClient, CreateClientOptions } from './connect.js';
import { BridgeError } from './errors.js';
import type { BridgeErrorCode } from './errors.js';
import type { ConnectionState, DeriveInfo } from './types.js';

export type UseSplendorPQOpts = CreateClientOptions;

export function useSplendorPQ(opts: UseSplendorPQOpts = {}) {
  const [client, setClient] = useState<SplendorClient | null>(null);
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
    derivation: opts.derivation || 'eip191-v1',
    allowUnpinnedWasm: opts.allowUnpinnedWasm === true,
    allowInsecureHttp: opts.allowInsecureHttp === true,
    sessionIdleMs: opts.sessionIdleMs,
    lockOnHidden: opts.lockOnHidden === true,
    watchMetaMaskAccounts: opts.watchMetaMaskAccounts,
  });
  const optsRef = useRef(opts);
  optsRef.current = opts;

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const createOpts: CreateClientOptions = {
      ...optsRef.current,
      ethereumProvider: optsRef.current.ethereumProvider,
    };
    const next = createSplendorClient(createOpts);
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

  const connect = useCallback(async () => {
    if (!client) throw new Error('Splendor client not ready (SSR or initializing)');
    setError(null);
    try {
      const derived = await client.wallet.connectFromMetaMask(optsRef.current.ethereumProvider);
      setInfo(derived);
      setState('connected');
      return derived;
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
      throw e;
    }
  }, [client]);

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

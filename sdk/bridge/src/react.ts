/**
 * React hook for Splendor PQ bridge (SQDP-1).
 * Peer-depend on react. Import: `import { useSplendorPQ } from '@splendor/pq-bridge/react'`
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { createSplendorClient } from './connect.js';
import type { SplendorClient, CreateClientOptions } from './connect.js';
import type { NetworkId } from './networks.js';
import type { ConnectionState, DeriveInfo } from './types.js';
import type { DerivationScheme } from './derive.js';
import type { EthereumProvider } from './metamask.js';

export type UseSplendorPQOpts = {
  network?: NetworkId;
  wasmUrl?: string;
  execUrl?: string;
  wasmSha256?: string;
  ethereumProvider?: EthereumProvider;
  derivation?: DerivationScheme;
};

export function useSplendorPQ(opts: UseSplendorPQOpts = {}) {
  const [client, setClient] = useState<SplendorClient | null>(null);
  const [state, setState] = useState<ConnectionState>('disconnected');
  const [info, setInfo] = useState<DeriveInfo | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const optsKey = JSON.stringify({
    network: opts.network || 'local',
    wasmUrl: opts.wasmUrl,
    execUrl: opts.execUrl,
    wasmSha256: opts.wasmSha256,
    derivation: opts.derivation || 'eip191-v1',
  });
  const providerRef = useRef(opts.ethereumProvider);
  providerRef.current = opts.ethereumProvider;

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const parsed = JSON.parse(optsKey) as UseSplendorPQOpts;
    const createOpts: CreateClientOptions = {
      network: parsed.network,
      wasmUrl: parsed.wasmUrl,
      execUrl: parsed.execUrl,
      wasmSha256: parsed.wasmSha256,
      derivation: parsed.derivation,
      ethereumProvider: providerRef.current,
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
    const offDisc = wallet.on('disconnect', () => {
      setInfo(null);
      setState('disconnected');
    });
    const offErr = wallet.on('error', (payload) => {
      setError(new Error(payload.message));
      setInfo(null);
      setState('disconnected');
    });
    return () => {
      offConnect();
      offChanged();
      offDisc();
      offErr();
    };
  }, [client]);

  const connect = useCallback(async () => {
    if (!client) throw new Error('Splendor client not ready (SSR or initializing)');
    setError(null);
    try {
      const derived = await client.wallet.connectFromMetaMask(providerRef.current);
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

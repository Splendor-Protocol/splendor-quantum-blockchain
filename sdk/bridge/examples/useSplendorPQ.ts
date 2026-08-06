/**
 * Sketch: React hook for Splendor PQ bridge (SQDP-1 one-touch).
 * Copy into your app; peer-depend on react.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  createSplendorClient,
  type NetworkId,
  type ConnectionState,
  type DeriveInfo,
  type SplendorClient,
} from '@splendor/pq-bridge/browser';

export type UseSplendorPQOpts = {
  network?: NetworkId;
  wasmUrl?: string;
  execUrl?: string;
};

export function useSplendorPQ(opts: UseSplendorPQOpts = {}) {
  const clientRef = useRef<SplendorClient | null>(null);
  if (!clientRef.current) {
    clientRef.current = createSplendorClient({
      network: opts.network || 'local',
      wasmUrl: opts.wasmUrl,
      execUrl: opts.execUrl,
    });
  }
  const { provider, wallet, network } = clientRef.current;

  const [state, setState] = useState<ConnectionState>('disconnected');
  const [info, setInfo] = useState<DeriveInfo | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
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
    return () => {
      offConnect();
      offChanged();
      offDisc();
    };
  }, [wallet]);

  const connect = useCallback(async () => {
    setError(null);
    try {
      const derived = await wallet.connectFromMetaMask();
      setInfo(derived);
      setState('connected');
      return derived;
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
      throw e;
    }
  }, [wallet]);

  const disconnect = useCallback(() => {
    wallet.disconnect();
    setInfo(null);
    setState('disconnected');
  }, [wallet]);

  return {
    network,
    provider,
    wallet,
    connect,
    disconnect,
    state,
    info,
    address: info?.address ?? null,
    evmAddress: info?.evmAddress ?? null,
    error,
  };
}

/**
 * Sketch: React hook for Splendor PQ bridge.
 * Copy into your app; peer-depend on react. Not published as a separate package.
 *
 * Usage:
 *   const { provider, wallet, connect, disconnect, address, state } = useSplendorPQ({
 *     rpcUrls: ['https://rpc.example'],
 *     wasmUrl: '/sqc_signer.wasm',
 *     execUrl: '/wasm_exec.js',
 *   });
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  SqcProvider,
  SplendorPQBridge,
  type BridgeOptions,
  type ConnectionState,
  type DeriveInfo,
} from '@splendor/pq-bridge/browser';

export type UseSplendorPQOpts = BridgeOptions;

export function useSplendorPQ(opts: UseSplendorPQOpts = {}) {
  const providerRef = useRef<SqcProvider | null>(null);
  if (!providerRef.current) {
    providerRef.current = new SqcProvider(opts);
  }
  const provider = providerRef.current;

  const walletRef = useRef<SplendorPQBridge | null>(null);
  if (!walletRef.current) {
    walletRef.current = SplendorPQBridge.fromProvider(provider, opts);
  }
  const wallet = walletRef.current;

  const [state, setState] = useState<ConnectionState>('disconnected');
  const [info, setInfo] = useState<DeriveInfo | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    const offConnect = wallet.on('connect', (p) => {
      setInfo(wallet.getDeriveInfo());
      setState('connected');
      void p;
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

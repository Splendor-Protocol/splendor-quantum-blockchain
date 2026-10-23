import { useCallback, useEffect, useRef, useState } from 'react';
import type { WriteOpts } from '@splendor/sqc-sdk';
import { connectErrorMessage } from '../../core/controller.js';
import { useConnectState } from '../context.js';
import { notReady } from '../hookErrors.js';
import { useVisiblePolling } from '../usePageVisible.js';
import { onSharedReadsInvalidated, sharedRead } from '../../core/readCache.js';
import { rememberDeployedToken } from '../../core/recentTokens.js';
import { useRecordPendingTx, useSingleFlight, useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useWallet, useAccount, useReadConnection } from './core.js';

export type { TxHookStatus } from '../useTxAction.js';

let tokensModule: Promise<typeof import('@splendor/sqc-sdk/tokens')> | null = null;
/** One shared lazy import of the token module. */
export function loadTokens(): Promise<typeof import('@splendor/sqc-sdk/tokens')> {
  tokensModule ??= import('@splendor/sqc-sdk/tokens').catch((e) => {
    tokensModule = null;
    throw e;
  });
  return tokensModule;
}

/** Token decimals when they are an integer in 0..36, else null. */
export function validDecimals(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 && v <= 36 ? v : null;
}

export function useQRC20(tokenAddress: string | null, pollMs = 12_000): {
  balance: string | null;
  tokenInfo: Record<string, unknown> | null;
  refresh: () => Promise<void>;
  transfer: (to: string, amount: string, opts?: WriteOpts) => Promise<{ hash?: string }>;
  /** Human-decimal transfer (uses on-chain decimals). `opts.fee` is in atto-SPLD. */
  transferHuman: (to: string, amountHuman: string, opts?: WriteOpts) => Promise<{ hash?: string }>;
  approve: (spender: string, amount: string) => Promise<{ hash?: string }>;
  mint: (to: string, amount: string) => Promise<{ hash?: string }>;
  pause: () => Promise<{ hash?: string }>;
  unpause: () => Promise<{ hash?: string }>;
  submitPermit: (signed: {
    token: string;
    owner: string;
    spender: string;
    amount: string;
    deadline: number;
    signature: number[];
    owner_pubkey: number[];
  }) => Promise<{ hash?: string }>;
  loading: boolean;
  /** Token decimals once `tokenInfo` has loaded and they are valid (0–36); null until then. */
  decimals: number | null;
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  /** The last transaction error, else the last read error. */
  error: Error | null;
  /** UI copy for `error`. */
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const readConnection = useReadConnection();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);
  const [balance, setBalance] = useState<string | null>(null);
  const [tokenInfo, setTokenInfo] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(false);
  const [readError, setReadError] = useState<Error | null>(null);
  const { address } = useAccount();
  /** Bumped per read; results for an older token/account/network are dropped. */
  const generation = useRef(0);

  useEffect(() => {
    generation.current += 1;
    setBalance(null);
    setTokenInfo(null);
    setReadError(null);
  }, [tokenAddress, network?.id]);

  useEffect(() => {
    setBalance(null);
  }, [address]);

  const load = useCallback(async (force: boolean) => {
    const gen = ++generation.current;
    if (!tokenAddress || !readConnection) {
      setBalance(null);
      setTokenInfo(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    setReadError(null);
    try {
      const { QRC20 } = await loadTokens();
      const token = new QRC20(readConnection, tokenAddress);
      const cacheOpts = { maxAgeMs: Math.min(pollMs / 2, 5_000), force };
      const [info, bal] = await Promise.all([
        sharedRead(readConnection, `qrc20-info|${tokenAddress}`, () => token.tokenInfo(), cacheOpts),
        address
          ? sharedRead(readConnection, `qrc20-bal|${tokenAddress}|${address}`, () => token.balanceOf(address), cacheOpts)
          : Promise.resolve(null),
      ]);
      if (gen !== generation.current) return;
      setTokenInfo(info as Record<string, unknown>);
      setBalance(bal);
    } catch (e) {
      if (gen !== generation.current) return;
      setReadError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (gen === generation.current) setLoading(false);
    }
  }, [tokenAddress, readConnection, address, pollMs]);

  const refresh = useCallback(() => load(true), [load]);

  useEffect(() => {
    void load(false);
  }, [load]);
  useVisiblePolling(() => void load(false), pollMs, !!tokenAddress);

  useEffect(() => {
    if (!tokenAddress || !readConnection || !address) return;
    const key = `qrc20-bal|${tokenAddress}|${address}`;
    return onSharedReadsInvalidated(readConnection, (prefix) => {
      if (key.startsWith(prefix)) void load(false);
    });
  }, [load, tokenAddress, readConnection, address]);

  const decimals = validDecimals(tokenInfo?.decimals);
  const symbol = typeof tokenInfo?.symbol === 'string' ? tokenInfo.symbol : null;

  const transfer = useCallback(
    async (to: string, amount: string, opts?: WriteOpts) => {
      if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
      const res = await tx.run(async () => {
        const { QRC20 } = await loadTokens();
        return new QRC20(wallet.connection, tokenAddress).transfer(wallet, to, amount, opts);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  const transferHuman = useCallback(
    async (to: string, amountHuman: string, opts?: WriteOpts) => {
      if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
      const res = await tx.run(async () => {
        const { QRC20 } = await loadTokens();
        const token = new QRC20(wallet.connection, tokenAddress);
        // Sign with the decimals the user saw; refuse if the chain now reports others.
        if (decimals != null) token.pinDecimals(decimals);
        return token.transferHuman(wallet, to, amountHuman, opts);
      }, { kind: 'token', summary: `Send ${amountHuman} ${symbol ?? 'tokens'} to ${to}` });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, decimals, symbol],
  );

  const approve = useCallback(
    async (spender: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
      const res = await tx.run(async () => {
        const { QRC20 } = await loadTokens();
        return new QRC20(wallet.connection, tokenAddress).approve(wallet, spender, amount);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  const mint = useCallback(
    async (to: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
      const res = await tx.run(async () => {
        const { QRC20 } = await loadTokens();
        return new QRC20(wallet.connection, tokenAddress).mint(wallet, to, amount);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  const pause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
    const res = await tx.run(async () => {
      const { QRC20 } = await loadTokens();
      return new QRC20(wallet.connection, tokenAddress).pause(wallet);
    });
    return { hash: res.hash };
  }, [ready, wallet, tokenAddress, tx]);

  const unpause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
    const res = await tx.run(async () => {
      const { QRC20 } = await loadTokens();
      return new QRC20(wallet.connection, tokenAddress).unpause(wallet);
    });
    return { hash: res.hash };
  }, [ready, wallet, tokenAddress, tx]);

  const submitPermit = useCallback(
    async (signed: {
      token: string;
      owner: string;
      spender: string;
      amount: string;
      deadline: number;
      signature: number[];
      owner_pubkey: number[];
    }) => {
      if (!ready || !wallet || !tokenAddress) throw notReady('Wallet or token not ready', ready && wallet ? 'token' : null);
      const res = await tx.run(async () => {
        const { QRC20 } = await loadTokens();
        return new QRC20(wallet.connection, tokenAddress).submitPermit(wallet, signed as never);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  return {
    balance,
    tokenInfo,
    refresh,
    transfer,
    transferHuman,
    approve,
    mint,
    pause,
    unpause,
    submitPermit,
    loading,
    decimals,
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error ?? readError,
    errorMessage: tx.error ? tx.errorMessage : readError ? connectErrorMessage(readError) : null,
    reset: tx.reset,
  };
}

export function useDeployToken(): {
  deploy: (params: {
    name: string;
    symbol: string;
    decimals: number;
    totalSupply: string;
    maxSupply?: string;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ address: string; hash?: string; confirmed: boolean }>;
  status: TxHookStatus;
  address: string | null;
  hash: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [address, setAddress] = useState<string | null>(null);
  const [hash, setHash] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setAddress(null);
    setHash(null);
    setError(null);
  }, []);

  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();
  const networkId = useConnectState((s) => s.network?.id ?? null);
  const deploy = useCallback(
    (params: {
      name: string;
      symbol: string;
      decimals: number;
      totalSupply: string;
      maxSupply?: string;
      fee?: string;
      waitForConfirmation?: boolean;
    }) =>
      singleFlight(async () => {
        if (!ready || !wallet) throw notReady('Wallet not connected');
        setStatus('pending');
        setError(null);
        try {
          const { deployQRC20 } = await loadTokens();
          const { address: tokenAddr, result } = await deployQRC20(wallet, params, {
            fee: params.fee,
            waitForConfirmation: params.waitForConfirmation,
          });
          setAddress(tokenAddr);
          setHash(result.hash ?? null);
          setStatus('success');
          recordPending(result, { kind: 'deploy', summary: `Deploy token ${params.symbol} (${tokenAddr})` });
          if (networkId) rememberDeployedToken({ address: tokenAddr, symbol: params.symbol, network: networkId });
          return { address: tokenAddr, hash: result.hash, confirmed: result.confirmation?.confirmed === true };
        } catch (e) {
          const err = e instanceof Error ? e : new Error(String(e));
          setError(err);
          setStatus('error');
          throw err;
        }
      }),
    [wallet, ready, singleFlight, recordPending, networkId],
  );

  return { deploy, status, address, hash, error, reset };
}


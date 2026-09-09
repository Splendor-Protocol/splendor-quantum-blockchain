import { useCallback, useEffect, useMemo, useState } from 'react';
import { useConnectState } from '../context.js';
import { useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useWallet, useAccount } from './core.js';

export type { TxHookStatus } from '../useTxAction.js';
export function useQRC20(tokenAddress: string | null, pollMs = 12_000): {
  balance: string | null;
  tokenInfo: Record<string, unknown> | null;
  refresh: () => Promise<void>;
  transfer: (to: string, amount: string) => Promise<{ hash?: string }>;
  /** Human-decimal transfer (uses on-chain decimals). */
  transferHuman: (to: string, amountHuman: string) => Promise<{ hash?: string }>;
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
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);
  const [balance, setBalance] = useState<string | null>(null);
  const [tokenInfo, setTokenInfo] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(false);
  const [readError, setReadError] = useState<Error | null>(null);
  const { address } = useAccount();

  const refresh = useCallback(async () => {
    if (!tokenAddress || !ready || !wallet) {
      setBalance(null);
      setTokenInfo(null);
      return;
    }
    setLoading(true);
    setReadError(null);
    try {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      const token = new QRC20(wallet.connection, tokenAddress);
      if (address) {
        const bal = await token.balanceOf(address);
        setBalance(bal);
      }
      const info = await token.tokenInfo();
      setTokenInfo(info as Record<string, unknown>);
    } catch (e) {
      setReadError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setLoading(false);
    }
  }, [tokenAddress, ready, wallet, address]);

  useEffect(() => {
    void refresh();
    if (!tokenAddress || !pollMs) return;
    const id = setInterval(() => void refresh(), pollMs);
    return () => clearInterval(id);
  }, [refresh, tokenAddress, pollMs]);

  const transfer = useCallback(
    async (to: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).transfer(wallet, to, amount);
      });
      await refresh();
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, refresh],
  );

  const transferHuman = useCallback(
    async (to: string, amountHuman: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).transferHuman(wallet, to, amountHuman);
      });
      await refresh();
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, refresh],
  );

  const approve = useCallback(
    async (spender: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).approve(wallet, spender, amount);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  const mint = useCallback(
    async (to: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).mint(wallet, to, amount);
      });
      await refresh();
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, refresh],
  );

  const pause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
    const res = await tx.run(async () => {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC20(wallet.connection, tokenAddress).pause(wallet);
    });
    return { hash: res.hash };
  }, [ready, wallet, tokenAddress, tx]);

  const unpause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
    const res = await tx.run(async () => {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
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
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
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
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error ?? readError,
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
  }) => Promise<{ address: string; hash?: string }>;
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

  const deploy = useCallback(
    async (params: {
      name: string;
      symbol: string;
      decimals: number;
      totalSupply: string;
      maxSupply?: string;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployQRC20 } = await import('@splendor/sqc-sdk/tokens');
        const { address: tokenAddr, result } = await deployQRC20(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
        setAddress(tokenAddr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: tokenAddr, hash: result.hash };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { deploy, status, address, hash, error, reset };
}


import { useCallback, useState } from 'react';
import { createAuthMessage, type AuthMessageParams, type SignedAuth } from '../auth.js';
import { useAccount, useNetwork, useWallet } from './core.js';
import { connectErrorMessage } from '../../core/controller.js';

export type UseSignInResult = {
  signIn: (params: Omit<AuthMessageParams, 'address' | 'chainId'> & Partial<AuthMessageParams>) => Promise<SignedAuth>;
  status: 'idle' | 'pending' | 'success' | 'error';
  result: SignedAuth | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
};

/**
 * Native ML-DSA Sign-In over `wallet.signMessage` (domain-separated Connect v2 message).
 * Not Ethereum SIWE — verify on the server with the WASM / SDK verify path.
 */
export function useSignIn(): UseSignInResult {
  const { wallet, ready } = useWallet();
  const { address } = useAccount();
  const { chainId } = useNetwork();
  const [status, setStatus] = useState<UseSignInResult['status']>('idle');
  const [result, setResult] = useState<SignedAuth | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setResult(null);
    setError(null);
  }, []);

  const signIn = useCallback(
    async (params: Omit<AuthMessageParams, 'address' | 'chainId'> & Partial<AuthMessageParams>) => {
      if (!ready || !wallet || !address) {
        throw new Error('Wallet not connected');
      }
      setStatus('pending');
      setError(null);
      try {
        const message = createAuthMessage({
          ...params,
          address: params.address ?? address,
          chainId: params.chainId ?? chainId ?? undefined,
        });
        const signed = await wallet.signMessage(message);
        const out: SignedAuth = {
          message: signed.message,
          signatureB64: signed.signatureB64,
          publicKeyB64: signed.publicKeyB64,
          address,
          messageDomain: 'Splendor Connect v2',
        };
        setResult(out);
        setStatus('success');
        return out;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready, address, chainId],
  );

  return {
    signIn,
    status,
    result,
    error,
    errorMessage: error ? connectErrorMessage(error) : null,
    reset,
  };
}

export { createAuthMessage };
export type { AuthMessageParams, SignedAuth };

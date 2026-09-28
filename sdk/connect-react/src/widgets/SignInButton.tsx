import { useState } from 'react';
import { useAccount, useConnect, useNetwork, useSignIn } from '../react/hooks.js';
import {
  authCheckMessage,
  checkAuthMessage,
  createAuthMessage,
  type AuthMessageParams,
  type SignedAuth,
} from '../react/auth.js';

export type SignInButtonProps = {
  /** Relying-party origin put in the challenge. Defaults to `window.location.origin`. */
  domain?: string;
  /**
   * Origin the challenge must match before signing. Defaults to `window.location.origin`
   * (falls back to `domain` during SSR).
   */
  expectedDomain?: string;
  statement?: string;
  /** Server nonce, or a function that fetches one right before signing. */
  nonce?: string | (() => string | Promise<string>);
  /** Challenge lifetime in seconds. Default 300. */
  expiresInSec?: number;
  onSignedIn?: (auth: SignedAuth) => void;
  className?: string;
  label?: string;
};

function currentOrigin(): string | undefined {
  try {
    return typeof window !== 'undefined' ? window.location.origin : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Drop-in ML-DSA Sign-In. Builds a Connect v2 challenge bound to the connected SQ:
 * address and chain, checks it locally, then asks the wallet to sign.
 * The server must still verify the signature and consume the nonce.
 */
export function SignInButton({
  domain,
  expectedDomain,
  statement,
  nonce,
  expiresInSec = 300,
  onSignedIn,
  className,
  label = 'Sign in',
}: SignInButtonProps) {
  const { address, isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { chainId } = useNetwork();
  const { signIn, status, result, errorMessage, reset } = useSignIn();
  const [checkError, setCheckError] = useState<string | null>(null);

  const cls = ['sqc-widget', className].filter(Boolean).join(' ');
  const rpDomain = domain ?? currentOrigin() ?? '';
  const mustMatch = expectedDomain ?? currentOrigin() ?? rpDomain;

  if (connStatus === 'locked') {
    return (
      <div className={cls}>
        <h3 className="sqc-widget__title">Sign in</h3>
        <p className="sqc-widget__hint">Wallet locked — unlock to sign in.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Unlock
        </button>
      </div>
    );
  }

  if (!isConnected || !address) {
    return (
      <div className={cls}>
        <h3 className="sqc-widget__title">Sign in</h3>
        <p className="sqc-widget__hint">Connect a wallet to sign in.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Connect
        </button>
      </div>
    );
  }

  const onSignIn = async () => {
    reset();
    setCheckError(null);
    let signing = false;
    try {
      const resolvedNonce = typeof nonce === 'function' ? await nonce() : nonce;
      const issuedAt = Math.floor(Date.now() / 1000);
      const params: AuthMessageParams = {
        domain: rpDomain,
        statement,
        nonce: resolvedNonce || undefined,
        issuedAt,
        expirationTime: issuedAt + expiresInSec,
        address,
        chainId: chainId ?? undefined,
      };
      const check = checkAuthMessage(createAuthMessage(params), {
        domain: mustMatch,
        now: issuedAt,
      });
      if (!check.ok) {
        setCheckError(authCheckMessage(check.reason));
        return;
      }
      signing = true;
      const signed = await signIn(params);
      onSignedIn?.(signed);
    } catch (e) {
      // Wallet errors are already surfaced by useSignIn's errorMessage.
      if (!signing) setCheckError(e instanceof Error ? e.message : String(e));
    }
  };

  const shownError = checkError ?? errorMessage;

  return (
    <div className={cls}>
      <h3 className="sqc-widget__title">Sign in</h3>
      <p className="sqc-widget__hint">
        {statement ? `${statement} · ` : ''}
        {rpDomain || 'this site'}
      </p>
      <button
        type="button"
        className="sqc-btn sqc-btn--primary"
        style={{ width: '100%' }}
        disabled={status === 'pending'}
        onClick={() => void onSignIn()}
      >
        {status === 'pending' ? 'Waiting for signature…' : label}
      </button>
      {status === 'success' && result ? (
        <p className="sqc-widget__msg" role="status">
          Signed in as {result.address}
        </p>
      ) : null}
      {shownError ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {shownError}
        </p>
      ) : null}
    </div>
  );
}

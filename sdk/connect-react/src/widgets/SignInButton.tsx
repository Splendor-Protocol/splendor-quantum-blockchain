import { useState } from 'react';
import { useAccount, useNetwork, useSignIn } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import {
  authCheckMessage,
  checkAuthMessage,
  createAuthMessage,
  type AuthMessageParams,
  type SignedAuth,
} from '../react/auth.js';
import { SIGN_IN_LABELS, formatLabel, useWidgetLabels, type SignInButtonLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { toError, useStatusChange } from './parts/useWidgetEffects.js';

export type SignInButtonProps = Omit<BaseWidgetProps<SignedAuth, SignInButtonLabels>, 'toast'> & {
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
  /** @deprecated Use `onSuccess`. */
  onSignedIn?: (auth: SignedAuth) => void;
  /** Button text; same as `labels.button`. */
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
  onSuccess,
  onSignedIn,
  onError,
  onStatusChange,
  className,
  style,
  title,
  hideTitle,
  labels,
  label,
}: SignInButtonProps) {
  const l = useWidgetLabels(SIGN_IN_LABELS, labels, 'signIn');
  const { address } = useAccount();
  const { chainId } = useNetwork();
  const { signIn, status, result, errorMessage, reset } = useSignIn();
  const [checkError, setCheckError] = useState<string | null>(null);
  useStatusChange(checkError ? 'error' : status, onStatusChange);

  const rpDomain = domain ?? currentOrigin() ?? '';
  const mustMatch = expectedDomain ?? currentOrigin() ?? rpDomain;

  const onSignIn = async () => {
    if (!address) return;
    reset();
    setCheckError(null);
    let signing = false;
    let resolvedNonce: string | undefined;
    try {
      resolvedNonce = typeof nonce === 'function' ? await nonce() : nonce;
    } catch (e) {
      // The app's nonce endpoint may fail with a browser TypeError or a server body; show neither.
      setCheckError(l.nonceFailed);
      onError?.(toError(e));
      return;
    }
    try {
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
        expectedNonce: params.nonce,
        expectedAddress: address,
        expectedChainId: params.chainId,
      });
      if (!check.ok) {
        const msg = authCheckMessage(check.reason);
        setCheckError(msg);
        onError?.(new Error(msg));
        return;
      }
      signing = true;
      const signed = await signIn(params);
      onSuccess?.(signed);
      onSignedIn?.(signed);
    } catch (e) {
      // Wallet errors are already surfaced by useSignIn's errorMessage; TypeErrors here are
      // our own auth-message validation, so their text is safe to show.
      if (!signing) setCheckError(e instanceof TypeError ? e.message : connectErrorMessage(e));
      onError?.(toError(e));
    }
  };

  const shownError = checkError ?? errorMessage;

  return (
    <WidgetShell
      kind="signin"
      status={checkError ? 'error' : status}
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gateLabels={l}
    >
      <p className="sqc-widget__hint">
        {statement ? `${statement} · ` : ''}
        {rpDomain || l.thisSite}
      </p>
      <button
        type="button"
        className="sqc-btn sqc-btn--primary sqc-btn--block"
        disabled={status === 'pending'}
        onClick={() => void onSignIn()}
      >
        {status === 'pending' ? l.waiting : (label ?? l.button)}
      </button>
      {status === 'success' && result ? (
        <p className="sqc-widget__msg" role="status">
          {formatLabel(l.signedInAs, { address: result.address })}
        </p>
      ) : null}
      {shownError ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {shownError}
        </p>
      ) : null}
    </WidgetShell>
  );
}

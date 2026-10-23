import { useEffect, useState } from 'react';
import { SignInButton, WidgetShell, useAccount, type SignedAuth } from '@splendor/connect-react';

export type MembersAreaProps = {
  title?: string;
  statement?: string;
  /** Members-only text shown after a successful sign-in. */
  content?: string;
  /**
   * Your server's base URL. When set, the block fetches `GET {apiUrl}/auth/nonce` and posts the
   * signed challenge to `POST {apiUrl}/auth/verify`, which must call `verifySignIn` from
   * `@splendor/sqc-sdk/auth`. Sign out posts to `POST {apiUrl}/auth/logout`.
   * Must be https. When empty it runs a client-only demo (not access control).
   */
  apiUrl?: string;
};

/** Sign-in gate: content unlocks after an ML-DSA-65 sign-in (server-verified when apiUrl is set). */
export function MembersArea({
  title = 'Members area',
  statement = 'Sign in to the members area',
  content = 'Welcome! Only signed-in members can see this.',
  apiUrl = '',
}: MembersAreaProps) {
  const { address } = useAccount();
  const [member, setMember] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const trimmed = apiUrl.trim();
  const badUrl = trimmed !== '' && !/^https:\/\//i.test(trimmed);
  // A non-https apiUrl is never used: the block stays in demo mode and says so.
  const base = badUrl ? '' : trimmed.replace(/\/+$/, '');
  const demoNote = (
    <p className="sqc-widget__msg dapp-note">
      {badUrl ? 'apiUrl must start with https:// and is ignored. ' : ''}Demo mode: not access control. Set <code>apiUrl</code> (https) to verify
      sign-ins on your server.
    </p>
  );

  // A sign-in belongs to one address: switching or disconnecting the wallet locks the area again.
  useEffect(() => {
    setMember((m) => (m && m === address ? m : null));
    setError(null);
  }, [address]);

  const nonce = base
    ? async () => {
        const res = await fetch(`${base}/auth/nonce`, { credentials: 'include' });
        if (!res.ok) throw new Error('Could not get a sign-in challenge');
        return ((await res.json()) as { nonce: string }).nonce;
      }
    : undefined;

  const verify = async (signed: SignedAuth) => {
    setError(null);
    if (!base) {
      setMember(signed.address);
      return;
    }
    try {
      const res = await fetch(`${base}/auth/verify`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(signed),
      });
      if (res.ok) setMember(signed.address);
      else setError('The server rejected the sign-in.');
    } catch {
      setError('Could not reach the sign-in server.');
    }
  };

  const signOut = () => {
    setMember(null);
    if (base) {
      void fetch(`${base}/auth/logout`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      }).catch(() => undefined);
    }
  };

  return (
    <WidgetShell kind="members-area" title={title}>
      {member ? (
        <div data-testid="dapp-members-content">
          <p className="sqc-widget__msg">Signed in as {member.slice(0, 12)}…</p>
          <p className="sqc-widget__msg">{content}</p>
          <button type="button" className="sqc-btn sqc-btn--block" onClick={signOut}>
            Sign out
          </button>
          {!base ? demoNote : null}
        </div>
      ) : (
        <>
          <SignInButton hideTitle statement={statement} nonce={nonce} onSuccess={verify} />
          {!base ? demoNote : null}
          {error ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {error}
            </p>
          ) : null}
        </>
      )}
    </WidgetShell>
  );
}

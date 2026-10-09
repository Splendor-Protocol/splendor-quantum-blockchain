import { useState } from 'react';
import { SignInButton, WidgetShell } from '@splendor/connect-react';

export type MembersAreaProps = {
  title?: string;
  statement?: string;
  /** Members-only text shown after a successful sign-in. */
  content?: string;
  /**
   * Your server's base URL. When set, the block fetches `GET {apiUrl}/auth/nonce` and posts the
   * signed challenge to `POST {apiUrl}/auth/verify`, which must call `verifySignIn` from
   * `@splendor/sqc-sdk/auth`. When empty it runs a client-only demo (not secure).
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
  const [member, setMember] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const base = apiUrl.replace(/\/+$/, '');

  const nonce = base
    ? async () => {
        const res = await fetch(`${base}/auth/nonce`, { credentials: 'include' });
        if (!res.ok) throw new Error('Could not get a sign-in challenge');
        return ((await res.json()) as { nonce: string }).nonce;
      }
    : undefined;

  return (
    <WidgetShell kind="members-area" title={title}>
      {member ? (
        <div data-testid="dapp-members-content">
          <p className="sqc-widget__msg">Signed in as {member.slice(0, 12)}…</p>
          <p className="sqc-widget__msg">{content}</p>
        </div>
      ) : (
        <>
          <SignInButton
            hideTitle
            statement={statement}
            nonce={nonce}
            onSuccess={async (signed) => {
              setError(null);
              if (!base) {
                setMember(signed.address);
                return;
              }
              const res = await fetch(`${base}/auth/verify`, {
                method: 'POST',
                credentials: 'include',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(signed),
              });
              if (res.ok) setMember(signed.address);
              else setError('The server rejected the sign-in.');
            }}
          />
          {!base ? (
            <p className="sqc-widget__msg dapp-note">
              Demo mode: set <code>apiUrl</code> to verify sign-ins on your server.
            </p>
          ) : null}
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

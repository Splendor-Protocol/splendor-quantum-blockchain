import { useState } from 'react';
import { SignInButton, useAccount } from '@splendor/connect-react';
import { api } from '../api/client';
import { useSession } from './session';

/** Wallet sign-in against the playground API (nonce → ML-DSA signature → httpOnly session cookie). */
export function SignInPanel({ statement = 'Sign in to the Splendor dApp Playground' }: { statement?: string }) {
  const { session, apiAvailable, setSession, logout } = useSession();
  const { isConnected } = useAccount();
  const [error, setError] = useState<string | null>(null);

  if (!apiAvailable) {
    return <p className="pg-muted">The playground API is not reachable, so sign-in and cloud saves are off. Projects are still saved in this browser.</p>;
  }
  if (session) {
    return (
      <div className="pg-row">
        <span className="pg-mono pg-truncate" title={session.address}>
          {session.address}
        </span>
        {session.isAdmin ? <span className="pg-badge">admin</span> : null}
        <button type="button" className="pg-btn pg-btn-ghost" onClick={() => void logout()}>
          Sign out
        </button>
      </div>
    );
  }
  return (
    <div className="pg-stack">
      {!isConnected ? <p className="pg-muted">Connect a wallet first (top right), then sign in.</p> : null}
      <SignInButton
        hideTitle
        statement={statement}
        nonce={async () => (await api.nonce()).nonce}
        onSuccess={async (signed) => {
          setError(null);
          try {
            setSession(
              await api.verify({
                message: signed.message,
                signatureB64: signed.signatureB64,
                publicKeyB64: signed.publicKeyB64,
                address: signed.address,
              }),
            );
          } catch (e) {
            setError(e instanceof Error ? e.message : String(e));
          }
        }}
      />
      {error ? <p className="pg-error" role="alert">{error}</p> : null}
    </div>
  );
}

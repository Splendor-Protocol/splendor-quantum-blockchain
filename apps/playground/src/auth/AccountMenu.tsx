import { useEffect, useState } from 'react';
import { ModalDialog } from '../components/ModalDialog';
import { SignInPanel } from './SignInPanel';
import { useSession } from './session';

const short = (address: string) => (address.length > 14 ? `${address.slice(0, 7)}…${address.slice(-4)}` : address);

/**
 * Playground account in the header. Separate from the wallet (ConnectKit): the wallet signs, the
 * account keeps projects and share links. Signing in needs a connected wallet first.
 */
export function AccountMenu() {
  const { session, ready, apiAvailable, logout } = useSession();
  const [open, setOpen] = useState(false);
  // Signing in inside the dialog closes it.
  useEffect(() => {
    if (session) setOpen(false);
  }, [session]);

  if (!ready) return null;
  if (!apiAvailable) {
    return (
      <span className="pg-account pg-muted" role="status" data-testid="account-offline" title="The playground API is not reachable. Sign-in and cloud saves are off; projects are kept in this browser.">
        Offline: saving in this browser
      </span>
    );
  }
  if (session) {
    return (
      <span className="pg-account" data-testid="account-signed-in">
        <span className="pg-mono" title={session.address}>
          {short(session.address)}
        </span>
        {session.isAdmin ? <span className="pg-badge">admin</span> : null}
        <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void logout()}>
          Sign out
        </button>
      </span>
    );
  }
  return (
    <>
      <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="account-sign-in" onClick={() => setOpen(true)}>
        Sign in
      </button>
      {open ? (
        <ModalDialog label="Sign in to the playground" onClose={() => setOpen(false)} className="pg-modal">
          <div className="pg-modal-panel">
            <h2 style={{ margin: 0, fontSize: '1.1rem' }}>Sign in to the playground</h2>
            <p className="pg-muted" style={{ margin: 0 }}>
              Signing in keeps your projects in your account, syncs them across devices and lets you share them. Your wallet signs a
              one-time message; nothing is sent on chain.
            </p>
            <SignInPanel />
            <div className="pg-row" style={{ justifyContent: 'flex-end' }}>
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => setOpen(false)}>
                Close
              </button>
            </div>
          </div>
        </ModalDialog>
      ) : null}
    </>
  );
}

import { useEffect, useState, type ReactNode } from 'react';
import { ConnectButton, SignInButton, useAccount, type SignedAuth } from '@splendor/connect-react';

export type AccessGateProps = {
  /** `public`: always shown. `wallet`: needs a connected wallet. `signin`: needs a signed sign-in. */
  access?: 'public' | 'wallet' | 'signin';
  /** Page title, used in the prompt. */
  title?: string;
  children?: ReactNode;
};

/** Signed-in address for this tab, shared by every gated page (cleared when the wallet changes). */
const SIGNED_KEY = 'sqc-dapp-signed-in';

function readSigned(): string | null {
  try {
    return sessionStorage.getItem(SIGNED_KEY);
  } catch {
    return null;
  }
}

function writeSigned(address: string | null) {
  try {
    if (address) sessionStorage.setItem(SIGNED_KEY, address);
    else sessionStorage.removeItem(SIGNED_KEY);
  } catch {
    /* storage disabled: the sign-in lasts until the page reloads */
  }
}

/**
 * Wraps a page body. Visitors who do not meet the page's access rule see a connect or sign-in
 * panel instead. Client-side only: protect real data on your server too.
 */
export function AccessGate({ access = 'public', title = 'this page', children }: AccessGateProps) {
  const { address } = useAccount();
  const [signed, setSigned] = useState<string | null>(() => readSigned());

  useEffect(() => {
    if (signed && signed !== address) {
      setSigned(null);
      writeSigned(null);
    }
  }, [address, signed]);

  if (access === 'public') return <>{children}</>;
  if (!address) {
    return (
      <section className="sqc-widget dapp-gate" data-sqc-widget="access-gate" data-gate="wallet">
        <h2 className="dapp-gate__title">Connect your wallet</h2>
        <p className="sqc-widget__msg">Connect a Splendor wallet to open {title}.</p>
        <ConnectButton />
      </section>
    );
  }
  if (access === 'signin' && signed !== address) {
    const onSuccess = (auth: SignedAuth) => {
      setSigned(auth.address);
      writeSigned(auth.address);
    };
    return (
      <section className="sqc-widget dapp-gate" data-sqc-widget="access-gate" data-gate="signin">
        <h2 className="dapp-gate__title">Sign in to continue</h2>
        <p className="sqc-widget__msg">Sign a message with your wallet to open {title}. It costs nothing.</p>
        <SignInButton hideTitle statement={`Sign in to ${title}`} onSuccess={onSuccess} />
      </section>
    );
  }
  return <>{children}</>;
}

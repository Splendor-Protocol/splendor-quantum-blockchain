import { useState } from 'react';
import { ConnectButton, useAccount } from '@splendor/connect-react';

export function WalletBar() {
  const { address, isConnected } = useAccount();
  const [copied, setCopied] = useState(false);

  const onCopy = async () => {
    if (!address) return;
    try {
      await navigator.clipboard.writeText(address);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* ignore */
    }
  };

  return (
    <>
      <header className="app-header">
        <p className="brand">
          Splendor <span>SQC</span>
        </p>
        <ConnectButton />
      </header>
      {isConnected && address ? (
        <div className="panel">
          <h2>Your address (receive SPLD / tokens here)</h2>
          <div className="addr-row">
            <code>{address}</code>
            <button type="button" className="btn ghost" onClick={() => void onCopy()}>
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <p className="meta">Funds live on this SQ: identity — not an Ethereum 0x address.</p>
        </div>
      ) : null}
    </>
  );
}

import { useState } from 'react';
import {
  SplendorConnectProvider,
  ConnectButton,
  useAccount,
  useSendTransfer,
} from '@splendor/connect-react';

function TransferPanel() {
  const { isConnected } = useAccount();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState('0.01');

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Transfer</h2>
        <p className="msg">Connect a wallet to send SPLD on your SQ: identity.</p>
      </div>
    );
  }

  const onSend = async () => {
    reset();
    try {
      await sendTransfer({
        to: to.trim(),
        amountSPLD: amount.trim(),
        waitForConfirmation: true,
      });
    } catch {
      /* surfaced via error */
    }
  };

  return (
    <div className="panel">
      <h2>Transfer</h2>
      <label htmlFor="to">To (SQ:…)</label>
      <input id="to" value={to} onChange={(e) => setTo(e.target.value)} placeholder="SQ:…" />
      <label htmlFor="amt">Amount (SPLD)</label>
      <input id="amt" value={amount} onChange={(e) => setAmount(e.target.value)} />
      <button
        type="button"
        disabled={status === 'pending' || !to.trim()}
        onClick={() => void onSend()}
      >
        {status === 'pending' ? 'Sending…' : 'Send'}
      </button>
      {hash ? (
        <p className="msg">
          {confirmed ? 'Confirmed' : 'Submitted'}: {hash}
          {explorerUrl ? (
            <>
              {' · '}
              <a href={explorerUrl} target="_blank" rel="noreferrer">
                Explorer
              </a>
            </>
          ) : null}
        </p>
      ) : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}

export function App() {
  return (
    <SplendorConnectProvider
      network="local"
      connectors="auto"
      theme="splendor"
      autoReconnect
      allowUnpinnedWasm
    >
      <div className="app-shell">
        <header className="app-header">
          <p className="brand">
            Splendor <span>Quantum</span>
          </p>
          <ConnectButton />
        </header>
        <section className="hero">
          <h1>Connect once. PQ signing stays local.</h1>
          <p>
            MetaMask signs a fixed domain message to derive your ML-DSA-65 wallet. Your dApp never
            sees the private key — only the SQ: address and signed transactions.
          </p>
        </section>
        <TransferPanel />
      </div>
    </SplendorConnectProvider>
  );
}

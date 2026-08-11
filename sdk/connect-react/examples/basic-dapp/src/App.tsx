import { useState } from 'react';
import {
  SplendorConnectProvider,
  ConnectKit,
  metamaskConnector,
  seedConnector,
  useAccount,
  useWallet,
} from '@splendor/connect-react';

function TransferPanel() {
  const { isConnected } = useAccount();
  const { wallet } = useWallet();
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState('0.01');
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Transfer</h2>
        <p className="msg">Connect a wallet to send SPLD on your SQ: identity.</p>
      </div>
    );
  }

  const onSend = async () => {
    setBusy(true);
    setMsg(null);
    setErr(null);
    try {
      const res = await wallet.sendTransfer({
        to: to.trim(),
        amountSPLD: amount.trim(),
        waitForConfirmation: true,
      });
      setMsg(`Submitted${res.hash ? `: ${res.hash}` : ''} (status ${res.status})`);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="panel">
      <h2>Transfer</h2>
      <label htmlFor="to">To (SQ:…)</label>
      <input id="to" value={to} onChange={(e) => setTo(e.target.value)} placeholder="SQ:…" />
      <label htmlFor="amt">Amount (SPLD)</label>
      <input id="amt" value={amount} onChange={(e) => setAmount(e.target.value)} />
      <button type="button" disabled={busy || !to.trim()} onClick={() => void onSend()}>
        {busy ? 'Sending…' : 'Send'}
      </button>
      {msg ? <p className="msg">{msg}</p> : null}
      {err ? <p className="msg error">{err}</p> : null}
    </div>
  );
}

export function App() {
  return (
    <SplendorConnectProvider
      network="local"
      connectors={[metamaskConnector(), seedConnector()]}
      theme="splendor"
      allowUnpinnedWasm
    >
      <div className="app-shell">
        <header className="app-header">
          <p className="brand">
            Splendor <span>Quantum</span>
          </p>
          <ConnectKit />
        </header>
        <section className="hero">
          <h1>Connect once. PQ signing stays local.</h1>
          <p>
            MetaMask signs a fixed domain message to derive your Dilithium wallet. Your dApp never
            sees the private key — only the SQ: address and signed transactions.
          </p>
        </section>
        <TransferPanel />
      </div>
    </SplendorConnectProvider>
  );
}

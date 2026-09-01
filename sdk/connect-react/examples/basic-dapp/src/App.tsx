import { useState } from 'react';
import {
  SplendorConnectProvider,
  ConnectButton,
  useAccount,
  useSendTransfer,
  useDeployToken,
} from '@splendor/connect-react';

function TokenDeployPanel() {
  const { isConnected } = useAccount();
  const { deploy, status, address, hash, error, reset } = useDeployToken();
  const [name, setName] = useState('Demo');
  const [symbol, setSymbol] = useState('DMO');
  const [supply, setSupply] = useState('1000000');

  if (!isConnected) return null;

  const onDeploy = async () => {
    reset();
    try {
      await deploy({
        name: name.trim(),
        symbol: symbol.trim(),
        decimals: 18,
        totalSupply: supply.trim(),
        waitForConfirmation: true,
      });
    } catch {
      /* error state */
    }
  };

  return (
    <div className="panel">
      <h2>Deploy QRC-20</h2>
      <label htmlFor="tok-name">Name</label>
      <input id="tok-name" value={name} onChange={(e) => setName(e.target.value)} />
      <label htmlFor="tok-sym">Symbol</label>
      <input id="tok-sym" value={symbol} onChange={(e) => setSymbol(e.target.value)} />
      <label htmlFor="tok-sup">Total supply (units)</label>
      <input id="tok-sup" value={supply} onChange={(e) => setSupply(e.target.value)} />
      <button type="button" disabled={status === 'pending'} onClick={() => void onDeploy()}>
        {status === 'pending' ? 'Deploying…' : 'Deploy token'}
      </button>
      {address ? <p className="msg">Token: {address}</p> : null}
      {hash ? <p className="msg">Tx: {hash}</p> : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}

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
      // Local dev only: this example builds the signer from source, so its
      // digest changes on every rebuild and there is nothing stable to pin.
      // A deployed app must pass wasmSha256 instead — the signer bundle is the
      // code that handles private keys, and it is fetched at runtime.
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
            Create a post-quantum ML-DSA-65 wallet in the browser, or import a seed you already
            hold. Your dApp never sees the private key — only the SQ: address and signed
            transactions.
          </p>
        </section>
        <TransferPanel />
        <TokenDeployPanel />
      </div>
    </SplendorConnectProvider>
  );
}

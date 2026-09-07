import {
  SplendorConnectProvider,
  ConnectButton,
  TransferWidget,
  AccountCard,
  DeployTokenWidget,
  useAccount,
} from '@splendor/connect-react';

function TokenNote() {
  const { isConnected } = useAccount();
  if (!isConnected) return null;
  return (
    <p className="msg">
      Connected — use the widgets below or open Account for details.
    </p>
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
        <TokenNote />
        <div className="grid">
          <AccountCard />
          <TransferWidget />
          <DeployTokenWidget />
        </div>
      </div>
    </SplendorConnectProvider>
  );
}

import {
  SplendorConnectProvider,
  ConnectKit,
  AccountCard,
  TransferWidget,
  DeployTokenWidget,
} from '@splendor/connect-react';

export function App() {
  return (
    <SplendorConnectProvider
      network="local"
      connectors="auto"
      theme="splendor"
      autoReconnect
      // Local only — pin wasmSha256 in production (see README).
      allowUnpinnedWasm
      // Optional: expose window.splendor for other page scripts
      // exposeInjectedProvider
    >
      <div className="app-shell">
        <header className="app-header">
          <p className="brand">
            Splendor <span>dApp</span>
          </p>
          <ConnectKit />
        </header>
        <section className="hero">
          <h1>Build on Splendor Quantum Chain</h1>
          <p>Connect a post-quantum wallet, then transfer SPLD or deploy a QRC-20.</p>
        </section>
        <div className="grid">
          <AccountCard />
          <TransferWidget />
          <DeployTokenWidget />
        </div>
      </div>
    </SplendorConnectProvider>
  );
}

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
  const wasmSha256 = (import.meta.env.VITE_SQC_WASM_SHA256 as string | undefined)?.trim();
  const isDev = import.meta.env.DEV;
  if (!isDev && !wasmSha256) {
    throw new Error(
      'VITE_SQC_WASM_SHA256 is required for production builds (or run with import.meta.env.DEV).',
    );
  }
  return (
    <SplendorConnectProvider
      network="local"
      connectors="auto"
      theme="splendor"
      autoReconnect
      {...(wasmSha256 ? { wasmSha256 } : { allowUnpinnedWasm: true })}
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

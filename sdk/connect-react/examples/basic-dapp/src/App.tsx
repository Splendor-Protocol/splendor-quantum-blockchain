import {
  SplendorConnectProvider,
  ConnectKit,
  TransferWidget,
  AccountCard,
  DeployTokenWidget,
  TokenPanel,
  TxToastHost,
  WrongNetworkBanner,
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
      <TxToastHost>
        <div className="app-shell">
          <header className="app-header">
            <p className="brand">
              Splendor <span>Quantum</span>
            </p>
            <ConnectKit />
          </header>
          <WrongNetworkBanner expectedNetworkId="local" />
          <section className="hero">
            <h1>Connect with the Splendor extension or a native seed.</h1>
            <p>
              Prefer the Splendor Quantum Wallet extension when installed. Otherwise create or
              import an ML-DSA-65 seed in the browser. Confirmation uses RPC receipts — explorer
              links are optional and do not affect tx success.
            </p>
          </section>
          <TokenNote />
          <div className="grid">
            <AccountCard />
            <TransferWidget />
            <TokenPanel />
            <DeployTokenWidget />
          </div>
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}

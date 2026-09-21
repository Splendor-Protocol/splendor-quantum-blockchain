import {
  SplendorConnectProvider,
  ConnectKit,
  AccountCard,
  TransferWidget,
  DeployTokenWidget,
  TokenPanel,
  TxToastHost,
  WrongNetworkBanner,
} from '@splendor/connect-react';

const wasmSha256 = (import.meta.env.VITE_SQC_WASM_SHA256 as string | undefined)?.trim();
const isDev = import.meta.env.DEV;

if (!isDev && !wasmSha256) {
  throw new Error(
    'VITE_SQC_WASM_SHA256 is required for production builds (or run with import.meta.env.DEV).',
  );
}

export function App() {
  return (
    <SplendorConnectProvider
      network="local"
      connectors="auto"
      theme="splendor"
      autoReconnect
      {...(wasmSha256
        ? { wasmSha256 }
        : { allowUnpinnedWasm: true })}
    >
      <TxToastHost>
        <div className="app-shell">
          <header className="app-header">
            <p className="brand">
              Splendor <span>dApp</span>
            </p>
            <ConnectKit />
          </header>
          <WrongNetworkBanner expectedNetworkId="local" />
          <section className="hero">
            <h1>Build on Splendor Quantum Chain</h1>
            <p>
              Connect the Splendor extension or a native ML-DSA-65 seed, then transfer SPLD or
              deploy a QRC-20.
            </p>
          </section>
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

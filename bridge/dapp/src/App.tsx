import { SplendorConnectProvider, WrongNetworkBanner, type NetworkId } from '@splendor/connect-react';
import { MigrationPanel } from './MigrationPanel';

const network = ((import.meta.env.VITE_SQC_NETWORK as NetworkId | undefined) || 'mainnet');

export function App() {
  const wasmSha256 = import.meta.env.VITE_SQC_WASM_SHA256?.trim();
  const isDev = import.meta.env.DEV;
  if (!isDev && !wasmSha256) {
    throw new Error('VITE_SQC_WASM_SHA256 is required for production builds.');
  }
  return (
    <SplendorConnectProvider
      network={network}
      connectors="auto"
      theme="splendor"
      autoReconnect
      {...(wasmSha256 ? { wasmSha256 } : { allowUnpinnedWasm: true })}
    >
      <div className="app-shell">
        <header className="app-header">
          <h1 className="brand">
            Splendor <span>→</span> Quantum Bridge
          </h1>
        </header>
        <WrongNetworkBanner expectedNetworkId={network} />
        <section className="hero">
          <p>
            One-way migration: burn SPLD/SPLV on the legacy chain (2691) and the relayer credits the
            same amount on the Splendor Quantum Chain.
          </p>
        </section>
        <MigrationPanel />
      </div>
    </SplendorConnectProvider>
  );
}

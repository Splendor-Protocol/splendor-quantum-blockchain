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
      <WrongNetworkBanner expectedNetworkId={network} />
      <div className="shell">
        <header>
          <h1>Splendor → Quantum Bridge</h1>
          <p className="sub">
            One-way migration: burn SPLD/SPLV on the legacy chain (2691) and the relayer credits the
            same amount on the Splendor Quantum Chain.
          </p>
        </header>
        <MigrationPanel />
      </div>
    </SplendorConnectProvider>
  );
}

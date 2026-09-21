import {
  SplendorConnectProvider,
  TxToastHost,
  WrongNetworkBanner,
  type NetworkId,
} from '@splendor/connect-react';
import { WalletBar } from './panels/WalletBar';
import { BalancePanel } from './panels/BalancePanel';
import { SendPanel } from './panels/SendPanel';
import { TokenPanel } from './panels/TokenPanel';
import { DexPanel } from './panels/DexPanel';
import { BridgePanel } from './panels/BridgePanel';
import { CollectionPanel } from './panels/CollectionPanel';
import { ExchangePanel } from './panels/ExchangePanel';
import { ContractPanel } from './panels/ContractPanel';

const network = (import.meta.env.VITE_SQC_NETWORK as NetworkId | undefined) || 'local';

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
      network={network}
      connectors="auto"
      theme="splendor"
      autoReconnect
      {...(wasmSha256 ? { wasmSha256 } : { allowUnpinnedWasm: true })}
    >
      <TxToastHost>
        <div className="app-shell">
          <WalletBar />
          <WrongNetworkBanner expectedNetworkId={network} />
          <section className="hero">
            <h1>SDK demo — connect, SPLD, modules</h1>
            <p>
              Extension or native ML-DSA-65 seed via ConnectKit. Balance, send SPLD, QRC-20 / 1155,
              Bridge, and Contract hooks. DEX / Exchange panels are leftover adapters — not
              node-routed (see banners below).
            </p>
          </section>
          <BalancePanel />
          <SendPanel />
          <TokenPanel />
          <CollectionPanel />
          <DexPanel />
          <ExchangePanel />
          <BridgePanel />
          <ContractPanel />
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}

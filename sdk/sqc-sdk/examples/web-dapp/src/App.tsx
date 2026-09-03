import {
  SplendorConnectProvider,
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
  return (
    <SplendorConnectProvider
      network={network}
      connectors="auto"
      theme="splendor"
      autoReconnect
      // Local / monorepo only: WASM digest changes on rebuild.
      // Deployed apps must pass wasmSha256 instead.
      allowUnpinnedWasm
    >
      <div className="app-shell">
        <WalletBar />
        <section className="hero">
          <h1>SDK demo — connect, SPLD, modules</h1>
          <p>
            Native ML-DSA-65 wallet via ConnectKit. Balance, send SPLD, QRC-20 / 1155, DEX, Exchange,
            Bridge, and Contract using <code className="mono">@splendor/connect-react</code> hooks.
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
    </SplendorConnectProvider>
  );
}

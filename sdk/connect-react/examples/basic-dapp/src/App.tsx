import {
  SplendorConnectProvider,
  ConnectKit,
  TxToastHost,
  WrongNetworkBanner,
  type NetworkId,
} from '@splendor/connect-react';
import { LabProgress, LabProvider, LabViewTabs, useLab } from './lab/ui';
import { GuideView } from './lab/Guide';
import { RpcHealth } from './lab/RpcHealth';
import { labNetworks } from './rpcProxy';
import {
  AccountCardSection,
  ConfirmationCard,
  ConnectCard,
  NetworkCard,
  OfflineHelpersCard,
  SignInCard,
  ToastCard,
  TokenCard,
  TransferCard,
} from './lab/CoreSections';
import {
  BridgeCard,
  ContractCard,
  ModulesCard,
  Qrc1155Card,
} from './lab/ModuleSections';

const NETWORK = ((import.meta.env.VITE_SQC_NETWORK as string | undefined)?.trim() ||
  'mainnet') as NetworkId;
const CUSTOM_NETWORKS = labNetworks();

const SECTIONS: Array<{ id: string; label: string }> = [
  { id: 'helpers', label: 'Helpers' },
  { id: 'toasts', label: 'Toasts' },
  { id: 'connect', label: 'Connect' },
  { id: 'network', label: 'Network' },
  { id: 'account', label: 'Account and preflight' },
  { id: 'transfer', label: 'SPLD transfer' },
  { id: 'confirm', label: 'Confirmation' },
  { id: 'signin', label: 'Sign-in' },
  { id: 'qrc20', label: 'QRC-20' },
  { id: 'qrc1155', label: 'QRC-1155' },
  { id: 'contract', label: 'Contract' },
  { id: 'modules', label: 'Chain modules' },
  { id: 'bridge', label: 'Bridge' },
];

function LabBody() {
  const { view } = useLab();
  return (
    <>
      <div hidden={view !== 'tests'}>
        <LabProgress />
        <div className="lab-layout">
          <nav className="lab-nav" aria-label="Sections">
            {SECTIONS.map((s, i) => (
              <a key={s.id} href={`#${s.id}`}>
                <span>{i + 1}</span>
                {s.label}
              </a>
            ))}
          </nav>
          <main className="lab-main">
            <OfflineHelpersCard step={1} />
            <ToastCard step={2} />
            <ConnectCard step={3} />
            <NetworkCard step={4} expected={NETWORK} />
            <AccountCardSection step={5} />
            <TransferCard step={6} />
            <ConfirmationCard step={7} />
            <SignInCard step={8} />
            <TokenCard step={9} />
            <Qrc1155Card step={10} />
            <ContractCard step={11} />
            <ModulesCard step={12} />
            <BridgeCard step={13} />
          </main>
        </div>
      </div>
      {view === 'guide' ? <GuideView /> : null}
    </>
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
      network={NETWORK}
      connectors="auto"
      theme="splendor"
      autoReconnect
      customNetworks={CUSTOM_NETWORKS}
      {...(wasmSha256 ? { wasmSha256 } : { allowUnpinnedWasm: true })}
    >
      <TxToastHost>
        <LabProvider>
          <div className="lab-shell">
            <header className="app-header">
              <p className="brand">
                Splendor <span>Quantum</span> · Feature Lab
              </p>
              <ConnectKit />
            </header>
            <WrongNetworkBanner expectedNetworkId={NETWORK} />
            <RpcHealth />
            {NETWORK === 'mainnet' ? (
              <p className="lab-mainnet-warning" role="note">
                Mainnet: every transfer, deploy and mint spends real SPLD and cannot be undone. Use
                small amounts and send only to addresses you control.
              </p>
            ) : null}
            <LabViewTabs />
            <LabBody />
          </div>
        </LabProvider>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}

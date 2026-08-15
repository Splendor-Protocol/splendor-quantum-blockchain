import type { DeriveInfo, NetworkConfig } from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';

export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'locked' | 'error';

/** Fine-grained connect progress for UX (WASM load / wallet sign). */
export type ConnectingStep = 'init' | 'sign' | null;

export type ConnectAccount = {
  address: string;
  evmAddress?: string;
  info: DeriveInfo;
};

export type ModalView = 'closed' | 'connect' | 'account';

export type ConnectControllerState = {
  status: ConnectionStatus;
  connectingStep: ConnectingStep;
  account: ConnectAccount | null;
  error: Error | null;
  activeConnectorId: string | null;
  modal: ModalView;
  connectors: Connector[];
  network: NetworkConfig | null;
  /** True while wallets are still being discovered (connectors="auto"). */
  connectorsLoading?: boolean;
};

export type ConnectControllerEvents = {
  status: ConnectionStatus;
  account: ConnectAccount | null;
  error: Error | null;
  modal: ModalView;
  activeConnectorId: string | null;
};

import type { ReactNode } from 'react';
import { useAccount, useConnect } from '../../react/hooks.js';
import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

export type WalletGateProps = {
  children: ReactNode;
  /** Shown above the Unlock button when the wallet is locked. */
  lockedText?: ReactNode;
  /** Shown above the Connect button when no wallet is connected. */
  disconnectedText?: ReactNode;
  /** Replaces the whole locked / disconnected UI. */
  fallback?: ReactNode;
  labels?: Partial<Pick<CommonWidgetLabels, 'connect' | 'unlock' | 'lockedHint' | 'disconnectedHint'>>;
};

/** Renders `children` only when a wallet is connected; otherwise an Unlock or Connect prompt. */
export function WalletGate({ children, lockedText, disconnectedText, fallback, labels }: WalletGateProps) {
  const { address, isConnected } = useAccount();
  const { status, openModal } = useConnect();
  const l = useWidgetLabels(COMMON_LABELS, labels);

  if (isConnected && address) return <>{children}</>;
  if (fallback !== undefined) return <>{fallback}</>;

  const locked = status === 'locked';
  return (
    <div className="sqc-wallet-gate" data-sqc-gate={locked ? 'locked' : 'disconnected'}>
      <p className="sqc-widget__hint">
        {locked ? (lockedText ?? l.lockedHint) : (disconnectedText ?? l.disconnectedHint)}
      </p>
      <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
        {locked ? l.unlock : l.connect}
      </button>
    </div>
  );
}

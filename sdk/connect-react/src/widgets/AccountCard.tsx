import { useState } from 'react';
import { useAccount, useBalance, useConnect, useNetwork } from '../react/hooks.js';
import { NetworkBadge } from '../ui/NetworkBadge.js';
import { copyText, truncateAddress } from './utils.js';

export type AccountCardProps = {
  className?: string;
  /** Show disconnect button. Default true. */
  showDisconnect?: boolean;
};

/**
 * Drop-in account summary: address, PQ badge, balance, copy, lock/unlock CTA.
 */
export function AccountCard({ className, showDisconnect = true }: AccountCardProps) {
  const { address, evmAddress, isConnected, info } = useAccount();
  const { balanceSPLD, refresh, loading } = useBalance();
  const { status, openModal, disconnect } = useConnect();
  const { network } = useNetwork();
  const [copied, setCopied] = useState(false);

  if (status === 'locked') {
    return (
      <div className={['sqc-widget', 'sqc-account-card', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Account</h3>
        <p className="sqc-widget__hint">Wallet locked — unlock to continue.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Unlock
        </button>
      </div>
    );
  }

  if (!isConnected || !address) {
    return (
      <div className={['sqc-widget', 'sqc-account-card', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Account</h3>
        <p className="sqc-widget__hint">Connect a wallet to view your SQ: identity.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Connect
        </button>
      </div>
    );
  }

  const pq = info?.postQuantumSecure !== false && !evmAddress;

  const onCopy = async () => {
    if (!(await copyText(address))) return;
    setCopied(true);
    setTimeout(() => setCopied(false), 1400);
  };

  return (
    <div className={['sqc-widget', 'sqc-account-card', className].filter(Boolean).join(' ')}>
      <div className="sqc-widget__header">
        <h3 className="sqc-widget__title">Account</h3>
        <NetworkBadge />
      </div>
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">
          SQ address
          <span
            className="sqc-connector-item__badge"
            title={pq ? 'Native ML-DSA-65 account' : 'EVM-derived — not quantum-safe for long-term value'}
          >
            {pq ? 'Post-quantum' : 'Not quantum-safe'}
          </span>
        </span>
        <div className="sqc-account-row__value">
          <span title={address}>{truncateAddress(address, 10, 8)}</span>
          <button type="button" className="sqc-copy" onClick={() => void onCopy()}>
            {copied ? 'Copied' : 'Copy'}
          </button>
        </div>
      </div>
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">Balance</span>
        <div className="sqc-account-row__value">
          <span>
            {balanceSPLD != null ? `${balanceSPLD} SPLD` : '—'}
            {network?.name ? (
              <span style={{ color: 'var(--sqc-text-muted)', marginLeft: '0.5rem' }}>
                · {network.name}
              </span>
            ) : null}
          </span>
          <button type="button" className="sqc-copy" onClick={() => void refresh()} disabled={loading}>
            {loading ? '…' : 'Refresh'}
          </button>
        </div>
      </div>
      {showDisconnect ? (
        <div className="sqc-modal__actions" style={{ marginTop: '0.75rem' }}>
          <button type="button" className="sqc-btn sqc-btn--danger" onClick={() => void disconnect()}>
            Disconnect
          </button>
        </div>
      ) : null}
    </div>
  );
}

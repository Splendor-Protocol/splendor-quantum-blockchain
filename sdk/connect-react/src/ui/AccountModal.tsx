import { useState } from 'react';
import { useAccount, useBalance, useConnect, useNetwork } from '../react/hooks.js';
import { ModalShell } from './ModalShell.js';
import { NetworkBadge } from './NetworkBadge.js';

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function AccountModal() {
  const { modal, closeModal, disconnect } = useConnect();
  const { address, evmAddress } = useAccount();
  const { balanceSPLD, refresh, loading } = useBalance();
  const { network } = useNetwork();
  const [copied, setCopied] = useState<'sq' | 'evm' | null>(null);

  const open = modal === 'account';

  const onCopy = async (kind: 'sq' | 'evm', value: string) => {
    const ok = await copyText(value);
    if (!ok) return;
    setCopied(kind);
    setTimeout(() => setCopied(null), 1400);
  };

  const onDisconnect = async () => {
    await disconnect();
  };

  return (
    <ModalShell
      open={open}
      title="Account"
      subtitle="Funds live on your SQ: address — not the EVM address."
      onClose={closeModal}
    >
      <div style={{ marginBottom: '1rem', display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
        <NetworkBadge />
        <button
          type="button"
          className="sqc-copy"
          onClick={() => void refresh()}
          disabled={loading}
        >
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      <div className="sqc-account-row">
        <span className="sqc-account-row__label">SQ address</span>
        <div className="sqc-account-row__value">
          <span>{address ?? '—'}</span>
          {address ? (
            <button type="button" className="sqc-copy" onClick={() => void onCopy('sq', address)}>
              {copied === 'sq' ? 'Copied' : 'Copy'}
            </button>
          ) : null}
        </div>
      </div>

      {evmAddress ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">Linked EVM (derivation only)</span>
          <div className="sqc-account-row__value">
            <span>{evmAddress}</span>
            <button
              type="button"
              className="sqc-copy"
              onClick={() => void onCopy('evm', evmAddress)}
            >
              {copied === 'evm' ? 'Copied' : 'Copy'}
            </button>
          </div>
        </div>
      ) : null}

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
        </div>
      </div>

      {network?.explorerUrl ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">Explorer</span>
          <div className="sqc-account-row__value">
            <a
              href={network.explorerUrl}
              target="_blank"
              rel="noreferrer"
              className="sqc-account-link"
            >
              Open explorer
            </a>
          </div>
        </div>
      ) : null}

      <div className="sqc-modal__actions">
        <button type="button" className="sqc-btn sqc-btn--ghost" onClick={closeModal}>
          Close
        </button>
        <button type="button" className="sqc-btn sqc-btn--danger" onClick={() => void onDisconnect()}>
          Disconnect
        </button>
      </div>
    </ModalShell>
  );
}

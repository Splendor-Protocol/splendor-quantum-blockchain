import type { CSSProperties, ReactNode } from 'react';
import { useAccount, useBalance, useConnect, useNetwork } from '../react/hooks.js';
import { NetworkBadge } from '../ui/NetworkBadge.js';
import { addressExplorerUrl } from './utils.js';
import { ACCOUNT_LABELS, useWidgetLabels, type AccountCardLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressDisplay } from './parts/AddressDisplay.js';

export type AccountCardProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<AccountCardLabels>;
  /** Show disconnect button. Default true. */
  showDisconnect?: boolean;
  /** Show the SPLD balance row with Refresh. Default true. */
  showBalance?: boolean;
  /** Show the network badge and network name. Default true. */
  showNetwork?: boolean;
  /** Show the explorer link for the address. Default true. */
  showExplorer?: boolean;
  /** Called after the address is copied. */
  onCopy?: (address: string) => void;
  /** Called after the wallet disconnects. */
  onDisconnect?: () => void;
};

/**
 * Drop-in account summary: address, PQ badge, balance, copy, connect/unlock/disconnect CTAs.
 * (Idle lock is driven by the wallet `lock` event — use Unlock when status is locked.)
 */
export function AccountCard({
  className,
  style,
  title,
  hideTitle,
  labels,
  showDisconnect = true,
  showBalance = true,
  showNetwork = true,
  showExplorer = true,
  onCopy,
  onDisconnect,
}: AccountCardProps) {
  const l = useWidgetLabels(ACCOUNT_LABELS, labels, 'account');
  const { address, info } = useAccount();
  const { balanceSPLD, refresh, loading } = useBalance();
  const { disconnect } = useConnect();
  const { network } = useNetwork();

  const pq = info?.postQuantumSecure !== false;
  const explorerHref = showExplorer ? addressExplorerUrl(network?.explorerUrl, address) : null;

  return (
    <WidgetShell
      kind="account"
      className={['sqc-account-card', className].filter(Boolean).join(' ')}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      headerExtra={showNetwork ? <NetworkBadge /> : null}
      gateLabels={l}
    >
      {address ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">
            {l.addressLabel}
            <span
              className="sqc-connector-item__badge sqc-connector-item__badge--ok"
              title="Native ML-DSA-65 (Splendor Quantum Wallet / seed)"
            >
              {pq ? l.pqBadge : l.pqCheck}
            </span>
          </span>
          <div className="sqc-account-row__value">
            <AddressDisplay value={address} href={explorerHref} labels={l} onCopy={onCopy} />
          </div>
        </div>
      ) : null}
      {showBalance ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">{l.balanceLabel}</span>
          <div className="sqc-account-row__value">
            <span>
              {balanceSPLD != null ? `${balanceSPLD} SPLD` : '—'}
              {showNetwork && network?.name ? <span className="sqc-account-row__muted"> · {network.name}</span> : null}
            </span>
            <button type="button" className="sqc-copy" onClick={() => void refresh()} disabled={loading}>
              {loading ? '…' : l.refresh}
            </button>
          </div>
        </div>
      ) : null}
      {showDisconnect ? (
        <div className="sqc-widget__actions">
          <button
            type="button"
            className="sqc-btn sqc-btn--danger"
            onClick={() => void disconnect().then(() => onDisconnect?.())}
          >
            {l.disconnect}
          </button>
        </div>
      ) : null}
    </WidgetShell>
  );
}

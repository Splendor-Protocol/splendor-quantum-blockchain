import { truncateAddress } from './utils.js';

export function RecentRecipients({
  recipients,
  onPick,
}: {
  recipients: string[];
  onPick: (address: string) => void;
}) {
  if (recipients.length === 0) return null;
  return (
    <div className="sqc-recent" aria-label="Recent recipients">
      <span className="sqc-widget__hint">Recent:</span>
      {recipients.map((addr) => (
        <button
          key={addr}
          type="button"
          className="sqc-copy"
          title={addr}
          onClick={() => onPick(addr)}
        >
          {truncateAddress(addr, 8, 6)}
        </button>
      ))}
    </div>
  );
}

export function TransferReviewPanel({
  to,
  amount,
  symbol,
  networkName,
  pending,
  onBack,
  onConfirm,
}: {
  to: string;
  amount: string;
  symbol: string;
  networkName?: string | null;
  pending: boolean;
  onBack: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="sqc-review" role="group" aria-label="Review transfer">
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">To</span>
        <div className="sqc-account-row__value">
          <span title={to} data-testid="sqc-review-to">
            {to}
          </span>
        </div>
      </div>
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">Amount</span>
        <div className="sqc-account-row__value">
          <span data-testid="sqc-review-amount">
            {amount} {symbol}
          </span>
        </div>
      </div>
      {networkName ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">Network</span>
          <div className="sqc-account-row__value">
            <span>{networkName}</span>
          </div>
        </div>
      ) : null}
      <div className="sqc-modal__actions" style={{ marginTop: '0.75rem' }}>
        <button type="button" className="sqc-btn sqc-btn--ghost" onClick={onBack} disabled={pending}>
          Back
        </button>
        <button
          type="button"
          className="sqc-btn sqc-btn--primary"
          onClick={onConfirm}
          disabled={pending}
        >
          {pending ? 'Sending…' : 'Confirm send'}
        </button>
      </div>
    </div>
  );
}

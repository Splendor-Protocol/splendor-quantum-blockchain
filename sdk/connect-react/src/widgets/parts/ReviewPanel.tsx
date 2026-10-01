import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

export type ReviewPanelProps = {
  to: string;
  amount: string;
  symbol: string;
  networkName?: string | null;
  pending: boolean;
  onBack: () => void;
  onConfirm: () => void;
  labels?: Partial<
    Pick<CommonWidgetLabels, 'reviewTo' | 'reviewAmount' | 'reviewNetwork' | 'back' | 'confirmSend' | 'sending'>
  >;
};

/** Read-only summary shown before the wallet is asked to sign. */
export function ReviewPanel({
  to,
  amount,
  symbol,
  networkName,
  pending,
  onBack,
  onConfirm,
  labels,
}: ReviewPanelProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  return (
    <div className="sqc-review" role="group" aria-label="Review transfer">
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">{l.reviewTo}</span>
        <div className="sqc-account-row__value">
          <span title={to} data-testid="sqc-review-to">
            {to}
          </span>
        </div>
      </div>
      <div className="sqc-account-row">
        <span className="sqc-account-row__label">{l.reviewAmount}</span>
        <div className="sqc-account-row__value">
          <span data-testid="sqc-review-amount">
            {amount} {symbol}
          </span>
        </div>
      </div>
      {networkName ? (
        <div className="sqc-account-row">
          <span className="sqc-account-row__label">{l.reviewNetwork}</span>
          <div className="sqc-account-row__value">
            <span>{networkName}</span>
          </div>
        </div>
      ) : null}
      <div className="sqc-widget__actions">
        <button type="button" className="sqc-btn sqc-btn--ghost" onClick={onBack} disabled={pending}>
          {l.back}
        </button>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={onConfirm} disabled={pending}>
          {pending ? l.sending : l.confirmSend}
        </button>
      </div>
    </div>
  );
}

/** @deprecated Use `ReviewPanel`. */
export const TransferReviewPanel = ReviewPanel;

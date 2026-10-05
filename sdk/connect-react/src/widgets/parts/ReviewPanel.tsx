import type { ReactNode } from 'react';
import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

export type ReviewRow = { label: ReactNode; value: ReactNode; testId?: string };

export type ReviewPanelProps = {
  /** Recipient. Omit for reviews without one (e.g. deploy) and pass `rows` instead. */
  to?: string;
  /** Amount as entered. Omit together with `to` for non-transfer reviews. */
  amount?: string;
  symbol?: string;
  /** Signing account, shown so the user sees which account pays. */
  from?: string | null;
  /** Token contract for QRC-20 transfers. */
  contract?: string | null;
  networkName?: string | null;
  /** Extra rows rendered after the built-in ones. */
  rows?: ReviewRow[];
  /** Rendered under the rows (fee estimate, warnings). */
  children?: ReactNode;
  pending: boolean;
  /** Keep Confirm disabled (wrong network, predicted revert, …). */
  confirmDisabled?: boolean;
  confirmLabel?: ReactNode;
  ariaLabel?: string;
  onBack: () => void;
  onConfirm: () => void;
  labels?: Partial<
    Pick<
      CommonWidgetLabels,
      | 'reviewTo'
      | 'reviewFrom'
      | 'reviewAmount'
      | 'reviewNetwork'
      | 'reviewContract'
      | 'reviewAria'
      | 'back'
      | 'confirmSend'
      | 'sending'
    >
  >;
};

function Row({ label, value, testId, title }: { label: ReactNode; value: ReactNode; testId?: string; title?: string }) {
  return (
    <div className="sqc-account-row">
      <span className="sqc-account-row__label">{label}</span>
      <div className="sqc-account-row__value">
        <span title={title} data-testid={testId}>
          {value}
        </span>
      </div>
    </div>
  );
}

/** Read-only summary shown before the wallet is asked to sign. */
export function ReviewPanel({
  to,
  amount,
  symbol = '',
  from,
  contract,
  networkName,
  rows,
  children,
  pending,
  confirmDisabled = false,
  confirmLabel,
  ariaLabel,
  onBack,
  onConfirm,
  labels,
}: ReviewPanelProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  return (
    <div className="sqc-review" role="group" aria-label={ariaLabel ?? l.reviewAria}>
      {from ? <Row label={l.reviewFrom} value={from} title={from} testId="sqc-review-from" /> : null}
      {to ? <Row label={l.reviewTo} value={to} title={to} testId="sqc-review-to" /> : null}
      {amount != null ? (
        <Row label={l.reviewAmount} value={`${amount} ${symbol}`.trim()} testId="sqc-review-amount" />
      ) : null}
      {contract ? (
        <Row label={l.reviewContract} value={contract} title={contract} testId="sqc-review-contract" />
      ) : null}
      {networkName ? <Row label={l.reviewNetwork} value={networkName} /> : null}
      {rows?.map((r, i) => <Row key={i} label={r.label} value={r.value} testId={r.testId} />)}
      {children}
      <div className="sqc-widget__actions">
        <button type="button" className="sqc-btn sqc-btn--ghost" onClick={onBack} disabled={pending}>
          {l.back}
        </button>
        <button
          type="button"
          className="sqc-btn sqc-btn--primary"
          onClick={onConfirm}
          disabled={pending || confirmDisabled}
        >
          {pending ? l.sending : (confirmLabel ?? l.confirmSend)}
        </button>
      </div>
    </div>
  );
}

/** @deprecated Use `ReviewPanel`. */
export const TransferReviewPanel = ReviewPanel;

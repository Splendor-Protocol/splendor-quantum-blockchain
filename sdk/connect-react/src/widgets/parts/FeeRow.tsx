import type { FeeEstimate } from '../../react/hooks/read.js';
import { COMMON_LABELS, formatLabel, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

export type FeeRowProps = {
  estimate: Pick<FeeEstimate, 'feeSpld' | 'feeIsFloor' | 'reverted' | 'revertReason' | 'loading' | 'error'>;
  labels?: Partial<Pick<CommonWidgetLabels, 'reviewFee' | 'feeEstimating' | 'feeUnavailable' | 'feeFloor' | 'willRevert'>>;
};

/** Network fee line for review screens; warns when the node predicts a revert. */
export function FeeRow({ estimate, labels }: FeeRowProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const value = estimate.loading
    ? l.feeEstimating
    : estimate.feeSpld == null
      ? l.feeUnavailable
      : estimate.feeIsFloor
        ? formatLabel(l.feeFloor, { fee: estimate.feeSpld })
        : `≈ ${estimate.feeSpld} SPLD`;
  return (
    <>
      <div className={`sqc-account-row sqc-fee-row${estimate.reverted ? ' sqc-fee-row--revert' : ''}`}>
        <span className="sqc-account-row__label">{l.reviewFee}</span>
        <div className="sqc-account-row__value">
          <span data-testid="sqc-review-fee">{value}</span>
        </div>
      </div>
      {estimate.reverted ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert" data-testid="sqc-review-revert">
          {formatLabel(l.willRevert, { reason: estimate.revertReason || 'reverted' })}
        </p>
      ) : null}
    </>
  );
}

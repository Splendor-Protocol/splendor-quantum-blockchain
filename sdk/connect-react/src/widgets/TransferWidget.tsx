import { useAccount, useBalance, useNetwork, useSendTransfer } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import { TRANSFER_LABELS, formatLabel, useWidgetLabels, type TransferWidgetLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressInput, AmountInput } from './parts/inputs.js';
import { ReviewPanel } from './parts/ReviewPanel.js';
import { TxResult } from './parts/TxResult.js';
import { useTransferForm } from './parts/useTransferForm.js';
import { toError, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';

export type TransferSentResult = {
  to: string;
  /** SPLD amount as entered (decimal). */
  amount: string;
  hash?: string;
  confirmed?: boolean;
  explorerUrl?: string | null;
};

export type TransferWidgetProps = BaseWidgetProps<TransferSentResult, TransferWidgetLabels> & {
  /** Default amount in SPLD. */
  defaultAmount?: string;
  /** Prefilled recipient (SQ: address). */
  defaultTo?: string;
  /** @deprecated Use `onSuccess`. */
  onSent?: (result: TransferSentResult) => void;
};

/**
 * Drop-in SPLD transfer panel (uses useSendTransfer / useBalance).
 * Validates the SQ: recipient and balance, then asks for review before signing.
 */
export function TransferWidget({
  className,
  style,
  title,
  hideTitle,
  labels,
  defaultAmount = '0.01',
  defaultTo = '',
  onSuccess,
  onSent,
  onError,
  onStatusChange,
  toast = true,
}: TransferWidgetProps) {
  const l = useWidgetLabels(TRANSFER_LABELS, labels, 'transfer');
  const { address } = useAccount();
  const { balanceSPLD } = useBalance();
  const { network } = useNetwork();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const form = useTransferForm({
    balance: balanceSPLD,
    symbol: 'SPLD',
    self: address,
    defaultTo,
    defaultAmount,
    labels: l,
  });
  const toasts = useWidgetToast(toast);
  useStatusChange(status, onStatusChange);

  const onConfirm = async () => {
    const review = form.review;
    if (!review.ok) return;
    reset();
    toasts.pending(l.toastPending);
    let res: Awaited<ReturnType<typeof sendTransfer>>;
    try {
      res = await sendTransfer({ to: review.to, amountSPLD: review.amount, waitForConfirmation: true });
    } catch (e) {
      toasts.error(l.toastError, e);
      onError?.(toError(e));
      return;
    }
    toasts.success(l.toastSuccess, res);
    form.remember(review.to);
    form.cancelReview();
    const result: TransferSentResult = {
      to: review.to,
      amount: review.amount,
      hash: res.hash,
      confirmed: res.confirmed,
      explorerUrl: res.explorerUrl,
    };
    onSuccess?.(result);
    onSent?.(result);
  };

  return (
    <WidgetShell
      kind="transfer"
      status={status}
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gateLabels={l}
    >
      {balanceSPLD != null ? (
        <p className="sqc-widget__hint">{formatLabel(l.balance, { balance: balanceSPLD, symbol: 'SPLD' })}</p>
      ) : null}
      {form.reviewing && form.review.ok ? (
        <ReviewPanel
          to={form.review.to}
          amount={form.review.amount}
          symbol="SPLD"
          networkName={network?.name}
          pending={status === 'pending'}
          onBack={form.cancelReview}
          onConfirm={() => void onConfirm()}
          labels={l}
        />
      ) : (
        <>
          <AddressInput
            label={l.toLabel}
            placeholder={l.toPlaceholder}
            value={form.to}
            onChange={form.setTo}
            error={form.errors.to}
            recents={form.recents}
            labels={l}
          />
          <AmountInput
            label={l.amountLabel}
            value={form.amount}
            onChange={form.setAmount}
            error={form.errors.amount}
            balance={balanceSPLD}
            symbol="SPLD"
            labels={l}
          />
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={!form.canReview || status === 'pending'}
            onClick={form.startReview}
          >
            {status === 'pending' ? l.sending : l.review}
          </button>
        </>
      )}
      <TxResult hash={hash} confirmed={confirmed} explorerUrl={explorerUrl} labels={l} />
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(error)}
        </p>
      ) : null}
    </WidgetShell>
  );
}

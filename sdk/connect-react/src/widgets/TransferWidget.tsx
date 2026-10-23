import { useMemo } from 'react';
import { MIN_FEE, fromUnits } from '@splendor/sqc-sdk';
import { useAccount, useBalance, useEstimateFee, useNetwork, useSendTransfer } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import { TRANSFER_LABELS, formatLabel, useWidgetLabels, type TransferWidgetLabels } from './labels.js';
import type { BaseWidgetProps } from './types.js';
import { WidgetShell } from './parts/WidgetShell.js';
import { AddressInput, AmountInput } from './parts/inputs.js';
import { ReviewPanel } from './parts/ReviewPanel.js';
import { FeeRow } from './parts/FeeRow.js';
import { TxResult } from './parts/TxResult.js';
import { useTransferForm } from './parts/useTransferForm.js';
import { toError, useResetOnContextChange, useStatusChange, useWidgetToast } from './parts/useWidgetEffects.js';
import { exceedsBalanceWithFee, spendableAfterFee } from './utils.js';

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
  /** Show the node's fee estimate in the review step (default true). */
  showFee?: boolean;
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
  showFee = true,
}: TransferWidgetProps) {
  const l = useWidgetLabels(TRANSFER_LABELS, labels, 'transfer');
  const { address } = useAccount();
  const { balanceSPLD } = useBalance();
  const { network, walletMismatch } = useNetwork();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const spendable = useMemo(() => spendableAfterFee(balanceSPLD, MIN_FEE), [balanceSPLD]);
  const form = useTransferForm({
    balance: spendable,
    symbol: 'SPLD',
    self: address,
    defaultTo,
    defaultAmount,
    decimals: 18,
    contextKey: network?.chainId ?? '',
    labels: l,
  });
  const feeRequest = useMemo(() => {
    if (!showFee || !form.reviewing || !form.review.ok || !address) return null;
    try {
      return { from: address, to: form.review.to, value: fromUnits(form.review.amount, 18) };
    } catch {
      return null;
    }
  }, [showFee, form.reviewing, form.review, address]);
  const fee = useEstimateFee(feeRequest);
  const overBalance =
    showFee && form.review.ok && exceedsBalanceWithFee(form.review.amount, fee.fee, balanceSPLD);
  const toasts = useWidgetToast(toast);
  useStatusChange(status, onStatusChange);
  useResetOnContextChange(reset, status === 'pending', address, network?.chainId);

  const onConfirm = async () => {
    const review = form.review;
    if (!review.ok || walletMismatch || status === 'pending') return;
    reset();
    toasts.pending(l.toastPending);
    let res: Awaited<ReturnType<typeof sendTransfer>>;
    try {
      res = await sendTransfer({
        to: review.to,
        amountSPLD: review.amount,
        ...(showFee && fee.fee ? { fee: fee.fee } : {}),
        waitForConfirmation: true,
      });
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
          from={address}
          to={form.review.to}
          amount={form.review.amount}
          symbol="SPLD"
          networkName={network?.name}
          pending={status === 'pending'}
          confirmDisabled={walletMismatch || (showFee && (fee.loading || fee.reverted)) || overBalance}
          onBack={form.cancelReview}
          onConfirm={() => void onConfirm()}
          labels={l}
        >
          {showFee ? <FeeRow estimate={fee} labels={l} /> : null}
          {overBalance ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert" data-testid="sqc-review-over-balance">
              {l.insufficientWithFee}
            </p>
          ) : null}
        </ReviewPanel>
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
            balance={spendable}
            symbol="SPLD"
            decimals={18}
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
          {connectErrorMessage(error, 'tx')}
        </p>
      ) : null}
    </WidgetShell>
  );
}

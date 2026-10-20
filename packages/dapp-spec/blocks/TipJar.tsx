import { MIN_FEE, fromUnits } from '@splendor/sqc-sdk';
import {
  AmountInput,
  FeeRow,
  TxResult,
  WidgetShell,
  connectErrorMessage,
  exceedsBalanceWithFee,
  spendableAfterFee,
  useAccount,
  useBalance,
  useEstimateFee,
  useSendTransfer,
  useTransferForm,
} from '@splendor/connect-react';

export type TipJarProps = {
  title?: string;
  /** SQ: address that receives the tips. */
  recipient?: string;
  /** Suggested tip in SPLD. */
  amount?: string;
};

/** Fixed-recipient tip jar built from widget parts, with the real fee and an affordability check. */
export function TipJar({ title = 'Tip jar', recipient = '', amount = '0.1' }: TipJarProps) {
  const { address } = useAccount();
  const { balanceSPLD } = useBalance();
  const { sendTransfer, status, hash, confirmed, explorerUrl, error } = useSendTransfer();
  const spendable = spendableAfterFee(balanceSPLD, MIN_FEE);
  const form = useTransferForm({ balance: spendable, self: address, defaultTo: recipient, defaultAmount: amount });
  const fee = useEstimateFee(
    form.review.ok && address ? { from: address, to: form.review.to, value: fromUnits(form.review.amount, 18) } : null,
  );
  const tooMuch = form.review.ok && exceedsBalanceWithFee(form.review.amount, fee.fee, balanceSPLD);

  if (!recipient) {
    return (
      <WidgetShell kind="tip-jar" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a recipient address for this tip jar.</p>
      </WidgetShell>
    );
  }

  const send = () => {
    if (form.review.ok) {
      // The hook keeps the error for display below; the rejection itself needs no handling here.
      sendTransfer({ to: form.review.to, amountSPLD: form.review.amount, fee: fee.fee ?? undefined, waitForConfirmation: true }).catch(
        () => undefined,
      );
    }
  };

  return (
    <WidgetShell kind="tip-jar" title={title} status={status === 'pending' ? 'pending' : 'idle'}>
      <p className="sqc-widget__msg dapp-mono">To {recipient.slice(0, 12)}…{recipient.slice(-6)}</p>
      <AmountInput label="Tip (SPLD)" value={form.amount} onChange={form.setAmount} balance={spendable} />
      {form.review.ok ? <FeeRow estimate={fee} /> : null}
      {tooMuch ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          Not enough SPLD for the tip plus the network fee.
        </p>
      ) : null}
      <button
        type="button"
        className="sqc-btn sqc-btn--primary sqc-btn--block"
        disabled={!form.canReview || fee.loading || fee.reverted || tooMuch || status === 'pending'}
        onClick={send}
      >
        {status === 'pending' ? 'Sending…' : 'Send tip'}
      </button>
      {hash ? <TxResult hash={hash} confirmed={confirmed} explorerUrl={explorerUrl} /> : null}
      {status === 'error' && error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert" data-testid="dapp-tip-error">
          {connectErrorMessage(error)}
        </p>
      ) : null}
    </WidgetShell>
  );
}

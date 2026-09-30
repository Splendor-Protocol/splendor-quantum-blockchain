import { useId, useState } from 'react';
import {
  useAccount,
  useBalance,
  useConnect,
  useNetwork,
  useSendTransfer,
} from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import {
  compareDecimal,
  getRecentRecipients,
  isPositiveAmount as isValidAmount,
  normalizeSqAddress,
  rememberRecipient,
  reviewTransfer,
  transferReviewMessage,
} from './utils.js';
import { RecentRecipients, TransferReviewPanel } from './TransferReview.js';

export type TransferSentResult = {
  to: string;
  /** SPLD amount as entered (decimal). */
  amount: string;
  hash?: string;
  confirmed?: boolean;
  explorerUrl?: string | null;
};

export type TransferWidgetProps = {
  className?: string;
  /** Default amount in SPLD. */
  defaultAmount?: string;
  /** Prefilled recipient (SQ: address). */
  defaultTo?: string;
  /** Called after the wallet submits the transfer. */
  onSent?: (result: TransferSentResult) => void;
  /** Called when signing or submitting fails (including a wallet rejection). */
  onError?: (error: Error) => void;
};

/**
 * Drop-in SPLD transfer panel (uses useSendTransfer / useBalance).
 * Validates the SQ: recipient and balance, then asks for review before signing.
 */
export function TransferWidget({
  className,
  defaultAmount = '0.01',
  defaultTo = '',
  onSent,
  onError,
}: TransferWidgetProps) {
  const uid = useId();
  const { address, isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { balanceSPLD } = useBalance();
  const { network } = useNetwork();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const [to, setTo] = useState(defaultTo);
  const [amount, setAmount] = useState(defaultAmount);
  const [reviewing, setReviewing] = useState(false);
  const [recents, setRecents] = useState<string[]>(() => getRecentRecipients());

  if (connStatus === 'locked') {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Transfer SPLD</h3>
        <p className="sqc-widget__hint">Wallet locked — unlock to send.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Unlock
        </button>
      </div>
    );
  }

  if (!isConnected) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Transfer SPLD</h3>
        <p className="sqc-widget__hint">Connect a wallet to send SPLD.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Connect
        </button>
      </div>
    );
  }

  const review = reviewTransfer({ to, amount, balance: balanceSPLD, self: address });
  const toInvalid = !!to.trim() && !normalizeSqAddress(to);
  const isSelf = !review.ok && review.reason === 'self';
  const amountInvalid = !!amount.trim() && !isValidAmount(amount);
  const overBalance =
    !amountInvalid &&
    balanceSPLD != null &&
    !!amount.trim() &&
    compareDecimal(amount, balanceSPLD) === 1;
  const canReview = review.ok && status !== 'pending';

  const editTo = (v: string) => {
    setTo(v);
    setReviewing(false);
  };
  const editAmount = (v: string) => {
    setAmount(v);
    setReviewing(false);
  };

  const onConfirm = async () => {
    if (!review.ok) return;
    reset();
    let res: Awaited<ReturnType<typeof sendTransfer>>;
    try {
      res = await sendTransfer({
        to: review.to,
        amountSPLD: review.amount,
        waitForConfirmation: true,
      });
    } catch (e) {
      onError?.(e instanceof Error ? e : new Error(String(e)));
      return;
    }
    setRecents(rememberRecipient(review.to));
    setReviewing(false);
    onSent?.({
      to: review.to,
      amount: review.amount,
      hash: res.hash,
      confirmed: res.confirmed,
      explorerUrl: res.explorerUrl,
    });
  };

  return (
    <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
      <h3 className="sqc-widget__title">Transfer SPLD</h3>
      {balanceSPLD != null ? (
        <p className="sqc-widget__hint">Balance: {balanceSPLD} SPLD</p>
      ) : null}
      {reviewing && review.ok ? (
        <TransferReviewPanel
          to={review.to}
          amount={review.amount}
          symbol="SPLD"
          networkName={network?.name}
          pending={status === 'pending'}
          onBack={() => setReviewing(false)}
          onConfirm={() => void onConfirm()}
        />
      ) : (
        <>
          <label className="sqc-widget__label" htmlFor={`${uid}-to`}>
            To (SQ:…)
          </label>
          <input
            id={`${uid}-to`}
            className="sqc-input"
            value={to}
            onChange={(e) => editTo(e.target.value)}
            placeholder="SQ:…"
            autoComplete="off"
            aria-invalid={toInvalid || isSelf}
          />
          <RecentRecipients recipients={recents} onPick={editTo} />
          {toInvalid ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">
              {transferReviewMessage('invalid-address')}
            </p>
          ) : isSelf ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">{transferReviewMessage('self')}</p>
          ) : null}
          <label className="sqc-widget__label" htmlFor={`${uid}-amt`}>
            Amount (SPLD)
          </label>
          <div style={{ display: 'flex', gap: '0.5rem' }}>
            <input
              id={`${uid}-amt`}
              className="sqc-input"
              value={amount}
              onChange={(e) => editAmount(e.target.value)}
              autoComplete="off"
              aria-invalid={amountInvalid || overBalance}
            />
            {balanceSPLD != null ? (
              <button type="button" className="sqc-copy" onClick={() => editAmount(balanceSPLD)}>
                Max
              </button>
            ) : null}
          </div>
          {amountInvalid ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">
              {transferReviewMessage('bad-amount')}
            </p>
          ) : overBalance ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">
              {transferReviewMessage('insufficient', 'SPLD')}
            </p>
          ) : null}
          <button
            type="button"
            className="sqc-btn sqc-btn--primary"
            style={{ marginTop: '0.75rem', width: '100%' }}
            disabled={!canReview}
            onClick={() => setReviewing(true)}
          >
            {status === 'pending' ? 'Sending…' : 'Review'}
          </button>
        </>
      )}
      {hash ? (
        <p className="sqc-widget__msg">
          {confirmed ? 'Confirmed' : 'Submitted'}: {hash}
          {explorerUrl ? (
            <>
              {' · '}
              <a href={explorerUrl} target="_blank" rel="noreferrer" className="sqc-account-link">
                Explorer
              </a>
            </>
          ) : null}
        </p>
      ) : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">{connectErrorMessage(error)}</p>
      ) : null}
    </div>
  );
}

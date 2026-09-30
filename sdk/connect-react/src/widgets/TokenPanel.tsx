import { useId, useState } from 'react';
import { toUnits } from '@splendor/sqc-sdk/units';
import { useAccount, useConnect, useNetwork, useQRC20 } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';
import {
  compareDecimal,
  getRecentRecipients,
  isPositiveAmount,
  normalizeSqAddress,
  rememberRecipient,
  reviewTransfer,
  transferReviewMessage,
} from './utils.js';
import { RecentRecipients, TransferReviewPanel } from './TransferReview.js';

export type TokenSentResult = {
  token: string;
  to: string;
  /** Token amount as entered (decimal, before applying decimals). */
  amount: string;
  hash?: string;
};

export type TokenPanelProps = {
  /** QRC-20 token address. Optional — panel shows an address field when omitted. */
  tokenAddress?: string;
  className?: string;
  /** Default transfer amount (decimal). Default "1". */
  defaultAmount?: string;
  /** Called after the wallet submits the token transfer. */
  onSent?: (result: TokenSentResult) => void;
  /** Called when signing or submitting fails (including a wallet rejection). */
  onError?: (error: Error) => void;
};

/**
 * Drop-in QRC-20 balance + human-amount transfer panel.
 */
export function TokenPanel({
  tokenAddress: tokenAddressProp,
  className,
  defaultAmount = '1',
  onSent,
  onError,
}: TokenPanelProps) {
  const uid = useId();
  const { address, isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { network } = useNetwork();
  const [addrInput, setAddrInput] = useState(tokenAddressProp?.trim() ?? '');
  const tokenAddress = (tokenAddressProp?.trim() || addrInput.trim()) || null;
  const { balance, tokenInfo, transferHuman, status, hash, explorerUrl, error, reset, loading } =
    useQRC20(tokenAddress);
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState(defaultAmount);
  const [reviewing, setReviewing] = useState(false);
  const [recents, setRecents] = useState<string[]>(() => getRecentRecipients());

  const symbol =
    tokenInfo && typeof tokenInfo.symbol === 'string' ? tokenInfo.symbol : 'TOKEN';
  const decimals =
    tokenInfo && typeof tokenInfo.decimals === 'number' ? tokenInfo.decimals : 18;

  let humanBalance: string | null = null;
  if (balance != null) {
    try {
      humanBalance = toUnits(balance, decimals);
    } catch {
      humanBalance = balance;
    }
  }

  if (connStatus === 'locked') {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">{symbol}</h3>
        <p className="sqc-widget__hint">Wallet locked — unlock to manage this token.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Unlock
        </button>
      </div>
    );
  }

  if (!isConnected) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">{symbol}</h3>
        <p className="sqc-widget__hint">Connect a wallet to manage this token.</p>
        <button type="button" className="sqc-btn sqc-btn--primary" onClick={() => openModal('connect')}>
          Connect
        </button>
      </div>
    );
  }

  const review = reviewTransfer({ to, amount, balance: humanBalance, self: address });
  const toInvalid = !!to.trim() && !normalizeSqAddress(to);
  const isSelf = !review.ok && review.reason === 'self';
  const amountInvalid = !!amount.trim() && !isPositiveAmount(amount);
  const overBalance =
    !amountInvalid &&
    humanBalance != null &&
    !!amount.trim() &&
    compareDecimal(amount, humanBalance) === 1;
  const canReview = !!tokenAddress && review.ok && status !== 'pending';

  const editTo = (v: string) => {
    setTo(v);
    setReviewing(false);
  };
  const editAmount = (v: string) => {
    setAmount(v);
    setReviewing(false);
  };

  const onConfirm = async () => {
    if (!tokenAddress || !review.ok) return;
    reset();
    let res: { hash?: string };
    try {
      res = await transferHuman(review.to, review.amount);
    } catch (e) {
      onError?.(e instanceof Error ? e : new Error(String(e)));
      return;
    }
    setRecents(rememberRecipient(review.to));
    setReviewing(false);
    onSent?.({ token: tokenAddress, to: review.to, amount: review.amount, hash: res.hash });
  };

  return (
    <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
      <h3 className="sqc-widget__title">{symbol}</h3>
      {!tokenAddressProp ? (
        <>
          <label className="sqc-widget__label" htmlFor={`${uid}-addr`}>
            Token (SQ:…)
          </label>
          <input
            id={`${uid}-addr`}
            className="sqc-input"
            value={addrInput}
            onChange={(e) => setAddrInput(e.target.value)}
            placeholder="SQ:…"
            autoComplete="off"
          />
        </>
      ) : null}
      <p className="sqc-widget__hint">
        {!tokenAddress
          ? 'Enter a QRC-20 token address.'
          : loading
            ? 'Loading…'
            : humanBalance != null
              ? `Balance: ${humanBalance} ${symbol}`
              : '—'}
      </p>
      {reviewing && review.ok ? (
        <TransferReviewPanel
          to={review.to}
          amount={review.amount}
          symbol={symbol}
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
            Amount (human, e.g. 1.5)
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
            {humanBalance != null ? (
              <button type="button" className="sqc-copy" onClick={() => editAmount(humanBalance)}>
                Max
              </button>
            ) : null}
          </div>
          {amountInvalid ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">Enter a valid decimal amount</p>
          ) : overBalance ? (
            <p className="sqc-widget__msg sqc-widget__msg--error">
              {transferReviewMessage('insufficient', symbol)}
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
          Tx: {hash}
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

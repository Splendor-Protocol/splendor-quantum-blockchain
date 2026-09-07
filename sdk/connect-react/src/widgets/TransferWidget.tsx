import { useState } from 'react';
import { useAccount, useBalance, useSendTransfer } from '../react/hooks.js';

export type TransferWidgetProps = {
  className?: string;
  /** Default amount in SPLD. */
  defaultAmount?: string;
};

/**
 * Drop-in SPLD transfer panel (uses useSendTransfer / useBalance).
 */
export function TransferWidget({ className, defaultAmount = '0.01' }: TransferWidgetProps) {
  const { isConnected } = useAccount();
  const { balanceSPLD } = useBalance();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState(defaultAmount);

  if (!isConnected) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Transfer SPLD</h3>
        <p className="sqc-widget__hint">Connect a wallet to send SPLD.</p>
      </div>
    );
  }

  const onSend = async () => {
    reset();
    try {
      await sendTransfer({
        to: to.trim(),
        amountSPLD: amount.trim(),
        waitForConfirmation: true,
      });
    } catch {
      /* error state */
    }
  };

  return (
    <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
      <h3 className="sqc-widget__title">Transfer SPLD</h3>
      {balanceSPLD != null ? (
        <p className="sqc-widget__hint">Balance: {balanceSPLD} SPLD</p>
      ) : null}
      <label className="sqc-widget__label" htmlFor="sqc-xfer-to">
        To (SQ:…)
      </label>
      <input
        id="sqc-xfer-to"
        className="sqc-input"
        value={to}
        onChange={(e) => setTo(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <label className="sqc-widget__label" htmlFor="sqc-xfer-amt">
        Amount (SPLD)
      </label>
      <input
        id="sqc-xfer-amt"
        className="sqc-input"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        autoComplete="off"
      />
      <button
        type="button"
        className="sqc-btn sqc-btn--primary"
        style={{ marginTop: '0.75rem', width: '100%' }}
        disabled={status === 'pending' || !to.trim()}
        onClick={() => void onSend()}
      >
        {status === 'pending' ? 'Sending…' : 'Send'}
      </button>
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
      {error ? <p className="sqc-widget__msg sqc-widget__msg--error">{error.message}</p> : null}
    </div>
  );
}

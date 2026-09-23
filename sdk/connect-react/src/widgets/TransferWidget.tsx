import { useId, useState } from 'react';
import { useAccount, useBalance, useConnect, useSendTransfer } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';

export type TransferWidgetProps = {
  className?: string;
  /** Default amount in SPLD. */
  defaultAmount?: string;
};

const AMOUNT_RE = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;

function isValidAmount(v: string): boolean {
  const t = v.trim();
  return !!t && AMOUNT_RE.test(t) && t !== '0' && !/^0\.0+$/.test(t);
}

/**
 * Drop-in SPLD transfer panel (uses useSendTransfer / useBalance).
 */
export function TransferWidget({ className, defaultAmount = '0.01' }: TransferWidgetProps) {
  const uid = useId();
  const { isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { balanceSPLD } = useBalance();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState(defaultAmount);

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

  const canSend = !!to.trim() && isValidAmount(amount) && status !== 'pending';

  const onSend = async () => {
    if (!canSend) return;
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
      <label className="sqc-widget__label" htmlFor={`${uid}-to`}>
        To (SQ:…)
      </label>
      <input
        id={`${uid}-to`}
        className="sqc-input"
        value={to}
        onChange={(e) => setTo(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <label className="sqc-widget__label" htmlFor={`${uid}-amt`}>
        Amount (SPLD)
      </label>
      <input
        id={`${uid}-amt`}
        className="sqc-input"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        autoComplete="off"
      />
      {amount.trim() && !isValidAmount(amount) ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">Enter a valid amount greater than zero</p>
      ) : null}
      <button
        type="button"
        className="sqc-btn sqc-btn--primary"
        style={{ marginTop: '0.75rem', width: '100%' }}
        disabled={!canSend}
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
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">{connectErrorMessage(error)}</p>
      ) : null}
    </div>
  );
}

import { useId, useState } from 'react';
import { toUnits } from '@splendor/sqc-sdk/units';
import { useAccount, useConnect, useQRC20 } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';

const AMOUNT_RE = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;

export type TokenPanelProps = {
  /** QRC-20 token address. Optional — panel shows an address field when omitted. */
  tokenAddress?: string;
  className?: string;
};

/**
 * Drop-in QRC-20 balance + human-amount transfer panel.
 */
export function TokenPanel({ tokenAddress: tokenAddressProp, className }: TokenPanelProps) {
  const uid = useId();
  const { isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const [addrInput, setAddrInput] = useState(tokenAddressProp?.trim() ?? '');
  const tokenAddress = (tokenAddressProp?.trim() || addrInput.trim()) || null;
  const { balance, tokenInfo, transferHuman, status, hash, explorerUrl, error, reset, loading } =
    useQRC20(tokenAddress);
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState('1');

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

  const amountOk = AMOUNT_RE.test(amount.trim());
  const canSend =
    !!tokenAddress &&
    !!to.trim() &&
    amountOk &&
    status !== 'pending';

  const onTransfer = async () => {
    if (!tokenAddress || !amountOk) return;
    reset();
    try {
      await transferHuman(to.trim(), amount.trim());
    } catch {
      /* error state */
    }
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
        Amount (human, e.g. 1.5)
      </label>
      <input
        id={`${uid}-amt`}
        className="sqc-input"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        autoComplete="off"
      />
      {amount.trim() && !amountOk ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">Enter a valid decimal amount</p>
      ) : null}
      <button
        type="button"
        className="sqc-btn sqc-btn--primary"
        style={{ marginTop: '0.75rem', width: '100%' }}
        disabled={!canSend}
        onClick={() => void onTransfer()}
      >
        {status === 'pending' ? 'Sending…' : `Transfer ${symbol}`}
      </button>
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

import { useState } from 'react';
import { toUnits } from '@splendor/sqc-sdk/units';
import { useAccount, useConnect, useQRC20 } from '../react/hooks.js';
import { connectErrorMessage } from '../core/controller.js';

export type TokenPanelProps = {
  /** QRC-20 token address. */
  tokenAddress: string;
  className?: string;
};

/**
 * Drop-in QRC-20 balance + human-amount transfer panel.
 */
export function TokenPanel({ tokenAddress, className }: TokenPanelProps) {
  const { isConnected } = useAccount();
  const { status: connStatus, openModal } = useConnect();
  const { balance, tokenInfo, transferHuman, status, hash, explorerUrl, error, reset, loading } =
    useQRC20(tokenAddress || null);
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

  if (!tokenAddress) {
    return (
      <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
        <h3 className="sqc-widget__title">Token</h3>
        <p className="sqc-widget__hint">Pass a QRC-20 tokenAddress prop.</p>
      </div>
    );
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
      </div>
    );
  }

  const onTransfer = async () => {
    reset();
    try {
      await transferHuman(to.trim(), amount.trim());
    } catch {
      /* error state */
    }
  };

  const canSend = !!to.trim() && !!amount.trim() && Number(amount) > 0 && status !== 'pending';

  return (
    <div className={['sqc-widget', className].filter(Boolean).join(' ')}>
      <h3 className="sqc-widget__title">{symbol}</h3>
      <p className="sqc-widget__hint">
        {loading
          ? 'Loading…'
          : humanBalance != null
            ? `Balance: ${humanBalance} ${symbol}`
            : '—'}
      </p>
      <label className="sqc-widget__label" htmlFor="sqc-tok-to">
        To (SQ:…)
      </label>
      <input
        id="sqc-tok-to"
        className="sqc-input"
        value={to}
        onChange={(e) => setTo(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <label className="sqc-widget__label" htmlFor="sqc-tok-amt">
        Amount (human, e.g. 1.5)
      </label>
      <input
        id="sqc-tok-amt"
        className="sqc-input"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        autoComplete="off"
      />
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

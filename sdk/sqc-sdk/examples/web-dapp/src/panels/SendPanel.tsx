import { useState } from 'react';
import { useAccount, useSendTransfer } from '@splendor/connect-react';

export function SendPanel() {
  const { isConnected } = useAccount();
  const { sendTransfer, status, hash, explorerUrl, confirmed, error, reset } = useSendTransfer();
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState('0.01');

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Send SPLD</h2>
        <p className="msg">Connect a wallet to send native coin.</p>
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
    <div className="panel">
      <h2>Send SPLD</h2>
      <label htmlFor="send-to">To (SQ:…)</label>
      <input
        id="send-to"
        value={to}
        onChange={(e) => setTo(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <label htmlFor="send-amt">Amount (SPLD)</label>
      <input
        id="send-amt"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        inputMode="decimal"
      />
      <button
        type="button"
        disabled={status === 'pending' || !to.trim()}
        onClick={() => void onSend()}
      >
        {status === 'pending' ? 'Sending…' : 'Send'}
      </button>
      {hash ? (
        <p className="msg ok">
          {confirmed ? 'Confirmed' : 'Submitted'}: {hash}
          {explorerUrl ? (
            <>
              {' · '}
              <a href={explorerUrl} target="_blank" rel="noreferrer">
                Explorer
              </a>
            </>
          ) : null}
        </p>
      ) : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}

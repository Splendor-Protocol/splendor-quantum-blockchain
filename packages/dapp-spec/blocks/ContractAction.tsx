import { useState } from 'react';
import { TxResult, WidgetShell, connectErrorMessage, useContract } from '@splendor/connect-react';

export type ContractActionProps = {
  title?: string;
  contract?: string;
  functionSig?: string;
  /** Fixed arguments, comma-separated. */
  args?: string;
  buttonLabel?: string;
};

/** One-click contract call with fixed arguments. */
export function ContractAction({
  title = 'Action',
  contract = '',
  functionSig = 'claim()',
  args = '',
  buttonLabel = 'Run',
}: ContractActionProps) {
  const c = useContract(contract || null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hash, setHash] = useState<string | null>(null);

  const run = async () => {
    if (!contract || !functionSig) return;
    setBusy(true);
    setError(null);
    setHash(null);
    try {
      const list = args
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const res = await c.call(functionSig, list, { waitForConfirmation: true });
      setHash(res.hash ?? null);
    } catch (e) {
      setError(connectErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!contract) {
    return (
      <WidgetShell kind="contract-action" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a contract address.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="contract-action" title={title}>
      <p className="sqc-widget__msg dapp-mono">{functionSig}</p>
      <button type="button" className="sqc-btn sqc-btn--primary sqc-btn--block" disabled={busy} onClick={() => void run()}>
        {busy ? 'Sending…' : buttonLabel}
      </button>
      {error ? <p className="sqc-widget__error">{error}</p> : null}
      {hash ? <TxResult hash={hash} confirmed /> : null}
    </WidgetShell>
  );
}

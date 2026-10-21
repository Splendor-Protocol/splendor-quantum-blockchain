import { useState } from 'react';
import { TxResult, WidgetShell, connectErrorMessage, useAccount, useContract } from '@splendor/connect-react';

export type ContractWriteProps = {
  title?: string;
  contract?: string;
  functionSig?: string;
  args?: string;
  buttonLabel?: string;
};

function parseArgs(raw: string): string[] {
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Form to call a write function; args are a single comma-separated field. */
export function ContractWrite({
  title = 'Contract write',
  contract = '',
  functionSig = 'set(uint256)',
  args: defaultArgs = '',
  buttonLabel = 'Send transaction',
}: ContractWriteProps) {
  const { address } = useAccount();
  const c = useContract(contract || null);
  const [args, setArgs] = useState(defaultArgs);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hash, setHash] = useState<string | null>(null);

  const needsArgs = !/\(\s*\)$/.test(functionSig);

  const submit = async () => {
    if (!contract || !functionSig) return;
    setBusy(true);
    setError(null);
    setHash(null);
    try {
      const res = await c.call(functionSig, parseArgs(args), { waitForConfirmation: true });
      setHash(res.hash ?? null);
    } catch (e) {
      setError(connectErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!address) {
    return (
      <WidgetShell kind="contract-write" title={title}>
        <p className="sqc-widget__msg">Connect a wallet to send a transaction.</p>
      </WidgetShell>
    );
  }
  if (!contract) {
    return (
      <WidgetShell kind="contract-write" title={title}>
        <p className="sqc-widget__msg">Set a contract address.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="contract-write" title={title}>
      <p className="sqc-widget__msg dapp-mono">{functionSig}</p>
      {needsArgs ? (
        <label className="sqc-field">
          <span>Arguments</span>
          <input value={args} onChange={(e) => setArgs(e.target.value)} placeholder="comma separated" />
        </label>
      ) : null}
      <button type="button" className="sqc-btn" disabled={busy} onClick={() => void submit()}>
        {busy ? 'Sending…' : buttonLabel}
      </button>
      {error ? <p className="sqc-widget__error">{error}</p> : null}
      {hash ? <TxResult hash={hash} /> : null}
    </WidgetShell>
  );
}

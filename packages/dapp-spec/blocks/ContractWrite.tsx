import { useEffect, useState } from 'react';
import { TxResult, WidgetShell, connectErrorMessage, useAccount, useContract } from '@splendor/connect-react';

export type ContractWriteProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `contract` before render; ignored here. */
  contractId?: string;
  /** ABI params of the picked function (JSON). Passed by generated code; not used here. */
  fnParams?: string;
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
  // New defaults from the parent (or the playground's settings form) replace what was typed.
  useEffect(() => setArgs(defaultArgs), [defaultArgs]);

  const sig = functionSig.trim();
  const needsArgs = !/\(\s*\)$/.test(sig);

  const submit = async () => {
    if (!contract || !sig) return;
    setBusy(true);
    setError(null);
    setHash(null);
    try {
      const res = await c.call(sig, parseArgs(args), { waitForConfirmation: true });
      setHash(res.hash ?? null);
    } catch (e) {
      setError(connectErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (!contract) {
    return (
      <WidgetShell kind="contract-write" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a contract address.</p>
      </WidgetShell>
    );
  }
  if (!sig) {
    return (
      <WidgetShell kind="contract-write" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a function signature, e.g. set(uint256).</p>
      </WidgetShell>
    );
  }
  if (!address) {
    return (
      <WidgetShell kind="contract-write" title={title}>
        <p className="sqc-widget__msg">Connect a wallet to send a transaction.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="contract-write" title={title}>
      <p className="sqc-widget__msg dapp-mono">{sig}</p>
      {needsArgs ? (
        <label className="sqc-field">
          <span>Arguments</span>
          <input value={args} onChange={(e) => setArgs(e.target.value)} placeholder="comma separated" />
        </label>
      ) : null}
      <button type="button" className="sqc-btn" disabled={busy} onClick={() => void submit()}>
        {busy ? 'Sending…' : buttonLabel}
      </button>
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {error}
        </p>
      ) : null}
      {hash ? <TxResult hash={hash} /> : null}
    </WidgetShell>
  );
}

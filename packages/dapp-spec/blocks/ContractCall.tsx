import { useEffect, useId, useState } from 'react';
import { TxResult, WidgetShell, connectErrorMessage, useContract } from '@splendor/connect-react';

export type ContractCallProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `contract` before render; ignored here. */
  contractId?: string;
  /** ABI params of the picked function (JSON). Passed by generated code; not used here. */
  fnParams?: string;
  /** SQ: address of the deployed contract. */
  contract?: string;
  /** Function signature as compiled, e.g. `get()` or `set(uint256)`. */
  functionSig?: string;
  /** Default arguments, comma separated: SQ: addresses, base-10 integers or 0x hex. */
  args?: string;
  /** `read` runs a free call (no wallet needed); `write` signs a transaction. */
  mode?: 'read' | 'write';
  /** How to show a read result: the raw return data or one uint256. */
  decode?: 'raw' | 'uint256';
  buttonLabel?: string;
};

function parseArgs(raw: string): string[] {
  return raw
    .split(',')
    .map((a) => a.trim())
    .filter(Boolean);
}

/** Read or call one function of a deployed contract, with editable arguments. */
export function ContractCall({
  title = 'Contract',
  contract = '',
  functionSig = '',
  args = '',
  mode = 'read',
  decode = 'raw',
  buttonLabel,
}: ContractCallProps) {
  const uid = useId();
  const c = useContract(contract || null);
  const [input, setInput] = useState(args);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // New defaults from the parent (or the playground's settings form) replace what was typed.
  useEffect(() => setInput(args), [args]);
  useEffect(() => {
    setResult(null);
    setError(null);
  }, [contract, functionSig, mode, decode]);
  const takesArgs = !/\(\s*\)$/.test(functionSig.trim());

  if (!contract || !functionSig.trim()) {
    return (
      <WidgetShell kind="contract-call" title={title} gate={false}>
        <p className="sqc-widget__msg">Set a contract address and a function signature for this block.</p>
      </WidgetShell>
    );
  }

  const run = async () => {
    setError(null);
    setResult(null);
    setBusy(true);
    try {
      const list = parseArgs(input);
      if (mode === 'write') {
        await c.call(functionSig.trim(), list, { waitForConfirmation: true });
      } else if (decode === 'uint256') {
        setResult((await c.readUint256(functionSig.trim(), list)).toString());
      } else {
        const res = await c.read(functionSig.trim(), list);
        if (res.reverted) throw new Error(res.revert_reason || 'The call reverted');
        setResult(res.return_data ?? '0x');
      }
    } catch (e) {
      setError(connectErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const pending = busy || c.status === 'pending';
  return (
    <WidgetShell kind="contract-call" title={title} gate={mode === 'write'} status={c.status === 'pending' ? 'pending' : 'idle'}>
      <p className="sqc-widget__msg dapp-mono">
        {functionSig.trim()} on {contract.slice(0, 12)}…{contract.slice(-6)}
      </p>
      {takesArgs ? (
        <div className="sqc-field">
          <label className="sqc-widget__label" htmlFor={`${uid}-args`}>
            Arguments (comma separated)
          </label>
          <input id={`${uid}-args`} className="sqc-input" value={input} onChange={(e) => setInput(e.target.value)} autoComplete="off" />
        </div>
      ) : null}
      <button type="button" className="sqc-btn sqc-btn--primary sqc-btn--block" disabled={pending} onClick={() => void run()}>
        {pending ? 'Working…' : (buttonLabel ?? (mode === 'write' ? 'Send transaction' : 'Read'))}
      </button>
      {result != null ? (
        <p className="sqc-widget__msg dapp-mono" data-testid="dapp-contract-result">
          Result: {result}
        </p>
      ) : null}
      {mode === 'write' && c.hash ? <TxResult hash={c.hash} explorerUrl={c.explorerUrl} /> : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {error}
        </p>
      ) : null}
    </WidgetShell>
  );
}

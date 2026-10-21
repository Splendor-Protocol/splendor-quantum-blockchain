import { useEffect, useState } from 'react';
import { WidgetShell, connectErrorMessage, useContract } from '@splendor/connect-react';

export type ContractReadProps = {
  title?: string;
  contract?: string;
  functionSig?: string;
  args?: string;
  decode?: string;
  refreshMs?: number;
};

function parseArgs(raw: string): string[] {
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Live read of one contract function with optional auto-refresh. */
export function ContractRead({
  title = 'On-chain value',
  contract = '',
  functionSig = 'get()',
  args = '',
  decode = 'uint256',
  refreshMs = 0,
}: ContractReadProps) {
  const c = useContract(contract || null);
  const [value, setValue] = useState<string>('—');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = async () => {
    if (!contract || !functionSig) return;
    setBusy(true);
    setError(null);
    try {
      const list = parseArgs(args);
      if (decode === 'uint256') {
        const n = await c.readUint256(functionSig, list);
        setValue(n.toString());
      } else {
        const res = await c.read(functionSig, list);
        if (res.reverted) throw new Error(res.revert_reason || 'reverted');
        setValue(res.return_data || '0x');
      }
    } catch (e) {
      setError(connectErrorMessage(e));
      setValue('—');
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void load();
    if (!refreshMs || refreshMs < 1000) return;
    const t = window.setInterval(() => void load(), refreshMs);
    return () => window.clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contract, functionSig, args, decode, refreshMs]);

  if (!contract) {
    return (
      <WidgetShell kind="contract-read" title={title}>
        <p className="sqc-widget__msg">Set a contract address to read.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="contract-read" title={title}>
      <p style={{ fontSize: '1.5rem', fontWeight: 700, margin: '0.25rem 0' }}>{busy ? '…' : value}</p>
      <p className="sqc-widget__msg dapp-mono" style={{ margin: 0 }}>
        {functionSig}
      </p>
      {error ? <p className="sqc-widget__error">{error}</p> : null}
      <button type="button" className="sqc-btn sqc-btn--ghost" style={{ marginTop: '0.75rem' }} onClick={() => void load()} disabled={busy}>
        Refresh
      </button>
    </WidgetShell>
  );
}

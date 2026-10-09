import { useState } from 'react';
import { useAccount, useConnectController, useConnectState, useReadConnection } from '@splendor/connect-react';
import type { SqcProvider } from '@splendor/sqc-sdk';
import { CodeBlock } from '../components/CodeBlock';
import type { SdkRecipeManifest } from '../registry/recipes';
import { ERROR_HINTS } from './guides';

function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, (_k, x) => (typeof x === 'bigint' ? x.toString() : x), 2);
  } catch {
    return String(v);
  }
}

type Outcome = { ok: true; value: unknown; ms: number } | { ok: false; error: string; code?: string; hint?: string; ms: number };

export function RecipeRunner({ recipe }: { recipe: SdkRecipeManifest }) {
  const ctrl = useConnectController();
  const network = useConnectState((s) => s.network);
  const connection = useReadConnection();
  const { address, isConnected } = useAccount();
  const [inputs, setInputs] = useState<Record<string, string>>(() =>
    Object.fromEntries((recipe.inputs ?? []).map((i) => [i.name, i.defaultValue ?? ''])),
  );
  const [running, setRunning] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const [outcome, setOutcome] = useState<Outcome | null>(null);

  const run = async () => {
    if (!network) return;
    setRunning(true);
    setLogs([]);
    setOutcome(null);
    const t0 = performance.now();
    try {
      const wallet = isConnected ? (ctrl.getWallet() as unknown as { signMessage(m: string): Promise<unknown> }) : null;
      const value = await recipe.run({
        network,
        provider: ctrl.getProvider() as unknown as SqcProvider,
        connection,
        address,
        wallet,
        inputs,
        log: (line) => setLogs((l) => [...l, line]),
      });
      setOutcome({ ok: true, value, ms: Math.round(performance.now() - t0) });
    } catch (e) {
      const code = typeof (e as { code?: unknown })?.code === 'string' ? (e as { code: string }).code : undefined;
      const userHint = (e as { userHint?: string })?.userHint;
      const known = code ? ERROR_HINTS[code] : undefined;
      setOutcome({
        ok: false,
        error: e instanceof Error ? e.message : String(e),
        code,
        hint: userHint ?? (known ? `${known.meaning} Retry: ${known.retry}` : undefined),
        ms: Math.round(performance.now() - t0),
      });
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="pg-stack" data-testid={`recipe-${recipe.id}`}>
      <div>
        <h2>{recipe.title}</h2>
        <p className="pg-muted">{recipe.description}</p>
      </div>
      <CodeBlock code={recipe.code} />
      {recipe.inputs?.length ? (
        <div className="pg-form">
          {recipe.inputs.map((i) => (
            <div key={i.name} className="pg-field">
              <label htmlFor={`recipe-${recipe.id}-${i.name}`}>{i.label}</label>
              <input
                id={`recipe-${recipe.id}-${i.name}`}
                value={inputs[i.name] ?? ''}
                placeholder={i.placeholder}
                className="pg-mono"
                onChange={(e) => setInputs((s) => ({ ...s, [i.name]: e.target.value }))}
              />
            </div>
          ))}
        </div>
      ) : null}
      <div className="pg-row">
        <button type="button" className="pg-btn" disabled={running || (recipe.requiresWallet && !isConnected)} onClick={() => void run()}>
          {running ? 'Running…' : `Run on ${network?.name ?? network?.id ?? '…'}`}
        </button>
        {recipe.requiresWallet && !isConnected ? <span className="pg-muted">Connect a wallet to run this recipe.</span> : null}
        {outcome ? <span className="pg-muted">{outcome.ms} ms</span> : null}
      </div>
      {logs.length ? <div className="pg-output">{logs.join('\n')}</div> : null}
      {outcome?.ok ? (
        <div className="pg-output" data-testid="recipe-result">
          {pretty(outcome.value)}
        </div>
      ) : null}
      {outcome && !outcome.ok ? (
        <div className="pg-issues" role="alert" data-testid="recipe-error">
          <strong>{outcome.code ? `${outcome.code}: ` : ''}</strong>
          {outcome.error}
          {outcome.hint ? <p className="pg-muted">{outcome.hint}</p> : null}
        </div>
      ) : null}
    </div>
  );
}

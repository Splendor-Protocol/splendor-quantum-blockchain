import { useState } from 'react';
import { useAccount, useConnectController, useConnectState, useReadConnection } from '@splendor/connect-react';
import type { Connection, SqcProvider } from '@splendor/sqc-sdk';
import type { NetworkConfig } from '@splendor/sqc-sdk/networks';
import { CodeBlock } from '../components/CodeBlock';
import { MOCK_ADDRESS, createPlaygroundMock } from '../preview/mock';
import type { RecipeContext, SdkRecipeManifest } from '../registry/recipes';
import { ERROR_HINTS } from './guides';

function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, (_k, x) => (typeof x === 'bigint' ? x.toString() : x), 2);
  } catch {
    return String(v);
  }
}

type Outcome = { ok: true; value: unknown; ms: number } | { ok: false; error: string; code?: string; hint?: string; ms: number };
type Mode = 'live' | 'sandbox';

/** Any address works in the sandbox: unknown tokens and collections answer with demo data. */
export const SANDBOX_ASSET = 'SQ:' + 'd0'.repeat(32);
const ASSET_INPUTS = new Set(['token', 'collection']);

/** Context backed by the preview's in-memory chain: no node, no wallet, nothing is sent. */
export function sandboxContext(network: NetworkConfig, inputs: Record<string, string>, log: (line: string) => void): RecipeContext {
  const ctrl = createPlaygroundMock({ network: network.id as never, state: 'connected' });
  const connection = ctrl.wallet.connection as unknown as Connection;
  let height = 1000;
  const provider = {
    getStatus: async () => ({ chain_id: network.chainId, network: network.id, latest_height: height, sandbox: true }),
    getLatestBlock: async () => ({ height: ++height, hash: 'ab'.repeat(32), time: new Date().toISOString(), sandbox: true }),
    getBalance: (address: string) => (connection as unknown as { getBalance(a: string): Promise<unknown> }).getBalance(address),
  } as unknown as SqcProvider;
  const filled = Object.fromEntries(
    Object.entries(inputs).map(([k, v]) => [k, !v.trim() && ASSET_INPUTS.has(k) ? SANDBOX_ASSET : v]),
  );
  return { network, provider, connection, address: MOCK_ADDRESS, wallet: ctrl.wallet as never, inputs: filled, log };
}

export function RecipeRunner({ recipe }: { recipe: SdkRecipeManifest }) {
  const ctrl = useConnectController();
  const network = useConnectState((s) => s.network);
  const connection = useReadConnection();
  const { address, isConnected } = useAccount();
  const [mode, setMode] = useState<Mode>('live');
  const [inputs, setInputs] = useState<Record<string, string>>(() =>
    Object.fromEntries((recipe.inputs ?? []).map((i) => [i.name, i.defaultValue ?? ''])),
  );
  const [running, setRunning] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const sandbox = mode === 'sandbox';
  const needsWallet = !sandbox && recipe.requiresWallet && !isConnected;

  const run = async () => {
    if (!network) return;
    setRunning(true);
    setLogs([]);
    setOutcome(null);
    const log = (line: string) => setLogs((l) => [...l, line]);
    const t0 = performance.now();
    try {
      const ctx: RecipeContext = sandbox
        ? sandboxContext(network, inputs, log)
        : {
            network,
            provider: ctrl.getProvider() as unknown as SqcProvider,
            connection,
            address,
            wallet: isConnected ? (ctrl.getWallet() as unknown as { signMessage(m: string): Promise<unknown> }) : null,
            inputs,
            log,
          };
      const value = await recipe.run(ctx);
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
      <div className="pg-segmented" role="group" aria-label="Run against">
        <button type="button" aria-pressed={!sandbox} onClick={() => setMode('live')}>
          Live network
        </button>
        <button type="button" aria-pressed={sandbox} onClick={() => setMode('sandbox')}>
          Mock sandbox
        </button>
      </div>
      {sandbox ? (
        <p className="pg-muted" style={{ margin: 0 }}>
          The sandbox answers with demo data and a mock wallet. Empty token or collection fields use a demo asset. Nothing is sent.
        </p>
      ) : null}
      {recipe.inputs?.length ? (
        <div className="pg-form">
          {recipe.inputs.map((i) => (
            <div key={i.name} className="pg-field">
              <label htmlFor={`recipe-${recipe.id}-${i.name}`}>{i.label}</label>
              <input
                id={`recipe-${recipe.id}-${i.name}`}
                value={inputs[i.name] ?? ''}
                placeholder={sandbox && ASSET_INPUTS.has(i.name) ? 'empty: demo asset' : i.placeholder}
                className="pg-mono"
                onChange={(e) => setInputs((s) => ({ ...s, [i.name]: e.target.value }))}
              />
            </div>
          ))}
        </div>
      ) : null}
      <div className="pg-row">
        <button type="button" className="pg-btn" disabled={running || needsWallet} onClick={() => void run()}>
          {running ? 'Running…' : sandbox ? 'Run in sandbox' : `Run on ${network?.name ?? network?.id ?? '…'}`}
        </button>
        {needsWallet ? <span className="pg-muted">Connect a wallet, or try it in the mock sandbox.</span> : null}
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

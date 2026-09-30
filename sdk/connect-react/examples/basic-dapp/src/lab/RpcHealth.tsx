import { useEffect, useState } from 'react';
import { useNetwork } from '@splendor/connect-react';

type Health =
  | { kind: 'checking' }
  | { kind: 'ok' }
  | { kind: 'server-down'; detail: string }
  | { kind: 'node-down'; detail: string }
  | { kind: 'wrong-chain'; detail: string };

const POLL_MS = 15_000;

/**
 * Explains "Network unavailable" errors: is the lab's dev server (RPC proxy) gone, is the
 * node down, or is the node on another chain than the selected network?
 */
export function RpcHealth() {
  const { network } = useNetwork();
  const rpc = network?.rpcUrls[0];
  const [health, setHealth] = useState<Health>({ kind: 'checking' });

  useEffect(() => {
    if (!rpc || !network) return;
    let cancelled = false;
    const viaProxy = rpc.startsWith(window.location.origin);
    const check = async () => {
      let next: Health;
      try {
        const res = await fetch(`${rpc}/status`, { signal: AbortSignal.timeout(8_000) });
        if (!res.ok) {
          next = { kind: 'node-down', detail: `HTTP ${res.status}` };
        } else {
          const body = (await res.json()) as { chain_id?: string };
          next =
            body.chain_id && body.chain_id !== network.chainId
              ? { kind: 'wrong-chain', detail: body.chain_id }
              : { kind: 'ok' };
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        next = { kind: viaProxy ? 'server-down' : 'node-down', detail: msg };
      }
      if (!cancelled) setHealth(next);
    };
    void check();
    const id = setInterval(() => void check(), POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [rpc, network]);

  if (health.kind === 'ok' || health.kind === 'checking' || !network) return null;

  const text =
    health.kind === 'server-down'
      ? `The lab server has stopped, so every call fails with "Network unavailable". ` +
        `Run npm run lab:${network.id} from the repo root, then reload this page.`
      : health.kind === 'wrong-chain'
        ? `The ${network.name} RPC reports chain ${health.detail}, but ${network.chainId} is configured. ` +
          `Check VITE_SQC_${network.id.toUpperCase()}_RPC_URL and _CHAIN_ID in examples/basic-dapp/.env.local.`
        : `The ${network.name} RPC node is not responding (${health.detail}). ` +
          (network.id === 'local'
            ? 'The local network needs a node on http://127.0.0.1:8080. Pick testnet or devnet in the badge instead.'
            : 'Try again in a minute, or pick another network in the badge.');

  return (
    <p className="lab-rpc-health" role="alert" data-kind={health.kind}>
      {text}
    </p>
  );
}

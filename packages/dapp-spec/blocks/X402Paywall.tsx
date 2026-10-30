import { useEffect, useState } from 'react';
import { fromUnits, toUnits } from '@splendor/sqc-sdk';
import { WidgetShell, connectErrorMessage, useAccount, useWallet } from '@splendor/connect-react';

export type X402PaywallProps = {
  title?: string;
  /** Teaser shown before payment. */
  teaser?: string;
  /**
   * Paid resource served behind `createX402Middleware` (from `@splendor/sqc-sdk/x402`). The block
   * requests it, reads the 402 requirements, pays with the wallet, and retries with `X-Payment`.
   * Must be https (http only for localhost). When empty the block runs a client-only demo.
   */
  resourceUrl?: string;
  /** Most the reader will pay (SPLD). A server asking for more is refused. */
  maxPrice?: string;
  /** Demo mode only: who receives the payment. */
  payTo?: string;
  /** Demo mode only: price in SPLD. */
  price?: string;
  /** Demo mode only: content revealed after paying. */
  content?: string;
  buttonLabel?: string;
};

const MAX_BODY = 20_000;
const SQ_RE = /^SQ:[0-9a-fA-F]{64}$/;
const UINT_RE = /^(0|[1-9]\d*)$/;

/** https anywhere, http only on this machine. */
function safeUrl(raw: string): string | null {
  try {
    const u = new URL(raw.trim());
    const local = u.hostname === 'localhost' || u.hostname === '127.0.0.1';
    if (u.username || u.password) return null;
    return u.protocol === 'https:' || (u.protocol === 'http:' && local) ? u.href : null;
  } catch {
    return null;
  }
}

function spld(atto: string): string {
  try {
    return toUnits(BigInt(atto), 18).replace(/\.0+$/, '');
  } catch {
    return atto;
  }
}

type Requirements = { pay_to: string; amount: string; description?: string };
type Stage =
  | { kind: 'idle' }
  | { kind: 'checking' }
  | { kind: 'quote'; req: Requirements }
  | { kind: 'paying' }
  | { kind: 'unlocked'; body: string; txHash?: string }
  | { kind: 'error'; message: string };

async function readBody(res: Response): Promise<string> {
  const text = await res.text();
  return text.length > MAX_BODY ? `${text.slice(0, MAX_BODY)}…` : text;
}

/** Pay-per-view content gate on the x402 protocol (HTTP 402 + on-chain settlement). */
export function X402Paywall({
  title = 'Premium content',
  teaser = 'Pay once with SPLD to unlock this content.',
  resourceUrl = '',
  maxPrice = '1',
  payTo = '',
  price = '0.1',
  content = 'Thanks for paying! This is the premium content.',
  buttonLabel = 'Unlock',
}: X402PaywallProps) {
  const { address } = useAccount();
  const { wallet, ready } = useWallet();
  const [stage, setStage] = useState<Stage>({ kind: 'idle' });
  const url = resourceUrl.trim() ? safeUrl(resourceUrl) : null;
  const badUrl = resourceUrl.trim() !== '' && !url;
  const demo = !resourceUrl.trim();
  let capAtto: bigint | null = null;
  try {
    capAtto = BigInt(fromUnits(maxPrice || '0', 18));
  } catch {
    capAtto = null;
  }

  // A payment belongs to one account: switching wallets locks the content again.
  useEffect(() => setStage({ kind: 'idle' }), [address, resourceUrl]);

  const pay = async (req: Requirements) => {
    if (!ready || !wallet) return;
    setStage({ kind: 'paying' });
    try {
      const res = await wallet.payX402({
        to: req.pay_to,
        amount: req.amount,
        requirements: req,
        settle: true,
        maxAmount: capAtto != null ? capAtto.toString() : req.amount,
      });
      const settle = (res.settle ?? {}) as { tx_hash?: string };
      if (demo) {
        setStage({ kind: 'unlocked', body: content, txHash: settle.tx_hash });
        return;
      }
      const retry = await fetch(url!, { credentials: 'omit', headers: { 'X-Payment': JSON.stringify(res.payload) } });
      if (!retry.ok) {
        const reason = await retry.json().then((j: { reason?: string; error?: string }) => j.reason ?? j.error, () => null);
        throw new Error(reason ? `The server did not accept the payment: ${reason}` : `The server responded ${retry.status}`);
      }
      setStage({ kind: 'unlocked', body: await readBody(retry), txHash: settle.tx_hash });
    } catch (e) {
      setStage({ kind: 'error', message: connectErrorMessage(e, 'tx') });
    }
  };

  const start = async () => {
    if (demo) {
      if (!SQ_RE.test(payTo) || !/^\d+(\.\d+)?$/.test(price)) {
        setStage({ kind: 'error', message: 'Set "Pay to" and "Price" in Content to try the demo.' });
        return;
      }
      setStage({ kind: 'quote', req: { pay_to: payTo, amount: fromUnits(price, 18), description: 'Demo payment' } });
      return;
    }
    if (!url) return;
    setStage({ kind: 'checking' });
    try {
      const res = await fetch(url, { credentials: 'omit' });
      if (res.ok) {
        setStage({ kind: 'unlocked', body: await readBody(res) });
        return;
      }
      if (res.status !== 402) throw new Error(`The server responded ${res.status}`);
      const body = (await res.json()) as { requirements?: Partial<Requirements> };
      const req = body.requirements;
      if (!req || typeof req.pay_to !== 'string' || !SQ_RE.test(req.pay_to) || typeof req.amount !== 'string' || !UINT_RE.test(req.amount)) {
        throw new Error('The server sent payment requirements this block cannot read.');
      }
      setStage({ kind: 'quote', req: { pay_to: req.pay_to, amount: req.amount, description: req.description } });
    } catch (e) {
      setStage({ kind: 'error', message: e instanceof Error ? e.message : String(e) });
    }
  };

  const overCap = stage.kind === 'quote' && capAtto != null && BigInt(stage.req.amount) > capAtto;

  return (
    <WidgetShell kind="x402-paywall" title={title}>
      {stage.kind === 'unlocked' ? (
        <div data-testid="dapp-x402-content">
          <p className="sqc-widget__msg" style={{ whiteSpace: 'pre-wrap' }}>
            {stage.body}
          </p>
          {stage.txHash ? <p className="sqc-widget__hint">Paid in transaction {stage.txHash.slice(0, 12)}…</p> : null}
        </div>
      ) : (
        <>
          <p className="sqc-widget__msg">{teaser}</p>
          {stage.kind === 'quote' ? (
            <div className="sqc-review" data-testid="dapp-x402-quote">
              <p className="sqc-widget__msg">
                Pay <strong>{spld(stage.req.amount)} SPLD</strong> to {stage.req.pay_to.slice(0, 12)}…
                {stage.req.description ? ` — ${stage.req.description}` : ''}
              </p>
              {overCap ? (
                <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
                  The price is above your limit of {maxPrice} SPLD.
                </p>
              ) : null}
              <button type="button" className="sqc-btn sqc-btn--primary sqc-btn--block" disabled={overCap || !ready} onClick={() => void pay(stage.req)}>
                Pay and unlock
              </button>
              <button type="button" className="sqc-btn sqc-btn--ghost sqc-btn--block" onClick={() => setStage({ kind: 'idle' })}>
                Cancel
              </button>
            </div>
          ) : (
            <button
              type="button"
              className="sqc-btn sqc-btn--primary sqc-btn--block"
              disabled={badUrl || stage.kind === 'checking' || stage.kind === 'paying'}
              onClick={() => void start()}
            >
              {stage.kind === 'checking' ? 'Checking…' : stage.kind === 'paying' ? 'Paying…' : buttonLabel}
            </button>
          )}
          {stage.kind === 'error' ? (
            <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
              {stage.message}
            </p>
          ) : null}
        </>
      )}
      {badUrl ? (
        <p className="sqc-widget__msg sqc-widget__msg--error">resourceUrl must be https (http only for localhost) and is ignored.</p>
      ) : null}
      {demo ? (
        <p className="sqc-widget__msg dapp-note">
          Demo mode: the payment is real but the content ships with the page, so this is not access control. Set
          <code> resourceUrl</code> to a server using <code>createX402Middleware</code>.
        </p>
      ) : null}
    </WidgetShell>
  );
}

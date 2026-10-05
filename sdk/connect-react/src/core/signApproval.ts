import { BridgeError } from '@splendor/sqc-sdk';

/** What an in-page (seed) wallet is about to sign. Shown to the user before signing. */
export type SignRequest = {
  kind: 'transfer' | 'call' | 'deploy' | 'module' | 'message' | 'sign';
  /** Wallet method that triggered the request (e.g. `sendTransfer`). */
  method: string;
  to?: string;
  /** Human SPLD amount when known, else a raw atto amount suffixed with ` atto`. */
  amount?: string;
  summary: string;
  /** Text to sign, for `kind: 'message'`. */
  message?: string;
};

/** Return true to sign, false to reject (the call then throws `USER_REJECTED`). */
export type SignApprover = (req: SignRequest) => boolean | Promise<boolean>;

/** Wallet methods that produce a signature with the in-page key. */
export const GATED_METHODS = [
  'sendTransfer',
  'callContract',
  'deployContract',
  'payX402',
  'signTransferOnly',
  'signTransaction',
  'sendTransaction',
  'signAndSendTransaction',
  'signMessage',
  'sendModuleCall',
  'signPermit',
  'signOrder',
  'signCancel',
  'signIncrementNonce',
  'signBridgeClaim',
] as const;

const GATED = new Set<string>(GATED_METHODS);

const str = (v: unknown): string | undefined =>
  typeof v === 'string' && v ? v : typeof v === 'number' || typeof v === 'bigint' ? String(v) : undefined;

function amountOf(o: Record<string, unknown>, spldKey: string, rawKey: string): string | undefined {
  const spld = str(o[spldKey]);
  if (spld) return `${spld} SPLD`;
  const raw = str(o[rawKey]);
  return raw ? `${raw} atto` : undefined;
}

function short(addr: string | undefined): string {
  if (!addr) return '';
  return addr.length > 20 ? `${addr.slice(0, 12)}…${addr.slice(-6)}` : addr;
}

/** Build a human summary for a gated wallet call. Never includes secrets. */
export function describeSignRequest(method: string, args: unknown[]): SignRequest {
  const a0 = args[0];
  const o = a0 && typeof a0 === 'object' ? (a0 as Record<string, unknown>) : {};
  switch (method) {
    case 'sendTransfer':
    case 'signTransferOnly':
    case 'payX402': {
      const to = str(o.to);
      const amount = amountOf(o, 'amountSPLD', 'amount');
      const verb = method === 'payX402' ? 'Pay' : 'Send';
      return {
        kind: 'transfer',
        method,
        to,
        amount,
        summary: `${verb} ${amount ?? 'SPLD'} to ${short(to) || 'an address'}`,
      };
    }
    case 'callContract': {
      const to = str(o.contract);
      const amount = amountOf(o, 'valueSPLD', 'value');
      const fn = str(o.function);
      return {
        kind: 'call',
        method,
        to,
        amount,
        summary: `Call ${fn ?? 'contract'} on ${short(to) || 'a contract'}${amount ? ` with ${amount}` : ''}`,
      };
    }
    case 'deployContract':
      return { kind: 'deploy', method, summary: 'Deploy a contract' };
    case 'sendModuleCall': {
      const mod = str(o.module) ?? 'module';
      const action = str(o.action) ?? 'call';
      const params = o.params && typeof o.params === 'object' ? (o.params as Record<string, unknown>) : {};
      const to = str(params.to) ?? str(params.recipient);
      const amount = str(params.amount);
      if (mod === 'qrc20' && action === 'deploy') {
        return { kind: 'deploy', method, summary: `Deploy token ${str(params.symbol) ?? ''}`.trim() };
      }
      return {
        kind: 'module',
        method,
        to,
        amount,
        summary: `${mod}.${action}${to ? ` to ${short(to)}` : ''}${amount ? ` (${amount})` : ''}`,
      };
    }
    case 'signMessage': {
      const message = typeof a0 === 'string' ? a0 : undefined;
      return { kind: 'message', method, message, summary: 'Sign a message' };
    }
    case 'sendTransaction':
    case 'signAndSendTransaction':
    case 'signTransaction': {
      const to = str(o.to);
      const amount = amountOf(o, 'amountSPLD', 'amount');
      return {
        kind: 'sign',
        method,
        to,
        amount,
        summary: `Sign a transaction${to ? ` to ${short(to)}` : ''}${amount ? ` (${amount})` : ''}`,
      };
    }
    default:
      return { kind: 'sign', method, summary: `Sign ${method.replace(/^sign/, '').toLowerCase() || 'data'}` };
  }
}

/**
 * Wrap a wallet so every signing method asks `approve` first. Internal calls inside the
 * wallet run on the unwrapped target, so one user action prompts once.
 */
export function withSignApproval<W extends object>(wallet: W, approve: SignApprover): W {
  const cache = new Map<PropertyKey, unknown>();
  return new Proxy(wallet, {
    get(target, prop, receiver) {
      const value = Reflect.get(target, prop, receiver);
      if (typeof value !== 'function') return value;
      if (cache.has(prop)) return cache.get(prop);
      const bound =
        typeof prop === 'string' && GATED.has(prop)
          ? async (...args: unknown[]) => {
              const ok = await approve(describeSignRequest(prop, args));
              if (!ok) throw BridgeError.userRejected('Signing was rejected');
              return (value as (...a: unknown[]) => unknown).apply(target, args);
            }
          : (value as (...a: unknown[]) => unknown).bind(target);
      cache.set(prop, bound);
      return bound;
    },
  });
}

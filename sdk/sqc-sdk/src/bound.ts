/**
 * Bind a wallet to a Program / Contract client so write methods omit the wallet arg.
 *
 * Unbound APIs stay intact: `token.transfer(wallet, to, amount)`.
 * Bound: `token.withSigner(wallet).transfer(to, amount)`.
 */
import type { SplendorPQBridge } from './splendor-wallet.js';

export type WalletLike = Pick<SplendorPQBridge, 'sendModuleCall' | 'callContract'> | import('./core/signer.js').Signer;

function isWalletLike(v: unknown): v is WalletLike {
  return (
    !!v &&
    typeof v === 'object' &&
    typeof (v as { sendModuleCall?: unknown }).sendModuleCall === 'function'
  );
}

/** Strip leading wallet parameter from write-method signatures. */
export type BoundClient<T> = {
  [K in keyof T]: T[K] extends (wallet: infer W, ...rest: infer R) => infer Ret
    ? W extends WalletLike
      ? (...args: R) => Ret
      : T[K]
    : T[K];
};

/**
 * Proxy that injects `wallet` as the first argument for known write methods.
 * Read methods and already-wallet-first calls pass through unchanged.
 */
export function withSigner<T extends object>(
  client: T,
  wallet: WalletLike,
  writeMethods: ReadonlySet<string>,
): BoundClient<T> {
  return new Proxy(client, {
    get(target, prop, receiver) {
      if (prop === '__signer') return wallet;
      const value = Reflect.get(target, prop, receiver);
      if (typeof value !== 'function') return value;
      const name = typeof prop === 'string' ? prop : String(prop);
      if (!writeMethods.has(name)) {
        return value.bind(target);
      }
      return (...args: unknown[]) => {
        if (args.length > 0 && isWalletLike(args[0])) {
          return (value as (...a: unknown[]) => unknown).apply(target, args);
        }
        return (value as (...a: unknown[]) => unknown).apply(target, [wallet, ...args]);
      };
    },
  }) as BoundClient<T>;
}

/** Attach withSigner onto a client prototype-style helper. */
export function attachWithSigner<T extends object>(
  client: T,
  writeMethods: ReadonlySet<string>,
): T & { withSigner(wallet: WalletLike): BoundClient<T> } {
  const extended = client as T & { withSigner(wallet: WalletLike): BoundClient<T> };
  extended.withSigner = (wallet: WalletLike) => withSigner(client, wallet, writeMethods);
  return extended;
}

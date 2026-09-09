/**
 * MetaMaskAccountWatcher — account/disconnect watchers for MetaMask-derived sessions.
 * Lives beside MetaMask connector; Wallet installs after connectFromMetaMask.
 */
import {
  signDerivationWithMetaMask,
  onMetaMaskAccountsChanged,
  onMetaMaskDisconnect,
  type EthereumProvider,
} from '../metamask.js';
import { assertSignatureMatchesAddress, type DerivationScheme } from '../derive.js';
import { strip0x } from '../derive.js';
import { wrapUnknown } from '../errors.js';
import { parseWasmJson, unwrap, type WasmSignerApi } from '../wasm-loader.js';
import type { DeriveInfo } from '../types.js';

export type MetaMaskWatchHost = {
  getEvmAddress(): string | undefined;
  init(): Promise<void>;
  api(): WasmSignerApi;
  sid(): string;
  setInfo(info: DeriveInfo): void;
  emitAccountsChanged(info: DeriveInfo): void;
  disconnect(): void;
  failAccountSwitch(err: unknown): void;
};

export type MetaMaskWatchOpts = {
  ethereumProvider?: EthereumProvider;
  derivation: DerivationScheme;
};

/**
 * Subscribe to MetaMask account / disconnect events. Returns unwatch.
 * Fail-closed: throws if watchers cannot be attached.
 */
export function attachMetaMaskAccountWatcher(
  host: MetaMaskWatchHost,
  opts: MetaMaskWatchOpts,
): () => void {
  try {
    const unsubAccounts = onMetaMaskAccountsChanged(async (accounts) => {
      if (!accounts.length) {
        host.disconnect();
        return;
      }
      const next = accounts[0];
      const current = host.getEvmAddress();
      if (current && next.toLowerCase() === current.toLowerCase()) {
        return;
      }
      try {
        const { evmAddress, signatureHex, scheme } = await signDerivationWithMetaMask({
          address: next,
          provider: opts.ethereumProvider,
          scheme: opts.derivation,
        });
        assertSignatureMatchesAddress(signatureHex, evmAddress, scheme);
        await host.init();
        const raw = unwrap(host.api().sqDeriveFromSigHex(host.sid(), strip0x(signatureHex)));
        const info = parseWasmJson<DeriveInfo>(raw as string);
        info.evmAddress = evmAddress;
        host.setInfo(info);
        host.emitAccountsChanged(info);
      } catch (e) {
        host.failAccountSwitch(e);
      }
    }, opts.ethereumProvider);
    const unsubDisconnect = onMetaMaskDisconnect(() => {
      host.disconnect();
    }, opts.ethereumProvider);
    return () => {
      unsubAccounts();
      unsubDisconnect();
    };
  } catch (e) {
    throw wrapUnknown(e, 'NO_PROVIDER');
  }
}

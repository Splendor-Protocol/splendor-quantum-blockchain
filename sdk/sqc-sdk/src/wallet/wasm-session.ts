/**
 * WasmSession — WASM signer lifecycle (init / lock / destroy / sign mutex / pending nonce).
 * Owned by Wallet; keeps document/MetaMask concerns out of the crypto session.
 */
import { loadWasmSigner, unwrap, type WasmSignerApi } from '../wasm-loader.js';
import { BridgeError } from '../errors.js';
import { MODULE_ROUTER_ADDRESS } from '../addresses/index.js';
import { getDefaultNetwork, isLoopbackOrLocalhostUrl } from '../networks.js';

export type WasmSessionInitOpts = {
  networkId?: string;
  rpcUrls: string[];
  chainId: string;
  wasmUrl?: string;
  execUrl?: string;
  wasmSha256?: string;
  execSha256?: string;
  allowUnpinnedWasm?: boolean;
};

export class WasmSession {
  private wasm: WasmSignerApi | null = null;
  private sessionId: string | null = null;
  private wasmChainId: string | null = null;
  private pendingNonce: number | null = null;
  private signChain: Promise<unknown> = Promise.resolve();

  get apiOrNull(): WasmSignerApi | null {
    return this.wasm;
  }

  get id(): string | null {
    return this.sessionId;
  }

  isReady(): boolean {
    return !!this.wasm && !!this.sessionId;
  }

  isUnlocked(): boolean {
    return !!this.wasm && !!this.sessionId && this.wasm.sqIsUnlocked(this.sessionId);
  }

  api(): WasmSignerApi {
    if (!this.wasm || !this.sessionId) {
      throw BridgeError.notConnected('Call init() or connect*() first');
    }
    return this.wasm;
  }

  sid(): string {
    if (!this.sessionId) throw BridgeError.notConnected();
    return this.sessionId;
  }

  syncChainId(chainId: string): void {
    if (!this.wasm || this.wasmChainId === chainId) return;
    unwrap(this.wasm.sqSetChainID(chainId));
    this.wasmChainId = chainId;
  }

  async init(opts: WasmSessionInitOpts): Promise<void> {
    if (this.wasm && this.sessionId) return;
    const isBrowser = typeof document !== 'undefined';
    const networkId = opts.networkId ?? getDefaultNetwork().id;
    const rpcUrls = opts.rpcUrls;
    const wasmUrl = opts.wasmUrl ?? '';
    const needsPin =
      isBrowser &&
      !opts.allowUnpinnedWasm &&
      !opts.wasmSha256 &&
      (networkId !== 'local' ||
        rpcUrls.some((u) => !isLoopbackOrLocalhostUrl(u)) ||
        (wasmUrl !== '' && !isLoopbackOrLocalhostUrl(wasmUrl)));
    if (needsPin) {
      throw BridgeError.wasm(
        `wasmSha256 is required for non-local endpoints (network "${networkId}") in the browser ` +
          '(pass wasmSha256 or allowUnpinnedWasm: true)',
      );
    }
    // A pinned .wasm is only as safe as the wasm_exec.js glue that instantiates it.
    if (isBrowser && opts.wasmSha256 && !opts.execSha256 && !opts.allowUnpinnedWasm) {
      throw BridgeError.wasm(
        'execSha256 is required whenever wasmSha256 is set (pin wasm_exec.js too; sqcWasmPlugin prints both hashes)',
      );
    }
    this.wasm = await loadWasmSigner({
      wasmUrl: opts.wasmUrl,
      execUrl: opts.execUrl,
      wasmSha256: opts.wasmSha256,
      execSha256: opts.execSha256,
    });
    this.syncChainId(opts.chainId);
    const wasmRouter = unwrap(this.wasm.sqGetModuleRouterAddress()) as string;
    if (wasmRouter !== MODULE_ROUTER_ADDRESS) {
      throw BridgeError.wasm(
        `module router mismatch: WASM reports ${wasmRouter}, SDK expects ${MODULE_ROUTER_ADDRESS}`,
      );
    }
    this.sessionId = this.wasm.sqCreateSession();
  }

  withSignLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.signChain.then(fn, fn);
    this.signChain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  async nextAccountNonce(chainNonce: number): Promise<number> {
    return this.pendingNonce != null ? Math.max(chainNonce, this.pendingNonce) : chainNonce;
  }

  reserveNonce(nonce: number): void {
    this.pendingNonce = nonce + 1;
  }

  /** Undo `reserveNonce(nonce)` when its tx was never accepted, so the next tx reuses the nonce. */
  releaseNonce(nonce: number): void {
    if (this.pendingNonce === nonce + 1) this.pendingNonce = null;
  }

  clearPendingNonce(): void {
    this.pendingNonce = null;
  }

  /** Clear key material; keep session slot for reconnect. */
  lock(): void {
    if (this.wasm && this.sessionId) {
      this.wasm.sqLock(this.sessionId);
    }
    this.pendingNonce = null;
  }

  /** Destroy WASM session slot. */
  destroy(): void {
    if (this.wasm && this.sessionId) {
      this.wasm.sqDestroySession(this.sessionId);
    }
    this.sessionId = null;
    this.pendingNonce = null;
  }

  moduleRouterAddress(): string | null {
    if (!this.wasm) return null;
    const r = this.wasm.sqGetModuleRouterAddress();
    return unwrap(r) as string;
  }
}

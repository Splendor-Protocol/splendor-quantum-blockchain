/**
 * SplendorPQBridge — signing wallet (WASM Dilithium sessions).
 */
import {
  assertSignatureMatchesAddress,
  evmAddressFromPrivateKey,
  signDerivationMessage,
  strip0x,
  type DerivationScheme,
} from './derive.js';
import { loadWasmSigner, parseWasmJson, unwrap, type WasmSignerApi } from './wasm-loader.js';
import { SqRpc, fromSPLD } from './rpc.js';
import { SqcProvider } from './provider.js';
import {
  signDerivationWithMetaMask,
  onMetaMaskAccountsChanged,
  type EthereumProvider,
} from './metamask.js';
import { BridgeError, wrapUnknown } from './errors.js';
import { assertAddress } from './address.js';
import {
  assertFeeAtLeastMin,
  encodePayload,
  bytesToHex,
  MIN_FEE,
} from './encoding.js';
import { encodeFunctionData } from './abi.js';
import { BridgeEmitter, type BridgeEventMap } from './events.js';
import type {
  BridgeOptions,
  DeriveInfo,
  ConnectionState,
  TransferOpts,
  CallOpts,
  DeployOpts,
  X402PayOpts,
  PreflightResult,
} from './types.js';

export type {
  BridgeOptions,
  DeriveInfo,
  ConnectionState,
  TransferOpts,
  CallOpts,
  DeployOpts,
  X402PayOpts,
  PreflightResult,
};

export class SplendorPQBridge {
  readonly rpc: SqRpc;
  readonly provider: SqcProvider;
  private wasm: WasmSignerApi | null = null;
  private sessionId: string | null = null;
  private info: DeriveInfo | null = null;
  private readonly opts: BridgeOptions;
  private readonly emitter = new BridgeEmitter();
  private unwatchMetaMask: (() => void) | null = null;
  private metaMaskWatchEnabled: boolean;
  private readonly ethereumProvider?: EthereumProvider;
  private readonly derivation: DerivationScheme;

  constructor(options: BridgeOptions = {}) {
    this.opts = options;
    this.metaMaskWatchEnabled = options.watchMetaMaskAccounts !== false;
    this.ethereumProvider = options.ethereumProvider;
    this.derivation = options.derivation || 'eip191-v1';
    if (options.provider) {
      this.provider = options.provider;
      this.rpc = options.provider.rpc;
    } else {
      this.rpc = new SqRpc(options);
      this.provider = new SqcProvider(options, this.rpc);
    }
  }

  static fromProvider(provider: SqcProvider, extra?: Omit<BridgeOptions, 'provider'>): SplendorPQBridge {
    return new SplendorPQBridge({ ...provider.toBridgeOptions(), ...extra, provider });
  }

  on<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): () => void {
    return this.emitter.on(event, handler);
  }

  off<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): void {
    this.emitter.off(event, handler);
  }

  async init(): Promise<void> {
    if (this.wasm && this.sessionId) return;
    this.wasm = await loadWasmSigner({
      wasmUrl: this.opts.wasmUrl,
      execUrl: this.opts.execUrl,
      wasmSha256: this.opts.wasmSha256,
    });
    this.sessionId = this.wasm.sqCreateSession();
  }

  private api(): WasmSignerApi {
    if (!this.wasm || !this.sessionId) {
      throw BridgeError.notConnected('Call init() or connect*() first');
    }
    return this.wasm;
  }

  private sid(): string {
    if (!this.sessionId) throw BridgeError.notConnected();
    return this.sessionId;
  }

  /** SQDP-1: assert chain before any sign/submit. */
  private async beforeSignAndSubmit(): Promise<void> {
    this.opts.onRequest?.({ method: 'assertChainId' });
    await this.rpc.assertChainId();
  }

  private emitConnect(info: DeriveInfo, changed = false): void {
    const payload = { address: info.address, evmAddress: info.evmAddress };
    if (changed) this.emitter.emit('accountsChanged', payload);
    else this.emitter.emit('connect', payload);
  }

  private failAccountSwitch(err: unknown): void {
    const be = wrapUnknown(err, 'USER_REJECTED');
    this.lock();
    this.emitter.emit('error', { code: be.code, message: be.message });
    this.emitter.emit('disconnect', {});
  }

  private ensureMetaMaskWatch(): void {
    if (!this.metaMaskWatchEnabled || this.unwatchMetaMask) return;
    try {
      this.unwatchMetaMask = onMetaMaskAccountsChanged(async (accounts) => {
        if (!accounts.length) {
          this.lock();
          this.emitter.emit('disconnect', {});
          return;
        }
        const next = accounts[0];
        if (this.info?.evmAddress && next.toLowerCase() === this.info.evmAddress.toLowerCase()) {
          return;
        }
        try {
          const { evmAddress, signatureHex, scheme } = await signDerivationWithMetaMask({
            address: next,
            provider: this.ethereumProvider,
            scheme: this.derivation,
          });
          assertSignatureMatchesAddress(signatureHex, evmAddress, scheme);
          await this.init();
          const raw = unwrap(this.api().sqDeriveFromSigHex(this.sid(), strip0x(signatureHex)));
          this.info = parseWasmJson<DeriveInfo>(raw as string);
          this.info.evmAddress = evmAddress;
          this.emitConnect(this.info, true);
        } catch (e) {
          this.failAccountSwitch(e);
        }
      }, this.ethereumProvider);
    } catch {
      /* MetaMask not available (Node) */
    }
  }

  async connectFromSeed(seedHex: string): Promise<DeriveInfo> {
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSeedHex(this.sid(), strip0x(seedHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    this.emitConnect(this.info);
    return this.info;
  }

  async connectFromEvmSignature(
    signatureHex: string,
    evmAddress?: string,
    scheme: DerivationScheme = this.derivation,
  ): Promise<DeriveInfo> {
    const recovered = assertSignatureMatchesAddress(signatureHex, evmAddress, scheme);
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSigHex(this.sid(), strip0x(signatureHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    this.info.evmAddress = evmAddress || recovered;
    this.emitConnect(this.info);
    return this.info;
  }

  async connectFromEvmPrivateKey(evmPrivateKeyHex: string): Promise<DeriveInfo> {
    const sig = signDerivationMessage(evmPrivateKeyHex, this.derivation);
    const addr = evmAddressFromPrivateKey(evmPrivateKeyHex);
    return this.connectFromEvmSignature(sig, addr, this.derivation);
  }

  async connectFromMetaMask(provider?: EthereumProvider): Promise<DeriveInfo> {
    try {
      const { evmAddress, signatureHex, scheme } = await signDerivationWithMetaMask({
        provider: provider || this.ethereumProvider,
        scheme: this.derivation,
      });
      const info = await this.connectFromEvmSignature(signatureHex, evmAddress, scheme);
      this.ensureMetaMaskWatch();
      return info;
    } catch (e) {
      this.opts.onError?.(e);
      throw wrapUnknown(e, 'USER_REJECTED');
    }
  }

  getAddress(): string {
    if (!this.info) throw BridgeError.notConnected();
    return this.info.address;
  }

  getPublicKeyB64(): string {
    if (!this.info) throw BridgeError.notConnected();
    return this.info.public_key_b64;
  }

  getDeriveInfo(): DeriveInfo | null {
    return this.info;
  }

  getConnectionState(): ConnectionState {
    return this.info && this.sessionId && this.wasm?.sqIsUnlocked(this.sessionId)
      ? 'connected'
      : 'disconnected';
  }

  isUnlocked(): boolean {
    return !!this.wasm && !!this.sessionId && this.wasm.sqIsUnlocked(this.sessionId);
  }

  /**
   * Clear Dilithium key material from WASM (best-effort zeroize) and drop local info.
   * Session id is retained so re-connect can reuse the session slot.
   */
  lock(): void {
    if (this.wasm && this.sessionId) {
      this.wasm.sqLock(this.sessionId);
    }
    this.info = null;
  }

  disconnect(): void {
    if (this.unwatchMetaMask) {
      this.unwatchMetaMask();
      this.unwatchMetaMask = null;
    }
    if (this.wasm && this.sessionId) {
      this.wasm.sqDestroySession(this.sessionId);
    }
    this.sessionId = null;
    this.info = null;
    this.emitter.emit('disconnect', {});
  }

  async getBalance(address?: string) {
    return this.rpc.getBalance(address || this.getAddress());
  }

  async getNonce(address?: string) {
    return this.rpc.getNonce(address || this.getAddress());
  }

  async getStatus() {
    return this.rpc.getStatus();
  }

  async getLatestBlock() {
    return this.rpc.getLatestBlock();
  }

  async getBlockByHeight(height: number | string) {
    return this.rpc.getBlockByHeight(height);
  }

  async getBlockByHash(hash: string) {
    return this.rpc.getBlockByHash(hash);
  }

  /**
   * Preflight: balance, nonce, fee, and whether balance covers amount+fee.
   * Does not sign.
   */
  async preflight(opts: {
    amount?: string;
    amountSPLD?: string;
    fee?: string;
    address?: string;
  }): Promise<PreflightResult> {
    const from = opts.address || this.getAddress();
    await this.rpc.assertChainId();
    const bal = await this.rpc.getBalance(from);
    const { nonce } = await this.rpc.getNonce(from);
    const fee = assertFeeAtLeastMin(opts.fee);
    const amount =
      opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount || '0';
    const need = BigInt(amount) + BigInt(fee);
    const have = BigInt(bal.balance);
    return {
      from,
      balance: bal.balance,
      balance_spld: bal.balance_spld,
      nonce,
      fee,
      chainId: this.rpc.chainId,
      sufficient: have >= need,
    };
  }

  async waitForConfirmation(opts: {
    nonceBefore: number;
    from?: string;
    hash?: string;
    timeoutMs?: number;
    pollMs?: number;
  }) {
    return this.rpc.waitForConfirmation({
      from: opts.from || this.getAddress(),
      nonceBefore: opts.nonceBefore,
      hash: opts.hash,
      timeoutMs: opts.timeoutMs,
      pollMs: opts.pollMs,
    });
  }

  async sendTransfer(opts: TransferOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    await this.init();
    assertAddress(opts.to);
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    if (!amount || BigInt(amount) < 0n) {
      throw BridgeError.invalidAmount('amount must be a non-negative integer string');
    }
    let fee: string;
    try {
      fee = assertFeeAtLeastMin(opts.fee);
    } catch (e) {
      throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
    }

    await this.beforeSignAndSubmit();
    const { nonce } = await this.getNonce();
    const params = {
      to: opts.to,
      amount,
      fee,
      nonce,
      memo: opts.memo || '',
    };
    this.opts.onRequest?.({ method: 'signTransfer' });
    const wireStr = unwrap(this.api().sqSignTransfer(this.sid(), JSON.stringify(params))) as string;
    const signedTx = JSON.parse(wireStr);
    const result = await this.rpc.submitTransaction(signedTx);

    let confirmation: { confirmed: true; nonce: number } | undefined;
    if (opts.waitForConfirmation) {
      confirmation = await this.waitForConfirmation({
        nonceBefore: nonce,
        hash: result.hash,
        timeoutMs: opts.confirmTimeoutMs,
      });
    }
    return { ...result, signedTx, confirmation };
  }

  async callContract(opts: CallOpts): Promise<{ status: string; hash: string; signedTx: object }> {
    await this.init();
    assertAddress(opts.contract);
    const value = opts.valueSPLD != null ? fromSPLD(opts.valueSPLD) : opts.value || '0';
    let fee: string;
    try {
      fee = assertFeeAtLeastMin(opts.fee);
    } catch (e) {
      throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
    }

    let calldata: Uint8Array | string;
    if (opts.function) {
      if (/\baddress\b/.test(opts.function) && !/\bsqaddress\b/.test(opts.function)) {
        console.warn(
          '[sqc] QSol ABI uses `sqaddress`, not Solidity `address`. Signature:',
          opts.function,
        );
      }
      calldata = '0x' + bytesToHex(encodeFunctionData(opts.function, opts.args || []));
    } else if (opts.calldata != null) {
      calldata = opts.calldata;
    } else {
      throw BridgeError.invalidAmount('callContract requires `function`+args or `calldata`');
    }

    await this.beforeSignAndSubmit();
    const { nonce } = await this.getNonce();
    const enc = encodePayload(calldata);
    const params: Record<string, unknown> = {
      contract: opts.contract,
      value,
      fee,
      nonce,
      ...(enc.calldata_hex ? { calldata_hex: enc.calldata_hex } : { calldata_b64: enc.calldata_b64 }),
    };
    this.opts.onRequest?.({ method: 'signCall' });
    const wireStr = unwrap(this.api().sqSignCall(this.sid(), JSON.stringify(params))) as string;
    const signedTx = JSON.parse(wireStr);
    const result = await this.rpc.submitTransaction(signedTx);
    return { ...result, signedTx };
  }

  async deployContract(opts: DeployOpts): Promise<{ status: string; hash: string; signedTx: object }> {
    await this.init();
    let fee: string;
    try {
      fee = assertFeeAtLeastMin(opts.fee);
    } catch (e) {
      throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
    }
    await this.beforeSignAndSubmit();
    const { nonce } = await this.getNonce();
    const enc = encodePayload(opts.bytecode);
    const params: Record<string, unknown> = {
      fee,
      nonce,
      ...(enc.bytecode_hex ? { bytecode_hex: enc.bytecode_hex } : { bytecode_b64: enc.bytecode_b64 }),
    };
    this.opts.onRequest?.({ method: 'signDeploy' });
    const wireStr = unwrap(this.api().sqSignDeploy(this.sid(), JSON.stringify(params))) as string;
    const signedTx = JSON.parse(wireStr);
    const result = await this.rpc.submitTransaction(signedTx);
    return { ...result, signedTx };
  }

  async payX402(opts: X402PayOpts): Promise<{
    payload: object;
    verify?: { valid: boolean; reason?: string };
    settle?: object;
  }> {
    await this.init();
    assertAddress(opts.to);
    await this.beforeSignAndSubmit();
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    const params = {
      to: opts.to,
      amount,
      valid_after: opts.validAfter || 0,
      valid_before: opts.validBefore || 0,
    };
    this.opts.onRequest?.({ method: 'signX402' });
    const wireStr = unwrap(this.api().sqSignX402(this.sid(), JSON.stringify(params))) as string;
    const payload = JSON.parse(wireStr);
    const out: {
      payload: object;
      verify?: { valid: boolean; reason?: string };
      settle?: object;
    } = { payload };
    if (opts.requirements) {
      out.verify = await this.rpc.x402Verify(opts.requirements, payload);
      if (opts.settle !== false && out.verify.valid) {
        out.settle = await this.rpc.x402Settle(opts.requirements, payload);
      }
    }
    return out;
  }

  async signTransferOnly(opts: TransferOpts & { nonce: number; assertChain?: boolean }): Promise<object> {
    await this.init();
    assertAddress(opts.to);
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    let fee: string;
    try {
      fee = assertFeeAtLeastMin(opts.fee);
    } catch (e) {
      throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
    }
    if (opts.assertChain !== false) {
      await this.beforeSignAndSubmit();
    }
    const wireStr = unwrap(
      this.api().sqSignTransfer(
        this.sid(),
        JSON.stringify({
          to: opts.to,
          amount,
          fee,
          nonce: opts.nonce,
          memo: opts.memo || '',
        }),
      ),
    ) as string;
    return JSON.parse(wireStr);
  }
}

export default SplendorPQBridge;

/** Re-export for callers that only need the fee constant near preflight. */
export { MIN_FEE };

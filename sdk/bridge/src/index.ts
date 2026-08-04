/**
 * @splendor/pq-bridge — Node.js-first EVM→PQ wallet bridge for Splendor Quantum Chain.
 *
 * Internally loads Go Dilithium (circl mode3) via WASM for wire-compatible signing.
 * Each SplendorPQBridge instance owns an isolated WASM session.
 */
import {
  DERIVATION_DOMAIN_MESSAGE,
  CHAIN_ID,
  seedFromEvmSignature,
  signDerivationMessage,
  strip0x,
} from './derive.js';
import { loadWasmSigner, parseWasmJson, unwrap, type WasmSignerApi } from './wasm-loader.js';
import { SqRpc, fromSPLD, toSPLD, type RpcOptions } from './rpc.js';
import { SqcProvider, type ProviderOptions } from './provider.js';
import {
  connectMetaMask,
  signDerivationWithMetaMask,
  onMetaMaskAccountsChanged,
} from './metamask.js';
import { BridgeError, isBridgeError, wrapUnknown } from './errors.js';
import { assertAddress, isValidAddress } from './address.js';
import {
  MIN_FEE,
  assertFeeAtLeastMin,
  encodePayload,
  bytesToHex,
  hexToBytes,
  isHexString,
} from './encoding.js';
import {
  encodeFunctionData,
  encodeFunctionDataHex,
  functionSelector,
  functionSelectorHex,
  decodeAddress,
  decodeUint256,
  decodeArgAddress,
  decodeArgUint256,
  type AbiArg,
} from './abi.js';
import { BridgeEmitter, type BridgeEventMap } from './events.js';

export {
  DERIVATION_DOMAIN_MESSAGE,
  CHAIN_ID,
  seedFromEvmSignature,
  signDerivationMessage,
  fromSPLD,
  toSPLD,
  SqRpc,
  SqcProvider,
  connectMetaMask,
  signDerivationWithMetaMask,
  onMetaMaskAccountsChanged,
  BridgeError,
  isBridgeError,
  isValidAddress,
  assertAddress,
  MIN_FEE,
  bytesToHex,
  hexToBytes,
  isHexString,
  encodeFunctionData,
  encodeFunctionDataHex,
  functionSelector,
  functionSelectorHex,
  decodeAddress,
  decodeUint256,
  decodeArgAddress,
  decodeArgUint256,
};

export type { RpcOptions, ProviderOptions, AbiArg, BridgeEventMap };

export type BridgeOptions = RpcOptions & {
  wasmUrl?: string;
  execUrl?: string;
  /** When true (default), MetaMask account switches re-derive the PQ wallet. */
  watchMetaMaskAccounts?: boolean;
  /** Optional shared read provider; RPC options are taken from it when provided. */
  provider?: SqcProvider;
};

export type DeriveInfo = {
  address: string;
  public_key_b64: string;
  public_key_hex: string;
  evmAddress?: string;
};

export type ConnectionState = 'disconnected' | 'connected';

export type TransferOpts = {
  to: string;
  amount: string;
  amountSPLD?: string;
  fee?: string;
  memo?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
};

export type CallOpts = {
  contract: string;
  /** Raw calldata (hex / bytes). Ignored when `function` is set. */
  calldata?: Uint8Array | string;
  /** QSol signature e.g. `transfer(sqaddress,uint256)` — encodes via SHA3-256 selector. */
  function?: string;
  args?: AbiArg[];
  value?: string;
  valueSPLD?: string;
  fee?: string;
};

export type DeployOpts = {
  bytecode: Uint8Array | string;
  fee?: string;
};

export type X402PayOpts = {
  to: string;
  amount: string;
  amountSPLD?: string;
  validAfter?: number;
  validBefore?: number;
  requirements?: object;
  settle?: boolean;
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

  constructor(options: BridgeOptions = {}) {
    this.opts = options;
    this.metaMaskWatchEnabled = options.watchMetaMaskAccounts !== false;
    if (options.provider) {
      this.provider = options.provider;
      this.rpc = options.provider.rpc;
    } else {
      this.rpc = new SqRpc(options);
      this.provider = new SqcProvider(options, this.rpc);
    }
  }

  /** Build a signing wallet that shares an existing read-only provider's RPC. */
  static fromProvider(provider: SqcProvider, extra?: Omit<BridgeOptions, 'provider'>): SplendorPQBridge {
    return new SplendorPQBridge({ ...provider.toBridgeOptions(), ...extra, provider });
  }

  on<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): () => void {
    return this.emitter.on(event, handler);
  }

  off<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): void {
    this.emitter.off(event, handler);
  }

  /** Load WASM module and create an isolated session. */
  async init(): Promise<void> {
    if (this.wasm && this.sessionId) return;
    this.wasm = await loadWasmSigner({
      wasmUrl: this.opts.wasmUrl,
      execUrl: this.opts.execUrl,
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

  private emitConnect(info: DeriveInfo, changed = false): void {
    const payload = { address: info.address, evmAddress: info.evmAddress };
    if (changed) this.emitter.emit('accountsChanged', payload);
    else this.emitter.emit('connect', payload);
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
          const { evmAddress, signatureHex } = await signDerivationWithMetaMask(next);
          await this.init();
          const raw = unwrap(this.api().sqDeriveFromSigHex(this.sid(), strip0x(signatureHex)));
          this.info = parseWasmJson<DeriveInfo>(raw as string);
          this.info.evmAddress = evmAddress;
          this.emitConnect(this.info, true);
        } catch (e) {
          // User may reject re-sign; leave prior key until they reconnect
          void e;
        }
      });
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

  async connectFromEvmSignature(signatureHex: string, evmAddress?: string): Promise<DeriveInfo> {
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSigHex(this.sid(), strip0x(signatureHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    if (evmAddress) this.info.evmAddress = evmAddress;
    this.emitConnect(this.info);
    return this.info;
  }

  async connectFromEvmPrivateKey(evmPrivateKeyHex: string): Promise<DeriveInfo> {
    const sig = signDerivationMessage(evmPrivateKeyHex);
    return this.connectFromEvmSignature(sig);
  }

  async connectFromMetaMask(): Promise<DeriveInfo> {
    try {
      const { evmAddress, signatureHex } = await signDerivationWithMetaMask();
      const info = await this.connectFromEvmSignature(signatureHex, evmAddress);
      this.ensureMetaMaskWatch();
      return info;
    } catch (e) {
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

  /** Clear derived key for this session (keeps WASM module loaded). */
  lock(): void {
    if (this.wasm && this.sessionId) {
      this.wasm.sqLock(this.sessionId);
    }
    this.info = null;
  }

  /** Lock and destroy the WASM session entirely. */
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

    await this.rpc.assertChainId();
    const { nonce } = await this.getNonce();
    const params = {
      to: opts.to,
      amount,
      fee,
      nonce,
      memo: opts.memo || '',
    };
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
      calldata = '0x' + bytesToHex(encodeFunctionData(opts.function, opts.args || []));
    } else if (opts.calldata != null) {
      calldata = opts.calldata;
    } else {
      throw BridgeError.invalidAmount('callContract requires `function`+args or `calldata`');
    }

    const { nonce } = await this.getNonce();
    const enc = encodePayload(calldata);
    const params: Record<string, unknown> = {
      contract: opts.contract,
      value,
      fee,
      nonce,
      ...(enc.calldata_hex ? { calldata_hex: enc.calldata_hex } : { calldata_b64: enc.calldata_b64 }),
    };
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
    const { nonce } = await this.getNonce();
    const enc = encodePayload(opts.bytecode);
    const params: Record<string, unknown> = {
      fee,
      nonce,
      ...(enc.bytecode_hex ? { bytecode_hex: enc.bytecode_hex } : { bytecode_b64: enc.bytecode_b64 }),
    };
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
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    const params = {
      to: opts.to,
      amount,
      valid_after: opts.validAfter || 0,
      valid_before: opts.validBefore || 0,
    };
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

  async signTransferOnly(opts: TransferOpts & { nonce: number }): Promise<object> {
    await this.init();
    assertAddress(opts.to);
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    let fee: string;
    try {
      fee = assertFeeAtLeastMin(opts.fee);
    } catch (e) {
      throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
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

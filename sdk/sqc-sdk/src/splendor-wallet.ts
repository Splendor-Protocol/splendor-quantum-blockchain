/**
 * SplendorPQBridge — signing wallet (WASM ML-DSA-65 sessions).
 * @deprecated Prefer importing `Wallet` — SplendorPQBridge remains an alias.
 */
import {
  strip0x,
} from './derive.js';
import { parseWasmJson, unwrap } from './wasm-loader.js';
import { SqRpc, fromSPLD } from './rpc.js';
import { SqcProvider } from './provider.js';
import { BridgeError, wrapUnknown } from './errors.js';
import { assertAddress } from './address.js';
import {
  assertFeeAtLeastMin,
  encodePayload,
  bytesToHex,
  randomNonceHex,
  MIN_FEE,
} from './encoding.js';
import { assertQSolSignature, encodeFunctionData } from './abi.js';
import { BridgeEmitter, type BridgeEventMap } from './events.js';
import { getDefaultNetwork } from './networks.js';
import { Connection } from './core/connection.js';
import { MODULE_ROUTER_ADDRESS } from './addresses/index.js';
import { encodeModuleCallHex } from './core/module-call.js';
import { prepareSignMessage } from './message-domain.js';
import { WasmSession } from './wallet/wasm-session.js';
import { installBrowserSessionPolicy } from './wallet/browser-session-policy.js';
import type {
  BridgeOptions,
  DeriveInfo,
  ConnectionState,
  TransferOpts,
  CallOpts,
  DeployOpts,
  X402PayOpts,
  PreflightResult,
  ModuleCallOpts,
  PermitParams,
  SignedPermit,
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
  BridgeClaimSignParams,
  SignedBridgeClaim,
  WalletAccount,
  WalletConnectOpts,
  UnsignedTx,
  SignedTx,
  SendTxResult,
  SignMessageOpts,
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
  ModuleCallOpts,
  PermitParams,
  SignedPermit,
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
  BridgeClaimSignParams,
  SignedBridgeClaim,
  WalletAccount,
  WalletConnectOpts,
  UnsignedTx,
  SignedTx,
  SendTxResult,
  SignMessageOpts,
};

export class SplendorPQBridge {
  readonly provider: SqcProvider;
  readonly connection: Connection;
  private readonly wasmSession = new WasmSession();
  private info: DeriveInfo | null = null;
  private readonly opts: BridgeOptions;
  private readonly emitter = new BridgeEmitter();
  private disposeBrowserPolicy: (() => void) | null = null;

  /**
   * @deprecated Prefer `connection` (Transport-backed). Only available when
   * Connection uses RpcTransport.
   */
  get rpc(): SqRpc {
    return this.connection.rpc;
  }

  constructor(options: BridgeOptions = {}) {
    this.opts = options;
    if (options.connection) {
      this.connection = options.connection;
      if (options.provider) {
        this.provider = options.provider;
      } else {
        try {
          this.provider = new SqcProvider(options, this.connection.rpc);
        } catch {
          // Non-RpcTransport: provider is deprecated and gets a detached SqRpc.
          this.provider = new SqcProvider(this.connection.toRpcOptions());
        }
      }
    } else if (options.provider) {
      this.provider = options.provider;
      this.connection = Connection.fromSqRpc(options.provider.rpc);
    } else {
      this.connection = new Connection(options);
      this.provider = new SqcProvider(options, this.connection.rpc);
    }
    // Host-owned idle/visibility policy (no document listeners inside Wallet methods).
    if (options.sessionIdleMs || options.lockOnHidden) {
      this.disposeBrowserPolicy = installBrowserSessionPolicy(this, {
        sessionIdleMs: options.sessionIdleMs,
        lockOnHidden: options.lockOnHidden,
      });
    }
  }

  static fromProvider(provider: SqcProvider, extra?: Omit<BridgeOptions, 'provider'>): SplendorPQBridge {
    return new SplendorPQBridge({ ...provider.toBridgeOptions(), ...extra, provider });
  }

  /** Send SPLD (beginner-friendly alias). */
  async sendSPLD(opts: TransferOpts) {
    return this.sendTransfer(opts);
  }

  on<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): () => void {
    return this.emitter.on(event, handler);
  }

  off<K extends keyof BridgeEventMap>(event: K, handler: (payload: BridgeEventMap[K]) => void): void {
    this.emitter.off(event, handler);
  }

  private withSignLock<T>(fn: () => Promise<T>): Promise<T> {
    return this.wasmSession.withSignLock(fn);
  }

  private signingIdentity(): { chainId: string; networkId: string } {
    return {
      chainId: this.connection.chainId,
      networkId: this.opts.networkId ?? getDefaultNetwork().id,
    };
  }

  /** Chain nonce, advanced past any locally reserved signed nonces (call under sign lock). */
  private async nextAccountNonce(): Promise<number> {
    const { nonce: chainNonce } = await this.getNonce();
    return this.wasmSession.nextAccountNonce(chainNonce);
  }

  private reserveNonce(nonce: number): void {
    this.wasmSession.reserveNonce(nonce);
  }

  async init(): Promise<void> {
    await this.wasmSession.init({
      networkId: this.opts.networkId,
      rpcUrls: this.connection.rpcUrls,
      chainId: this.connection.chainId,
      wasmUrl: this.opts.wasmUrl,
      execUrl: this.opts.execUrl,
      wasmSha256: this.opts.wasmSha256,
      execSha256: this.opts.execSha256,
      allowUnpinnedWasm: this.opts.allowUnpinnedWasm,
    });
  }

  /** Bind WASM signer to the RPC network chain_id (permits, orders, txs). */
  private syncWasmChainId(): void {
    this.wasmSession.syncChainId(this.connection.chainId);
  }

  private api() {
    return this.wasmSession.api();
  }

  private sid(): string {
    return this.wasmSession.sid();
  }

  /** SQDP-1: assert chain before any sign/submit. */
  private async beforeSignAndSubmit(): Promise<void> {
    if (!this.isUnlocked()) {
      throw BridgeError.notConnected('Wallet is locked or disconnected — connect/unlock first');
    }
    this.syncWasmChainId();
    this.opts.onRequest?.({ method: 'assertChainId' });
    await this.connection.assertChainId();
  }

  /**
   * Sign + submit under the mutex; confirmation runs outside so parallel signs are not blocked.
   */
  private async signSubmitThenMaybeConfirm(
    signAndSubmit: () => Promise<{
      status: string;
      hash: string;
      signedTx: object;
      nonce: number;
    }>,
    opts: { waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    const { status, hash, signedTx, nonce } = await this.withSignLock(signAndSubmit);
    let confirmation: { confirmed: true; nonce: number } | undefined;
    if (opts.waitForConfirmation) {
      confirmation = await this.waitForConfirmation({
        nonceBefore: nonce,
        hash,
        timeoutMs: opts.confirmTimeoutMs,
      });
    }
    return { status, hash, signedTx, confirmation };
  }

  private emitConnect(info: DeriveInfo, changed = false): void {
    const payload = { address: info.address, evmAddress: info.evmAddress };
    if (changed) this.emitter.emit('accountsChanged', payload);
    else this.emitter.emit('connect', payload);
  }

  /**
   * Connect from post-quantum seed entropy. This is the recommended path: the
   * resulting account's security rests on ML-DSA-65 alone.
   */
  async connectFromSeed(seedHex: string): Promise<DeriveInfo> {
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSeedHex(this.sid(), strip0x(seedHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    this.info.keyOrigin = 'native';
    this.info.postQuantumSecure = true;
    this.emitConnect(this.info);
    return this.info;
  }

  /**
   * Single connect entry — seed-only (native ML-DSA).
   * For browser extension unlock use ExtensionSigner / connect({ mode: 'extension' }).
   */
  async connect(opts: WalletConnectOpts): Promise<DeriveInfo> {
    if (opts.mode !== 'seed') {
      throw BridgeError.rpc(`Unknown connect mode: ${JSON.stringify(opts)}`);
    }
    return this.connectFromSeed(opts.seed);
  }

  /**
   * Nullable account snapshot — null when locked or disconnected (does not throw).
   */
  get account(): WalletAccount | null {
    if (!this.info || !this.isUnlocked()) return null;
    return {
      address: this.info.address,
      publicKeyB64: this.info.public_key_b64,
      keyOrigin: this.info.keyOrigin ?? 'native',
      postQuantumSecure: this.info.postQuantumSecure !== false,
      evmAddress: this.info.evmAddress,
    };
  }

  /** Network id from construction options (may be undefined). */
  get networkId(): string | undefined {
    return this.opts.networkId;
  }

  /** Connected SQ: addresses (length 0 or 1). */
  getAddresses(): string[] {
    const a = this.account;
    return a ? [a.address] : [];
  }

  /**
   * Emit `chainChanged` after a soft network switch (ConnectKit) or manual update.
   * Does not rebuild RPC — callers recreate Connection / client for new endpoints.
   */
  notifyChainChanged(payload: { chainId: number | string; networkId: string }): void {
    const chainId =
      typeof payload.chainId === 'number'
        ? payload.chainId
        : Number.parseInt(String(payload.chainId).replace(/\D/g, ''), 10) || 0;
    this.emitter.emit('chainChanged', { chainId, networkId: payload.networkId });
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
    return this.info && this.wasmSession.isUnlocked() ? 'connected' : 'disconnected';
  }

  isUnlocked(): boolean {
    return this.wasmSession.isUnlocked();
  }

  /**
   * Clear ML-DSA-65 key material from WASM (best-effort zeroize) and drop local info.
   * Session id is retained so re-connect can reuse the session slot.
   * Emits `lock` so UI kits can clear the connected chip without destroying the session.
   */
  lock(): void {
    const hadSession = !!this.info || this.isUnlocked();
    this.wasmSession.lock();
    this.info = null;
    if (hadSession) this.emitter.emit('lock', {});
  }

  disconnect(): void {
    this.wasmSession.destroy();
    this.info = null;
    this.emitter.emit('disconnect', {});
  }

  async getBalance(address?: string) {
    return this.connection.getBalance(address || this.getAddress());
  }

  async getNonce(address?: string) {
    return this.connection.getNonce(address || this.getAddress());
  }

  async getStatus() {
    return this.connection.getStatus();
  }

  async getLatestBlock() {
    return this.connection.getLatestBlock();
  }

  async getBlockByHeight(height: number | string) {
    return this.connection.getBlockByHeight(height);
  }

  async getBlockByHash(hash: string) {
    return this.connection.getBlockByHash(hash);
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
    await this.connection.assertChainId();
    const bal = await this.connection.getBalance(from);
    const { nonce } = await this.connection.getNonce(from);
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
      chainId: this.connection.chainId,
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
    return this.connection.waitForConfirmation({
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
    return this.signSubmitThenMaybeConfirm(async () => {
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
      const nonce = await this.nextAccountNonce();
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
      this.reserveNonce(nonce);
      const result = await this.connection.submitTransaction(signedTx);
      return { ...result, signedTx, nonce };
    }, opts);
  }

  async callContract(opts: CallOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    return this.signSubmitThenMaybeConfirm(async () => {
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
        assertQSolSignature(opts.function);
        calldata = '0x' + bytesToHex(encodeFunctionData(opts.function, opts.args || []));
      } else if (opts.calldata != null) {
        calldata = opts.calldata;
      } else {
        throw BridgeError.rpc('callContract requires `function`+args or `calldata`');
      }

      await this.beforeSignAndSubmit();
      const nonce = await this.nextAccountNonce();
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
      this.reserveNonce(nonce);
      const result = await this.connection.submitTransaction(signedTx);
      return { ...result, signedTx, nonce };
    }, opts);
  }

  async deployContract(opts: DeployOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    return this.signSubmitThenMaybeConfirm(async () => {
      await this.init();
      let fee: string;
      try {
        fee = assertFeeAtLeastMin(opts.fee);
      } catch (e) {
        throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
      }
      await this.beforeSignAndSubmit();
      const nonce = await this.nextAccountNonce();
      const enc = encodePayload(opts.bytecode);
      const params: Record<string, unknown> = {
        fee,
        nonce,
        ...(enc.bytecode_hex ? { bytecode_hex: enc.bytecode_hex } : { bytecode_b64: enc.bytecode_b64 }),
      };
      this.opts.onRequest?.({ method: 'signDeploy' });
      const wireStr = unwrap(this.api().sqSignDeploy(this.sid(), JSON.stringify(params))) as string;
      const signedTx = JSON.parse(wireStr);
      this.reserveNonce(nonce);
      const result = await this.connection.submitTransaction(signedTx);
      return { ...result, signedTx, nonce };
    }, opts);
  }

  async payX402(opts: X402PayOpts): Promise<{
    payload: object;
    verify?: { valid: boolean; reason?: string };
    settle?: object;
  }> {
    return this.withSignLock(async () => {
      await this.init();
      assertAddress(opts.to);
      await this.beforeSignAndSubmit();
      const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;

      // Pin the payment nonce here so the authorisation payload and the on-chain
      // settlement transaction commit to the SAME value. The node binds the two
      // together and the executor derives the replay key from it, so two
      // independently generated nonces would be rejected.
      const nonceHex = randomNonceHex();

      const params = {
        to: opts.to,
        amount,
        valid_after: opts.validAfter || 0,
        valid_before: opts.validBefore || 0,
        nonce_hex: nonceHex,
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
        out.verify = await this.connection.x402Verify(opts.requirements, payload);
        if (opts.settle !== false && out.verify.valid) {
          // Settlement runs through consensus: the payer signs a TxX402 that the
          // node admits to the mempool. The node cannot sign this on our behalf,
          // because it debits this account and consumes this account's nonce.
          const accountNonce = await this.nextAccountNonce();
          this.opts.onRequest?.({ method: 'signX402Settlement' });
          const settlementStr = unwrap(
            this.api().sqSignX402Settlement(
              this.sid(),
              JSON.stringify({
                to: opts.to,
                amount,
                nonce_hex: nonceHex,
                fee: opts.fee ?? MIN_FEE,
                account_nonce: accountNonce,
              }),
            ),
          ) as string;
          this.reserveNonce(accountNonce);
          out.settle = await this.connection.x402Settle(
            opts.requirements,
            payload,
            JSON.parse(settlementStr),
          );
        }
      }
      return out;
    });
  }

  private async signTransferUnlocked(
    opts: TransferOpts & { nonce: number; assertChain?: boolean },
  ): Promise<object> {
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

  async signTransferOnly(opts: TransferOpts & { nonce: number; assertChain?: boolean }): Promise<object> {
    return this.withSignLock(() => this.signTransferUnlocked(opts));
  }

  /**
   * Sign an unsigned envelope without submitting (wallet-standard shaped).
   * Fetches nonce when not provided via opts (transfer/call/deploy use account nonce).
   */
  async signTransaction(tx: UnsignedTx): Promise<SignedTx> {
    switch (tx.type) {
      case 'transfer': {
        return this.withSignLock(async () => {
          await this.init();
          await this.beforeSignAndSubmit();
          const nonce = await this.nextAccountNonce();
          const signedTx = await this.signTransferUnlocked({ ...tx, nonce, assertChain: false });
          this.reserveNonce(nonce);
          return {
            type: 'transfer',
            signedTx,
            nonce,
            ...this.signingIdentity(),
          };
        });
      }
      case 'call': {
        return this.withSignLock(async () => {
          await this.init();
          assertAddress(tx.contract);
          const value = tx.valueSPLD != null ? fromSPLD(tx.valueSPLD) : tx.value || '0';
          let fee: string;
          try {
            fee = assertFeeAtLeastMin(tx.fee);
          } catch (e) {
            throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
          }
          let calldata: Uint8Array | string;
          if (tx.function) {
            assertQSolSignature(tx.function);
            calldata = '0x' + bytesToHex(encodeFunctionData(tx.function, tx.args || []));
          } else if (tx.calldata != null) {
            calldata = tx.calldata;
          } else {
            throw BridgeError.rpc('call requires `function`+args or `calldata`');
          }
          await this.beforeSignAndSubmit();
          const nonce = await this.nextAccountNonce();
          const enc = encodePayload(calldata);
          const params: Record<string, unknown> = {
            contract: tx.contract,
            value,
            fee,
            nonce,
            ...(enc.calldata_hex ? { calldata_hex: enc.calldata_hex } : { calldata_b64: enc.calldata_b64 }),
          };
          this.opts.onRequest?.({ method: 'signCall' });
          const wireStr = unwrap(this.api().sqSignCall(this.sid(), JSON.stringify(params))) as string;
          this.reserveNonce(nonce);
          return {
            type: 'call',
            signedTx: JSON.parse(wireStr),
            nonce,
            ...this.signingIdentity(),
          };
        });
      }
      case 'deploy': {
        return this.withSignLock(async () => {
          await this.init();
          let fee: string;
          try {
            fee = assertFeeAtLeastMin(tx.fee);
          } catch (e) {
            throw BridgeError.invalidFee(e instanceof Error ? e.message : String(e));
          }
          await this.beforeSignAndSubmit();
          const nonce = await this.nextAccountNonce();
          const enc = encodePayload(tx.bytecode);
          const params: Record<string, unknown> = {
            fee,
            nonce,
            ...(enc.bytecode_hex ? { bytecode_hex: enc.bytecode_hex } : { bytecode_b64: enc.bytecode_b64 }),
          };
          this.opts.onRequest?.({ method: 'signDeploy' });
          const wireStr = unwrap(this.api().sqSignDeploy(this.sid(), JSON.stringify(params))) as string;
          this.reserveNonce(nonce);
          return {
            type: 'deploy',
            signedTx: JSON.parse(wireStr),
            nonce,
            ...this.signingIdentity(),
          };
        });
      }
      case 'module': {
        const envelope = {
          module: tx.module,
          action: tx.action,
          params: tx.params,
        };
        const signed = await this.signTransaction({
          type: 'call',
          contract: MODULE_ROUTER_ADDRESS,
          calldata: encodeModuleCallHex(envelope),
          value: '0',
          fee: tx.fee,
        });
        return {
          type: 'module',
          signedTx: signed.signedTx,
          nonce: signed.nonce,
          chainId: signed.chainId,
          networkId: signed.networkId,
        };
      }
      default: {
        const _exhaustive: never = tx;
        throw BridgeError.rpc(`Unknown tx type: ${JSON.stringify(_exhaustive)}`);
      }
    }
  }

  /**
   * Submit a previously signed tx, or sign+send an unsigned envelope.
   */
  async sendTransaction(
    tx: UnsignedTx | SignedTx,
    opts?: { waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<SendTxResult> {
    const isSigned =
      tx &&
      typeof tx === 'object' &&
      'signedTx' in tx &&
      tx.signedTx != null &&
      typeof (tx as SignedTx).nonce === 'number' &&
      'type' in tx;

    if (isSigned) {
      const signed = tx as SignedTx;
      await this.connection.assertChainId();
      const current = this.signingIdentity();
      if (!signed.chainId || !signed.networkId) {
        throw BridgeError.chainMismatch('(missing signed identity)', `${current.chainId}@${current.networkId}`);
      }
      if (signed.chainId !== current.chainId) {
        throw BridgeError.chainMismatch(signed.chainId, current.chainId);
      }
      if (signed.networkId !== current.networkId) {
        throw BridgeError.chainMismatch(
          `${signed.chainId}@${signed.networkId}`,
          `${current.chainId}@${current.networkId}`,
        );
      }
      const result = await this.connection.submitTransaction(signed.signedTx);
      let confirmation: { confirmed: true; nonce: number } | undefined;
      if (opts?.waitForConfirmation) {
        confirmation = await this.waitForConfirmation({
          nonceBefore: signed.nonce,
          hash: result.hash,
          timeoutMs: opts.confirmTimeoutMs,
        });
      }
      return { ...result, signedTx: signed.signedTx, confirmation };
    }
    return this.signAndSendTransaction(tx as UnsignedTx, opts);
  }

  /** Sign and submit in one step (wallet-standard shaped). */
  async signAndSendTransaction(
    tx: UnsignedTx,
    opts?: { waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<SendTxResult> {
    const wait = opts?.waitForConfirmation ?? ('waitForConfirmation' in tx ? tx.waitForConfirmation : undefined);
    const confirmTimeoutMs =
      opts?.confirmTimeoutMs ?? ('confirmTimeoutMs' in tx ? tx.confirmTimeoutMs : undefined);
    switch (tx.type) {
      case 'transfer':
        return this.sendTransfer({ ...tx, waitForConfirmation: wait, confirmTimeoutMs });
      case 'call':
        return this.callContract({ ...tx, waitForConfirmation: wait, confirmTimeoutMs });
      case 'deploy':
        return this.deployContract({ ...tx, waitForConfirmation: wait, confirmTimeoutMs });
      case 'module':
        return this.sendModuleCall({ ...tx, waitForConfirmation: wait, confirmTimeoutMs });
      default: {
        const _exhaustive: never = tx;
        throw BridgeError.rpc(`Unknown tx type: ${JSON.stringify(_exhaustive)}`);
      }
    }
  }

  /**
   * Sign an arbitrary message with the connected quantum (ML-DSA-65) key —
   * the primitive Splendor Connect v2 needs to authenticate an `SQ:` identity
   * (the tx signers only sign structured payloads). Returns the SQ: address,
   * the base64 public key, and the base64 signature over the prepared message bytes.
   *
   * Unstructured messages are domain-separated with `SPLENDOR-QC-MSG-v1\\n` unless
   * `opts.raw` is true or the message already starts with a known protocol prefix
   * (e.g. Splendor Connect v2 challenges).
   */
  async signMessage(
    message: string,
    opts?: SignMessageOpts,
  ): Promise<{ address: string; publicKeyB64: string; signatureB64: string; message: string }> {
    return this.withSignLock(async () => {
      await this.init();
      if (!this.isUnlocked()) {
        throw BridgeError.notConnected('Wallet is locked or disconnected — connect/unlock first');
      }
      const prepared = prepareSignMessage(message, opts);
      const signed = unwrap(this.api().sqSignMessage(this.sid(), prepared)) as {
        address: string;
        publicKeyB64: string;
        signatureB64: string;
      };
      return { ...signed, message: prepared };
    });
  }

  /**
   * Verify an ML-DSA-65 signature over a message — a PURE check (no key,
   * no session), so a Node gateway can authenticate a Connect v2 challenge
   * server-side (there is no native-JS CIRCL Mode-3 verifier). Only the WASM
   * needs to be loaded.
   *
   * Applies the same domain preparation as signMessage unless `opts.raw`.
   */
  async verifyMessage(
    publicKeyB64: string,
    message: string,
    signatureB64: string,
    opts?: SignMessageOpts,
  ): Promise<boolean> {
    await this.init();
    const prepared = prepareSignMessage(message, opts);
    const r = unwrap(this.api().sqVerifyMessage(publicKeyB64, prepared, signatureB64)) as {
      valid: boolean;
    };
    return Boolean(r.valid);
  }

  /**
   * Submit a native module call (qrc20, dex, etc.) via the module router.
   */
  async sendModuleCall(opts: ModuleCallOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    const envelope = {
      module: opts.module,
      action: opts.action,
      params: opts.params,
    };
    return this.callContract({
      contract: MODULE_ROUTER_ADDRESS,
      calldata: encodeModuleCallHex(envelope),
      value: '0',
      fee: opts.fee,
      waitForConfirmation: opts.waitForConfirmation,
      confirmTimeoutMs: opts.confirmTimeoutMs,
    });
  }

  /**
   * Sign a QRC-20 permit (off-chain ML-DSA authorisation for gasless approvals).
   */
  async signPermit(params: PermitParams): Promise<SignedPermit> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.token);
      assertAddress(params.spender);
      this.opts.onRequest?.({ method: 'signPermit' });
      const wireStr = unwrap(
        this.api().sqSignPermit(
          this.sid(),
          JSON.stringify({
            token: params.token,
            spender: params.spender,
            amount: params.amount,
            nonce: params.nonce,
            deadline: params.deadline,
          }),
        ),
      ) as string;
      return JSON.parse(wireStr) as SignedPermit;
    });
  }

  /** Sign an exchange order (off-chain ML-DSA authorisation). */
  async signOrder(params: OrderParams): Promise<SignedOrder> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.exchange);
      if (params.recipient) assertAddress(params.recipient);
      this.opts.onRequest?.({ method: 'signOrder' });
      const wireStr = unwrap(this.api().sqSignOrder(this.sid(), JSON.stringify(params))) as string;
      return JSON.parse(wireStr) as SignedOrder;
    });
  }

  /** Sign an exchange order cancellation. Salt must match the original order. */
  async signCancel(params: OrderParams): Promise<SignedCancel> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.exchange);
      if (!params.salt) {
        throw BridgeError.rpc('signCancel requires the exact order salt');
      }
      this.opts.onRequest?.({ method: 'signCancel' });
      const wireStr = unwrap(this.api().sqSignCancel(this.sid(), JSON.stringify(params))) as string;
      return JSON.parse(wireStr) as SignedCancel;
    });
  }

  /** Sign exchange.increment_nonce (invalidates prior orders at this exchange). */
  async signIncrementNonce(params: IncrementNonceParams): Promise<SignedIncrementNonce> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.exchange);
      this.opts.onRequest?.({ method: 'signIncrementNonce' });
      const wireStr = unwrap(
        this.api().sqSignIncrementNonce(this.sid(), JSON.stringify(params)),
      ) as string;
      return JSON.parse(wireStr) as SignedIncrementNonce;
    });
  }

  /**
   * Sign a bridge claim attestation (CtxBridge). Returns pubkey + signature
   * for inclusion in bridge.claim attestations[].
   */
  async signBridgeClaim(params: BridgeClaimSignParams): Promise<SignedBridgeClaim> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.bridge);
      assertAddress(params.recipient);
      this.opts.onRequest?.({ method: 'signBridgeClaim' });
      const wireStr = unwrap(
        this.api().sqSignBridgeClaim(this.sid(), JSON.stringify(params)),
      ) as string;
      return JSON.parse(wireStr) as SignedBridgeClaim;
    });
  }

  /** Module router address (from WASM or offline derivation). */
  getModuleRouterAddress(): string {
    const wasm = this.wasmSession.apiOrNull;
    if (wasm) {
      const r = wasm.sqGetModuleRouterAddress();
      if (typeof r === 'string') return r;
    }
    return MODULE_ROUTER_ADDRESS;
  }

  static async fromSeed(
    options: BridgeOptions & { seed: string },
  ): Promise<SplendorPQBridge> {
    const wallet = new SplendorPQBridge(options);
    await wallet.init();
    await wallet.connectFromSeed(options.seed);
    return wallet;
  }
}

/** Primary wallet class name in @splendor/sqc-sdk v2. */
export const Wallet = SplendorPQBridge;

export default SplendorPQBridge;

/** Re-export for callers that only need the fee constant near preflight. */
export { MIN_FEE };

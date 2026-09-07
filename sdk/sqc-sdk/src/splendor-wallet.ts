/**
 * SplendorPQBridge — signing wallet (WASM ML-DSA-65 sessions).
 * @deprecated Prefer importing `Wallet` — SplendorPQBridge remains an alias.
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
  onMetaMaskDisconnect,
  type EthereumProvider,
} from './metamask.js';
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
  VerifyInstructionInput,
  SignedVerifyInstruction,
  VerifyAttestationSignParams,
  SignedVerifyAttestation,
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
  VerifyInstructionInput,
  SignedVerifyInstruction,
  VerifyAttestationSignParams,
  SignedVerifyAttestation,
};

export class SplendorPQBridge {
  readonly provider: SqcProvider;
  readonly connection: Connection;
  private wasm: WasmSignerApi | null = null;
  private sessionId: string | null = null;
  private info: DeriveInfo | null = null;
  private readonly opts: BridgeOptions;
  private readonly emitter = new BridgeEmitter();
  private unwatchMetaMask: (() => void) | null = null;
  private metaMaskWatchEnabled: boolean;
  private readonly ethereumProvider?: EthereumProvider;
  private readonly derivation: DerivationScheme;
  private idleTimer: ReturnType<typeof setTimeout> | null = null;
  private idleUnsubs: Array<() => void> = [];
  private signChain: Promise<unknown> = Promise.resolve();
  /** Last chain_id stamped into WASM digests (sqSetChainID). */
  private wasmChainId: string | null = null;

  /**
   * @deprecated Prefer `connection` (Transport-backed). Only available when
   * Connection uses RpcTransport.
   */
  get rpc(): SqRpc {
    return this.connection.rpc;
  }

  constructor(options: BridgeOptions = {}) {
    this.opts = options;
    this.metaMaskWatchEnabled = options.watchMetaMaskAccounts !== false;
    this.ethereumProvider = options.ethereumProvider;
    this.derivation = options.derivation || 'eip191-v1';
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
    const run = this.signChain.then(fn, fn);
    this.signChain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  async init(): Promise<void> {
    if (this.wasm && this.sessionId) return;
    const isBrowser = typeof document !== 'undefined';
    const networkId = this.opts.networkId ?? getDefaultNetwork().id;
    if (isBrowser && networkId !== 'local' && !this.opts.wasmSha256 && !this.opts.allowUnpinnedWasm) {
      throw BridgeError.wasm(
        `wasmSha256 is required for network "${networkId}" in the browser ` +
          '(pass wasmSha256 or allowUnpinnedWasm: true)',
      );
    }
    this.wasm = await loadWasmSigner({
      wasmUrl: this.opts.wasmUrl,
      execUrl: this.opts.execUrl,
      wasmSha256: this.opts.wasmSha256,
      execSha256: this.opts.execSha256,
    });
    this.syncWasmChainId();
    const wasmRouter = unwrap(this.wasm.sqGetModuleRouterAddress()) as string;
    if (wasmRouter !== MODULE_ROUTER_ADDRESS) {
      throw BridgeError.wasm(
        `module router mismatch: WASM reports ${wasmRouter}, SDK expects ${MODULE_ROUTER_ADDRESS}`,
      );
    }
    this.sessionId = this.wasm.sqCreateSession();
  }

  /** Bind WASM signer to the RPC network chain_id (permits, orders, txs). */
  private syncWasmChainId(): void {
    if (!this.wasm || this.wasmChainId === this.connection.chainId) return;
    unwrap(this.wasm.sqSetChainID(this.connection.chainId));
    this.wasmChainId = this.connection.chainId;
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
    this.syncWasmChainId();
    this.opts.onRequest?.({ method: 'assertChainId' });
    await this.connection.assertChainId();
  }

  private clearIdleLock(): void {
    if (this.idleTimer != null) {
      clearTimeout(this.idleTimer);
      this.idleTimer = null;
    }
    for (const unsub of this.idleUnsubs) unsub();
    this.idleUnsubs = [];
  }

  private setupIdleLock(): void {
    this.clearIdleLock();
    const ms = this.opts.sessionIdleMs;
    if (!ms || ms <= 0) return;
    if (typeof document === 'undefined') return;

    const reset = () => {
      if (this.idleTimer != null) clearTimeout(this.idleTimer);
      this.idleTimer = setTimeout(() => this.lock(), ms);
    };

    const onActivity = () => reset();

    document.addEventListener('pointerdown', onActivity);
    this.idleUnsubs.push(() => document.removeEventListener('pointerdown', onActivity));
    document.addEventListener('keydown', onActivity);
    this.idleUnsubs.push(() => document.removeEventListener('keydown', onActivity));

    // Optional: lock when tab is hidden (off by default — avoids UI desync on tab switch).
    if (this.opts.lockOnHidden === true) {
      const onVisibility = () => {
        if (document.visibilityState === 'hidden') this.lock();
      };
      const onPageHide = () => this.lock();
      document.addEventListener('visibilitychange', onVisibility);
      this.idleUnsubs.push(() => document.removeEventListener('visibilitychange', onVisibility));
      globalThis.addEventListener?.('pagehide', onPageHide);
      this.idleUnsubs.push(() => globalThis.removeEventListener?.('pagehide', onPageHide));
    }

    reset();
  }

  private emitConnect(info: DeriveInfo, changed = false): void {
    const payload = { address: info.address, evmAddress: info.evmAddress };
    if (changed) this.emitter.emit('accountsChanged', payload);
    else this.emitter.emit('connect', payload);
    this.setupIdleLock();
  }

  private failAccountSwitch(err: unknown): void {
    const be = wrapUnknown(err, 'USER_REJECTED');
    this.emitter.emit('error', { code: be.code, message: be.message });
    // Destroy session + clear watchers (not merely lock).
    this.disconnect();
  }

  private ensureMetaMaskWatch(): void {
    if (!this.metaMaskWatchEnabled || this.unwatchMetaMask) return;
    try {
      const unsubAccounts = onMetaMaskAccountsChanged(async (accounts) => {
        if (!accounts.length) {
          this.disconnect();
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
      const unsubDisconnect = onMetaMaskDisconnect(() => {
        this.disconnect();
      }, this.ethereumProvider);
      this.unwatchMetaMask = () => {
        unsubAccounts();
        unsubDisconnect();
      };
    } catch {
      /* MetaMask not available (Node) */
    }
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
   * Connect by deriving an ML-DSA key from an Ethereum signature.
   *
   * NOT quantum-safe. The seed is SHA3-256 of a deterministic ECDSA signature,
   * so anyone who can break secp256k1 can recover the EVM key, recompute that
   * signature, and derive this exact ML-DSA key. Use for onboarding
   * convenience; use connectFromSeed for accounts holding value.
   */
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
    this.info.keyOrigin = 'evm-derived';
    this.info.postQuantumSecure = false;
    this.opts.onWarning?.({
      code: 'NOT_POST_QUANTUM',
      message:
        'This account was derived from an Ethereum signature. Signing uses ML-DSA-65, but the key ' +
        'is only as strong as secp256k1 — a quantum adversary could reproduce it from any ECDSA ' +
        'signature this EVM account has published. Use a native seed for funds you intend to hold.',
    });
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
      // Only map clear user-reject signals; preserve BridgeError and other failures.
      const msg = e instanceof Error ? e.message : String(e);
      const code = (e as { code?: number | string })?.code;
      const rejected =
        code === 4001 ||
        code === 'ACTION_REJECTED' ||
        /user rejected|user denied|rejected by user/i.test(msg);
      throw wrapUnknown(e, rejected ? 'USER_REJECTED' : 'UNKNOWN');
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
   * Clear ML-DSA-65 key material from WASM (best-effort zeroize) and drop local info.
   * Session id is retained so re-connect can reuse the session slot.
   * Emits `lock` so UI kits can clear the connected chip without destroying the session.
   */
  lock(): void {
    const hadSession = !!this.info || this.isUnlocked();
    this.clearIdleLock();
    if (this.wasm && this.sessionId) {
      this.wasm.sqLock(this.sessionId);
    }
    this.info = null;
    if (hadSession) this.emitter.emit('lock', {});
  }

  disconnect(): void {
    this.clearIdleLock();
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
    return this.withSignLock(async () => {
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
      const result = await this.connection.submitTransaction(signedTx);

      let confirmation: { confirmed: true; nonce: number } | undefined;
      if (opts.waitForConfirmation) {
        confirmation = await this.waitForConfirmation({
          nonceBefore: nonce,
          hash: result.hash,
          timeoutMs: opts.confirmTimeoutMs,
        });
      }
      return { ...result, signedTx, confirmation };
    });
  }

  async callContract(opts: CallOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    return this.withSignLock(async () => {
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
      const result = await this.connection.submitTransaction(signedTx);
      let confirmation: { confirmed: true; nonce: number } | undefined;
      if (opts.waitForConfirmation) {
        confirmation = await this.waitForConfirmation({
          nonceBefore: nonce,
          hash: result.hash,
          timeoutMs: opts.confirmTimeoutMs,
        });
      }
      return { ...result, signedTx, confirmation };
    });
  }

  async deployContract(opts: DeployOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number };
  }> {
    return this.withSignLock(async () => {
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
      const result = await this.connection.submitTransaction(signedTx);
      let confirmation: { confirmed: true; nonce: number } | undefined;
      if (opts.waitForConfirmation) {
        confirmation = await this.waitForConfirmation({
          nonceBefore: nonce,
          hash: result.hash,
          timeoutMs: opts.confirmTimeoutMs,
        });
      }
      return { ...result, signedTx, confirmation };
    });
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
          const { nonce: accountNonce } = await this.getNonce();
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

  async signTransferOnly(opts: TransferOpts & { nonce: number; assertChain?: boolean }): Promise<object> {
    return this.withSignLock(async () => {
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
    });
  }

  /**
   * Sign an arbitrary message with the connected quantum (ML-DSA-65) key —
   * the primitive Splendor Connect v2 needs to authenticate an `SQ:` identity
   * (the tx signers only sign structured payloads). Returns the SQ: address,
   * the base64 public key, and the base64 signature over the raw message bytes.
   */
  async signMessage(
    message: string,
  ): Promise<{ address: string; publicKeyB64: string; signatureB64: string }> {
    return this.withSignLock(async () => {
      await this.init();
      return unwrap(this.api().sqSignMessage(this.sid(), message)) as {
        address: string;
        publicKeyB64: string;
        signatureB64: string;
      };
    });
  }

  /**
   * Verify an ML-DSA-65 signature over a message — a PURE check (no key,
   * no session), so a Node gateway can authenticate a Connect v2 challenge
   * server-side (there is no native-JS CIRCL Mode-3 verifier). Only the WASM
   * needs to be loaded.
   */
  async verifyMessage(publicKeyB64: string, message: string, signatureB64: string): Promise<boolean> {
    await this.init();
    const r = unwrap(this.api().sqVerifyMessage(publicKeyB64, message, signatureB64)) as { valid: boolean };
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

  /**
   * The SLH-DSA-SHA2-128s public key of this session (hex, 32 bytes). It is
   * derived from the same seed as the ML-DSA key, under its own domain, so it
   * is stable across sessions without a second secret to back up.
   */
  async getSLHPublicKeyHex(): Promise<string> {
    await this.init();
    return unwrap(this.api().sqGetSLHPublicKeyHex(this.sid())) as string;
  }

  /**
   * Dual-sign an SQVL instruction (ML-DSA-65 + SLH-DSA under CtxVerify). The
   * returned `instruction` is what `verify.register` and `POST /verify/check`
   * take. Signing SLH-DSA-128s takes seconds in the browser; keep it off the
   * UI thread's critical path.
   */
  async signVerifyInstruction(input: VerifyInstructionInput): Promise<SignedVerifyInstruction> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      if (input.from) assertAddress(input.from);
      if (input.to) assertAddress(input.to);
      if (input.token) assertAddress(input.token);
      this.opts.onRequest?.({ method: 'signVerifyInstruction' });
      const wireStr = unwrap(
        this.api().sqSignVerifyInstruction(
          this.sid(),
          JSON.stringify({ ...input, instruction_id: strip0x(input.instruction_id) }),
        ),
      ) as string;
      return JSON.parse(wireStr) as SignedVerifyInstruction;
    });
  }

  /**
   * Dual-sign an operator attestation for one instruction inside one verify
   * instance. `digest_hex` is the instruction digest (from `signVerifyInstruction`,
   * `/verify/check` or the `proof` read).
   */
  async signVerifyAttestation(params: VerifyAttestationSignParams): Promise<SignedVerifyAttestation> {
    return this.withSignLock(async () => {
      await this.init();
      await this.beforeSignAndSubmit();
      assertAddress(params.verify);
      this.opts.onRequest?.({ method: 'signVerifyAttestation' });
      const wireStr = unwrap(
        this.api().sqSignVerifyAttestation(
          this.sid(),
          JSON.stringify({ verify: params.verify, digest_hex: strip0x(params.digest_hex) }),
        ),
      ) as string;
      return JSON.parse(wireStr) as SignedVerifyAttestation;
    });
  }

  /** Module router address (from WASM or offline derivation). */
  getModuleRouterAddress(): string {
    if (this.wasm) {
      const r = this.wasm.sqGetModuleRouterAddress();
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

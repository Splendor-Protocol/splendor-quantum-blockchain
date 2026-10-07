/**
 * ExtensionSigner — Signer backed by the Splendor Quantum Wallet Chrome extension.
 * Crypto stays in the extension; this class never holds seed/WASM session material.
 *
 * Wire format matches the official extension inject (array params, wallet_disconnect).
 */
import type { Connection } from '../core/connection.js';
import type { Signer, TxResult } from '../core/signer.js';
import { BridgeError, mapProviderError } from '../errors.js';
import { BridgeEmitter, type BridgeEventMap } from '../events.js';
import type {
  CallOpts,
  ConnectionState,
  DeriveInfo,
  DeployOpts,
  ModuleCallOpts,
  PreflightResult,
  SendTxResult,
  SignMessageOpts,
  TransferOpts,
  UnsignedTx,
  WalletAccount,
  X402PayOpts,
} from '../types.js';
import { prepareSignMessage } from '../message-domain.js';
import { fromSPLD } from '../rpc.js';
import { assertFeeAtLeastMin } from '../encoding.js';
import {
  discoverExtensionProvider,
  getExtensionProvider,
  requestWithAlias,
  toExtensionMethod,
  type SplendorProviderLike,
} from './extension-provider.js';

export type ExtensionSignerOptions = {
  connection: Connection;
  provider?: SplendorProviderLike;
  networkId?: string;
  /**
   * Send even when the extension cannot report its chain (`sqc_chainId` unsupported).
   * Off by default: an unknown wallet chain could land the tx on a chain the app is not watching.
   */
  allowUnknownWalletChain?: boolean;
};

function asAddresses(raw: unknown): string[] {
  if (Array.isArray(raw)) return raw.map(String).filter((a) => a.startsWith('SQ:'));
  if (typeof raw === 'string' && raw.startsWith('SQ:')) return [raw];
  if (raw && typeof raw === 'object') {
    const o = raw as Record<string, unknown>;
    if (typeof o.address === 'string') return [o.address];
    if (Array.isArray(o.accounts)) return asAddresses(o.accounts);
  }
  return [];
}

const TX_HASH_RE = /^(0x)?[0-9a-fA-F]{64}$/;

function normalizeTxResult(raw: unknown): SendTxResult {
  if (typeof raw === 'string') {
    if (!TX_HASH_RE.test(raw)) throw BridgeError.rpc('Extension returned an invalid transaction hash');
    return { status: 'ok', hash: raw, signedTx: {} };
  }
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  const hash = String(o.hash ?? o.txHash ?? o.tx_hash ?? '');
  if (!TX_HASH_RE.test(hash)) {
    throw BridgeError.rpc('Extension did not return a valid transaction hash');
  }
  return {
    status: String(o.status ?? 'ok'),
    hash,
    signedTx: (o.signedTx as object) ?? {},
    confirmation: o.confirmation as SendTxResult['confirmation'],
  };
}

type WaitOpts = { waitForConfirmation?: boolean; confirmTimeoutMs?: number; allowNonceConfirmation?: boolean };

/**
 * The extension base64-encodes the module-call JSON with `btoa`, which throws on any character
 * above U+00FF, so such params must be refused here with a readable error.
 */
function assertExtensionEncodable(opts: ModuleCallOpts): void {
  const json = JSON.stringify({ module: opts.module, action: opts.action, params: opts.params });
  const bad = [...json].find((ch) => ch.codePointAt(0)! > 0xff);
  if (bad === undefined) return;
  throw new BridgeError(
    'INVALID_PARAMS',
    `Module call contains a character the extension cannot encode: "${bad}" (U+${bad
      .codePointAt(0)!
      .toString(16)
      .toUpperCase()
      .padStart(4, '0')})`,
    undefined,
    'Module call params contain characters the Splendor wallet cannot encode (for example the placeholder "SQ:…"). Use plain ASCII values such as real SQ: addresses.',
  );
}

/** Flatten UnsignedTx / TransferOpts into extension sendTransaction body. */
function toExtensionTransferBody(tx: UnsignedTx | object): Record<string, unknown> {
  const t = tx as Record<string, unknown>;
  if (t.type === 'transfer' || (!t.type && (t.to || t.amount || t.amountSPLD))) {
    const body: Record<string, unknown> = {
      to: t.to,
    };
    // The extension only accepts whole base units in `amount`; decimal SPLD is rejected.
    if (t.amountSPLD != null) body.amount = fromSPLD(String(t.amountSPLD));
    else if (t.amount != null) body.amount = String(t.amount);
    if (t.fee != null) body.fee = t.fee;
    if (t.memo != null) body.memo = t.memo;
    return body;
  }
  // Non-transfer envelopes: pass through as flat object for module/call/deploy fallbacks.
  return { ...t };
}

export class ExtensionSigner implements Signer {
  private _connection: Connection;
  private readonly provider: SplendorProviderLike;
  private readonly emitter = new BridgeEmitter();
  private info: DeriveInfo | null = null;
  private networkId: string;
  /** Chain id the extension is signing for (its own Settings), as reported by sqc_chainId / chainChanged. */
  private _walletChainId: string | null = null;
  private readonly allowUnknownWalletChain: boolean;
  private unsubs: Array<() => void> = [];
  /** Serializes sign / submit RPCs so concurrent calls do not race. */
  private opChain: Promise<unknown> = Promise.resolve();

  get connection(): Connection {
    return this._connection;
  }

  constructor(opts: ExtensionSignerOptions) {
    const provider = opts.provider ?? getExtensionProvider();
    if (!provider) {
      throw BridgeError.noProvider(
        'Splendor Quantum Wallet extension not found (window.splendor)',
      );
    }
    this.provider = provider;
    this._connection = opts.connection;
    this.networkId = opts.networkId ?? 'local';
    this.allowUnknownWalletChain = opts.allowUnknownWalletChain === true;
    this.wireProviderEvents();
  }

  /**
   * Soft network switch — keep extension session; point RPC at a new Connection.
   * Does not call wallet_disconnect. Extension chain is owned by wallet Settings.
   */
  rebindConnection(next: Connection, meta?: { networkId?: string }): void {
    this._connection = next;
    if (meta?.networkId) {
      this.networkId = meta.networkId;
      this.notifyChainChanged({
        chainId: next.chainId,
        networkId: meta.networkId,
      });
    }
  }

  private wireProviderEvents(): void {
    const on = this.provider.on?.bind(this.provider);
    const off = this.provider.removeListener?.bind(this.provider);
    if (!on) return;

    const onAccounts = (...args: unknown[]) => {
      const addrs = asAddresses(args[0] ?? args);
      if (!addrs.length) {
        this.info = null;
        this.emitter.emit('disconnect', {});
        return;
      }
      const address = addrs[0]!;
      this.info = {
        address,
        public_key_b64: this.info?.public_key_b64 ?? '',
        public_key_hex: this.info?.public_key_hex ?? '',
        keyOrigin: 'native',
        postQuantumSecure: true,
      };
      this.emitter.emit('accountsChanged', { address });
    };
    const onDisconnect = () => {
      this.info = null;
      this.emitter.emit('disconnect', {});
    };
    const onLock = () => {
      this.info = null;
      this.emitter.emit('lock', {});
    };
    const onConnect = (...args: unknown[]) => {
      // Extension may emit { chainId } or accounts array.
      const first = args[0];
      const addrs = asAddresses(first);
      if (addrs[0]) {
        this.info = {
          address: addrs[0],
          public_key_b64: '',
          public_key_hex: '',
          keyOrigin: 'native',
          postQuantumSecure: true,
        };
        this.emitter.emit('connect', { address: addrs[0] });
        return;
      }
      if (this.info?.address) {
        this.emitter.emit('connect', { address: this.info.address });
      }
    };
    const onChainChanged = (...args: unknown[]) => {
      const raw = args[0];
      if (typeof raw === 'string') {
        if (raw) this._walletChainId = raw;
        this.notifyChainChanged({ chainId: raw, networkId: this.networkId });
        return;
      }
      if (raw && typeof raw === 'object') {
        const o = raw as Record<string, unknown>;
        if (typeof o.chainId === 'string' && o.chainId) this._walletChainId = o.chainId;
        this.notifyChainChanged({
          chainId: (o.chainId as string | number) ?? this.connection.chainId,
          networkId: String(o.networkId ?? this.networkId),
        });
      }
    };

    on('accountsChanged', onAccounts);
    on('disconnect', onDisconnect);
    on('lock', onLock);
    on('connect', onConnect);
    on('chainChanged', onChainChanged);
    this.unsubs.push(() => {
      off?.('accountsChanged', onAccounts);
      off?.('disconnect', onDisconnect);
      off?.('lock', onLock);
      off?.('connect', onConnect);
      off?.('chainChanged', onChainChanged);
    });
  }

  private async req(method: string, params?: unknown): Promise<unknown> {
    const preferred = toExtensionMethod(method);
    try {
      return await requestWithAlias(this.provider, preferred, params);
    } catch (e) {
      throw mapProviderError(e, 'RPC');
    }
  }

  /** Last chain id reported by the extension (null until known or when unsupported). */
  get walletChainId(): string | null {
    return this._walletChainId;
  }

  /** Ask the extension which chain it is on. Returns the cached value when the call is unsupported. */
  async refreshWalletChainId(): Promise<string | null> {
    try {
      const raw = await this.req('sqc_chainId');
      if (typeof raw === 'string' && raw) this._walletChainId = raw;
    } catch {
      /* older extensions: keep cached */
    }
    return this._walletChainId;
  }

  /**
   * The extension signs and submits on its own selected network while this signer
   * reads confirmations from `connection`. Refuse to send when they disagree, so a tx
   * never lands on a chain the app is not watching.
   */
  private async assertWalletChain(): Promise<void> {
    const wallet = await this.refreshWalletChainId();
    const app = this.connection.chainId;
    if (!wallet && !this.allowUnknownWalletChain) {
      throw new BridgeError(
        'CHAIN_MISMATCH',
        `Extension did not report its chain; app is on ${app}`,
        { walletChainId: null, appChainId: app },
        'Update the Splendor wallet extension so this app can confirm it is on the same network',
      );
    }
    if (wallet && app && wallet !== app) {
      throw new BridgeError(
        'CHAIN_MISMATCH',
        `Extension is on chain ${wallet}, app is on ${app}`,
        { walletChainId: wallet, appChainId: app },
        `Your Splendor wallet is on ${wallet} but this app uses ${app} — switch the network in the wallet (or in the app) so they match`,
      );
    }
  }

  /** Await previous sign/submit promise before running `fn` (simple serializing mutex). */
  private withOpLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.opChain.then(fn, fn);
    this.opChain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  /** Request accounts from the extension (opens approval UI). */
  async connect(): Promise<DeriveInfo> {
    let accounts = asAddresses(await this.req('splendor_connect'));
    if (!accounts.length) {
      accounts = asAddresses(await this.req('splendor_accounts'));
    }
    if (!accounts.length) {
      throw BridgeError.userRejected('Extension returned no accounts');
    }
    const address = accounts[0]!;
    this.info = {
      address,
      public_key_b64: '',
      public_key_hex: '',
      keyOrigin: 'native',
      postQuantumSecure: true,
    };
    this.emitter.emit('connect', { address });
    return this.info;
  }

  get account(): WalletAccount | null {
    if (!this.info) return null;
    return {
      address: this.info.address,
      publicKeyB64: this.info.public_key_b64,
      keyOrigin: 'native',
      postQuantumSecure: true,
    };
  }

  getAddress(): string {
    if (!this.info?.address) throw BridgeError.notConnected();
    return this.info.address;
  }

  getAddresses(): string[] {
    return this.info?.address ? [this.info.address] : [];
  }

  getDeriveInfo(): DeriveInfo | null {
    return this.info;
  }

  getConnectionState(): ConnectionState {
    return this.info ? 'connected' : 'disconnected';
  }

  isUnlocked(): boolean {
    return !!this.info;
  }

  on<K extends keyof BridgeEventMap>(
    event: K,
    handler: (payload: BridgeEventMap[K]) => void,
  ): () => void {
    return this.emitter.on(event, handler);
  }

  disconnect(): void {
    for (const u of this.unsubs) u();
    this.unsubs = [];
    // Origin-scoped revoke on the extension (wallet_disconnect).
    void this.req('wallet_disconnect').catch(() => undefined);
    this.info = null;
    this.emitter.emit('disconnect', {});
  }

  lock(): void {
    // Extension owns lock UX; clear local session view.
    this.info = null;
    this.emitter.emit('lock', {});
  }

  notifyChainChanged(payload: { chainId: number | string; networkId: string }): void {
    const chainId =
      typeof payload.chainId === 'number'
        ? payload.chainId
        : Number.parseInt(String(payload.chainId).replace(/\D/g, ''), 10) || 0;
    this.emitter.emit('chainChanged', { chainId, networkId: payload.networkId });
  }

  async signMessage(
    message: string,
    opts?: SignMessageOpts,
  ): Promise<{ address: string; publicKeyB64: string; signatureB64: string; message: string }> {
    if (opts?.raw === true) {
      throw BridgeError.userRejected(
        'splendor_signMessage rejects raw:true — domain separation required',
      );
    }
    return this.withOpLock(() => this.signMessageUnlocked(message, opts));
  }

  private async signMessageUnlocked(
    message: string,
    opts?: SignMessageOpts,
  ): Promise<{ address: string; publicKeyB64: string; signatureB64: string; message: string }> {
    if (!this.info) throw BridgeError.notConnected();
    const prepared = prepareSignMessage(message, opts);
    // Extension: params [message, { encoding }]
    const raw = await this.req('splendor_signMessage', [
      prepared,
      { encoding: 'utf8' },
    ]);
    if (raw && typeof raw === 'object') {
      const o = raw as Record<string, unknown>;
      return {
        address: String(o.address ?? this.info.address),
        publicKeyB64: String(o.publicKeyB64 ?? o.public_key_b64 ?? ''),
        signatureB64: String(o.signatureB64 ?? o.signature ?? o.sig ?? ''),
        message: prepared,
      };
    }
    if (typeof raw === 'string') {
      return {
        address: this.info.address,
        publicKeyB64: '',
        signatureB64: raw,
        message: prepared,
      };
    }
    throw BridgeError.rpc('Extension signMessage returned unexpected payload');
  }

  async sendTransfer(opts: TransferOpts): Promise<SendTxResult> {
    return this.sendTransaction({ type: 'transfer', ...opts }, opts);
  }

  async sendTransaction(tx: UnsignedTx | object, opts?: WaitOpts): Promise<SendTxResult> {
    return this.submitThenWait(() => this.sendTransactionUnlocked(tx), opts);
  }

  /**
   * Submit under the op lock, then (when asked) wait for the receipt outside it so a slow
   * confirmation never blocks the next signature.
   */
  private async submitThenWait(submit: () => Promise<SendTxResult>, opts?: WaitOpts): Promise<SendTxResult> {
    let nonceBefore = 0;
    const res = await this.withOpLock(async () => {
      if (opts?.waitForConfirmation && this.info) {
        nonceBefore = (await this.connection.getNonce(this.info.address)).nonce;
      }
      return submit();
    });
    if (!opts?.waitForConfirmation || res.confirmation) return res;
    const confirmation = await this.connection.waitForConfirmation({
      from: this.getAddress(),
      nonceBefore,
      hash: res.hash,
      timeoutMs: opts.confirmTimeoutMs,
      requireReceipt: !!res.hash && opts.allowNonceConfirmation !== true,
    });
    return { ...res, confirmation: { confirmed: true, nonce: confirmation.nonce } };
  }

  private async sendTransactionUnlocked(tx: UnsignedTx | object): Promise<SendTxResult> {
    if (!this.info) throw BridgeError.notConnected();
    await this.assertWalletChain();
    const body = toExtensionTransferBody(tx);
    // Extension: params [{ to, amount|amountSPLD, fee?, memo? }] → hash string
    const raw = await this.req('splendor_sendTransaction', [body]);
    return normalizeTxResult(raw);
  }

  async sendModuleCall(opts: ModuleCallOpts): Promise<TxResult> {
    assertExtensionEncodable(opts);
    return this.submitThenWait(async () => {
      await this.assertWalletChain();
      try {
        const raw = await this.req('sqc_moduleCall', [opts]);
        return normalizeTxResult(raw);
      } catch (e) {
        // Only fall back when the method is unsupported — never swallow USER_REJECTED / lock / chain errors.
        const err = mapProviderError(e, 'RPC');
        const msg = err.message;
        const unsupported =
          err.code === 'RPC' &&
          /unsupported|unknown method|not support|4200/i.test(msg);
        if (!unsupported) throw err;
        return this.sendTransactionUnlocked({ type: 'module', ...opts });
      }
    }, opts);
  }

  async callContract(opts: CallOpts): Promise<TxResult> {
    return this.submitThenWait(() => this.sendTransactionUnlocked({ type: 'call', ...opts }), opts);
  }

  async deployContract(opts: DeployOpts): Promise<TxResult> {
    return this.submitThenWait(() => this.sendTransactionUnlocked({ type: 'deploy', ...opts }), opts);
  }

  async waitForConfirmation(opts: {
    nonceBefore: number;
    from?: string;
    hash?: string;
    timeoutMs?: number;
    pollMs?: number;
    requireReceipt?: boolean;
  }) {
    return this.connection.waitForConfirmation({
      ...opts,
      from: opts.from ?? this.getAddress(),
    });
  }

  async getNonce(address?: string) {
    const addr = address ?? this.getAddress();
    try {
      const raw = await this.req('sqc_nonce', [addr]);
      if (typeof raw === 'number') return { address: addr, nonce: raw };
      if (raw && typeof raw === 'object' && 'nonce' in (raw as object)) {
        return raw as { address: string; nonce: number };
      }
    } catch {
      /* fall through */
    }
    return this.connection.getNonce(addr);
  }

  async getBalance(address?: string) {
    const addr = address ?? this.getAddress();
    try {
      const raw = await this.req('sqc_getBalance', [addr]);
      if (typeof raw === 'string') {
        return {
          address: addr,
          balance: raw,
          balance_spld: raw,
          unit: 'atto',
        };
      }
      if (raw && typeof raw === 'object') return raw as Awaited<ReturnType<Connection['getBalance']>>;
    } catch {
      /* fall through */
    }
    return this.connection.getBalance(addr);
  }

  async getLatestBlock() {
    try {
      return await this.req('sqc_getBlock', []);
    } catch {
      return this.connection.getLatestBlock();
    }
  }

  async preflight(opts?: {
    fee?: string;
    amount?: string;
    amountSPLD?: string;
    address?: string;
  }): Promise<PreflightResult> {
    const from = opts?.address || this.getAddress();
    await this.connection.assertChainId();
    await this.assertWalletChain();
    const bal = await this.getBalance(from);
    const { nonce } = await this.getNonce(from);
    const fee = assertFeeAtLeastMin(opts?.fee);
    const amount =
      opts?.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts?.amount || '0';
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

  async payX402(_opts: X402PayOpts): Promise<{
    payload: object;
    verify?: { valid: boolean; reason?: string };
    settle?: object;
  }> {
    throw new BridgeError(
      'RPC',
      'payX402 requires an in-page WASM session — use the seed connector (extension path does not settle x402 yet)',
      undefined,
      'X402 payments need the seed wallet — extension settle is not available yet',
    );
  }
}

export type ConnectExtensionResult = {
  wallet: ExtensionSigner;
  info: DeriveInfo;
  connection: Connection;
};

/** Unlock an ExtensionSigner against an existing Connection (async provider discovery). */
export async function connectExtension(
  opts: ExtensionSignerOptions & { discoveryTimeoutMs?: number },
): Promise<ConnectExtensionResult> {
  const provider =
    opts.provider ??
    (await discoverExtensionProvider(opts.discoveryTimeoutMs ?? 5_000).catch(() => {
      throw BridgeError.noProvider(
        'Splendor Quantum Wallet extension not found. Please install it.',
      );
    }));
  const wallet = new ExtensionSigner({ ...opts, provider });
  const info = await wallet.connect();
  return { wallet, info, connection: opts.connection };
}

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

function normalizeTxResult(raw: unknown): SendTxResult {
  if (typeof raw === 'string' && raw.length > 0) {
    return { status: 'ok', hash: raw, signedTx: {} };
  }
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  return {
    status: String(o.status ?? 'ok'),
    hash: String(o.hash ?? o.txHash ?? o.tx_hash ?? ''),
    signedTx: (o.signedTx as object) ?? {},
    confirmation: o.confirmation as SendTxResult['confirmation'],
  };
}

/** Flatten UnsignedTx / TransferOpts into extension sendTransaction body. */
function toExtensionTransferBody(tx: UnsignedTx | object): Record<string, unknown> {
  const t = tx as Record<string, unknown>;
  if (t.type === 'transfer' || (!t.type && (t.to || t.amount || t.amountSPLD))) {
    const body: Record<string, unknown> = {
      to: t.to,
    };
    if (t.amountSPLD != null) body.amountSPLD = t.amountSPLD;
    else if (t.amount != null) body.amount = t.amount;
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
        this.notifyChainChanged({ chainId: raw, networkId: this.networkId });
        return;
      }
      if (raw && typeof raw === 'object') {
        const o = raw as Record<string, unknown>;
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
        message: String(o.message ?? prepared),
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
    return this.sendTransaction({ type: 'transfer', ...opts });
  }

  async sendTransaction(
    tx: UnsignedTx | object,
    opts?: { waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<SendTxResult> {
    return this.withOpLock(() => this.sendTransactionUnlocked(tx, opts));
  }

  private async sendTransactionUnlocked(
    tx: UnsignedTx | object,
    _opts?: { waitForConfirmation?: boolean; confirmTimeoutMs?: number },
  ): Promise<SendTxResult> {
    if (!this.info) throw BridgeError.notConnected();
    const body = toExtensionTransferBody(tx);
    // Extension: params [{ to, amount|amountSPLD, fee?, memo? }] → hash string
    const raw = await this.req('splendor_sendTransaction', [body]);
    return normalizeTxResult(raw);
  }

  async sendModuleCall(opts: ModuleCallOpts): Promise<TxResult> {
    return this.withOpLock(async () => {
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
    });
  }

  async callContract(opts: CallOpts): Promise<TxResult> {
    return this.withOpLock(() => this.sendTransactionUnlocked({ type: 'call', ...opts }));
  }

  async deployContract(opts: DeployOpts): Promise<TxResult> {
    return this.withOpLock(() => this.sendTransactionUnlocked({ type: 'deploy', ...opts }));
  }

  async waitForConfirmation(opts: {
    nonceBefore: number;
    from?: string;
    hash?: string;
    timeoutMs?: number;
    pollMs?: number;
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

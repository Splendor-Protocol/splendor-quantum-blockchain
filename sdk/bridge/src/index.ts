/**
 * @splendor/pq-bridge — Node.js-first EVM→PQ wallet bridge for Splendor Quantum Chain.
 *
 * Internally loads Go Dilithium (circl mode3) via WASM for wire-compatible signing.
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
import { connectMetaMask, signDerivationWithMetaMask } from './metamask.js';

export {
  DERIVATION_DOMAIN_MESSAGE,
  CHAIN_ID,
  seedFromEvmSignature,
  signDerivationMessage,
  fromSPLD,
  toSPLD,
  SqRpc,
  connectMetaMask,
  signDerivationWithMetaMask,
};

export type BridgeOptions = RpcOptions & {
  wasmUrl?: string;
  execUrl?: string;
};

export type DeriveInfo = {
  address: string;
  public_key_b64: string;
  public_key_hex: string;
  evmAddress?: string;
};

export type TransferOpts = {
  to: string;
  amount: string; // smallest units OR use amountSPLD
  amountSPLD?: string;
  fee?: string;
  memo?: string;
};

export type CallOpts = {
  contract: string;
  calldata: Uint8Array | string; // bytes or hex/base64
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
  private wasm: WasmSignerApi | null = null;
  private info: DeriveInfo | null = null;
  private readonly opts: BridgeOptions;

  constructor(options: BridgeOptions = {}) {
    this.opts = options;
    this.rpc = new SqRpc(options);
  }

  /** Load WASM signer (idempotent). */
  async init(): Promise<void> {
    if (this.wasm) return;
    this.wasm = await loadWasmSigner({
      wasmUrl: this.opts.wasmUrl,
      execUrl: this.opts.execUrl,
    });
  }

  private api(): WasmSignerApi {
    if (!this.wasm) throw new Error('Call init() or connect*() first');
    return this.wasm;
  }

  /** Connect from a raw 32-byte Dilithium seed (hex). */
  async connectFromSeed(seedHex: string): Promise<DeriveInfo> {
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSeedHex(strip0x(seedHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    return this.info;
  }

  /** Connect from an EVM personal_sign signature (hex) of the domain message. */
  async connectFromEvmSignature(signatureHex: string, evmAddress?: string): Promise<DeriveInfo> {
    await this.init();
    const raw = unwrap(this.api().sqDeriveFromSigHex(strip0x(signatureHex)));
    this.info = parseWasmJson<DeriveInfo>(raw as string);
    if (evmAddress) this.info.evmAddress = evmAddress;
    return this.info;
  }

  /**
   * Connect from an EVM secp256k1 private key (hex).
   * Signs the domain message locally, then derives the PQ wallet.
   */
  async connectFromEvmPrivateKey(evmPrivateKeyHex: string): Promise<DeriveInfo> {
    const sig = signDerivationMessage(evmPrivateKeyHex);
    return this.connectFromEvmSignature(sig);
  }

  /** Browser: MetaMask personal_sign → PQ wallet. */
  async connectFromMetaMask(): Promise<DeriveInfo> {
    const { evmAddress, signatureHex } = await signDerivationWithMetaMask();
    return this.connectFromEvmSignature(signatureHex, evmAddress);
  }

  getAddress(): string {
    if (!this.info) throw new Error('Not connected');
    return this.info.address;
  }

  getPublicKeyB64(): string {
    if (!this.info) throw new Error('Not connected');
    return this.info.public_key_b64;
  }

  getDeriveInfo(): DeriveInfo | null {
    return this.info;
  }

  isUnlocked(): boolean {
    return !!this.wasm && this.api().sqIsUnlocked();
  }

  lock(): void {
    if (this.wasm) this.api().sqLock();
    this.info = null;
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

  async sendTransfer(opts: TransferOpts): Promise<{ status: string; hash: string; signedTx: object }> {
    await this.init();
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    const { nonce } = await this.getNonce();
    const params = {
      to: opts.to,
      amount,
      fee: opts.fee || '1000',
      nonce,
      memo: opts.memo || '',
    };
    const wireStr = unwrap(this.api().sqSignTransfer(JSON.stringify(params))) as string;
    const signedTx = JSON.parse(wireStr);
    const result = await this.rpc.submitTransaction(signedTx);
    return { ...result, signedTx };
  }

  async callContract(opts: CallOpts): Promise<{ status: string; hash: string; signedTx: object }> {
    await this.init();
    const value =
      opts.valueSPLD != null ? fromSPLD(opts.valueSPLD) : opts.value || '0';
    const { nonce } = await this.getNonce();
    const calldata =
      typeof opts.calldata === 'string'
        ? opts.calldata
        : Buffer.from(opts.calldata).toString('base64');
    const params: Record<string, unknown> = {
      contract: opts.contract,
      value,
      fee: opts.fee || '1000',
      nonce,
    };
    if (typeof opts.calldata === 'string' && /^[0-9a-fA-Fx]+$/.test(opts.calldata.replace(/^0x/, ''))) {
      params.calldata_hex = opts.calldata;
    } else {
      params.calldata_b64 = calldata;
    }
    const wireStr = unwrap(this.api().sqSignCall(JSON.stringify(params))) as string;
    const signedTx = JSON.parse(wireStr);
    const result = await this.rpc.submitTransaction(signedTx);
    return { ...result, signedTx };
  }

  async deployContract(opts: DeployOpts): Promise<{ status: string; hash: string; signedTx: object }> {
    await this.init();
    const { nonce } = await this.getNonce();
    const params: Record<string, unknown> = {
      fee: opts.fee || '1000',
      nonce,
    };
    if (typeof opts.bytecode === 'string') {
      if (opts.bytecode.includes('=') || opts.bytecode.length % 4 === 0 && !/^[0-9a-fA-F]+$/.test(strip0x(opts.bytecode))) {
        params.bytecode_b64 = opts.bytecode;
      } else {
        params.bytecode_hex = opts.bytecode;
      }
    } else {
      params.bytecode_b64 = Buffer.from(opts.bytecode).toString('base64');
    }
    const wireStr = unwrap(this.api().sqSignDeploy(JSON.stringify(params))) as string;
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
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    const params = {
      to: opts.to,
      amount,
      valid_after: opts.validAfter || 0,
      valid_before: opts.validBefore || 0,
    };
    const wireStr = unwrap(this.api().sqSignX402(JSON.stringify(params))) as string;
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

  /** Sign a transfer without submitting (for proxy gateway mode). */
  async signTransferOnly(opts: TransferOpts & { nonce: number }): Promise<object> {
    await this.init();
    const amount = opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount;
    const wireStr = unwrap(
      this.api().sqSignTransfer(
        JSON.stringify({
          to: opts.to,
          amount,
          fee: opts.fee || '1000',
          nonce: opts.nonce,
          memo: opts.memo || '',
        }),
      ),
    ) as string;
    return JSON.parse(wireStr);
  }
}

export default SplendorPQBridge;

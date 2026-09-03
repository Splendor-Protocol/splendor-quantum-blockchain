/**
 * Thin Signer interface for Program/Contract writes (wallet or mocks).
 * WASM signing stays inside Wallet; this is the submit surface Programs need.
 */
import type { ModuleCallOpts, CallOpts } from '../../types.js';

export type SignerAddress = { getAddress(): string };

export type Signer = SignerAddress & {
  sendModuleCall(opts: ModuleCallOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number; receipt?: Record<string, unknown> };
  }>;
  callContract(opts: CallOpts): Promise<{
    status: string;
    hash: string;
    signedTx: object;
    confirmation?: { confirmed: true; nonce: number; receipt?: Record<string, unknown> };
  }>;
};

export type MockSignerHandlers = {
  sendModuleCall?: Signer['sendModuleCall'];
  callContract?: Signer['callContract'];
};

/** In-memory Signer for Program unit tests. */
export class MockSigner implements Signer {
  readonly address: string;
  private handlers: MockSignerHandlers;
  readonly calls: {
    sendModuleCall: ModuleCallOpts[];
    callContract: CallOpts[];
  } = { sendModuleCall: [], callContract: [] };

  constructor(address: string, handlers: MockSignerHandlers = {}) {
    this.address = address;
    this.handlers = handlers;
  }

  getAddress(): string {
    return this.address;
  }

  setHandlers(handlers: MockSignerHandlers): void {
    this.handlers = { ...this.handlers, ...handlers };
  }

  async sendModuleCall(opts: ModuleCallOpts) {
    this.calls.sendModuleCall.push(opts);
    if (this.handlers.sendModuleCall) return this.handlers.sendModuleCall(opts);
    return { status: 'ok', hash: '0x' + '11'.repeat(32), signedTx: {} };
  }

  async callContract(opts: CallOpts) {
    this.calls.callContract.push(opts);
    if (this.handlers.callContract) return this.handlers.callContract(opts);
    return { status: 'ok', hash: '0x' + '22'.repeat(32), signedTx: {} };
  }
}

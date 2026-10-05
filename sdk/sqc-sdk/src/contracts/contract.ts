/**
 * Bytecode contract client (SQC-VM deploy/call).
 */
import type { Connection } from '../core/connection.js';
import type { Signer, TxResult } from '../core/signer.js';
import { requireCapability, signerAccountNonce } from '../core/signer.js';
import { bytecodeContractAddress } from '../addresses/index.js';
import {
  encodeFunctionData,
  encodeFunctionDataHex,
  decodeUint256,
  decodeAddress,
  type AbiArg,
} from '../abi.js';
import type { CallOpts, DeployOpts } from '../types.js';
import { withSigner, type BoundClient, type WalletLike } from '../bound.js';
import { WalletError } from '../errors.js';

export type ContractReadResult = {
  return_data?: string;
  gas_used?: number;
  reverted?: boolean;
  revert_reason?: string;
};

const CONTRACT_WRITES = new Set(['call']);

export class Contract {
  constructor(
    readonly connection: Connection,
    readonly address: string,
  ) {}

  static predictAddress(deployer: string, nonce: number): string {
    return bytecodeContractAddress(deployer, nonce);
  }

  getAddress(): string {
    return this.address;
  }

  withSigner(wallet: WalletLike): BoundClient<this> {
    return withSigner(this, wallet, CONTRACT_WRITES);
  }

  async read(functionSig: string, args: AbiArg[] = [], from?: string): Promise<ContractReadResult> {
    const data = encodeFunctionDataHex(functionSig, args);
    return this.connection.call({
      to: this.address,
      data,
      from,
    }) as Promise<ContractReadResult>;
  }

  async readUint256(functionSig: string, args: AbiArg[] = [], from?: string): Promise<bigint> {
    const res = await this.read(functionSig, args, from);
    if (res.reverted) {
      throw new WalletError('TX_FAILED', res.revert_reason || 'contract call reverted');
    }
    if (!res.return_data) throw new WalletError('TX_FAILED', 'empty return data');
    return decodeUint256(res.return_data);
  }

  async readAddress(functionSig: string, args: AbiArg[] = [], from?: string): Promise<string> {
    const res = await this.read(functionSig, args, from);
    if (res.reverted) {
      throw new WalletError('TX_FAILED', res.revert_reason || 'contract call reverted');
    }
    if (!res.return_data) throw new WalletError('TX_FAILED', 'empty return data');
    return decodeAddress(res.return_data);
  }

  async call(
    wallet: Signer,
    functionSig: string,
    args: AbiArg[] = [],
    opts?: Omit<CallOpts, 'contract' | 'function' | 'args'>,
  ) {
    return wallet.callContract({
      contract: this.address,
      function: functionSig,
      args,
      ...opts,
    });
  }

  async info() {
    return this.connection.getContractInfo(this.address);
  }

  async verifySource(source: string) {
    return this.connection.verifyContract(this.address, source);
  }
}

/** Deploy bytecode and return predicted address + tx result. */
export async function deployBytecode(
  wallet: Signer,
  bytecode: Uint8Array | string,
  opts?: Omit<DeployOpts, 'bytecode'>,
): Promise<{
  address: string;
  result: TxResult;
}> {
  const nonce = await signerAccountNonce(wallet);
  const predicted = Contract.predictAddress(wallet.getAddress(), nonce);
  const deployContract = requireCapability(wallet, 'deployContract');
  const result = await deployContract.call(wallet, { bytecode, ...opts });
  return { address: predicted, result };
}

export function getContract(connection: Connection, address: string): Contract {
  return new Contract(connection, address);
}

export { encodeFunctionData, encodeFunctionDataHex, functionSelector } from '../abi.js';
export type { CallOpts, DeployOpts, AbiArg };

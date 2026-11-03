/**
 * QRC-1155 multi-token Program client.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import { signerAccountNonce, signedTxNonce, writeOpts } from '../core/signer.js';
import { qrc1155Address } from '../addresses/index.js';
import { assertAddress, canonicalizeAddress } from '../address.js';
import { BridgeError } from '../errors.js';
import type { WriteOpts } from '../types.js';

export type QRC1155DeployParams = {
  name: string;
  uriPrefix?: string;
};

const QRC1155_WRITES = new Set([
  'mint',
  'mintBatch',
  'transfer',
  'batchTransfer',
  'setApprovalForAll',
  'burn',
  'burnBatch',
  'transferOwnership',
]);

const UINT_RE = /^(0|[1-9]\d*)$/;
const UINT256_LIMIT = 1n << 256n;
/** vm.MaxQRC1155Batch: the node refuses any longer batch. */
export const QRC1155_MAX_BATCH = 64;
const NAME_MAX_BYTES = 64;
const URI_MAX_BYTES = 2048;

function byteLen(s: string): number {
  return new TextEncoder().encode(s).length;
}

function capUri(uri: unknown, label: string): void {
  if (typeof uri !== 'string') throw BridgeError.invalidParams(`${label} must be a string`);
  if (byteLen(uri) > URI_MAX_BYTES) throw BridgeError.invalidParams(`${label} must be at most ${URI_MAX_BYTES} bytes`);
}

function uint(value: unknown, label: string): string {
  const s = typeof value === 'number' && Number.isSafeInteger(value) ? String(value) : String(value ?? '').trim();
  if (!UINT_RE.test(s) || BigInt(s) >= UINT256_LIMIT) {
    throw BridgeError.invalidParams(`${label}: expected a non-negative integer below 2^256`);
  }
  return s;
}

function amount(value: unknown, label = 'amount'): string {
  const s = String(value ?? '').trim();
  if (!UINT_RE.test(s) || BigInt(s) >= UINT256_LIMIT) {
    throw BridgeError.invalidAmount(`${label}: expected a non-negative integer string below 2^256`);
  }
  return s;
}

/** Mint and burn refuse a zero amount on the node. */
function positiveAmount(value: unknown, label = 'amount'): string {
  const s = amount(value, label);
  if (s === '0') throw BridgeError.invalidAmount(`${label} must be positive`);
  return s;
}

function sameLength(ids: unknown[], amounts: unknown[]): void {
  if (!Array.isArray(ids) || !Array.isArray(amounts) || ids.length !== amounts.length || ids.length === 0) {
    throw BridgeError.invalidParams('ids and amounts must be non-empty arrays of the same length');
  }
  if (ids.length > QRC1155_MAX_BATCH) {
    throw BridgeError.invalidParams(`batch of ${ids.length} exceeds the cap of ${QRC1155_MAX_BATCH}`);
  }
}

export class QRC1155 extends Program {
  protected moduleName(): string {
    return 'qrc1155';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return QRC1155_WRITES;
  }

  protected instanceParamKey(): string {
    return 'collection';
  }

  static predictAddress(deployer: string, nonce: number): string {
    return qrc1155Address(deployer, nonce);
  }

  async balanceOf(owner: string, id: string): Promise<string> {
    const res = await this.read<{ balance: string }>('balance_of', {
      owner: assertAddress(owner),
      id: uint(id, 'id'),
    });
    return res.balance;
  }

  async balanceOfBatch(owners: string[], ids: string[]): Promise<string[]> {
    if (!Array.isArray(owners) || !Array.isArray(ids) || owners.length !== ids.length) {
      throw BridgeError.invalidParams('owners and ids must be arrays of the same length');
    }
    if (owners.length > QRC1155_MAX_BATCH) {
      throw BridgeError.invalidParams(`batch of ${owners.length} exceeds the cap of ${QRC1155_MAX_BATCH}`);
    }
    const res = await this.read<{ balances: string[] }>('balance_of_batch', {
      owners: owners.map((o) => assertAddress(o)),
      ids: ids.map((i) => uint(i, 'id')),
    });
    return res.balances;
  }

  async uri(id: string): Promise<string> {
    const res = await this.read<{ uri: string }>('uri', { id: uint(id, 'id') });
    return res.uri;
  }

  async collectionInfo(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('token_info', {});
  }

  async isApprovedForAll(owner: string, operator: string): Promise<boolean> {
    const res = await this.read<{ approved: boolean }>('is_approved_for_all', {
      owner: assertAddress(owner),
      operator: assertAddress(operator),
    });
    return !!res.approved;
  }

  async totalSupply(id: string): Promise<string> {
    const res = await this.read<{ total_supply: string }>('total_supply', { id: uint(id, 'id') });
    return res.total_supply;
  }

  async mint(wallet: Signer, to: string, id: string, amt: string, uri?: string, opts?: WriteOpts) {
    if (uri != null) capUri(uri, 'uri');
    return this.write(wallet, 'mint', {
      to: assertAddress(to),
      id: uint(id, 'id'),
      amount: positiveAmount(amt),
      ...(uri ? { uri } : {}),
    }, opts);
  }

  /** `uris` may be shorter than `ids`; the remaining tokens fall back to the collection uri prefix. */
  async mintBatch(wallet: Signer, to: string, ids: string[], amounts: string[], uris?: string[], opts?: WriteOpts) {
    sameLength(ids, amounts);
    if (uris != null) {
      if (!Array.isArray(uris) || uris.length > ids.length) {
        throw BridgeError.invalidParams('uris must not be longer than ids');
      }
      uris.forEach((u, i) => capUri(u, `uris[${i}]`));
    }
    return this.write(wallet, 'mint_batch', {
      to: assertAddress(to),
      ids: ids.map((i) => uint(i, 'id')),
      amounts: amounts.map((a) => positiveAmount(a)),
      ...(uris?.length ? { uris } : {}),
    }, opts);
  }

  async transfer(wallet: Signer, to: string, id: string, amt: string, from?: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer', {
      to: this.recipient(to),
      id: uint(id, 'id'),
      amount: amount(amt),
      ...(from ? { from: assertAddress(from) } : {}),
    }, opts);
  }

  async batchTransfer(wallet: Signer, to: string, ids: string[], amounts: string[], from?: string, opts?: WriteOpts) {
    sameLength(ids, amounts);
    return this.write(wallet, 'batch_transfer', {
      to: this.recipient(to),
      ids: ids.map((i) => uint(i, 'id')),
      amounts: amounts.map((a) => amount(a)),
      ...(from ? { from: assertAddress(from) } : {}),
    }, opts);
  }

  async setApprovalForAll(wallet: Signer, operator: string, approved: boolean, opts?: WriteOpts) {
    if (typeof approved !== 'boolean') throw BridgeError.invalidParams('approved must be a boolean');
    const op = assertAddress(operator);
    let self: string | null = null;
    try {
      self = wallet.getAddress();
    } catch {
      /* a locked signer fails at send with its own error */
    }
    if (self && canonicalizeAddress(self) === op) throw BridgeError.invalidParams('cannot approve yourself as operator');
    return this.write(wallet, 'set_approval_for_all', { operator: op, approved }, opts);
  }

  async burn(wallet: Signer, id: string, amt: string, opts?: WriteOpts) {
    return this.write(wallet, 'burn', { id: uint(id, 'id'), amount: positiveAmount(amt) }, opts);
  }

  async burnBatch(wallet: Signer, ids: string[], amounts: string[], opts?: WriteOpts) {
    sameLength(ids, amounts);
    return this.write(wallet, 'burn_batch', {
      ids: ids.map((i) => uint(i, 'id')),
      amounts: amounts.map((a) => positiveAmount(a)),
    }, opts);
  }

  /** The node refuses a transfer to the collection's own address. */
  private recipient(to: string): string {
    const addr = assertAddress(to);
    if (addr === canonicalizeAddress(this.address)) {
      throw BridgeError.invalidParams('cannot transfer to the collection address');
    }
    return addr;
  }

  async transferOwnership(wallet: Signer, newOwner: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer_ownership', { new_owner: assertAddress(newOwner) }, opts);
  }
}

export async function deployQRC1155(
  wallet: Signer,
  params: QRC1155DeployParams,
  opts?: WriteOpts,
): Promise<{ address: string; result: Awaited<ReturnType<Signer['sendModuleCall']>> }> {
  const name = String(params.name ?? '').trim();
  if (!name) throw BridgeError.invalidParams('collection name is required');
  if (byteLen(name) > NAME_MAX_BYTES) {
    throw BridgeError.invalidParams(`collection name must be at most ${NAME_MAX_BYTES} bytes`);
  }
  if (params.uriPrefix != null) capUri(params.uriPrefix, 'uriPrefix');
  const nonce = await signerAccountNonce(wallet);
  const result = await wallet.sendModuleCall({
    module: 'qrc1155',
    action: 'deploy',
    params: {
      name,
      ...(params.uriPrefix ? { uri_prefix: params.uriPrefix } : {}),
    },
    ...writeOpts(opts),
  });
  const address = QRC1155.predictAddress(wallet.getAddress(), signedTxNonce(result) ?? nonce);
  return { address, result };
}

export function getCollection(connection: Connection, address: string): QRC1155 {
  return new QRC1155(connection, address);
}

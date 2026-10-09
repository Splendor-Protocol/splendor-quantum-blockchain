/**
 * QRC-721 non-fungible token Program client.
 * Covers only the actions the node router dispatches (vm.KnownModuleActions["qrc721"]).
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { Signer } from '../core/signer.js';
import { signerAccountNonce, signedTxNonce, writeOpts } from '../core/signer.js';
import { qrc721Address } from '../addresses/index.js';
import { assertAddress, canonicalizeAddress } from '../address.js';
import { BridgeError } from '../errors.js';
import type { WriteOpts } from '../types.js';

export type QRC721DeployParams = {
  name: string;
  symbol: string;
};

export type QRC721CollectionInfo = {
  name: string;
  symbol: string;
  owner: string;
  total_supply: string;
};

const QRC721_WRITES = new Set(['mint', 'transfer', 'burn']);
const UINT_RE = /^(0|[1-9]\d*)$/;
const NAME_MAX_BYTES = 64;
const URI_MAX_BYTES = 2048;

function tokenId(value: unknown): string {
  const s = typeof value === 'number' && Number.isSafeInteger(value) ? String(value) : String(value ?? '').trim();
  if (!UINT_RE.test(s)) throw BridgeError.invalidParams('id: expected a non-negative integer');
  return s;
}

function byteLen(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** The node returns addresses as a 32-byte array here; accept that or an `SQ:` string. */
function decodeAddress(raw: unknown): string {
  if (typeof raw === 'string') {
    const a = canonicalizeAddress(raw);
    if (a) return a;
  }
  if (Array.isArray(raw) && raw.length === 32 && raw.every((b) => Number.isInteger(b) && b >= 0 && b <= 255)) {
    return 'SQ:' + raw.map((b: number) => b.toString(16).padStart(2, '0')).join('');
  }
  throw BridgeError.rpc('qrc721: unexpected address in response');
}

/** The node returns counts as JSON numbers or strings. */
function decodeUint(raw: unknown, label: string): string {
  const s = typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0 ? String(raw) : String(raw ?? '');
  if (!UINT_RE.test(s)) throw BridgeError.rpc(`qrc721: unexpected ${label} in response`);
  return s;
}

export class QRC721 extends Program {
  protected moduleName(): string {
    return 'qrc721';
  }

  protected writeMethodNames(): ReadonlySet<string> {
    return QRC721_WRITES;
  }

  protected instanceParamKey(): string {
    return 'collection';
  }

  static predictAddress(deployer: string, nonce: number): string {
    return qrc721Address(deployer, nonce);
  }

  async ownerOf(id: string): Promise<string> {
    return decodeAddress(await this.read<unknown>('owner_of', { id: tokenId(id) }));
  }

  async balanceOf(owner: string): Promise<string> {
    return decodeUint(await this.read<unknown>('balance_of', { owner: assertAddress(owner) }), 'balance');
  }

  async collectionInfo(): Promise<QRC721CollectionInfo> {
    const res = await this.read<Record<string, unknown>>('token_info', {});
    return {
      name: String(res.name ?? ''),
      symbol: String(res.symbol ?? ''),
      owner: res.owner == null || res.owner === '' ? '' : decodeAddress(res.owner),
      total_supply: decodeUint(res.total_supply ?? '0', 'total_supply'),
    };
  }

  /** Collection owner only. `id` must be unminted. */
  async mint(wallet: Signer, to: string, id: string, uri?: string, opts?: WriteOpts) {
    if (uri != null && byteLen(uri) > URI_MAX_BYTES) {
      throw BridgeError.invalidParams(`uri must be at most ${URI_MAX_BYTES} bytes`);
    }
    return this.write(wallet, 'mint', {
      to: assertAddress(to),
      id: tokenId(id),
      ...(uri ? { uri } : {}),
    }, opts);
  }

  /** Token owner, its approved address, or an operator. */
  async transfer(wallet: Signer, to: string, id: string, opts?: WriteOpts) {
    return this.write(wallet, 'transfer', { to: assertAddress(to), id: tokenId(id) }, opts);
  }

  async burn(wallet: Signer, id: string, opts?: WriteOpts) {
    return this.write(wallet, 'burn', { id: tokenId(id) }, opts);
  }
}

/** Deploy a new QRC-721 collection; the deployer becomes its owner. */
export async function deployQRC721(
  wallet: Signer,
  params: QRC721DeployParams,
  opts?: WriteOpts,
): Promise<{ address: string; result: Awaited<ReturnType<Signer['sendModuleCall']>> }> {
  const name = String(params.name ?? '').trim();
  const symbol = String(params.symbol ?? '').trim();
  if (!name || !symbol) throw BridgeError.invalidParams('collection name and symbol are required');
  if (byteLen(name) > NAME_MAX_BYTES || byteLen(symbol) > NAME_MAX_BYTES) {
    throw BridgeError.invalidParams(`collection name and symbol must be at most ${NAME_MAX_BYTES} bytes`);
  }
  const nonce = await signerAccountNonce(wallet);
  const result = await wallet.sendModuleCall({
    module: 'qrc721',
    action: 'deploy',
    params: { name, symbol },
    ...writeOpts(opts),
  });
  const address = QRC721.predictAddress(wallet.getAddress(), signedTxNonce(result) ?? nonce);
  return { address, result };
}

export function getNftCollection(connection: Connection, address: string): QRC721 {
  return new QRC721(connection, address);
}

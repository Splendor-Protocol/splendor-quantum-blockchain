/**
 * QRC-1155 multi-token Program client.
 */
import { Program } from '../core/program.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { qrc1155Address } from '../addresses/index.js';

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
    const res = await this.read<{ balance: string }>('balance_of', { owner, id });
    return res.balance;
  }

  async balanceOfBatch(owners: string[], ids: string[]): Promise<string[]> {
    const res = await this.read<{ balances: string[] }>('balance_of_batch', { owners, ids });
    return res.balances;
  }

  async uri(id: string): Promise<string> {
    const res = await this.read<{ uri: string }>('uri', { id });
    return res.uri;
  }

  async collectionInfo(): Promise<Record<string, unknown>> {
    return this.read<Record<string, unknown>>('token_info', {});
  }

  async isApprovedForAll(owner: string, operator: string): Promise<boolean> {
    const res = await this.read<{ approved: boolean }>('is_approved_for_all', {
      owner,
      operator,
    });
    return !!res.approved;
  }

  async totalSupply(id: string): Promise<string> {
    const res = await this.read<{ total_supply: string }>('total_supply', { id });
    return res.total_supply;
  }

  async mint(
    wallet: SplendorPQBridge,
    to: string,
    id: string,
    amount: string,
    uri?: string,
  ) {
    return this.write(wallet, 'mint', { to, id, amount, ...(uri ? { uri } : {}) });
  }

  async mintBatch(
    wallet: SplendorPQBridge,
    to: string,
    ids: string[],
    amounts: string[],
    uris?: string[],
  ) {
    return this.write(wallet, 'mint_batch', {
      to,
      ids,
      amounts,
      ...(uris ? { uris } : {}),
    });
  }

  async transfer(
    wallet: SplendorPQBridge,
    to: string,
    id: string,
    amount: string,
    from?: string,
  ) {
    return this.write(wallet, 'transfer', {
      to,
      id,
      amount,
      ...(from ? { from } : {}),
    });
  }

  async batchTransfer(
    wallet: SplendorPQBridge,
    to: string,
    ids: string[],
    amounts: string[],
    from?: string,
  ) {
    return this.write(wallet, 'batch_transfer', {
      to,
      ids,
      amounts,
      ...(from ? { from } : {}),
    });
  }

  async setApprovalForAll(wallet: SplendorPQBridge, operator: string, approved: boolean) {
    return this.write(wallet, 'set_approval_for_all', { operator, approved });
  }

  async burn(wallet: SplendorPQBridge, id: string, amount: string) {
    return this.write(wallet, 'burn', { id, amount });
  }

  async burnBatch(wallet: SplendorPQBridge, ids: string[], amounts: string[]) {
    return this.write(wallet, 'burn_batch', { ids, amounts });
  }

  async transferOwnership(wallet: SplendorPQBridge, newOwner: string) {
    return this.write(wallet, 'transfer_ownership', { new_owner: newOwner });
  }
}

export async function deployQRC1155(
  wallet: SplendorPQBridge,
  params: QRC1155DeployParams,
  opts?: { fee?: string; waitForConfirmation?: boolean },
): Promise<{ address: string; result: Awaited<ReturnType<SplendorPQBridge['sendModuleCall']>> }> {
  const { nonce } = await wallet.getNonce();
  const predicted = QRC1155.predictAddress(wallet.getAddress(), nonce);
  const result = await wallet.sendModuleCall({
    module: 'qrc1155',
    action: 'deploy',
    params: {
      name: params.name,
      ...(params.uriPrefix ? { uri_prefix: params.uriPrefix } : {}),
    },
    fee: opts?.fee,
    waitForConfirmation: opts?.waitForConfirmation,
  });
  return { address: predicted, result };
}

export function getCollection(connection: Connection, address: string): QRC1155 {
  return new QRC1155(connection, address);
}

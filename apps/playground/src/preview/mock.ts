import type { NetworkId } from '@splendor/connect-react';
import { MOCK_ADDRESS, createMockConnectController, mockWallet, type MockConnectController } from '@splendor/connect-react/testing';

export type MockWalletState = 'connected' | 'disconnected';

const TOKEN_UNITS = '1000' + '0'.repeat(18);

/**
 * Mock wallet for previews: no WASM, no RPC, every signature succeeds instantly. Module reads
 * answer with demo data so token and NFT widgets have something to show.
 */
export function createPlaygroundMock(opts: { network: NetworkId; state: MockWalletState; balance?: string }): MockConnectController {
  const balance = opts.balance ?? '1250.5';
  const wallet = mockWallet({
    balanceSPLD: balance,
    balanceRaw: balance.replace('.', '').padEnd(balance.split('.')[0]!.length + 18, '0'),
    estimate: { fee: '21000000000000', fee_is_floor: false, gas: 21000 },
    moduleCall: ({ module, action }) => {
      if (action === 'token_info') {
        return module === 'qrc20'
          ? { name: 'Demo Token', symbol: 'DEMO', decimals: 18, total_supply: '1000000' + '0'.repeat(18), owner: MOCK_ADDRESS, exists: true }
          : { name: 'Demo Collection', symbol: 'DEMO', owner: MOCK_ADDRESS, total_supply: '12', uri_prefix: 'ipfs://demo/' };
      }
      if (action === 'balance_of') return module === 'qrc20' ? { balance: TOKEN_UNITS } : { balance: '3' };
      if (action === 'total_supply') return { total_supply: module === 'qrc20' ? '1000000' + '0'.repeat(18) : '12' };
      return {};
    },
  });
  return createMockConnectController({ status: opts.state, network: opts.network, balance, wallet });
}

export { MOCK_ADDRESS };

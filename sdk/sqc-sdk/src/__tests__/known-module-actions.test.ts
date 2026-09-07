/**
 * Drift guard: SDK Program coverage vs on-chain KnownModuleActions.
 * Golden mirrors vm/module_router_read.go — update both when chain adds actions.
 */
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const HERE = dirname(fileURLToPath(import.meta.url));
const GOLDEN = JSON.parse(
  readFileSync(join(HERE, 'fixtures', 'known-module-actions.json'), 'utf8').replace(/^\uFEFF/, ''),
) as Record<string, { writes: string[]; reads: string[] }>;

/** SDK-covered actions (explicit allowlist of intentional gaps). */
const SDK_COVERAGE: Record<string, { writes: string[]; reads: string[]; allowlistGaps?: string[] }> = {
  qrc20: {
    writes: [
      'deploy',
      'mint',
      'transfer',
      'approve',
      'burn',
      'permit',
      'transfer_from',
      'increase_allowance',
      'decrease_allowance',
      'burn_from',
      'transfer_ownership',
      'pause',
      'unpause',
    ],
    reads: [
      'balance_of',
      'total_supply',
      'allowance',
      'token_info',
      'permit_nonce',
      'paused',
      'owner',
    ],
  },
  qrc1155: {
    writes: [
      'deploy',
      'mint',
      'mint_batch',
      'transfer',
      'batch_transfer',
      'set_approval_for_all',
      'burn',
      'burn_batch',
      'transfer_ownership',
    ],
    reads: ['balance_of', 'balance_of_batch', 'is_approved_for_all', 'uri', 'total_supply', 'token_info'],
  },
  dex: {
    writes: ['deploy_pool', 'add_liquidity', 'swap'],
    reads: ['reserves', 'pool_info'],
  },
  exchange: {
    writes: [
      'deploy',
      'create_condition',
      'split',
      'merge',
      'match',
      'cancel_order',
      'increment_nonce',
      'resolve',
      'redeem',
      'redeem_for',
      'pause',
      'unpause',
      'set_operator',
      'transfer_ownership',
      'set_redeem_fee_bps',
      'set_fee_recipient',
    ],
    reads: [
      'exchange_info',
      'condition_info',
      'order_status',
      'order_remaining',
      'order_hash',
      'nonce',
      'is_operator',
      'position',
      'position_id',
      'simulate_match',
    ],
  },
  bridge: {
    writes: [
      'deploy',
      'claim',
      'set_operator',
      'set_threshold',
      'pause',
      'unpause',
      'transfer_ownership',
    ],
    reads: ['bridge_info', 'claim_status'],
  },
  verify: {
    writes: ['deploy', 'register', 'attest', 'finalize', 'spend', 'set_operator', 'set_threshold', 'pause', 'unpause'],
    reads: ['proof', 'instance'],
  },
  validators: {
    writes: ['register', 'unregister', 'finalize_exit'],
    reads: ['validator', 'list', 'count', 'bond'],
  },
  lending: {
    writes: ['deploy', 'enable_asset', 'set_oracle', 'deposit', 'withdraw', 'borrow', 'repay'],
    reads: ['pool', 'deposit_balance'],
  },
  oracle: {
    writes: ['deploy', 'register_feeder', 'submit_price'],
    reads: ['price', 'feeder'],
  },
  multisig: {
    writes: ['deploy', 'submit'],
    reads: ['threshold', 'proposal'],
  },
  governance: {
    writes: ['submit', 'vote'],
    reads: ['proposal', 'count'],
  },
  qrc721: {
    writes: ['deploy', 'mint', 'transfer', 'burn'],
    reads: ['owner_of', 'balance_of', 'token_info'],
  },
  qrc4626: {
    writes: ['deploy', 'deposit'],
    reads: ['total_assets', 'name'],
  },
};

describe('KnownModuleActions sync', () => {
  it('golden matches Go KnownModuleActions for routed modules', () => {
    for (const [module, acts] of Object.entries(GOLDEN)) {
      expect(SDK_COVERAGE[module], `SDK_COVERAGE missing module ${module}`).toBeTruthy();
      const cov = SDK_COVERAGE[module]!;
      const gaps = new Set(cov.allowlistGaps ?? []);
      for (const w of acts.writes) {
        if (gaps.has(`w:${w}`)) continue;
        expect(cov.writes, `${module} write ${w}`).toContain(w);
      }
      for (const r of acts.reads) {
        if (gaps.has(`r:${r}`)) continue;
        expect(cov.reads, `${module} read ${r}`).toContain(r);
      }
    }
  });
});

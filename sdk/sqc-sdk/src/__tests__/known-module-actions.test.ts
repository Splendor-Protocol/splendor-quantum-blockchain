/**
 * Drift guard: SDK Program coverage vs on-chain KnownModuleActions.
 * Golden mirrors vm/module_router_read.go — update both when chain adds actions.
 */
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { ROUTED_NATIVE_MODULE_IDS } from '../modules.js';

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
    writes: ['register', 'unregister', 'finalize_exit', 'approve', 'revoke'],
    reads: ['validator', 'list', 'count', 'bond', 'approved'],
  },
  qrc721: {
    writes: ['deploy', 'mint', 'transfer', 'burn'],
    reads: ['owner_of', 'balance_of', 'token_info'],
  },
};

describe('KnownModuleActions sync', () => {
  it('golden lists only the routed catalog', () => {
    expect(Object.keys(GOLDEN).sort()).toEqual([...ROUTED_NATIVE_MODULE_IDS].sort());
  });

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

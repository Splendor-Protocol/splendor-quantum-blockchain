/**
 * Optional live node test. Skips unless SQC_LIVE=1 and RPC responds.
 *   SQC_LIVE=1 SQC_RPC=http://127.0.0.1:8080 PQ_SEED=... npm test
 */
import { describe, expect, it } from 'vitest';
import { SplendorPQBridge } from '../index.js';

const live = process.env.SQC_LIVE === '1' || process.env.SQC_LIVE === 'true';
const rpcUrl = process.env.SQC_RPC || 'http://127.0.0.1:8080';
const seed = process.env.PQ_SEED || '01'.repeat(32);

describe.skipIf(!live)('live node e2e', () => {
  it('transfer + waitForConfirmation', async () => {
    const bridge = new SplendorPQBridge({ rpcUrl, retries: 1, timeoutMs: 10_000 });
    await bridge.init();
    await bridge.connectFromSeed(seed);
    await bridge.rpc.assertChainId();

    const bal = await bridge.getBalance();
    if (BigInt(bal.balance) === 0n) {
      console.warn('skip transfer: zero balance for', bridge.getAddress());
      bridge.disconnect();
      return;
    }

    const res = await bridge.sendTransfer({
      to: bridge.getAddress(),
      amount: '1',
      fee: '1000',
      waitForConfirmation: true,
      confirmTimeoutMs: 90_000,
    });
    expect(res.hash).toBeTruthy();
    expect(res.confirmation?.confirmed).toBe(true);
    bridge.disconnect();
  }, 120_000);
});

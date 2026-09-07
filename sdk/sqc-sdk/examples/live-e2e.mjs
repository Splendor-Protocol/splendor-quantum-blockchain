/**
 * Live integration: transfer + waitForConfirmation against a running SQC node.
 *
 * Default: probes SQC_RPC; skips (exit 0) if unreachable.
 * Strict: LIVE_E2E=1 requires node + funded wallet or exits 1.
 *
 *   SQC_RPC=http://localhost:8080 PQ_SEED=<64hex> LIVE_E2E=1 node examples/live-e2e.mjs
 *
 * Funding: allocate your PQ address in genesis or send SPLD before running.
 * (cmd/node does not auto-fund the demo seed.)
 */
import { SplendorPQBridge } from '../dist/index.js';

const rpcUrl = process.env.SQC_RPC || 'http://127.0.0.1:8080';
const seed = process.env.PQ_SEED || '01'.repeat(32);
const strict = process.env.LIVE_E2E === '1' || process.env.LIVE_E2E === 'true';
const to = process.env.TO; // optional recipient; defaults to self

async function probe(url) {
  try {
    const res = await fetch(url.replace(/\/$/, '') + '/status', {
      signal: AbortSignal.timeout(3000),
    });
    return res.ok;
  } catch {
    return false;
  }
}

async function main() {
  console.log('Live e2e against', rpcUrl);

  const up = await probe(rpcUrl);
  if (!up) {
    const msg = `RPC not reachable at ${rpcUrl}`;
    if (strict) {
      console.error('FAIL:', msg, '(LIVE_E2E=1)');
      process.exit(1);
    }
    console.log('SKIP:', msg);
    process.exit(0);
  }

  const bridge = new SplendorPQBridge({
    rpcUrls: [rpcUrl, process.env.SQC_RPC_FALLBACK].filter(Boolean),
    retries: 2,
  });
  await bridge.init();
  await bridge.connectFromSeed(seed);
  console.log('SQ address:', bridge.getAddress());

  await bridge.rpc.assertChainId();
  const bal = await bridge.getBalance();
  console.log('Balance:', bal.balance_spld, 'SPLD');

  if (BigInt(bal.balance) === 0n) {
    const msg = 'account has zero balance — fund SQ address before transfer test';
    if (strict) {
      console.error('FAIL:', msg);
      process.exit(1);
    }
    console.log('SKIP:', msg);
    // Still exercise read path + confirmation helper API with a no-op wait edge case
    bridge.disconnect();
    process.exit(0);
  }

  const recipient = to || bridge.getAddress();
  const { nonce: nonceBefore } = await bridge.getNonce();
  console.log('Sending self/peer transfer (nonce', nonceBefore, ')…');

  const res = await bridge.sendTransfer({
    to: recipient,
    amount: '1', // 1 base unit
    fee: '1000',
    waitForConfirmation: true,
    confirmTimeoutMs: 90_000,
  });

  console.log('submit:', res.status, res.hash);
  if (!res.confirmation?.confirmed) {
    throw new Error('expected confirmation from waitForConfirmation');
  }
  console.log('confirmed; nonce now', res.confirmation.nonce);

  bridge.disconnect();
  console.log('\nLive e2e passed.');
}

main().catch((e) => {
  console.error('FAIL:', e);
  process.exit(1);
});

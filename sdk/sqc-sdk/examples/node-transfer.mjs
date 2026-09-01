/**
 * Node example: derive PQ wallet from an EVM private key and query/send.
 *
 * Usage:
 *   SQC_RPC=http://localhost:8080 EVM_KEY=0x... node examples/node-transfer.mjs
 */
import { SplendorPQBridge, DERIVATION_DOMAIN_MESSAGE } from '../dist/index.js';

const rpcUrl = process.env.SQC_RPC || 'http://localhost:8080';
const evmKey = process.env.EVM_KEY;
const seed = process.env.PQ_SEED; // optional: direct 32-byte hex seed

async function main() {
  console.log('Domain message:\n' + DERIVATION_DOMAIN_MESSAGE + '\n');
  const bridge = new SplendorPQBridge({ rpcUrl });
  await bridge.init();

  if (seed) {
    await bridge.connectFromSeed(seed);
  } else if (evmKey) {
    await bridge.connectFromEvmPrivateKey(evmKey);
  } else {
    // Demo seed for offline derive (will not have funds)
    const demoSeed = '01'.repeat(32);
    console.log('No EVM_KEY/PQ_SEED set — using demo seed');
    await bridge.connectFromSeed(demoSeed);
  }

  const info = bridge.getDeriveInfo();
  console.log('SQ address:', info.address);
  console.log('Pubkey b64 (first 40):', info.public_key_b64.slice(0, 40) + '...');

  try {
    const status = await bridge.getStatus();
    console.log('Chain status:', status);
    const bal = await bridge.getBalance();
    console.log('Balance:', bal.balance_spld, 'SPLD');
  } catch (e) {
    console.warn('RPC not reachable:', e.message);
  }

  const to = process.env.TO;
  const amountSPLD = process.env.AMOUNT_SPLD;
  if (to && amountSPLD) {
    const res = await bridge.sendTransfer({ to, amount: '0', amountSPLD });
    console.log('Transfer result:', res);
  } else {
    console.log('Set TO=SQ:... AMOUNT_SPLD=0.001 to send a transfer');
  }

  bridge.lock();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});

/**
 * Offline e2e: load WASM, derive from fixed seed, sign transfer, verify JSON shape.
 * Does not require a running chain node.
 */
import { SplendorPQBridge } from '../dist/index.js';

const FIXED_SEED = '01'.repeat(32);

async function main() {
  console.log('1) Loading WASM signer...');
  const bridge = new SplendorPQBridge({ rpcUrl: 'http://127.0.0.1:9' }); // unused for offline
  await bridge.init();
  console.log('   ok');

  console.log('2) Derive from fixed seed...');
  const info = await bridge.connectFromSeed(FIXED_SEED);
  console.log('   address:', info.address);
  if (!info.address.startsWith('SQ:') || info.address.length !== 67) {
    throw new Error('bad address format');
  }
  if (!info.public_key_b64) throw new Error('missing pubkey');

  console.log('3) Sign transfer (nonce 0 includes pubkey)...');
  const signed = await bridge.signTransferOnly({
    to: info.address,
    amount: '1000',
    fee: '1000',
    nonce: 0,
  });
  if (!Array.isArray(signed.from) || signed.from.length !== 32) {
    throw new Error('from must be JSON array of 32 bytes (Go wire format)');
  }
  if (!signed.signature || !signed.public_key) {
    throw new Error('missing signature or public_key');
  }
  console.log('   wire from[0..3]:', signed.from.slice(0, 4));
  console.log('   sig bytes:', signed.signature.length);

  console.log('4) Re-derive same seed is stable...');
  const bridge2 = new SplendorPQBridge();
  await bridge2.init();
  const info2 = await bridge2.connectFromSeed(FIXED_SEED);
  if (info2.address !== info.address) {
    throw new Error('same seed must derive same address');
  }
  console.log('   address match:', info2.address);
  bridge2.lock();

  console.log('5) Lock / disconnect...');
  bridge.lock();
  if (bridge.isUnlocked()) throw new Error('still unlocked after lock');
  bridge.disconnect();

  console.log('\nAll offline e2e checks passed.');
}

main().catch((e) => {
  console.error('FAIL:', e);
  process.exit(1);
});

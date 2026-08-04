/**
 * Offline e2e: load WASM, derive from fixed seed, sign transfer, verify JSON shape.
 * Does not require a running chain node.
 */
import { SplendorPQBridge } from '../dist/index.js';
import { seedFromEvmSignature, signDerivationMessage } from '../dist/derive.js';

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

  console.log('4) EVM key → same derivation path...');
  const evmKey = '11'.repeat(32);
  const sig = signDerivationMessage(evmKey);
  const seed = seedFromEvmSignature(sig);
  console.log('   seed:', seed.slice(0, 16) + '...');
  const bridge2 = new SplendorPQBridge();
  await bridge2.init();
  const info2 = await bridge2.connectFromEvmPrivateKey(evmKey);
  const info3 = await (async () => {
    const b = new SplendorPQBridge();
    await b.init();
    return b.connectFromEvmSignature(sig);
  })();
  if (info2.address !== info3.address) {
    throw new Error('EVM key path != signature path');
  }
  console.log('   mapped SQ:', info2.address);

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

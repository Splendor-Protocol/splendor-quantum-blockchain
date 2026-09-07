import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadWasmSigner, resetWasmLoader } from '../wasm-loader.js';
import { WASM_EXPORTS } from '../wasm-types.js';

const here = dirname(fileURLToPath(import.meta.url));
const golden = JSON.parse(
  readFileSync(join(here, '../../testdata/golden-vectors.json'), 'utf8'),
);

// A signer binary that never exported sqSignBridgeClaim shipped on main and no
// test noticed, because nothing called it and the loader only checked that
// sqCreateSession existed. These two cases are what would have caught it: the
// first fails on any binary missing an export, the second on a binary whose
// bridge digest disagrees with the Go signer.
describe('bridge claim signing', () => {
  it('the loaded binary exports every name the SDK binds', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const missing = WASM_EXPORTS.filter(
      (name) => typeof (wasm as Record<string, unknown>)[name] !== 'function',
    );
    expect(missing).toEqual([]);
  }, 60_000);

  it('sqSignBridgeClaim digest matches the Go golden vector', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const sid = wasm.sqCreateSession();
    const derive = wasm.sqDeriveFromSeedHex(sid, golden.seedHex);
    if (typeof derive === 'object' && 'error' in derive) {
      throw new Error(derive.error);
    }

    const b = golden.bridge;
    const raw = wasm.sqSignBridgeClaim(
      sid,
      JSON.stringify({
        bridge: b.bridge,
        id: b.id,
        asset: b.asset,
        amount: b.amount,
        recipient: b.recipient,
        evm_tx: b.evmTx,
      }),
    );
    if (typeof raw === 'object' && 'error' in raw) {
      throw new Error(raw.error);
    }
    const signed = JSON.parse(raw as string);
    expect(signed.digest_hex).toBe(b.digest);
    expect(signed.chain_id).toBe(b.chainId);
    expect(signed.bridge).toBe(b.bridge);
    expect(signed.recipient).toBe(b.recipient);
    expect(signed.evm_tx).toBe(b.evmTx);
    expect(signed.amount).toBe(b.amount);
    expect(signed.id).toBe(b.id);
    expect(signed.asset).toBe(b.asset);
    expect(typeof signed.signature).toBe('string');
    expect(signed.signature.length).toBeGreaterThan(0);
  }, 60_000);
});

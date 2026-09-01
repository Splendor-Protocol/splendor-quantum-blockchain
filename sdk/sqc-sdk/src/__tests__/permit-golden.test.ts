import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadWasmSigner, resetWasmLoader } from '../wasm-loader.js';

const here = dirname(fileURLToPath(import.meta.url));
const golden = JSON.parse(
  readFileSync(join(here, '../../testdata/golden-vectors.json'), 'utf8'),
);

describe('signPermit golden vector', () => {
  it('sqSignPermit digest matches golden fromGoldenSeed', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const sid = wasm.sqCreateSession();
    const seedHex = golden.seedHex;
    const derive = wasm.sqDeriveFromSeedHex(sid, seedHex);
    if (typeof derive === 'object' && 'error' in derive) {
      throw new Error(derive.error);
    }

    const p = golden.permit;
    const raw = wasm.sqSignPermit(
      sid,
      JSON.stringify({
        token: p.contract,
        spender: p.spender,
        amount: p.amount,
        nonce: p.nonce,
        deadline: p.deadline,
      }),
    );
    if (typeof raw === 'object' && 'error' in raw) {
      throw new Error(raw.error);
    }
    const signed = JSON.parse(raw as string);
    expect(signed.digest_hex).toBe(p.fromGoldenSeed.digestHex);
    expect(signed.owner).toBe(golden.address);
  }, 60_000);
});

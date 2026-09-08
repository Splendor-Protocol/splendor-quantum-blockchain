import { describe, expect, it } from 'vitest';
import { loadWasmSigner, resetWasmLoader } from '../wasm-loader.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

// The WASM build must agree with the Go signer on the SQVL digests (pinned
// by signer.TestGoldenVerifyVectorsPinDigests) and derive the same SLH-DSA
// key from the golden seed. SLH-DSA-128s signing is slow; each `it` signs at
// most once and the file-level timeout covers it.
describe('SQVL signing in the WASM signer', () => {
  it('derives the golden SLH-DSA public key from the golden seed', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const sid = wasm.sqCreateSession();
    const derive = wasm.sqDeriveFromSeedHex(sid, golden.seedHex);
    if (typeof derive === 'object' && 'error' in derive) throw new Error(derive.error);
    expect(wasm.sqGetSLHPublicKeyHex(sid)).toBe(golden.verify.fromGoldenSeed.slhPublicKeyHex);
    // Locked sessions have no SLH key either.
    wasm.sqLock(sid);
    const locked = wasm.sqGetSLHPublicKeyHex(sid);
    expect(typeof locked === 'object' && 'error' in locked).toBe(true);
  }, 60_000);

  it('sqSignVerifyInstruction reports the golden digest and a hex id', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const sid = wasm.sqCreateSession();
    const derive = wasm.sqDeriveFromSeedHex(sid, golden.seedHex);
    if (typeof derive === 'object' && 'error' in derive) throw new Error(derive.error);

    const raw = wasm.sqSignVerifyInstruction(sid, JSON.stringify(golden.verify.instruction));
    if (typeof raw === 'object' && 'error' in raw) throw new Error(raw.error);
    const signed = JSON.parse(raw as string);
    expect(signed.digest_hex).toBe(golden.verify.digest);
    expect(signed.chain_id).toBe(golden.verify.chainId);
    expect(signed.signer).toBe(golden.address);
    const ins = signed.instruction;
    expect(ins.instruction_id).toBe(golden.verify.instruction.instruction_id);
    expect(ins.version).toBe(1);
    expect(ins.payload).toBe(golden.verify.instruction.payload);
    // Base64 byte fields of the FIPS sizes: 1952 / 3309 / 32 / 7856 bytes.
    const len = (b64: string) => Buffer.from(b64, 'base64').length;
    expect(len(ins.mldsa_pubkey)).toBe(1952);
    expect(len(ins.sig_mldsa)).toBe(3309);
    expect(len(ins.slh_pubkey)).toBe(32);
    expect(len(ins.sig_slh)).toBe(7856);
    expect(Buffer.from(ins.slh_pubkey, 'base64').toString('hex')).toBe(
      golden.verify.fromGoldenSeed.slhPublicKeyHex,
    );
  }, 60_000);

  it('sqSignVerifyAttestation binds the instance and reports the golden attestation digest', async () => {
    resetWasmLoader();
    const wasm = await loadWasmSigner({});
    const sid = wasm.sqCreateSession();
    const derive = wasm.sqDeriveFromSeedHex(sid, golden.seedHex);
    if (typeof derive === 'object' && 'error' in derive) throw new Error(derive.error);

    const raw = wasm.sqSignVerifyAttestation(
      sid,
      JSON.stringify({ verify: golden.verify.attestation.instance, digest_hex: golden.verify.digest }),
    );
    if (typeof raw === 'object' && 'error' in raw) throw new Error(raw.error);
    const att = JSON.parse(raw as string);
    expect(att.attestation_digest_hex).toBe(golden.verify.attestation.digest);
    expect(att.digest_hex).toBe(golden.verify.digest);
    expect(att.verify).toBe(golden.verify.attestation.instance);
    expect(att.attestor).toBe(golden.address);
    expect(att.pubkey).toHaveLength(1952 * 2);
    expect(att.sig_mldsa).toHaveLength(3309 * 2);
    expect(att.slh_pubkey).toBe(golden.verify.fromGoldenSeed.slhPublicKeyHex);
    expect(att.sig_slh).toHaveLength(7856 * 2);

    const bad = wasm.sqSignVerifyAttestation(sid, JSON.stringify({ verify: 'nope', digest_hex: golden.verify.digest }));
    expect(typeof bad === 'object' && 'error' in bad).toBe(true);
  }, 60_000);
});

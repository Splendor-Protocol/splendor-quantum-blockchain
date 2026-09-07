/**
 * External-app SQVL demo (sign → check → attest → proof → settle → replay fails).
 *
 * This is a client of the native node RPC. It does not run a sidecar.
 *
 * Usage (against a local node with verify_v1 active):
 *   node examples/sqvl-external-app.mjs http://127.0.0.1:8080
 *
 * The cryptographic signing of ML-DSA-65 + SLH-DSA instructions is done by
 * sqc-node tests / the WASM signer (`sqGenerateSLH` + `sqSignVerifyInstruction`).
 * This script documents the HTTP product surface any chain or bank calls.
 */
const base = (process.argv[2] || 'http://127.0.0.1:8080').replace(/\/$/, '');

async function rpc(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: { 'content-type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json };
}

const steps = [];
steps.push(['GET /status', await rpc('GET', '/status')]);
steps.push(['GET /validators', await rpc('GET', '/validators')]);

console.log('SQVL external-app demo against', base);
for (const [name, out] of steps) {
  console.log('\n' + name, out.status);
  console.log(JSON.stringify(out.json, null, 2).slice(0, 1200));
}

console.log(`
Flow (submit signed txs via POST /tx/submit or the X402-style doors):
  1. Sign instruction (ML-DSA-65 + SLH-DSA) under CtxVerify
  2. POST /verify/check          — valid=true, no state
  3. TxCall verify.register
  4. N operators: TxCall verify.attest  (or POST /verify/attest {tx})
  5. GET /verify/proof/{id}?instance=   — on-chain proof
  6. POST /verify/settle {tx: spend}    — native_transfer | qrc20_transfer | finalize_only
  7. Replay the same instruction_id     — rejected
`);

/**
 * Gateway smoke against hardened proxy defaults.
 * Usage: GATEWAY_URL=http://127.0.0.1:3090 node examples/gateway-smoke.mjs
 *
 * Public proxy MUST reject seed / evmPrivateKey / derive-with-creds.
 * Prefer signedTx submit path.
 */
const base = (process.env.GATEWAY_URL || 'http://127.0.0.1:3090').replace(/\/$/, '');

async function main() {
  const health = await fetch(`${base}/health`).then((r) => r.json());
  console.log('health:', health);
  if (!health.ok) throw new Error('health not ok');

  if (health.mode === 'proxy' && !health.custodialProxy) {
    const derive = await fetch(`${base}/v1/derive`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seed: '01'.repeat(32) }),
    });
    const deriveBody = await derive.json();
    if (derive.ok) throw new Error('proxy should reject /v1/derive with seed');
    console.log('proxy correctly rejected seed derive:', deriveBody.code);

    const transfer = await fetch(`${base}/v1/tx/transfer`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        seed: '01'.repeat(32),
        to: 'SQ:' + 'ab'.repeat(32),
        amount: '1',
      }),
    });
    if (transfer.ok) throw new Error('proxy should reject transfer with seed');
    console.log('proxy correctly rejected custodial transfer');

    const rejKey = await fetch(`${base}/v1/derive`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ evmPrivateKey: '11'.repeat(32) }),
    });
    if (rejKey.ok) throw new Error('proxy should reject evmPrivateKey');
    console.log('proxy correctly rejected evmPrivateKey');
  } else {
    console.log('skipping custodial rejection checks (custodialProxy or non-proxy mode)');
  }

  console.log('gateway smoke passed');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});

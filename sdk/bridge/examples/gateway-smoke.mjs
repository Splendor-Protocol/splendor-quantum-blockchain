/**
 * Gateway smoke: start is assumed external; this hits /health and /v1/derive with a seed.
 * Usage: GATEWAY_URL=http://127.0.0.1:3090 node examples/gateway-smoke.mjs
 */
const base = (process.env.GATEWAY_URL || 'http://127.0.0.1:3090').replace(/\/$/, '');

async function main() {
  const health = await fetch(`${base}/health`).then((r) => r.json());
  console.log('health:', health);
  if (!health.ok) throw new Error('health not ok');

  const derive = await fetch(`${base}/v1/derive`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ seed: '01'.repeat(32) }),
  }).then(async (r) => {
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || r.statusText);
    return j;
  });
  console.log('derive:', derive.address);
  if (!String(derive.address).startsWith('SQ:')) throw new Error('bad address');

  // proxy must reject private key
  const rej = await fetch(`${base}/v1/derive`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ evmPrivateKey: '11'.repeat(32) }),
  });
  if (health.mode === 'proxy') {
    if (rej.ok) throw new Error('proxy should reject evmPrivateKey');
    console.log('proxy correctly rejected evmPrivateKey');
  }

  console.log('gateway smoke passed');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});

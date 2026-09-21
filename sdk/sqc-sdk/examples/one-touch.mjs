/**
 * One-touch connect demo — uses sqc.networks.json defaults.
 *
 *   node examples/one-touch.mjs
 *   SQC_NETWORK=local PQ_SEED=01… node examples/one-touch.mjs
 */
import {
  createSplendorClient,
  connectSplendor,
  listNetworks,
  PROTOCOL,
  SQDP_VERSION,
} from '../dist/index.js';

async function main() {
  console.log('SQDP', SQDP_VERSION, 'PROTOCOL.defaultNetwork=', PROTOCOL.defaultNetwork);
  console.log(
    'Networks:',
    listNetworks()
      .map((n) => `${n.id}→${n.rpcUrls[0]}`)
      .join(', '),
  );

  const networkId = process.env.SQC_NETWORK || PROTOCOL.defaultNetwork;
  const { provider, wallet, network } = createSplendorClient({ network: networkId });
  console.log('Client network:', network.id, network.chainId);
  console.log('RPC:', provider.rpcUrls);

  try {
    const status = await provider.getStatus();
    console.log('Status ok, chain_id=', status.chain_id);
  } catch (e) {
    console.warn('RPC not up (ok for offline demo):', e instanceof Error ? e.message : e);
  }

  const seed = process.env.PQ_SEED;
  if (seed) {
    const connected = await connectSplendor({
      network: networkId,
      connect: 'seed',
      seed,
    });
    console.log('Connected SQ:', connected.info?.address);
    connected.wallet.disconnect();
  } else {
    console.log('Set PQ_SEED=… to demo connect:seed (or connect:extension in browser)');
    wallet.disconnect();
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});

# dApp integration guide

Connect → fund → transfer → wait. Use a read-only `SqcProvider` until the user connects a signing wallet.

## Mental model

| Concept | What it is |
|---------|------------|
| EVM / MetaMask `0x…` | Only used to **derive** the PQ key via `personal_sign` |
| `SQ:` address | Real on-chain identity (32-byte SHA3-256 of Dilithium pubkey) |
| WASM signer | Same Go Dilithium (circl mode3) as the node |

**Security FAQ:** funds and nonces live on the **`SQ:`** address, not the MetaMask address. Never treat them as interchangeable.

## 1. Read-only provider (no WASM)

```ts
import { SqcProvider } from '@splendor/pq-bridge';

const provider = new SqcProvider({
  rpcUrls: ['https://rpc-a.example', 'https://rpc-b.example'],
  retries: 2,
});

const status = await provider.getStatus();
const bal = await provider.getBalance('SQ:…');
```

Share one `SqcProvider` across React components. Load WASM only after connect.

## 2. Connect (signing wallet)

```ts
import { SplendorPQBridge } from '@splendor/pq-bridge';
// browser: import from '@splendor/pq-bridge/browser'

const wallet = SplendorPQBridge.fromProvider(provider, {
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
});

wallet.on('connect', ({ address, evmAddress }) => {
  console.log('PQ', address, 'from', evmAddress);
});
wallet.on('accountsChanged', ({ address }) => {
  console.log('re-derived after MetaMask switch', address);
});
wallet.on('disconnect', () => console.log('disconnected'));

await wallet.connectFromMetaMask();
// Node / agents: await wallet.connectFromEvmPrivateKey(key)
// or: await wallet.connectFromSeed(seedHex)
```

MetaMask `accountsChanged` triggers a fresh `personal_sign` and re-derives the PQ wallet (opt out with `watchMetaMaskAccounts: false`).

## 3. Fund

Send SPLD to `wallet.getAddress()` (the `SQ:` address) from a faucet or another PQ wallet. MetaMask cannot send native EVM ETH onto this chain.

## 4. Transfer + confirmation

```ts
const { nonce } = await wallet.getNonce();
const res = await wallet.sendTransfer({
  to: 'SQ:…',
  amountSPLD: '0.01',
  waitForConfirmation: true,
  confirmTimeoutMs: 60_000,
});
// confirmation uses nonce advancement (node tx-by-hash index is not live yet)
```

## 5. QSol contract calls

```ts
import { encodeFunctionData, decodeArgUint256 } from '@splendor/pq-bridge';

await wallet.callContract({
  contract: 'SQ:…',
  function: 'transfer(sqaddress,uint256)',
  args: ['SQ:…', 1_000_000_000_000_000_000n],
});

// or build calldata yourself:
const data = encodeFunctionData('balanceOf(sqaddress)', [addr]);
```

Selector = `SHA3-256(signature)[0:4]` (same as Go `vm.SelectorFromSignature`).

## 6. Gateway (Docker one-pager)

```bash
cd sdk/bridge
# build wasm first: ./wasm/build.ps1
cd gateway
docker compose up --build
# → http://localhost:3090
```

| Env | Default | Notes |
|-----|---------|--------|
| `SQC_RPC` | `http://localhost:8080` | Chain node |
| `MODE` | `proxy` | Prefer for end users |
| `PORT` | `3090` | Gateway listen |

**Proxy mode rejects client `evmPrivateKey`** — credentials stay on the client. Use `service-wallet` only for your own automation keys.

## React hook sketch

See [`examples/useSplendorPQ.ts`](../examples/useSplendorPQ.ts).

## Checklist

- [ ] Serve `sqc_signer.wasm` + `wasm_exec.js` with correct MIME / CORS
- [ ] Display **SQ:** address after connect (not 0x)
- [ ] Handle `BridgeError` codes: `USER_REJECTED`, `TIMEOUT`, `TX_NOT_CONFIRMED`, `CHAIN_MISMATCH`
- [ ] Use `rpcUrls` failover in production
- [ ] Confirm via `waitForConfirmation` / nonce poll until tx index exists

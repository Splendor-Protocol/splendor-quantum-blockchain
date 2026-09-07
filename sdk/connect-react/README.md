# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/sqc-sdk`](../sqc-sdk) WASM ML-DSA-65 wallets — PQ crypto stays internal.

The first action in the modal is a **native ML-DSA-65 wallet** — create one from browser CSPRNG entropy, or import a 32-byte seed. MetaMask and other injected wallets are still there, last, under a **Legacy** heading: they derive the ML-DSA key from an ECDSA signature, so those accounts are only as strong as secp256k1.

`@splendor/pq-bridge` remains a deprecated shim; new apps should depend on `@splendor/sqc-sdk`.

## Production readiness

| Capability | Status |
|------------|--------|
| Vite / Next WASM helpers | Yes |
| Provider-hosted modals | Yes (`hostModals`, default) |
| SSR-safe hooks | Yes — stub until client |
| Native ML-DSA wallet (create / import) | Yes — first item in the modal |
| Auto connectors (EIP-6963) | Yes — `connectors="auto"`, listed after native |
| Soft network switch + reconnect | Yes |
| Idle lock syncs UI | Yes — bridge `lock` event → `status: 'locked'` |
| Inline / custom networks | Yes — `networks` / `networkConfig` / `registerNetwork` |
| Tx hooks + receipt confirmation | Yes (receipt when node indexes) |
| QRC-20 hooks (`useQRC20`, `useDeployToken`) | Yes |
| Module hooks | Yes — see matrix below |

### Hook coverage matrix

| Module / action | Hook |
|-----------------|------|
| Connect / account / balance / network | `useConnect`, `useAccount`, `useBalance`, `useNetwork` |
| Native SPLD transfer | `useSendTransfer` |
| QRC-20 read / transfer / approve | `useQRC20` |
| Deploy QRC-20 | `useDeployToken` |
| QRC-1155 | `useQRC1155`, `useDeployCollection` |
| DEX swap / liquidity | `useDex`, `useDeployPool` |
| Exchange | `useExchange`, `useDeployExchange` |
| Bridge claim | `useBridge`, `useDeployBridge` |
| Bytecode contract | `useContract`, `useDeployBytecode` (`useDeployContract` deprecated alias) |
| Bound module factory | `useModules()` |
| Raw module router | `useModuleCall` (power user) |
| X402 micropayment | `usePayX402` |

```tsx
const { swap, status, hash } = useDex(poolAddress);
await swap({ tokenIn: 0, amountIn: '100', minAmountOut: '1' });

const { claim } = useBridge(bridgeAddress);
await claim({ id, asset: 'spld', amount, recipient, evmTx, attestations });
```

**Out of scope:** WalletConnect v2, full EIP-1193 SQC provider for wagmi, CDN WASM, desktop/mobile wallet apps, browser extension (separate repo).

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/sqc-sdk react react-dom
```

```ts
// vite.config.ts
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
plugins: [react(), sqcWasmPlugin()],
```

```tsx
import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';

// connectors="auto" (default): native ML-DSA first, MetaMask/EIP-6963 after it.
// wasmSha256 pins the signer bundle — see "Pin the signer" below.
<SplendorConnectProvider network="testnet" wasmSha256="<digest>">
  <ConnectKit />
</SplendorConnectProvider>
```

`autoReconnect` only ever resumes an injected wallet — a native seed is typed in
by the user, so it is never persisted and never silently reconnected.

### Pin the signer

`wasmSha256` is the SHA-256 of the `@splendor/sqc-sdk` signer bundle. The
provider hashes what it loaded and refuses to run if the two differ.

Pin it in anything you ship. The bundle is the code that touches private keys,
and it is fetched at runtime — without a pin, whoever can answer for that asset
(a CDN, a compromised build step, a proxy on a hostile network) can substitute a
signer that exfiltrates seeds, and nothing in the page would look wrong.

Read the digest straight from the file you are serving:

```bash
shasum -a 256 node_modules/@splendor/sqc-sdk/wasm/sqc_signer.wasm
# or, if the package publishes under wasm/dist:
shasum -a 256 node_modules/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm
```

`allowUnpinnedWasm` skips the check. It is for local development against a
`file:`/dev-server build whose digest changes on every rebuild — nowhere else.
It has no legitimate use in a deployed app:

```tsx
// Local development ONLY — never in anything you deploy.
<SplendorConnectProvider network="local" allowUnpinnedWasm>
  <ConnectKit />
</SplendorConnectProvider>
```

Or inline:

```tsx
<SplendorConnectProvider
  networkConfig={{
    id: 'my-net',
    name: 'My Net',
    networkId: 'my-net',
    chainId: 'splendor-quantum-1',
    rpcUrls: ['https://rpc.example'],
    gatewayUrl: '',
    gatewayPort: 443,
    wasmUrl: '/sqc_signer.wasm',
    execUrl: '/wasm_exec.js',
    wasmSha256: '',
    execSha256: '',
    timeoutMs: 20000,
    retries: 3,
    explorerUrl: 'https://explorer.example',
  }}
/>
```

## Connect order

```tsx
import {
  SplendorConnectProvider,
  seedConnector,
  metamaskConnector,
} from '@splendor/connect-react';

<SplendorConnectProvider
  network="testnet"
  connectors={[
    seedConnector(),      // native ML-DSA-65 — the happy path
    metamaskConnector(),  // fallback, rendered under "Legacy / not post-quantum"
  ]}
/>
```

The modal groups by `keyOrigin`, not by array position: `native` connectors lead,
everything else falls to the legacy group. A connector that declares nothing is
grouped as legacy — an account is never shown as quantum-safe by omission.

**Native only** (no ECDSA path at all):

```tsx
<SplendorConnectProvider network="testnet" connectors={[seedConnector()]} />
```

Headless seed generation uses the same helper the modal does:

```ts
import { generateSeedHex } from '@splendor/connect-react/connectors';

const seed = generateSeedHex();   // 32 bytes of CSPRNG entropy, hex
await controller.connect('seed', { seed });
```

The seed **is** the account: it is held in component state only, never written
to storage, logged, or broadcast. A CLI `.sqwallet` file holds an encrypted
ML-DSA-65 private key rather than a seed, so it is not importable in the browser
— keep signing those with `sqc-wallet`.

## Subpath exports

| Import | Contents |
|--------|----------|
| `@splendor/connect-react` | UI + hooks + connectors |
| `@splendor/connect-react/hooks` | Provider + hooks |
| `@splendor/connect-react/connectors` | Connectors |
| `@splendor/connect-react/core` | Headless controller |
| `@splendor/connect-react/vite` | `sqcWasmPlugin` |
| `@splendor/connect-react/next` | `copySqcWasmToPublic` |
| `@splendor/connect-react/style.css` | Theme tokens |

## License

MIT

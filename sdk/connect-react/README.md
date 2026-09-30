# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/sqc-sdk`](../sqc-sdk) WASM ML-DSA-65 wallets — PQ crypto stays internal.

**Supported wallets only:** Splendor Quantum Wallet **browser extension** and **native in-page seed** (create/import). MetaMask, WalletConnect, and other EIP-1193 / ECDSA wallets are not offered.

`@splendor/pq-bridge` remains a deprecated shim; new apps should depend on `@splendor/sqc-sdk`.

**New here?** Read the [SDK guide](docs/GUIDE.md): setup, every hook and widget, chain modules
and `useModuleCall`, error handling, and the same flows in plain Node with `@splendor/sqc-sdk`,
each with a working example.

## Scaffold a dApp

```bash
npm exec create-splendor-dapp my-dapp
cd my-dapp && npm install && npm run dev
```

Ships Vite + React + `ConnectKit` + `TransferWidget` + `AccountCard` + WASM plugin.

## Feature Lab

`npm run lab` builds the SDKs, runs the unit tests, and starts `examples/basic-dapp`: one card per
feature against a live network (the one set by `VITE_SQC_NETWORK` in `.env.local`, testnet out of
the box), each with a checklist and a Done / Failed result. See
[examples/basic-dapp/README.md](examples/basic-dapp/README.md) for the step-by-step test guide.

## Production readiness

| Capability | Status |
|------------|--------|
| Vite / Next WASM helpers | Yes |
| Provider-hosted modals | Yes (`hostModals`, default) |
| SSR-safe hooks | Yes — stub until client |
| Native ML-DSA wallet (create / import) | Yes — first item in the modal |
| Auto connectors | Yes — `connectors="auto"` → extension + seed |
| Soft network switch + reconnect | Yes (`chainChanged` emitted) |
| Idle lock syncs UI | Yes — bridge `lock` event → `status: 'locked'` |
| Inline / custom networks | Yes — `networks` / `networkConfig` / `registerNetwork` |
| Tx hooks + receipt confirmation | Yes (receipt when node indexes) |
| Drop-in widgets | Yes — **connect + SPLD/QRC-20 only**: `TransferWidget`, `TokenPanel`, `DeployTokenWidget`, `AccountCard` |
| Optional `window.splendor` | Yes — `exposeInjectedProvider` |
| QRC-20 hooks (`useQRC20`, `useDeployToken`) | Yes |
| ML-DSA Sign-In | Yes — `useSignIn` / `createAuthMessage` |
| Tx toasts | Yes — `<TxToastHost />` + `useTxToast` |
| Wrong-network banner | Yes — `<WrongNetworkBanner expectedNetworkId="…" />` |
| Module hooks | Yes — QRC-1155 / bridge / contract / bound modules (no Swap widgets) |

### Widget scope

Connect-react ships **ConnectKit + native SPLD / QRC-20 widgets**. Bridge, QRC-1155, and bytecode contracts are available as **hooks** for app-built UI — not as packaged AppKit widgets. **DEX / Exchange are not public** until the node routes them (leftover adapters live under `@splendor/sqc-sdk/defi` only). WalletConnect remains deferred.

### Hook coverage matrix

| Module / action | Hook |
|-----------------|------|
| Connect / account / balance / network | `useConnect`, `useAccount`, `useBalance`, `useNetwork` |
| Native SPLD transfer | `useSendTransfer` |
| QRC-20 read / transfer / approve | `useQRC20` |
| Deploy QRC-20 | `useDeployToken` |
| QRC-1155 | `useQRC1155`, `useDeployCollection` |
| Bridge claim | `useBridge`, `useDeployBridge` |
| Bytecode contract | `useContract`, `useDeployBytecode` (`useDeployContract` deprecated alias) |
| Typed module clients (qrc20, qrc1155, bridge, contract) | `useModules()` |
| Routed modules without a typed client (verify, validators, qrc721) | `useModuleCall({ module, action, params })` |
| ML-DSA Sign-In | `useSignIn`, `createAuthMessage`, `checkAuthMessage`, `<SignInButton />` |
| Account / tx DTOs | `useAccountDto`, `toTxResultDto` |
| Send safety helpers | `reviewTransfer`, `normalizeSqAddress`, `compareDecimal`, `getRecentRecipients` |

x402 payments are not part of connect-react (removed in 2.0.0; the wallet extension cannot pay
them). Seed wallets and servers can still use `wallet.payX402` and the `x402` helpers from
`@splendor/sqc-sdk`.

`TransferWidget` and `TokenPanel` reject `0x` / malformed recipients, self-sends, and amounts above the
displayed balance, and show a review step before signing. Recent recipients (public `SQ:` addresses
only, max 5) are kept in `localStorage` under `sqc.connect.recentRecipients`.

### Widget props

| Widget | Prop | Description |
|--------|------|-------------|
| `TransferWidget` | `defaultTo` | Prefilled recipient (`SQ:` address) |
| | `defaultAmount` | Prefilled amount in SPLD |
| | `onSent({ to, amount, hash, confirmed, explorerUrl })` | Called after the wallet submits the transfer |
| | `onError(error)` | Called when signing or submitting fails, including a wallet rejection |
| `TokenPanel` | `tokenAddress` | QRC-20 address; the panel shows an address field when omitted |
| | `defaultAmount` | Prefilled transfer amount in whole tokens (default `"1"`) |
| | `onSent({ token, to, amount, hash })` | Called after the wallet submits the token transfer |
| | `onError(error)` | Called when signing or submitting fails |
| `DeployTokenWidget` | `defaultName`, `defaultSymbol`, `defaultSupply`, `decimals` | Form defaults; the supply is in whole tokens (default 1,000,000) |
| | `onDeployed({ address, hash })` | Called after the deploy confirms, with the new token address |
| | `onError(error)` | Called when signing or submitting the deploy fails |

All widgets also take `className`.

```tsx
<DeployTokenWidget onDeployed={({ address }) => setToken(address)} />
<TokenPanel tokenAddress={token} onSent={({ hash }) => toast(`Sent: ${hash}`)} />
```

`checkAuthMessage(message, { domain })` checks the Connect v2 layout, domain, expiry, and
future-dated `Issued At`. It does **not** verify the signature — the server must still verify it
and consume the nonce once.

```tsx
const { claim } = useBridge(bridgeAddress);
await claim({ id, asset: 'spld', amount, recipient, evmTx, attestations });
```

**Out of scope:** WalletConnect v2, Ethereum `eth_*` / wagmi adapters, CDN WASM, desktop/mobile wallet apps, browser extension (separate repo), public DEX/Exchange hooks.  
**In scope:** SQC `window.splendor` via `exposeInjectedProvider` (`splendor_*` methods).

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/sqc-sdk react react-dom
# or scaffold: npm exec create-splendor-dapp my-dapp
```

```ts
// vite.config.ts
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
plugins: [react(), sqcWasmPlugin()],
```

```tsx
import '@splendor/connect-react/style.css';
import {
  SplendorConnectProvider,
  ConnectKit,
  TransferWidget,
  AccountCard,
} from '@splendor/connect-react';

// connectors="auto" (default): Splendor extension + native seed.
// wasmSha256 pins the signer bundle — see "Pin the signer" below.
<SplendorConnectProvider network="testnet" wasmSha256="<digest>">
  <ConnectKit />
  <AccountCard />
  <TransferWidget />
</SplendorConnectProvider>
```

Widgets are also available as `@splendor/connect-react/widgets`.

`autoReconnect` defaults to **true**. It only resumes the **Splendor Quantum Wallet
extension** (via `sqc_accounts` / `splendor_accounts`). A native seed is typed each
session and is never persisted or silently reconnected. MetaMask / EIP-1193 /
WalletConnect are not supported — ConnectKit offers extension + native seed only.

The extension uses **`wallet_disconnect`** (origin-scoped) and **array** RPC params.
ConnectKit’s `ExtensionSigner` matches that wire format. The extension signs and
submits on the network picked in its own Settings, so the kit tracks it:
`useNetwork()` returns `walletChainId`, `walletNetwork` and `walletMismatch`
(updated live from the extension’s `chainChanged`), `<WrongNetworkBanner />` shows
“Your wallet is on …, but this app is on …” with a one-click “Use …” when no
`expectedNetworkId` is pinned, and `ExtensionSigner` refuses sends, module calls
and preflight with `CHAIN_MISMATCH` until both sides match — a tx never lands on a
chain the app is not watching.

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
  extensionConnector,
  seedConnector,
} from '@splendor/connect-react';

<SplendorConnectProvider
  network="testnet"
  connectors={[
    extensionConnector(), // Splendor Quantum Wallet extension
    seedConnector(),      // in-page native ML-DSA-65
  ]}
/>
```

Or use the default: `connectors="auto"` → `defaultBrowserConnectors()` (extension + seed).

**Seed only:**

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

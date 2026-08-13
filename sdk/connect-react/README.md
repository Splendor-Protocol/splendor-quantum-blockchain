# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/pq-bridge`](../bridge) WASM Dilithium wallets — PQ crypto stays internal.

## Production readiness (~9/10)

| Capability | Status |
|------------|--------|
| Vite plugin auto-copies WASM | Yes — `@splendor/connect-react/vite` |
| Next / non-Vite WASM copy | Yes — `npx sqc-copy-wasm` / `@splendor/connect-react/next` |
| Drop-in `<ConnectButton />` | Yes (includes modals + connect steps) |
| SSR-safe hooks | Yes — stub controller until client |
| Auto connectors (EIP-6963) | Yes — `connectors="auto"` (default) |
| Network switch (soft) | Yes — keeps last connector; reconnects after switch |
| Custom / override RPCs | Yes — `rpcUrls` / `gatewayUrl` / `customNetworks` / `registerNetwork` |
| Tx hooks + explorer URLs | Yes — transfer / call / deploy / preflight |
| Generic advanced connectors | Yes — `connectFields` |
| Multi-tab disconnect sync | Yes — BroadcastChannel + localStorage |

**Not in scope (keeps score under a perfect 10):** WalletConnect v2 protocol, full EIP-1193 SQC provider for wagmi, hosted CDN WASM.

**Done means:** install → Vite/Next WASM helper → `<ConnectButton />` → `useSendTransfer` — no manual WASM paths or PQ crypto knowledge.

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/pq-bridge react react-dom
```

### Vite (recommended)

```ts
// vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';

export default defineConfig({
  plugins: [react(), sqcWasmPlugin()],
});
```

### Next.js / Webpack

```bash
npx sqc-copy-wasm ./public
# or in a script:
# import { copySqcWasmToPublic } from '@splendor/connect-react/next';
# copySqcWasmToPublic('./public');
```

See [WASM_HOSTING.md](../bridge/docs/WASM_HOSTING.md).

```tsx
import '@splendor/connect-react/style.css';
import {
  SplendorConnectProvider,
  ConnectButton,
  useSendTransfer,
} from '@splendor/connect-react';

function App() {
  return (
    <SplendorConnectProvider
      network="local"
      // connectors="auto" is the default (MetaMask + EIP-6963 + seed)
      theme="splendor"
      autoReconnect
      allowUnpinnedWasm // local only; pin wasmSha256 on testnet/mainnet
      labels={{ connectTitle: 'Connect wallet' }}
    >
      <ConnectButton />
    </SplendorConnectProvider>
  );
}
```

### Real testnet / custom RPC

```tsx
<SplendorConnectProvider
  network="testnet"
  rpcUrls={['https://rpc.your-testnet.example']}
  gatewayUrl="https://gateway.your-testnet.example"
  // or:
  // customNetworks={[{ id: 'my-net', rpcUrls: [...], gatewayUrl: '...', explorerUrl: '...', ... }]}
/>
```

Optional fonts (opt-in):

```html
<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@500;700&family=IBM+Plex+Mono&display=swap" rel="stylesheet" />
```

## Subpath exports

| Import | Contents |
|--------|----------|
| `@splendor/connect-react` | Full UI + hooks + connectors |
| `@splendor/connect-react/hooks` | Provider + hooks |
| `@splendor/connect-react/connectors` | Connectors only |
| `@splendor/connect-react/core` | Headless controller (no UI) |
| `@splendor/connect-react/vite` | `sqcWasmPlugin` |
| `@splendor/connect-react/next` | `copySqcWasmToPublic` |
| `@splendor/connect-react/style.css` | Theme tokens |

## Hooks

| Hook | Use |
|------|-----|
| `useConnect()` | connect / disconnect / modal / `connectingStep` / labels |
| `useAccount()` | `SQ:` address, `isConnected` |
| `useBalance()` | SPLD balance |
| `useNetwork()` | network + soft `switchNetwork` + selectable list |
| `useWallet()` | raw bridge wallet (null on SSR stub) |
| `usePreflight()` | balance / fee preflight |
| `useSendTransfer()` | transfer + `hash` / `explorerUrl` / `confirmed` |
| `useCallContract()` | contract call status machine |
| `useDeployContract()` | deploy status machine |
| `useWaitForConfirmation()` | nonce confirmation helper |

## Connectors

```ts
// Default: connectors="auto"
metamaskConnector()
injectedConnector({ ethereumProvider })
seedConnector() // declares connectFields for Advanced UI
await discoverInjectedConnectors() // EIP-6963
await defaultBrowserConnectors()
```

Custom advanced connector: set `advanced: true` and `connectFields: [{ name, type, label }]`.

## Session & networks

- Last connector + last network in **localStorage**
- Soft `switchNetwork` disconnects without clearing last connector, then reconnects when previously connected
- `autoReconnect` on page load only if `authorized()` (e.g. `eth_accounts`)
- Cross-tab disconnect via `BroadcastChannel`
- Non-local networks default `sessionIdleMs` to 15 minutes
- Bridge: `registerNetwork` / `resolveNetwork(..., { rpcUrls, gatewayUrl })` / `txExplorerUrl`

## Tests

```bash
cd sdk/connect-react && npm test
```

## Headless / Node

Agents should use `@splendor/pq-bridge` (`connectSplendor`). Or `@splendor/connect-react/core` for the headless controller in browsers without UI.

## License

MIT

# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/pq-bridge`](../bridge) WASM Dilithium wallets — PQ crypto stays internal.

## Production readiness (~9/10 after hardening)

| Capability | Status |
|------------|--------|
| Vite / Next WASM helpers | Yes |
| Provider-hosted modals | Yes (`hostModals`, default) |
| SSR-safe hooks | Yes — stub until client |
| Auto connectors (EIP-6963) | Yes — `connectors="auto"` |
| Soft network switch + reconnect | Yes |
| Idle lock syncs UI | Yes — bridge `lock` event → `status: 'locked'` |
| Inline / custom networks | Yes — sync `customNetworks` / `networkConfig` |
| Tx hooks + receipt confirmation | Yes (receipt when node indexes) |

**Out of scope:** WalletConnect v2, full EIP-1193 SQC provider for wagmi, CDN WASM.

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/pq-bridge react react-dom
```

```ts
// vite.config.ts
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
plugins: [react(), sqcWasmPlugin()],
```

```tsx
import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';

<SplendorConnectProvider network="local" autoReconnect allowUnpinnedWasm>
  <ConnectKit />
</SplendorConnectProvider>
```

Testnet with real RPCs (gateway optional for SqRpc):

```tsx
<SplendorConnectProvider
  network="testnet"
  rpcUrls={['https://rpc.your-testnet.example']}
  wasmSha256="…"
/>
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

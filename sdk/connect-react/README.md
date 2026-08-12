# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/pq-bridge`](../bridge) WASM Dilithium wallets — PQ crypto stays internal.

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/pq-bridge react react-dom
```

**WASM:** `createSplendorClient` / the Provider already resolve `wasmUrl` / `execUrl` from `sqc.networks.json`. You still must **host** those files (usually `/sqc_signer.wasm` + `/wasm_exec.js`).

```bash
# from your dApp root (after bridge wasm is built)
node node_modules/@splendor/connect-react/scripts/copy-wasm-assets.mjs ./public
# or monorepo:
node ../scripts/copy-wasm-assets.mjs ./public
```

See [WASM_HOSTING.md](../bridge/docs/WASM_HOSTING.md).

```tsx
import '@splendor/connect-react/style.css';
import {
  SplendorConnectProvider,
  ConnectButton, // self-contained: includes connect + account modals
  metamaskConnector,
  seedConnector,
  useWallet,
} from '@splendor/connect-react';

function App() {
  return (
    <SplendorConnectProvider
      network="local"
      connectors={[metamaskConnector(), seedConnector()]}
      theme="splendor"
      autoReconnect // only if MetaMask already authorized (eth_accounts)
      allowUnpinnedWasm // local only; pin wasmSha256 on testnet/mainnet
    >
      <header style={{ display: 'flex', justifyContent: 'flex-end', padding: 16 }}>
        <ConnectButton />
      </header>
      <SendDemo />
    </SplendorConnectProvider>
  );
}

function SendDemo() {
  const { wallet } = useWallet();
  // After connect: wallet.sendTransfer({ to: 'SQ:…', amountSPLD: '0.01' })
  return null;
}
```

`ConnectKit` is an alias of self-contained `ConnectButton`. Do **not** also mount `ConnectModal` / `AccountModal` unless you pass `includeModals={false}` on the button.

## What dApps see vs what stays internal

| Visible | Hidden (managed by bridge) |
|---------|----------------------------|
| `SQ:` address, balance, network | Dilithium private key (WASM) |
| Connect / disconnect | Seed derivation from MetaMask sig |
| Transfer / call via `useWallet()` | Nonce, fee defaults, tx JSON hash |

MetaMask `0x…` is only a **derivation oracle**. Funds live on `SQ:`.

## API

### Provider

`SplendorConnectProvider` — wraps `createSplendorClient` + connector list.

Props: `network`, `connectors`, `theme` (`'splendor' | 'light' | 'dark' | ThemeOverrides`), `autoReconnect`, plus bridge options (`wasmUrl`, `wasmSha256`, …). Network config already supplies WASM URLs — override only when needed.

### Hooks

| Hook | Use |
|------|-----|
| `useConnect()` | `connect`, `disconnect`, `openModal`, `status`, `errorMessage` |
| `useAccount()` | `address`, `evmAddress`, `isConnected` |
| `useBalance()` | `balanceSPLD`, `refresh` |
| `useNetwork()` | `network`, `chainId` |
| `useWallet()` | `wallet` (`SplendorPQBridge`), `provider` |

### UI

- `ConnectButton` / `ConnectKit` — button + modals (default)
- Advanced compose: `<ConnectButton includeModals={false} />` + `<ConnectModal />` + `<AccountModal />`
- `NetworkBadge`

### Connectors

```ts
metamaskConnector()           // primary (+ authorized via eth_accounts)
injectedConnector({ ethereumProvider })
seedConnector()               // Advanced accordion in modal (64-hex seed)
```

Implement `Connector` (with optional `authorized()`) to add a future wallet without rewriting the modal.

### autoReconnect

When `autoReconnect` is true, the kit restores the last non-advanced connector **only if** `ready()` and `authorized()` (e.g. `eth_accounts` non-empty). It will not pop MetaMask for a cold session.

## Tests

```bash
cd sdk/connect-react && npm test
```

## Headless / Node

Agents and CLIs should keep using `@splendor/pq-bridge` (`connectSplendor`, seed / evmKey). This package is for browser React dApps.

## License

MIT

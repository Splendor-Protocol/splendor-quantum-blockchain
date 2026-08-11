# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/pq-bridge`](../bridge) WASM Dilithium wallets — PQ crypto stays internal.

## 5-minute integrate

```bash
npm install @splendor/connect-react @splendor/pq-bridge react react-dom
```

Serve WASM assets from `@splendor/pq-bridge` (`sqc_signer.wasm` + `wasm_exec.js`) — see [WASM_HOSTING.md](../bridge/docs/WASM_HOSTING.md).

```tsx
import '@splendor/connect-react/style.css';
import {
  SplendorConnectProvider,
  ConnectKit,
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
      allowUnpinnedWasm // local only; pin wasmSha256 on testnet/mainnet
    >
      <header style={{ display: 'flex', justifyContent: 'flex-end', padding: 16 }}>
        <ConnectKit />
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

Props: `network`, `connectors`, `theme` (`'splendor' | 'light' | 'dark' | ThemeOverrides`), `autoReconnect`, plus bridge options (`wasmUrl`, `wasmSha256`, …).

### Hooks

| Hook | Use |
|------|-----|
| `useConnect()` | `connect`, `disconnect`, `openModal`, `status`, `errorMessage` |
| `useAccount()` | `address`, `evmAddress`, `isConnected` |
| `useBalance()` | `balanceSPLD`, `refresh` |
| `useNetwork()` | `network`, `chainId` |
| `useWallet()` | `wallet` (`SplendorPQBridge`), `provider` |

### UI

- `ConnectKit` — button + connect modal + account modal
- Or compose: `ConnectButton`, `ConnectModal`, `AccountModal`, `NetworkBadge`

### Connectors

```ts
metamaskConnector()           // primary
injectedConnector({ ethereumProvider })
seedConnector()               // Advanced accordion in modal
```

Implement `Connector` to add a future wallet without rewriting the modal.

## Headless / Node

Agents and CLIs should keep using `@splendor/pq-bridge` (`connectSplendor`, seed / evmKey). This package is for browser React dApps.

## License

MIT

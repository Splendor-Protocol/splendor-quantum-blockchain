# `@splendor/connect-react`

AppKit-style **Connect UI kit** for Splendor Quantum Chain. Modular connectors unlock [`@splendor/sqc-sdk`](../sqc-sdk) WASM ML-DSA-65 wallets — PQ crypto stays internal.

**Supported wallets only:** Splendor Quantum Wallet **browser extension** and **native in-page seed** (create/import). MetaMask, WalletConnect, and other EIP-1193 / ECDSA wallets are not offered.

`@splendor/pq-bridge` remains a deprecated shim; new apps should depend on `@splendor/sqc-sdk`.

**New here?** Read the [SDK guide](docs/GUIDE.md): setup, every hook and widget, chain modules
and `useModuleCall`, error handling, and the same flows in plain Node with `@splendor/sqc-sdk`,
each with a working example.

## Scaffold a dApp

```bash
npm create splendor-dapp@latest
# non-interactive
npm create splendor-dapp@latest my-dapp -- --template wallet-dashboard --framework vite --network testnet
cd my-dapp && npm install && npm run dev
```

[`create-splendor-dapp`](../../packages/create-splendor-dapp) uses the same templates as the
dApp Playground (`wallet-dashboard`, `token-launchpad`, `tip-jar`, `members-area`, `nft-drop`), for
Vite or Next.js. It pins the connect-react and sqc-sdk versions it was released with. Generated
projects pin the signer digests in production builds and proxy the public RPCs on localhost.

The older `npm exec create-splendor-dapp` bin bundled in this package still works, but it only
creates the single Vite starter (`ConnectKit` + `TransferWidget` + `AccountCard` + WASM plugin).

## Widget manifest

`@splendor/connect-react/manifest` describes every drop-in widget and placeable part as data:
its props (type, format, defaults), status, the version it first shipped in, and its docs link.
It has no React import and no side effects, so build tools and servers can load it.

```ts
import { WIDGET_MANIFESTS, getWidgetManifest } from '@splendor/connect-react/manifest';

const transfer = getWidgetManifest('transfer');
transfer?.props.map((p) => `${p.name}: ${p.type}`); // ['title: string', 'hideTitle: boolean', 'defaultTo: string', …]
```

The dApp Playground builds its catalog, prop forms and generated code from this list, and
`@splendor/dapp-spec` validates saved specs against it. A new widget therefore appears in the
playground once it has an entry here. Ids are stable: never rename one; add a new id instead.
Bump `MANIFEST_SCHEMA_VERSION` only when the manifest shape changes incompatibly.

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
| QRC-721 | `useQRC721`, `useDeployQRC721` |
| Bridge claim | `useBridge`, `useDeployBridge` |
| Bytecode contract | `useContract`, `useDeployBytecode` (`useDeployContract` deprecated alias) |
| Typed module clients (qrc20, qrc1155, qrc721, bridge, contract) | `useModules()` |
| Routed modules without a typed client (verify, validators) | `useModuleCall({ module, action, params })` |
| ML-DSA Sign-In | `useSignIn`, `createAuthMessage`, `checkAuthMessage`, `<SignInButton />` |
| Account / tx DTOs | `useAccountDto`, `toTxResultDto` |
| Send safety helpers | `reviewTransfer`, `normalizeSqAddress`, `compareDecimal`, `getRecentRecipients`, `spendableAfterFee`, `exceedsBalanceWithFee` |
| Fee estimate / pending txs | `useEstimateFee`, `usePendingTransactions` |
| Controller access | `useConnectController`, `useConnectState`, `useNetworkSwitch` |

x402 payments are not part of connect-react (removed in 2.0.0; the wallet extension cannot pay
them). Seed wallets and servers can still use `wallet.payX402` and the `x402` helpers from
`@splendor/sqc-sdk`.

`TransferWidget` and `TokenPanel` reject `0x` / malformed recipients, self-sends, and amounts above the
displayed balance, and show a review step before signing. Recent recipients (public `SQ:` addresses
only, max 5) are kept in `localStorage` under `sqc.connect.recentRecipients`.

The review shows the network fee estimated for the real call, and the reviewed fee is the fee that
is sent. `TransferWidget`, `TokenPanel` and `DeployTokenWidget` keep Confirm disabled while the fee
is loading, when the node predicts a revert, and when the SPLD balance cannot cover the amount plus
the fee. Forms reset when the wallet or network changes, unless a send is pending.

### Widget props

`TransferWidget`, `TokenPanel`, `DeployTokenWidget` and `SignInButton` take the same base props
(`BaseWidgetProps`):

| Prop | Description |
|------|-------------|
| `className`, `style` | Added to the widget root (`.sqc-widget`) |
| `title`, `hideTitle` | Replace or hide the heading |
| `labels` | Partial text overrides for this widget (see [Text and labels](#text-and-labels)) |
| `onSuccess(result)` | Called once the action succeeds |
| `onError(error)` | Called when the action fails, including a wallet rejection |
| `onStatusChange(status)` | Called on every change of `'idle' \| 'pending' \| 'success' \| 'error'` after mount |
| `toast` | Push pending / success / error toasts when a `<TxToastHost>` is mounted (default `true`; `SignInButton` has no toasts) |

`AccountCard` has no action, so it takes only `className`, `style`, `title`, `hideTitle` and `labels`, plus its own props below.

| Widget | Prop | Description |
|--------|------|-------------|
| `TransferWidget` | `defaultTo`, `defaultAmount` | Prefilled recipient (`SQ:` address) and amount in SPLD (default `"0.01"`) |
| | `showFee` | Estimate and show the network fee on the review step (default `true`) |
| | `onSuccess({ to, amount, hash, confirmed, explorerUrl })` | Result after the wallet submits the transfer |
| `TokenPanel` | `tokenAddress` | QRC-20 address; the panel shows an address field when omitted |
| | `defaultAmount` | Prefilled amount in whole tokens (default `"1"`) |
| | `showFee` | Estimate and show the network fee on the review step (default `true`) |
| | `onSuccess({ token, to, amount, hash })` | Result after the wallet submits the token transfer |
| `DeployTokenWidget` | `defaultName`, `defaultSymbol`, `defaultSupply`, `decimals` | Form defaults; the supply is in whole tokens (default 1,000,000, 18 decimals) |
| | `showDecimals` | Show an editable Decimals field (0–18) |
| | `maxSupply` | Optional cap in whole tokens; must be at least the total supply |
| | `review` | Show a summary before signing (default `true`); with `false` the fee checks block the Deploy button |
| | `showFee` | Estimate and show the network fee (default `true`) |
| | `onSuccess({ address, hash, explorerUrl })` | Result after the deploy confirms |
| `AccountCard` | `showBalance`, `showNetwork`, `showExplorer`, `showDisconnect` | Toggle rows (all default `true`) |
| | `onCopy(address)`, `onDisconnect()` | Called after copy / disconnect |
| `SignInButton` | `domain`, `expectedDomain`, `statement`, `nonce`, `expiresInSec` | Challenge options (see [Sign-In](docs/GUIDE.md#8-sign-in-with-the-wallet)) |
| | `onSuccess(auth)` | The signed `{ message, signatureB64, publicKeyB64, address, messageDomain }` |
| | `label` | Button text (same as `labels.button`) |

`onSent` (transfer, token), `onDeployed` (deploy) and `onSignedIn` (sign-in) still work but are deprecated;
when both are passed, both fire.

```tsx
<DeployTokenWidget onSuccess={({ address }) => setToken(address)} />
<TokenPanel
  tokenAddress={token}
  labels={{ title: 'Send LAB' }}
  onStatusChange={(s) => setBusy(s === 'pending')}
  onSuccess={({ hash }) => track('sent', hash)}
/>
```

### Text and labels

Text is resolved in this order: English defaults, then provider `labels.widgets.common`, then
`labels.widgets.<kind>` (`transfer`, `token`, `deploy`, `account`, `signIn`), then the widget's `labels` prop.
Values like `'Balance: {balance} {symbol}'` are templates; keep the `{placeholders}`.

```tsx
<SplendorConnectProvider
  labels={{
    widgets: {
      common: { review: 'Vérifier', back: 'Retour' },
      transfer: { title: 'Envoyer SPLD' },
    },
  }}
>
```

The defaults are exported (`TRANSFER_LABELS`, `TOKEN_LABELS`, `DEPLOY_TOKEN_LABELS`, `ACCOUNT_LABELS`,
`SIGN_IN_LABELS`, `COMMON_LABELS`) so you can see every key.

### Compose your own widget

The widgets are built from exported parts. Use them to build a flow the stock widgets don't cover:

| Part | What it does |
|------|--------------|
| `WidgetShell` | Card root with title, `data-sqc-widget` / `data-status`, and the wallet gate |
| `WalletGate` | Renders children only when an unlocked wallet is connected; otherwise a Connect / Unlock prompt |
| `AddressInput`, `AmountInput` | Labeled fields with `SQ:` / amount validation, recent recipients and a Max button |
| `ReviewPanel` | To / amount / network summary with Back and Confirm |
| `FeeRow` | Network fee line for a `useEstimateFee` result; warns when the node predicts a revert |
| `PendingTxList` | Unconfirmed transactions on the current network, kept across reloads |
| `spendableAfterFee`, `exceedsBalanceWithFee` | Balance minus a fee for Max / validation, and the amount-plus-fee check |
| `TxResult` | Submitted / confirmed hash with copy and explorer link |
| `AddressDisplay`, `CopyButton` | Truncated address with copy and explorer link |
| `useTransferForm` | Headless state for recipient + amount + review (`errors`, `canReview`, `startReview`, `remember`, `reset`) |
| `useWidgetLabels`, `formatLabel` | Same label merge the widgets use |
| `useOptionalTxToast` | Toast API, or `null` when no `<TxToastHost>` is mounted |

```tsx
import { MIN_FEE, fromUnits } from '@splendor/sqc-sdk';
import {
  WidgetShell, AddressInput, AmountInput, FeeRow, TxResult,
  useAccount, useBalance, useEstimateFee, useSendTransfer, useTransferForm,
  spendableAfterFee, exceedsBalanceWithFee,
} from '@splendor/connect-react';

function PayInvoice({ to, amount }: { to: string; amount: string }) {
  const { address } = useAccount();
  const { balanceSPLD } = useBalance();
  const { sendTransfer, status, hash, confirmed, explorerUrl } = useSendTransfer();
  const spendable = spendableAfterFee(balanceSPLD, MIN_FEE);
  const form = useTransferForm({ balance: spendable, self: address, defaultTo: to, defaultAmount: amount });
  const fee = useEstimateFee(
    form.review.ok && address ? { from: address, to: form.review.to, value: fromUnits(form.review.amount, 18) } : null,
  );
  const tooMuch = form.review.ok && exceedsBalanceWithFee(form.review.amount, fee.fee, balanceSPLD);

  const pay = () => {
    if (form.review.ok) {
      void sendTransfer({ to: form.review.to, amountSPLD: form.review.amount, fee: fee.fee ?? undefined });
    }
  };

  return (
    <WidgetShell kind="invoice" title="Pay invoice" status={status}>
      <AddressInput label="To" value={form.to} onChange={form.setTo} self={address} />
      <AmountInput label="Amount (SPLD)" value={form.amount} onChange={form.setAmount} balance={spendable} />
      {form.review.ok && <FeeRow estimate={fee} />}
      <button
        className="sqc-btn sqc-btn--primary sqc-btn--block"
        disabled={!form.canReview || fee.loading || fee.reverted || tooMuch}
        onClick={pay}
      >
        Pay
      </button>
      {hash && <TxResult hash={hash} confirmed={confirmed} explorerUrl={explorerUrl} />}
    </WidgetShell>
  );
}
```

### Styling reference

Widgets use the theme tokens from `@splendor/connect-react/style.css`. Override them with CSS variables
or `theme` on the provider (`accent`, `bg`, `surface`, `text`, `textMuted`, `border`, `danger`,
`success`, `radius`, `widgetPadding`).

| Selector / attribute | Element |
|----------------------|---------|
| `.sqc-widget`, `.sqc-widget--<kind>` | Widget root (`kind` = `transfer`, `token`, `deploy`, `account`, `signin`) |
| `[data-sqc-widget="<kind>"]`, `[data-status]` | Same root; `data-status` is the current `WidgetStatus` |
| `.sqc-widget__title`, `.sqc-widget__hint`, `.sqc-widget__msg`, `.sqc-widget__actions` | Heading, hints, messages, button row |
| `.sqc-field`, `.sqc-field__row`, `.sqc-label`, `.sqc-input` | Form fields |
| `.sqc-wallet-gate`, `[data-sqc-gate="locked" \| "disconnected"]` | Connect / unlock prompt |
| `.sqc-tx-result`, `.sqc-tx-result__row` | Transaction result |
| `.sqc-address`, `.sqc-address__value`, `.sqc-copy` | Address display |

| Token | Default |
|-------|---------|
| `--sqc-widget-padding` | `1.1rem 1.25rem` |
| `--sqc-widget-gap` | `0.5rem` |
| `--sqc-widget-max-width` | `none` |
| `--sqc-success` | `#4ade80` (dark), `#15803d` (light) |

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
# or scaffold: npm create splendor-dapp@latest
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

## Security

**Approve every seed signature.** The extension prompts for each signature itself;
an in-page seed wallet has no such prompt, so the kit adds one. Before any send,
deploy, module call or message signature from a seed session it shows
`<SignApprovalModal />` (mounted by `ConnectButton` and `ModalHost`). If no approval
UI is mounted, signing fails closed with `USER_REJECTED` rather than signing
silently. Bring your own approval with `onBeforeSign`:

```tsx
<SplendorConnectProvider
  network="testnet"
  onBeforeSign={async (req) => confirm(`${req.summary}${req.to ? ` to ${req.to}` : ''}?`)}
>
```

`req` is `{ kind, method, summary, to?, amount?, message? }`. Return `true` to sign.
`onBeforeSign={() => true}` turns approval off — only for tests and kiosk apps.

**Pin the signer from the build.** `sqcWasmPlugin()` prints both digests, writes
`sqc-wasm.sha256.json` next to the copied files and defines
`import.meta.env.VITE_SQC_WASM_SHA256` / `VITE_SQC_EXEC_SHA256`:

```tsx
<SplendorConnectProvider
  network="testnet"
  wasmSha256={import.meta.env.VITE_SQC_WASM_SHA256}
  execSha256={import.meta.env.VITE_SQC_EXEC_SHA256}
>
```

**Content Security Policy.** The Go WASM runtime needs `'wasm-unsafe-eval'`
(older browsers: `'unsafe-eval'`). Keep everything else tight and forbid framing,
so another site cannot overlay your Approve button:

```
Content-Security-Policy:
  default-src 'self';
  script-src 'self' 'wasm-unsafe-eval';
  connect-src 'self' https://rpc.your-network.example;
  img-src 'self' data: https:;
  frame-ancestors 'none';
  base-uri 'none';
  object-src 'none'
```

**Verify sign-in on the server.** `useSignIn` / `<SignInButton>` produce a signed
message; a signature checked only in the browser proves nothing to your backend.
Issue the nonce on the server and check it with `verifySignIn` from
`@splendor/sqc-sdk/auth` — see “Server sign-in” in [`docs/GUIDE.md`](docs/GUIDE.md).

**Seed hygiene in the modal.** Seed inputs are uncontrolled (never in React state),
carry `autocomplete="new-password"` plus `data-private` / `data-sentry-mask` /
`data-hj-suppress` so replay and error tools mask them, and are wiped after a failed
import. A copied seed is cleared from the clipboard after 45 s if it is still there,
a revealed seed re-masks after 60 s, and a new seed must be confirmed by typing 4 of
its characters back.

**Links.** Explorer links only render for `https://` explorers (or `http://` on
loopback) and open with `rel="noopener noreferrer"`.

## Subpath exports

| Import | Contents |
|--------|----------|
| `@splendor/connect-react` | UI + hooks + connectors |
| `@splendor/connect-react/hooks` | Provider + hooks |
| `@splendor/connect-react/connectors` | Connectors |
| `@splendor/connect-react/core` | Headless controller |
| `@splendor/connect-react/vite` | `sqcWasmPlugin` |
| `@splendor/connect-react/next` | `copySqcWasmToPublic` |
| `@splendor/connect-react/testing` | `createMockConnectController`, `MockSplendorProvider`, `mockWallet`, `mockConnector` |
| `@splendor/connect-react/manifest` | `WIDGET_MANIFESTS`, `getWidgetManifest`, `MANIFEST_SCHEMA_VERSION` (data only, no React) |
| `@splendor/connect-react/style.css` | Theme tokens |

## License

MIT

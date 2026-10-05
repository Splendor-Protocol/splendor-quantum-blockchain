# Using the Splendor SDKs

This guide shows how an app talks to Splendor Quantum Chain through the two SDK packages, with
a working call for each feature.

| Package | Use it for |
|---------|------------|
| `@splendor/connect-react` | React apps: connect modal, hooks, drop-in widgets |
| `@splendor/sqc-sdk` | Everything underneath: wallets, RPC, modules. Use it directly in Node scripts, servers and bots |

Every example below runs in the [Feature Lab](../examples/basic-dapp/README.md) (`npm run lab`),
so you can try a call there before writing it into your app.

## 1. Install and set up

```bash
npm install @splendor/connect-react @splendor/sqc-sdk react react-dom
```

The signer is a WASM bundle. Vite needs the plugin to serve it:

```ts
// vite.config.ts
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';

export default { plugins: [react(), sqcWasmPlugin()] };
```

For Next.js, copy the bundle into `public/` with `copySqcWasmToPublic` from
`@splendor/connect-react/next`.

Wrap the app in the provider and add the connect button:

```tsx
import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';

export function Root() {
  return (
    <SplendorConnectProvider network="testnet" wasmSha256="<sha256 of sqc_signer.wasm>">
      <ConnectKit />
      <App />
    </SplendorConnectProvider>
  );
}
```

- `network` is `'mainnet'`, `'testnet'`, `'devnet'` or `'local'`. Pass `networkConfig={…}` for a
  custom RPC.
- `wasmSha256` pins the signer bundle. See "Pin the signer" in the [README](../README.md#pin-the-signer).
  Use `allowUnpinnedWasm` only for local development.
- `connectors` defaults to `"auto"`, which offers the Splendor Quantum Wallet extension and an
  in-page seed wallet.

## 2. Connect and read the account

```tsx
import { useAccount, useBalance, useConnect, useNetwork } from '@splendor/connect-react';

function AccountBar() {
  const { address, isConnected } = useAccount();
  const { balanceSPLD } = useBalance();          // polls every 12 s
  const { networkId, walletMismatch } = useNetwork();
  const { disconnect } = useConnect();

  if (!isConnected) return <p>Not connected</p>;
  return (
    <p>
      {address} holds {balanceSPLD} SPLD on {networkId}
      {walletMismatch && ' (the wallet is on a different network)'}
      <button onClick={() => void disconnect()}>Disconnect</button>
    </p>
  );
}
```

`ConnectKit` opens the modal for you. To connect from your own button, call
`useConnect().connect('extension')`, or `connect('seed', { seed })` for a seed wallet.

`useWallet()` returns the underlying `{ wallet, provider, ready }` when you need a raw SDK call.
`provider.getStatus()` returns node status such as the height and fork schedule.

## 3. Send SPLD

Check the fee and balance first, then send:

```tsx
import { usePreflight, useSendTransfer } from '@splendor/connect-react';

function Pay({ to }: { to: string }) {
  const { preflight, result } = usePreflight();
  const { sendTransfer, status, hash, explorerUrl, errorMessage } = useSendTransfer();

  const pay = async () => {
    const check = await preflight({ amountSPLD: '0.5' });
    if (!check.sufficient) return;               // balance does not cover amount + fee
    await sendTransfer({ to, amountSPLD: '0.5', waitForConfirmation: true });
  };

  return (
    <>
      <button onClick={() => void pay()} disabled={status === 'pending'}>Pay 0.5 SPLD</button>
      {result && <p>Fee {result.fee}, nonce {result.nonce}</p>}
      {hash && <a href={explorerUrl ?? '#'}>{hash}</a>}
      {errorMessage && <p>{errorMessage}</p>}
    </>
  );
}
```

`amountSPLD` takes whole SPLD as a decimal string (`'0.5'`). `amount` takes base units instead.
The SDK converts `amountSPLD` for you, so use one or the other.

Every transaction hook follows the same shape: an action function plus
`status` (`'idle' | 'pending' | 'success' | 'error'`), `hash`, `error`, `errorMessage` and `reset`.

## 4. QRC-20 tokens

```tsx
import { useDeployToken, useQRC20 } from '@splendor/connect-react';

const { deploy } = useDeployToken();
const { address } = await deploy({
  name: 'Lab Token',
  symbol: 'LAB',
  decimals: 18,
  totalSupply: '1000000000000000000000000',     // 1,000,000 tokens in the smallest unit
  waitForConfirmation: true,
});

const token = useQRC20(address);
token.balance;                                   // smallest-unit string, polled
token.tokenInfo;                                 // { name, symbol, decimals, … }
await token.transferHuman(to, '1.5');            // uses the token's decimals
await token.transfer(to, '1500000000000000000'); // same amount in the smallest unit
await token.approve(spender, '1000000000000000000');
```

`transfer`, `approve` and `mint` take smallest-unit strings. `transferHuman` takes the amount a
person would type. `fromUnits('1.5', 18)` from `@splendor/sqc-sdk` does the conversion yourself.

## 5. QRC-1155 collections

```tsx
import { useAccount, useDeployCollection, useQRC1155 } from '@splendor/connect-react';

const { address: me } = useAccount();
const { deploy } = useDeployCollection();
const { address } = await deploy({ name: 'Lab Collection', uriPrefix: 'https://example.com/meta/', waitForConfirmation: true });

const c = useQRC1155(address);
await c.mint(me!, '1', '10');                    // id 1, amount 10
await c.transfer(to, '1', '3');
await c.balanceOf(me!, '1');                     // "7"
await c.collectionInfo();
```

## 6. Bridge and bytecode contracts

```tsx
import { useBridge, useContract } from '@splendor/connect-react';

const bridge = useBridge(bridgeAddress);
await bridge.info();                             // EVM chain id, attestation threshold, …
await bridge.claimed(1);
await bridge.claim({ id, asset: 'spld', amount, recipient, evmTx, attestations });

const ct = useContract(contractAddress);
await ct.read('get()');                          // { return_data, reverted, … }
await ct.readUint256('get()');                   // bigint
await ct.call('set(uint256)', ['42'], { waitForConfirmation: true });
```

String arguments are base-10 (`'42'` is forty-two); write hex as `'0x2a'`. `SQ:` addresses, bigints
and safe integers are also accepted. Bare hex and free text throw instead of being guessed.

A claim needs signed attestations from the bridge relayer; pass its output straight to `claim`.
Deploy compiled bytecode with `useDeployBytecode().deploy(bytecode, { waitForConfirmation: true })`.

## 7. Chain modules

Tokens, collections, the bridge and a few other features are **modules**: native code inside the
node, not deployed contracts. Each module has actions (`transfer`, `mint`, `balance_of`, …) and
takes its parameters as JSON. Some modules are switched on by a fork, so they only exist on
networks where that fork is active.

| Module | Typed client | Fork |
|--------|--------------|------|
| `qrc20` | `useQRC20`, `useModules().qrc20(addr)` | always on |
| `qrc1155` | `useQRC1155`, `useModules().qrc1155(addr)` | `qrc1155_v1` |
| `bridge` | `useBridge`, `useModules().bridge(addr)` | `bridge_v1` |
| `validators` | none, use `useModuleCall` | `validators_v1` |
| `verify` | none, use `useModuleCall` | `verify_v1` |
| `qrc721` | none, use `useModuleCall` | `qrc721_v1` |

`useModules()` returns address-bound clients that already carry the wallet (`null` until
connected):

```tsx
import { useModules } from '@splendor/connect-react';

const m = useModules();
await m?.qrc20(tokenAddress).balanceOf(owner);
await m?.qrc20(tokenAddress).transfer(to, '1000000000000000000');
m?.registered;                                   // module ids known to the SDK
```

`useModuleCall()` sends any module action directly. Use it for modules without a typed client:

```tsx
import { useModuleCall } from '@splendor/connect-react';

const { call, status, hash, error } = useModuleCall();

await call({
  module: 'qrc20',
  action: 'transfer',
  params: { token: 'SQ:<token address>', to: 'SQ:<recipient>', amount: '1000000000000000000' },
  waitForConfirmation: true,
});
```

Rules for `params`:

- It must be a JSON object. `call` rejects a blank `module` or `action`, or params that are not an
  object, before the wallet is asked to sign.
- Use plain ASCII. The Splendor wallet extension cannot sign characters outside Latin-1 (for
  example the `…` in a copied placeholder), and the SDK refuses such calls with an
  `INVALID_PARAMS` error instead of letting the wallet crash.
- qrc20 amounts are smallest-unit decimal strings.

Parameter names per module:

| Module | Params |
|--------|--------|
| `qrc20` | `token`, `to`, `from`, `spender`, `new_owner`, `amount` |
| `qrc1155` | `collection`, `to`, `from`, `operator`, `approved`, `id`, `amount`, `uri`, `ids`, `amounts` |
| `bridge` (reads) | `bridge`, `id` |

Actions per module:

| Module | Reads | Writes |
|--------|-------|--------|
| `qrc20` | `balance_of`, `total_supply`, `allowance`, `token_info`, `permit_nonce`, `paused`, `owner` | `transfer`, `approve`, `mint`, `burn`, … |
| `qrc1155` | `balance_of`, `balance_of_batch`, `is_approved_for_all`, `uri`, `total_supply`, `token_info` | `mint`, `transfer`, … |
| `bridge` | `bridge_info`, `claim_status` | `claim` |
| `validators` | `validator`, `list`, `count`, `bond`, `approved` | `register`, `unregister`, `finalize_exit`, `approve`, `revoke` (the last two need `validators_admission_v1`) |
| `verify` | `proof`, `instance` | `deploy`, `register`, `attest`, `finalize`, `spend`, `set_operator`, `set_threshold`, `pause`, `unpause` |
| `qrc721` | `owner_of`, `balance_of`, `token_info` | `deploy`, `mint`, `transfer`, `burn` |

To see which modules a network has switched on, compare the fork heights with the current height:

```tsx
const { provider } = useWallet();
const status = await provider!.getStatus();
const forks = (status.forks ?? {}) as Record<string, number>;
const verifyLive = typeof forks.verify_v1 === 'number' && Number(status.height) >= forks.verify_v1;
```

## 8. Sign-In with the wallet

```tsx
import { useSignIn, checkAuthMessage, authCheckMessage } from '@splendor/connect-react';

const { signIn } = useSignIn();
const signed = await signIn({
  domain: window.location.origin,
  statement: 'Sign in to Example',
  nonce: serverNonce,
});
// POST signed.message, signed.signatureB64 and signed.publicKeyB64 to your server.

// On the server (or before sending), check the message layout:
const check = checkAuthMessage(signed.message, { domain: 'https://app.example.com' });
if (!check.ok) throw new Error(authCheckMessage(check.reason));
```

`checkAuthMessage` checks the message layout only (domain, age or expiry, and the nonce, address
and chain when you pass `expectedNonce`, `expectedAddress`, `expectedChainId`). Messages without an
expiry are rejected after `maxAgeSec` (default 600 s). `<SignInButton />` wraps the same flow in a
button.

### Server sign-in

A signature checked in the browser proves nothing to your backend. Issue the nonce on the server and
verify there with `@splendor/sqc-sdk/auth`. Express:

```ts
import express from 'express';
import { createMemoryNonceStore, verifySignIn } from '@splendor/sqc-sdk/auth';

const app = express();
app.use(express.json());
const nonces = createMemoryNonceStore({ ttlSec: 300 }); // one process; use Redis SET NX / GETDEL in production

app.get('/api/auth/nonce', (_req, res) => res.json({ nonce: nonces.issue() }));

app.post('/api/auth/verify', async (req, res) => {
  const { message, signatureB64, publicKeyB64, address } = req.body;
  const r = await verifySignIn({
    message, signatureB64, publicKeyB64, address,
    domain: 'https://app.example.com',
    chainId: 'splendor-quantum-1',
    nonceStore: nonces,
  });
  if (!r.ok) return res.status(401).json({ error: r.reason });
  req.session.address = r.address; // your session layer
  res.json({ address: r.address });
});
```

Next.js route handler (`app/api/auth/verify/route.ts`), with the store in a shared module:

```ts
import { verifySignIn } from '@splendor/sqc-sdk/auth';
import { nonces } from '@/lib/nonces';

export async function POST(req: Request) {
  const body = await req.json();
  const r = await verifySignIn({ ...body, domain: process.env.APP_ORIGIN!, nonceStore: nonces });
  return r.ok ? Response.json({ address: r.address }) : Response.json({ error: r.reason }, { status: 401 });
}
```

The client fetches the nonce, signs, and posts the result:

```tsx
const { nonce } = await (await fetch('/api/auth/nonce')).json();
const signed = await signIn({ domain: window.location.origin, statement: 'Sign in to Example', nonce });
await fetch('/api/auth/verify', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(signed),
});
```

`verifySignIn` returns `{ ok: true, address }` only when the message is for this domain and time
window, the address derives from the public key (`SQ:` + SHA3-256), the ML-DSA-65 signature is
valid, and the nonce was issued by your store and not used before. The nonce is burned only after
the signature passes. Failure reasons: `bad-address`, `domain`, `expired`, `too-old`,
`not-yet-valid`, `address`, `chain`, `missing-nonce`, `key-mismatch`, `bad-signature`,
`nonce-used`.

## 9. Drop-in widgets

```tsx
import { AccountCard, DeployTokenWidget, TokenPanel, TransferWidget } from '@splendor/connect-react';

<AccountCard />
<TransferWidget
  defaultTo="SQ:…"
  defaultAmount="0.1"
  onSuccess={({ to, amount, hash, explorerUrl }) => console.log('sent', amount, 'to', to, hash)}
  onError={(err) => console.error(err.message)}
/>
<TokenPanel
  tokenAddress={tokenAddress}
  defaultAmount="1"
  onSuccess={({ token, to, amount, hash }) => console.log(token, to, amount, hash)}
  onError={(err) => console.error(err.message)}
/>
<DeployTokenWidget
  defaultName="Lab Token"
  defaultSymbol="LAB"
  onSuccess={({ address }) => setTokenAddress(address)}
  onError={(err) => console.error(err.message)}
/>
```

`TransferWidget` and `TokenPanel` validate the recipient, block self-sends and amounts above the
balance, and show a review step before the wallet is asked to sign. The review lists From, To,
the amount, the token contract and the network fee; `TransferWidget` also disables Confirm when the
node predicts the transfer will revert. `DeployTokenWidget` has the same review step. Pass
`showFee={false}` to hide the fee row.

All five widgets share one contract: `className`, `style`, `title`, `hideTitle`, `labels`, `onSuccess`,
`onError`, `onStatusChange` and `toast`. A widget only renders its form when an unlocked wallet is
connected; otherwise it shows a Connect or Unlock button. The older `onSent`, `onDeployed` and
`onSignedIn` callbacks still fire but are deprecated.
The full prop list is in the [README](../README.md#widget-props).

### Customize text and styling

Pass `labels` to one widget, or set `labels.widgets` on the provider to change every widget at once:

```tsx
<SplendorConnectProvider labels={{ widgets: { common: { review: 'Check' }, transfer: { title: 'Pay' } } }}>
  …
  <TransferWidget labels={{ amountLabel: 'How much?' }} hideTitle />
</SplendorConnectProvider>
```

Restyle with CSS variables (`--sqc-accent`, `--sqc-widget-padding`, `--sqc-success`, …) or target the
stable hooks: `.sqc-widget--transfer`, `[data-sqc-widget="token"]`, `[data-status="pending"]`.

```css
.sqc-widget[data-status='pending'] { opacity: 0.85; }
.sqc-widget--transfer { --sqc-widget-max-width: 420px; }
```

### Build your own widget

The parts the widgets are built from are exported: `WidgetShell`, `WalletGate`, `AddressInput`,
`AmountInput`, `ReviewPanel`, `TxResult`, `AddressDisplay` and the headless `useTransferForm`.

```tsx
const { address } = useAccount();
const { balanceSPLD } = useBalance();
const { sendTransfer, status, hash, confirmed, explorerUrl } = useSendTransfer();
const form = useTransferForm({ balance: balanceSPLD, self: address });

<WidgetShell kind="tip" title="Tip the author" status={status}>
  <AddressInput label="To" value={form.to} onChange={form.setTo} self={address} />
  <AmountInput label="Amount" value={form.amount} onChange={form.setAmount} balance={balanceSPLD} />
  <button
    className="sqc-btn sqc-btn--primary sqc-btn--block"
    disabled={!form.canReview}
    onClick={() => form.review.ok && sendTransfer({ to: form.review.to, amountSPLD: form.review.amount })}
  >
    Send tip
  </button>
  {hash && <TxResult hash={hash} confirmed={confirmed} explorerUrl={explorerUrl} />}
</WidgetShell>
```

The lab's **Build your own** card runs this pattern.

## 10. Toasts and the wrong-network banner

```tsx
import { TxToastHost, useTxToast, WrongNetworkBanner } from '@splendor/connect-react';

<TxToastHost autoDismissMs={8000}>
  <WrongNetworkBanner />
  <App />
</TxToastHost>

function SendButton() {
  const toast = useTxToast();
  const { sendTransfer } = useSendTransfer();
  const onClick = async () => {
    const id = toast.push({ title: 'Sending', status: 'pending', hash: null, explorerUrl: null, errorMessage: null });
    const res = await sendTransfer({ to, amountSPLD: '1' });
    toast.dismiss(id);
    toast.push({ title: 'Sent', status: 'success', hash: res.hash ?? null, explorerUrl: res.explorerUrl ?? null, errorMessage: null });
  };
  return <button onClick={() => void onClick()}>Send</button>;
}
```

`WrongNetworkBanner` appears when the extension is on a different network than the app and offers
a one-click switch.

## 11. Read without a wallet

Landing pages, dashboards and token pages can show chain data before anyone connects. These hooks
use the connected wallet's connection when there is one and the app's network otherwise:

```tsx
import { useReadContract, useTokenBalance, useTokenInfo } from '@splendor/connect-react';

const info = useTokenInfo(tokenAddress);                     // { data: { name, symbol, decimals, totalSupply } }
const bal = useTokenBalance(tokenAddress, 'SQ:<holder>');    // { data: raw, formatted: '12.5', decimals }
const supply = useReadContract(counter, 'get()', [], { pollMs: 10_000 });

<p>{info.data?.symbol}: {bal.formatted ?? '…'}</p>
```

Each returns `{ data, loading, error, refresh }`. Results for an old address are dropped when the
inputs change. `useQRC20`, `useQRC1155` and `useContract().read` also work while disconnected.

## 12. Show the fee before signing

```tsx
import { FeeRow, useEstimateFee } from '@splendor/connect-react';

const fee = useEstimateFee(to && address ? { from: address, to, value: fromUnits(amount, 18) } : null);

<FeeRow estimate={fee} />
<button disabled={fee.reverted || fee.loading}>Confirm</button>
```

`useEstimateFee` debounces (300 ms by default) and returns `fee` (atto), `feeSpld`, `feeIsFloor`
(the node returned the minimum fee), `gas`, `reverted` and `revertReason`. Pass `null` to skip.

## 13. Pending transactions across reloads

Sends from the hooks and widgets that are not confirmed yet are kept in `localStorage`
(`sqc.connect.pendingTx`, at most 20, dropped after 24 h). After a reload,
`usePendingTransactions` resumes polling for receipts and pushes a toast when each one lands:

```tsx
import { PendingTxList, usePendingTransactions } from '@splendor/connect-react';

<PendingTxList emptyText="Nothing pending" />

// Or build your own list:
const { pending, all, remove, clear } = usePendingTransactions({ pollMs: 5000 });
```

`pending` holds the current network's entries; `all` holds every network's. Mount the hook (or
`PendingTxList`) once so receipts are not polled twice.

## 14. Approve seed signatures

The extension shows its own prompt for every signature. An in-page seed wallet does not, so the kit
shows `<SignApprovalModal />` before each send, deploy, module call and message signature from a
seed session. `ConnectButton` and `ModalHost` mount it. If it is not mounted, signing fails with
`USER_REJECTED` instead of happening silently.

Use your own approval UI with `onBeforeSign`:

```tsx
<SplendorConnectProvider
  network="testnet"
  onBeforeSign={async (req) => myConfirmDialog(req.summary, req.to, req.amount, req.message)}
>
```

`req.kind` is `'transfer'`, `'call'`, `'deploy'`, `'module'`, `'message'` or `'sign'`. Return
`true` to sign; returning `false` throws `USER_REJECTED` to the caller. Use `() => true` only in
tests and kiosk apps.

## 15. Test your app with mocks

`@splendor/connect-react/testing` runs hooks and widgets without WASM or a node:

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { TransferWidget } from '@splendor/connect-react';
import { createMockConnectController, MockSplendorProvider, mockWallet } from '@splendor/connect-react/testing';

it('sends 1 SPLD', async () => {
  const ctrl = createMockConnectController({ balance: '3' });
  render(
    <MockSplendorProvider controller={ctrl}>
      <TransferWidget defaultTo="SQ:<recipient>" defaultAmount="1" />
    </MockSplendorProvider>,
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Review' }));
  fireEvent.click(screen.getByRole('button', { name: /Confirm send/ }));
  await waitFor(() => expect(ctrl.wallet.calls.map((c) => c.method)).toContain('sendTransfer'));
});

// Error paths: make a method reject.
const wallet = mockWallet({ fail: { sendTransfer: new Error('User rejected transaction') } });
const ctrl = createMockConnectController({ wallet });
```

`ctrl.setState({...})` drives any state (locked, wrong network, error). `mockConnector` connects
instantly or fails with `fail`. `mockWallet` takes `estimate`, `moduleCall` and balance options for
fee rows and token reads.

## 16. Errors

SDK failures are `BridgeError`s with a `code` and, usually, a `userHint` written for end users.
Hooks expose `errorMessage`, which already picks the best text; for raw errors use
`connectErrorMessage(err)`:

```ts
import { connectErrorMessage } from '@splendor/connect-react';
import { isBridgeError } from '@splendor/sqc-sdk';

try {
  await sendTransfer({ to, amountSPLD: '1' });
} catch (err) {
  if (isBridgeError(err) && err.code === 'USER_REJECTED') return;   // user closed the wallet prompt
  showError(connectErrorMessage(err));
}
```

| Code | Meaning |
|------|---------|
| `USER_REJECTED` | The user declined in the wallet |
| `NOT_CONNECTED` | No wallet connected, or the wallet is locked |
| `CHAIN_MISMATCH` | The wallet and the app are on different networks; nothing was sent |
| `INVALID_ADDRESS` / `INVALID_AMOUNT` / `INVALID_FEE` | An input failed validation before signing |
| `INVALID_PARAMS` | Module-call params the wallet cannot sign (non-ASCII text) |
| `RPC` / `NETWORK` / `RPC_UNAVAILABLE` | The node returned an error or could not be reached |
| `TX_FAILED` | The node rejected or reverted the transaction |
| `TIMEOUT` / `TX_NOT_CONFIRMED` | The transaction was not confirmed in time |

Use `usePreflight` before a send to catch a balance that does not cover the amount plus the fee
(`result.sufficient === false`).

## 17. Without React: `@splendor/sqc-sdk`

Scripts, servers and bots use the core SDK directly. `createSession` connects once and returns
clients with the wallet already bound:

```ts
import { createSession, fromUnits } from '@splendor/sqc-sdk';

const s = await createSession({ network: 'testnet', mode: 'seed', seed: process.env.SEED });

await s.getBalance();                                   // { balance, balance_spld, … }
await s.sendSPLD('SQ:<recipient>', '0.01');

const { address: token } = await s.deployToken({
  name: 'Bot Token', symbol: 'BOT', decimals: 18, totalSupply: fromUnits('1000000', 18),
});
await s.token(token).transferHuman('SQ:<recipient>', '1.5');
await s.collection(collectionAddr).mint('SQ:<recipient>', '1', '10');
await s.bridge(bridgeAddr).info();

await s.wallet.sendModuleCall({
  module: 'qrc20',
  action: 'transfer',
  params: { token, to: 'SQ:<recipient>', amount: fromUnits('2', 18) },
  waitForConfirmation: true,
});
```

The same `sendModuleCall`, `preflight`, `sendTransfer` and `signMessage` methods are on
`useWallet().wallet` inside React. See the [sqc-sdk README](../../sqc-sdk/README.md) for the
lower-level `SqcProvider`, `Wallet` and gateway APIs.

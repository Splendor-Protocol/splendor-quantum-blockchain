# Error catalog

Every SDK failure is a `BridgeError` (alias `WalletError`) with a string `code`, a technical
`message`, and usually a `userHint` that is safe to show in UI. Branch on `code`, show
`userHint` (or `connectErrorMessage(err)` in `@splendor/connect-react`), and log `message`.

```ts
import { isBridgeError } from '@splendor/sqc-sdk';

try {
  await wallet.sendTransfer({ to, amountSPLD: '1', waitForConfirmation: true });
} catch (e) {
  if (isBridgeError(e) && e.code === 'USER_REJECTED') return;
  showToast(isBridgeError(e) ? (e.userHint ?? 'Something went wrong') : 'Something went wrong');
}
```

"Retry" below means: is it reasonable to try the same request again without the user changing anything?

| Code | Meaning | Retry |
|------|---------|-------|
| `NOT_CONNECTED` | No wallet session, or the extension is locked. | After the user connects / unlocks. |
| `USER_REJECTED` | The user declined in the wallet or the seed approval modal. | No. Do not re-prompt automatically. |
| `NO_PROVIDER` | The Splendor Quantum Wallet extension is not installed. | After install. |
| `BUSY` | Another connect, or another transaction from the same hook, is still in flight. | Yes, after the first one finishes. |
| `RPC` | The node answered with an error (4xx, malformed body). | Usually no; `message` says why. |
| `RPC_UNAVAILABLE` | Every configured RPC endpoint failed, or confirmation polling hit repeated transport errors. | Yes, with backoff. |
| `NETWORK` | Fetch or stream failed before a response. | Yes, with backoff. |
| `TIMEOUT` | A request exceeded `timeoutMs`. | Yes. |
| `WASM` | The ML-DSA signer failed to load or sign (bad pin, wrong MIME, CSP). | No. Fix hosting; see `WASM_HOSTING.md`. |
| `INVALID_ADDRESS` | Not a canonical `SQ:` + 64 hex address. | No. User input. |
| `INVALID_AMOUNT` | Amount is not a valid non-negative integer / decimal for the token. | No. User input. |
| `INVALID_FEE` | Fee below `MIN_FEE`, above `maxFee`, or rejected by the node as too low for current load (`fee too low`). | Re-estimate the fee, review again, then send. |
| `INVALID_PARAMS` | Bad call parameters (token decimals, name/symbol, module params). | No. Fix the call. |
| `CHAIN_MISMATCH` | Wallet, signed tx, or RPC reports a different `chain_id` than the app expects. | No. Switch network. |
| `TX_FAILED` | The transaction was included and its receipt reports failure. | No. Funds for the fee are spent. |
| `TX_NOT_CONFIRMED` | No successful receipt before `confirmTimeoutMs`. The tx may still land. | Check the hash later; do not resend blindly (it can double-spend the intent). |
| `UNKNOWN` | Anything not mapped above. | Treat as `RPC`. |

## Confirmation

Send paths that wait for confirmation require a successful receipt. A nonce advance alone is
not accepted, because another transaction from the same account also advances the nonce. On a
node without a receipt index, pass `allowNonceConfirmation: true` and accept that caveat.

## Request correlation

`new Connection({ ..., requestId: true })` (or `RpcOptions.requestId`) sends a random
`X-Request-Id` header on every RPC call; pass a function to supply your own ids. It is off by
default. Browser apps need a node whose CORS allow-list includes `X-Request-Id` (nodes from this
repo do).

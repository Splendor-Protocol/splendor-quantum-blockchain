# Feature Lab (basic dApp example)

A Vite + React page that runs every `@splendor/connect-react` feature against a live Splendor
network. Each feature has a numbered card with a "What to check" list, a live Done / Failed result,
and a **Verified** box that is saved in your browser, so you can work through them one at a time.

To write the same calls in your own app, open the **SDK guide** tab at the top of the lab. It
renders [docs/GUIDE.md](../../docs/GUIDE.md) with a section menu, and each card has a
"Read … in the SDK guide" link that jumps to the matching section. **Back to the tests** returns
to that card with your inputs intact.

## Run it

From the repo root (or `sdk/connect-react`, same script):

```bash
npm run lab          # build both SDKs, run both unit-test suites, typecheck, start the lab
npm run lab:testnet  # start on testnet     (also lab:devnet, lab:mainnet)
npm run lab:fast     # skip the unit tests, rebuild and start
npm run lab:check    # build + tests + typecheck only, no server
```

The first run creates `examples/basic-dapp/.env.local` from `.env.example` with every network's
RPC, chain id and explorer filled in, so there is nothing to set up. If port 5173 is taken, the
next free port is used and printed.

> **Start network** comes from `VITE_SQC_NETWORK` in `.env.local` (mainnet when unset). Test on
> **testnet** or devnet, where coins are free. On mainnet every transfer, deploy and mint spends
> real SPLD and cannot be undone.
>
> **Using the Splendor Quantum Wallet extension?** Select the same network in the extension as in
> the lab. If they differ, the lab shows "Your wallet is on …, but this app is on …" and refuses
> to send until they match.
>
> **"Network unavailable" everywhere?** A red banner under the header names the cause: the lab
> server has stopped (run `npm run lab` again), the node is not responding, or the chain id in
> `.env.local` is wrong.

Open http://localhost:5173.

| Command | What it does |
|---------|--------------|
| `npm run lab` | Build both SDKs, run 290 + 277 unit tests, typecheck, start the dev server |
| `npm run lab -- --skip-tests` | Same without the unit tests (faster restart) |
| `npm run lab -- --skip-build` | Only start the dev server (SDKs already built) |
| `npm run lab -- --network=devnet` | Use devnet (free faucet coins); also `testnet`, `local` |
| `npm run lab -- --network=testnet --rpc=<url> --chain-id=<id> --explorer=<url>` | Override that network's RPC, chain id or explorer |
| `npm run lab -- --port=5180` | Use a different port |
| `npm run lab:check` | Build + tests + typecheck, no server (for CI) |
| `npm run lab -- --preview --wasm-sha256=<digest>` | Production build with the signer pinned, then serve it |

The script installs missing `node_modules` on the first run. It needs a built `sqc_signer.wasm`
(`sdk/sqc-sdk/wasm/dist`); if that is missing, build it once with Go:
`cd sdk/sqc-sdk && npm run build:wasm`.

### Networks and configuration

| Network | RPC | Chain id | Explorer |
|---------|-----|----------|----------|
| `mainnet` | https://quantum-rpc.splendor.org | `splendor-quantum-1` | https://quantum-explorer.splendor.org |
| `testnet` | https://quantum-rpc-testnet.splendor.org | `splendor-quantum-testnet-1` | https://quantum-testnet-explorer.splendor.org |
| `devnet` | https://quantum-rpc-devnet.splendor.org | `splendor-quantum-devnet-1` | https://quantum-devnet-explorer.splendor.org |
| `local` | http://127.0.0.1:8080 | `splendor-quantum-1` | — |

These are the SDK defaults. To change one without touching code, edit
`VITE_SQC_<NETWORK>_RPC_URL`, `VITE_SQC_<NETWORK>_CHAIN_ID` or `VITE_SQC_<NETWORK>_EXPLORER_URL` in
`.env.local` (gitignored), or pass `--rpc` / `--chain-id` / `--explorer` to `npm run lab` (they
apply to the `--network` you picked). `VITE_SQC_NETWORK` picks the start network. The RPC proxy,
the network badge, explorer links and the wrong-network checks all use the overridden values.
Restart the dev server after editing `.env.local`.

### Why there is an RPC proxy

The public Splendor RPCs do not send CORS headers for `localhost`, so a page on `localhost` cannot
call them directly ("Failed to fetch"). The lab's `vite.config.ts` proxies `/rpc/<network>` to the
real RPC, and `src/rpcProxy.ts` points the SDK at that proxy when the page runs on `localhost`.
Your own local dApp needs the same setup. Set `VITE_SQC_DIRECT_RPC=1` to skip the proxy when the
page is served from an origin the RPC allows.

## Test guide

Work from top to bottom. Each card lists what to check; tick **Verified** when all of it holds.
The bar at the top counts verified cards.

### 1. No wallet needed

| Card | How to test |
|------|-------------|
| 1 Helpers | Edit the inputs; the results update as you type. Try `0x…`, a mixed-case `SQ:` address, `1.50` vs `1.5`, an SVG icon URL, and change the auth message's domain or `Issued At`. |
| 2 Toasts | Click Pending, Success, Error and Clear. |
| 4 Network | Get node status returns the chain id. Pick another network in the badge, then use the banner to switch back. |

### 2. Connect a wallet

Card 3. Click **Connect**:

- **Native seed**: Create a new wallet, Reveal, **copy the seed**, tick "I saved this seed in a
  safe place", then Unlock. Keep the seed if you plan to fund this account: a reload signs a seed
  session out on purpose.
- **Extension** (if the Splendor Quantum Wallet extension is installed): pick it and approve.
  Reload the page and it reconnects by itself. Lock it in the extension to see `status: locked`.

Card 3 then shows your `SQ:` address with `postQuantumSecure: true`. These checks work with an
empty wallet: card 5 **Preflight** (`sufficient: false`), card 7 **Read nonce**, card 8 **Sign in**
and the other-site challenge, the address, self and over-balance errors in cards 6 and 10, and the
module list and **Check availability** in card 13.

### 3. Fund the account

Copy your address (card 5, **Copy**) and send it a small amount of SPLD. On testnet or devnet use
https://quantum-faucet.splendor.org; mainnet has no faucet, so send from a wallet you already hold.
Click **Refresh** in card 5 until the balance appears. To come back later with the same account,
choose **Import an existing seed** and paste your saved seed.

### 4. Transactions (need SPLD)

| Card | How to test |
|------|-------------|
| 5 Account | The explorer link opens `…/address/<your SQ:>`; Preflight 0.01 returns `sufficient: true`. |
| 6 SPLD transfer | Send 0.001 to another `SQ:` address: Review, then Confirm send, then "Confirmed: <hash>". The recipient then shows under "Recent:", and the panel under the widget shows the `onSuccess` result. |
| 7 Confirmation | Read nonce, click Wait for next tx, and send from card 6 within 60 seconds. The wait finishes when the nonce moves. |
| 9 QRC-20 | Deploy "Lab Token" (the widget title and button text come from `labels`), paste its address into the token panel (balance 1000 LAB), then transfer with the review step. |
| 10 Build your own | A tip jar made from the exported parts (`WalletGate`, `useTransferForm`, `AddressInput`, `AmountInput`, `ReviewPanel`, `TxResult`). Same checks as card 6. |
| 11 QRC-1155 | Deploy a collection (the address fills in), Collection info, Mint to me, Balance of me returns 10, Transfer 3. |
| 13 Chain modules | Check availability shows which modules are live on this network. Pick the **qrc20 transfer** preset, paste a real token and recipient, and send to get a hash. Amounts are in the token's smallest unit. An empty `token` / `to`, broken JSON, or a blank Module / Action is caught locally and nothing is sent. |

### 5. Needs extra inputs

| Card | Input |
|------|-------|
| 12 Contract | Compiled SQC-VM bytecode hex (e.g. from the QSol compiler in `/compiler`). Deploy, then `read("get()")`, then `call("set(uint256)", ["42"])`. |
| 14 Bridge | The `SQ:` address of a deployed bridge, from the relayer or deploy logs. Read-only: bridge info and claim status. Claims need relayer attestations. |

### What chain modules are

Tokens, the bridge and the other native features run as modules inside the node rather than as
deployed bytecode. A module call is a signed transaction sent to the node's module router with
`{ module, action, params }`.

| Module | Client in connect-react | Switched on by |
|--------|------------------------|----------------|
| `qrc20` | `useQRC20`, `useDeployToken`, `useModules().qrc20` | always on (`qrc20_v2` adds permit/allowance) |
| `qrc1155` | `useQRC1155`, `useDeployCollection` | `qrc1155_v1` |
| `bridge` | `useBridge` | `bridge_v1` |
| `validators` | `useModuleCall` | `validators_v1` |
| `verify` | `useModuleCall` | `verify_v1` |
| `qrc721` | `useQRC721`, `useDeployQRC721` | `qrc721_v1` |

`useModules().contract` is the client for SQC-VM bytecode contracts; it is not a module. A module
only works on a network where its fork is active; card 13's **Check availability** reads that from
the node's `/status`. DEX and exchange are not chain modules.

### What a failure means

- **"Failed to fetch"**: the RPC was blocked. Open `http://localhost:<port>` (the proxy only runs
  on localhost), not `127.0.0.1` or a LAN address.
- **"insufficient" or `sufficient: false`**: fund the account first.
- **"Wallet rejected the request: …"**: the extension refused the request; the text after the
  colon is the extension's own reason.
- **"Module call params contain characters the Splendor wallet cannot encode"**: a value in
  Params has non-ASCII text, usually a pasted `SQ:…` placeholder. Replace it with a real address.
- **"Connection failed"**: the SDK's generic message; the underlying error is in DevTools, under Console.
- If a card's checks do not match what you see, that is a real bug: note the card number and the
  result text.

## Automated tests

The browser lab is for manual end-to-end checks. The automated suites run without a network:

```bash
cd sdk/connect-react && npm test   # 277 tests: hooks, widgets, widget contract, send safety, sign-in, connectors
cd sdk/sqc-sdk && npm test         # 290 tests: SDK core, signer, RPC, modules
```

# create-splendor-dapp

Create a Splendor Quantum Chain dApp from the same templates as the
[Splendor dApp Playground](https://playground.splendor.org).

```bash
npm create splendor-dapp@latest
# or non-interactive
npm create splendor-dapp@latest my-dapp -- --template token-launchpad --framework vite --network testnet
```

| Template | What you get |
|----------|--------------|
| `wallet-dashboard` | Account card, SPLD transfer, pending transactions |
| `token-launchpad` | Deploy a QRC-20 token, then manage it on a second page |
| `tip-jar` | Fixed-recipient tips with the real fee (`--set tips.recipient=SQ:…`) |
| `members-area` | Wallet sign-in that unlocks content; verify on your server with `verifySignIn` |
| `nft-drop` | Deploy or use a QRC-1155 collection, mint and show balances |

Options: `--framework vite|next`, `--network testnet|devnet|mainnet|local`,
`--set block.prop=value`, `--yes` (no prompts). `--set` values follow the prop's type, so
`--set deploy.decimals=6` and `--set deploy.review=false` give a number and a boolean.

Each release pins the `@splendor/connect-react` and `@splendor/sqc-sdk` versions listed in this
package's own `package.json`, so a project created today keeps working with the SDK it was made
for. Generated projects pin the signer WASM digests in production builds and proxy public RPCs
on localhost; see the generated `README.md`.

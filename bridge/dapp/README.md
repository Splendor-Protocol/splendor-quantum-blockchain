# Splendor → Quantum Bridge dapp

One-way migration UI: connect MetaMask on the legacy Splendor chain (2691) and
the Splendor Quantum wallet, burn SPLD or SPLV on the legacy side, and watch the
relayer credit the same amount on the quantum chain.

## Configure

Copy `.env.example` to `.env` (or set the vars in your shell / CI):

| Var | Required | Default | Meaning |
|---|---|---|---|
| `VITE_BURN_CONTRACT` | ✅ | — | Deployed `QuantumMigrationBurn` address (chain 2691) |
| `VITE_SPLV_TOKEN` | — | `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689` | SPLV ERC-20 on 2691 |
| `VITE_LEGACY_CHAIN_ID` | — | `2691` | Legacy EVM chain id |
| `VITE_BRIDGE_ADDRESS` | — | — | Deployed `bridge_v1` instance (SQ:…) for claim-status polling |
| `VITE_SQC_NETWORK` | — | `mainnet` | Quantum network for the wallet (`mainnet` / `testnet` / `local`) |
| `VITE_SQC_WASM_SHA256` | prod | — | Pinned WASM hash for production builds |

## Run

```bash
npm install
npm run dev     # http://localhost:5175
npm run build   # dist/
```

## Flow

1. Connect the quantum wallet (extension/seed) and MetaMask.
2. Pick SPLD or SPLV and an amount (≥ 1 whole token).
3. Burn — `burnSpld` (native) or `burnSplv` (approve + burn) — the `Burned`
   event id is read from the receipt.
4. The dapp polls `/bridge/{addr}/claim/{id}` and shows when the relayer has
   credited the funds on the quantum chain.

The burn on 2691 is **real and irreversible**. The relayer, escrow and
attestation set are operator-run — see `contracts/legacy-burn/` and
`docs/modules/bridge.md` for the operator side.

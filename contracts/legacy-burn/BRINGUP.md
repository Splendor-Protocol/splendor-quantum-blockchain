# Splendor Migration Bridge — Bring-up Runbook

**Topology:** legacy EVM **mainnet (chain 2691, real SPLD/SPLV)** → **Splendor Quantum TESTNET**, 1-of-1 attestor.
**Result when complete:** `https://dapps.splendor.org/bridge` is live and functional — a holder connects MetaMask on 2691, burns SPLD/SPLV, and the relayer credits the same amount on the quantum **testnet** (SPLD native, SPLV as a QRC-20). The dapp shows a loud **TESTNET** banner because the destination is a testnet.

> The burn on 2691 is **real and irreversible**. The credited tokens are **testnet** tokens (no monetary value, resettable). This is a deliberate, disclosed choice.

---

## Resolved parameters (verified 2026-08-31)

| Name | Value | Source |
|---|---|---|
| `CAP` (per-burn max == `MAX_BURN` == `max_claim_amount`) | **`10000000000000000000000000`** (10,000,000 tokens) | chosen default — **immutable**, change before deploy if you want a different ceiling |
| `SPLV_TOKEN` (2691 ERC-20) | `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689` | wallet/CLAUDE map |
| `splv_max_supply` (QRC-20 cap) | **`2464997185739837851637500000`** (2,464,997,185.74 SPLV) | live `totalSupply()` on 2691 |
| Legacy RPC | `https://mainnet-rpc.splendor.org/` | infra |
| Legacy chain id | `2691` (`0xa83`) | — |
| Quantum testnet RPC | `https://quantum-rpc-testnet.splendor.org` | infra |
| Quantum testnet chain-id | **`splendor-quantum-testnet-1`** | live `/status` (NOT the default `splendor-quantum-1`) |
| Quantum testnet head | ~`8940` → fork activation height **`H` must be > 8940** (use e.g. `9500`) | live `/status` |
| `module_revert_v1` | active at height 1 (bridge_v1 dependency satisfied) | live `/status` |
| Burned event topic0 | `0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58` | `keccak256("Burned(uint256,address,uint8,uint256,bytes32)")` |
| Confirmations (mainnet) | `120` | design |

Addresses learned during bring-up (fill in as you go): `BURN_ADDR` (Step 1), `BURN_DEPLOY_BLOCK` (Step 1), `INSTANCE` = `SQ:…` (Step 3).

---

## Order of operations

`CAP` is decision zero. Then two independent tracks converge:

```
choose CAP
  ├─ Track A (mainnet, your 2691 key):      Step 1  deploy QuantumMigrationBurn  → BURN_ADDR, BURN_DEPLOY_BLOCK
  └─ Track B (testnet consensus, infra):    Step 2  activate bridge_v1 (H>8940) on all testnet nodes
                                                   │
                     ┌─────────────────────────────┘
                     ▼
Step 3  deploy bridge instance (owner key) → INSTANCE ; register relayer as operator ; fund SPLD pool
Step 5  run the relayer (operator key = the 1-of-1 attestor)
Step 6  wire the dapp (BURN_ADDR + INSTANCE) and redeploy → dapps.splendor.org/bridge is live
```

`bridge.deploy` (Step 3) is HARD-gated on `bridge_v1` being active (Step 2) and stores `evm_contract = BURN_ADDR` with no setter (Step 1 must exist first). Step 4 is reference only.

---

## STEP 1 — Deploy `QuantumMigrationBurn` on legacy chain 2691  ⚠️ mainnet, irreversible, needs your 2691 key

Canonical, tested source is in the chain repo: `contracts/legacy-burn/{src,script,test}` (26/26 forge tests pass; zero deps, solc 0.8.28 / london). The wallet Foundry repo is actively developed and wipes untracked files on branch switches, so keep it canonical here and copy it into `wallet/contracts` at deploy time:

```sh
cp contracts/legacy-burn/src/QuantumMigrationBurn.sol            wallet/contracts/src/
cp contracts/legacy-burn/script/DeployQuantumMigrationBurn.s.sol wallet/contracts/script/
```

Then run the deploy from `wallet/contracts` (or `forge` directly against `contracts/legacy-burn`).

```sh
cd wallet/contracts

SPLV_TOKEN=0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689 \
BURN_PAUSER=<PAUSER_ADDR_2691>            # incident brake; owner-changeable later; default = your deployer EOA
MAX_BURN=10000000000000000000000000 \      # == CAP. IMMUTABLE.
forge script script/DeployQuantumMigrationBurn.s.sol \
  --rpc-url https://mainnet-rpc.splendor.org/ \
  --broadcast --verify \
  --private-key <DEPLOYER_PRIVKEY_2691>     # or --account <keystore> / --ledger
```

Record from the broadcast: the deployed **`BURN_ADDR`** (lowercase) and its **`BURN_DEPLOY_BLOCK`**. Both feed later steps.

**`MAX_BURN` must equal the bridge's `max_claim_amount` (Step 3).** If `MAX_BURN` were larger, a user could burn above the claim cap — the burn succeeds irreversibly but the claim is refused on-chain and the funds are stranded forever. Both values are immutable; changing `CAP` later means redeploying both.

---

## STEP 2 — Activate `bridge_v1` on the quantum testnet  ⚠️ consensus change, all validators

This is being prepared as a **splendor-infra change** (add `bridge_v1` at height `H` (> 8940, e.g. `9500`) to the quantum-testnet tier's fork schedule, with `allow_fork_extension` on, rolled to every node). The exact PR/trigger is filled in once the infra mapping completes — see the companion note. Precondition already satisfied: `module_revert_v1` is active (height 1), so `module_revert_v1 ≤ bridge_v1` holds automatically.

Verify after rollout:
```sh
curl -s https://quantum-rpc-testnet.splendor.org/status   # forks should now include "bridge_v1": <H>
```

---

## STEP 3 — Create instance + threshold=1 + register attestor  (owner key, on quantum testnet)

No CLI exists — these are signed `TxCall` (type `0x03`) transactions to the **module router** with a JSON `ModuleCall` in `tx.Data`. The relayer's submit path (`cmd/bridge-relayer/quantum.go`) is the signing helper. `tx.Amount` is ignored on the router path — never attach value.

### 3a. `bridge.deploy` — one tx sets threshold + max_claim + auto-creates the instance-owned SPLV QRC-20

Signed by the **owner-to-be** (the sender becomes `owner`). `tx.Data`:

```json
{"module":"bridge","action":"deploy","params":{
  "evm_chain_id": 2691,
  "evm_contract": "0x<BURN_ADDR_40_LOWERCASE_HEX>",
  "splv_name": "Splendor Vested",
  "splv_symbol": "SPLV",
  "splv_max_supply": "2464997185739837851637500000",
  "max_claim_amount": "10000000000000000000000000",
  "threshold": 1
}}
```

Then read the instance address back from the deploy receipt (it folds block height, so you cannot precompute it):
```sh
curl -s https://quantum-rpc-testnet.splendor.org/tx/<DEPLOY_TXHASH>/receipt   # -> contract_address = SQ:<INSTANCE>
```

> `threshold:1` and `max_claim_amount` are set here, so **no separate `set_threshold` is needed** for 1-of-1.

### 3b. Fund the SPLD escrow pool (SPLD claims pay from it; SPLV claims mint)

```sh
sqc-wallet send --wallet <treasury.sqwallet> --node https://quantum-rpc-testnet.splendor.org \
  --to SQ:<INSTANCE> --amount <SPLD_FLOAT>
```

### 3c. `bridge.set_operator` — register the relayer wallet as the 1-of-1 attestor (owner-signed)

For 1-of-1, the one attestor **is** the relayer's own wallet (it self-attests and submits). Its SQ address = `sqc-wallet address -w <relayer.sqwallet>`.

```json
{"module":"bridge","action":"set_operator","params":{
  "bridge": "SQ:<INSTANCE>",
  "operator": "SQ:<RELAYER_OPERATOR_ADDR>",
  "active": true
}}
```

Verify:
```sh
curl -s https://quantum-rpc-testnet.splendor.org/bridge/SQ:<INSTANCE>
# expect owner=<you>, operators=[SQ:<RELAYER>], threshold=1, evm_chain_id=2691,
#        evm_contract=0x<BURN_ADDR>, max_claim_amount=10000000000000000000000000, pool_balance=<funded>
```

⚠️ `evm_chain_id`, `evm_contract`, `splv_max_supply`, and the SPLV token address are **fixed at deploy** (no setters). Get them right or redeploy a fresh instance.

---

## STEP 4 — What an attestor signs (reference)

Per burn, the attestor key signs `SHA3-256(preimage)` under context `CtxBridge = "SPLENDOR-QC-BRIDGE-v1"`, preimage = fixed-width ordered:
```
"bridge:claim:v1:" ‖ u16be(len(chainID)) ‖ chainID ‖ bridge[32] ‖ id[8 BE] ‖ asset[1] ‖ amount[32 BE] ‖ recipient[32] ‖ evm_tx[32]
```
`chainID`+`bridge`+`id` pin each attestation to one burn on one instance — it can't be replayed. The relayer does this automatically.

---

## STEP 5 — Run the relayer  (operator key; AWS host + KMS — see the AWS spec)

1-of-1 ⇒ omit `--attestation-dir`. Needs the operator `.sqwallet` unlocked via `SQC_WALLET_PASS`.

```sh
export SQC_WALLET_PASS='<operator wallet passphrase>'
bridge-relayer \
  --evm-rpc       https://mainnet-rpc.splendor.org/ \
  --evm-chain-id  2691 \
  --evm-contract  0x<BURN_ADDR> \
  --start-block   <BURN_DEPLOY_BLOCK> \
  --node          https://quantum-rpc-testnet.splendor.org \
  --chain-id      splendor-quantum-testnet-1 \
  --bridge        SQ:<INSTANCE> \
  --wallet        /secure/operator.sqwallet \
  --state         /var/sqc/bridge-relayer-testnet.json \
  --confirmations 120
```

Preflight fails closed unless: legacy `eth_chainId==2691`, quantum `/status.chain_id==splendor-quantum-testnet-1`, the bridge's stored `evm_chain_id`/`evm_contract` match, and the wallet is owner/active-operator. Only a **mined-and-reverted** claim counts toward `--max-attempts`; congestion/pause/undecodable-log **hold** the cursor rather than dropping a burn.

---

## STEP 6 — Wire the dapp and go live

Set the `production` env vars in `splendor-quantum-dapps/wrangler.jsonc` and redeploy:

| var | value |
|---|---|
| `BURN_CONTRACT_ADDRESS` | `0x<BURN_ADDR>` |
| `QUANTUM_RPC_BASE` | `https://quantum-rpc-testnet.splendor.org` |
| `QUANTUM_BRIDGE_ADDRESS` | `<INSTANCE>` (the 64-hex, with or without `SQ:`) |
| `QUANTUM_EXPLORER_URL` | `https://quantum-testnet-explorer.splendor.org` |
| `QUANTUM_NETWORK` | `testnet` (lights the TESTNET banner; also auto-inferred from the RPC host) |
| `SPLV_TOKEN_ADDRESS` | `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689` |

```sh
gh workflow run deploy.yml -R Splendor-Protocol/splendor-quantum-dapps -f environment=production
```

Then `https://dapps.splendor.org/bridge` reads its config from `/bridge/api/info`, the TESTNET banner shows, and the burn→credit flow is live.

Also add `dapps.splendor.org` to the `wallet-api` gateway's `CONNECT_ALLOWED_ORIGINS` for the optional "Connect Splendor Wallet" button (the paste path works regardless).

---

## What needs YOUR keys (everything else is automated/prepared)

1. **2691 deployer key** — Step 1 (irreversible mainnet deploy).
2. **Testnet validators' signing keys** — Step 2 (consensus rollout; driven via the infra PR/deploy).
3. **Bridge owner key** — Step 3 (`bridge.deploy` + `set_operator`, on quantum testnet).
4. **Relayer/operator key** — Step 5 (the sole attestor + claim submitter).

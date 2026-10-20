> **Archived lab model.**
> This page describes the feat/validator pass/tier lab, not the live
> 2,500 SPLD `validators_v1` registry. Public admission has not launched.
> Do not treat these numbers or procedures as mainnet admission.
> Current lifecycle: [`VALIDATOR.md`](VALIDATOR.md).

# Feat/validator lab — short FAQ (shareable)

Plain-language prep for the **feat/validator** demo lab.  
**Chain id:** `sqc-featval-1` · **Not mainnet.**  
Live numbers below were pulled **2026-09-07 ~16:44 CEST** from  
`https://quantum-validator.splendor.org/api/v1/validators` and the master RPC  
(`/status`, `/staking/tiers` — endpoint in the internal runbook).

Explorer: **https://quantum-validator.splendor.org** (validators dashboard: `/validators`)

---

## 1. Validator role vs Master

| Role | What it is on this lab |
|---|---|
| **Master** | Genesis producer / admission admin. Implicitly passed and **bond-exempt** (no stake required). Starts the chain and grants/revokes admission passes. |
| **Validator (V1–V3 today)** | Full node that may **produce blocks** only when it has an admission **pass** and an active **bond** at or above the min stake (unless bond-exempt). |

On this lab, production is **multi-producer PoA** (round-robin over admitted keys). That is **not** full BFT voting yet.

Rule of thumb:

`CanProduce = pass AND (bond-exempt OR active bond ≥ min stake)`

---

## 2. End-to-end picture (what “works” today)

1. **Pass** — Master (admin) grants an admission pass to a consensus key.  
2. **Bond** — That key registers and locks stake (≥ Bronze / min stake).  
3. **CanProduce** — Both gates true → key is eligible to produce.  
4. **PoA now** — Eligible producers take turns sealing blocks (lab harness).  
5. **P2P mesh** — Four nodes peer together (sealed P2P); followers sync the same chain.  
6. **Explorer** — Public UI shows height, peers, bonds, CanProduce, recent producer share: https://quantum-validator.splendor.org/validators  

**Live snapshot (lab):** height ~13.1k · peers **4** · CanProduce **4/4** · passes **3** (+ genesis) · total bonded **20,000 SPLD**.

---

## 3. How to add a validator (high level)

No secrets here — only the steps.

1. **Key** — Generate an ML-DSA-65 consensus keypair (address = SHA3 of the pubkey).  
2. **Fund** — Get enough lab SPLD for the bond + fees.  
3. **Pass** — Admin calls staking `set_pass` for that pubkey (`passed: true`). Without this, bond alone → **not** CanProduce.  
4. **Bond** — Validator calls staking `register` with `pubkey` + `amount` ≥ min stake (Bronze floor). Stake moves into escrow; status `active`.  
5. **Peer** — Run the node with peers pointing at the mesh (private IPs on the AWS VPC), same genesis / chain id `sqc-featval-1`.  
6. **CanProduce** — Confirm eligible via explorer or RPC (`can_produce` / validators API). Then the PoA roster can include the key.

**Do not** revoke the last producer’s pass without admitting a replacement first — that can halt the chain.

---

## 4. Fees (who gets what today)

| Topic | Lab reality |
|---|---|
| **MinFeeV1 floor** | Fork `min_fee_v1` is active on this chain (from height **300**). Floor = **0.001 SPLD** (`10^15` smallest units). Below that, txs are refused by mempool / producer. |
| **Who gets fees today** | Transaction fees in a block are credited to the **block producer** address. |
| **Rewards** | Staking records have a `rewards` field, but **issuance is not live** (`rewardsIssuanceLive: false`). Explorer labels this “not issued on chain yet.” Values stay at 0. |

So today: **fees → producer**; **block rewards → not issued yet**.

---

## 5. Min SPLD to bond / tiers (live)

**Minimum to be eligible (Bronze floor / `min_stake`): 3,947 SPLD**

Live tier table from the master RPC `/staking/tiers` (endpoint in the internal
runbook):

| Tier | Min SPLD |
|---|---|
| **Bronze** | **3,947** |
| **Silver** | **39,474** |
| **Gold** | **394,737** |
| **Platinum** | **3,947,368** |

Tiers **rank** validators; the Bronze number **admits** them (gate floor).

**Current lab bonds:** V1–V3 each bonded **5,000 SPLD** (Bronze tier label on explorer) · Master bond-exempt / stake 0 · **total bonded 20,000 SPLD**.

---

## 6. Where the validators run

| | |
|---|---|
| **Cloud** | AWS, region **us-east-1** (account in the internal runbook) |
| **Size** | **1× t4g.large** (master) + **3× t4g.small** (validators) — Ubuntu arm64 / Graviton |
| **Topology** | 1 master + 3 validators (PoA producers when admitted) |
| **Master RPC** | internal runbook (the lab's master node) |
| **Explorer** | https://quantum-validator.splendor.org |

This lab is tagged separately from production tofu state; it does not replace mainnet/testnet/devnet tiers.

---

## 7. Day-to-day operator checklist

- [ ] Explorer tip advancing: https://quantum-validator.splendor.org  
- [ ] `/validators` shows **CanProduce** count and active bonds looking healthy  
- [ ] Master `/status`: `chain_id` = `sqc-featval-1`, `peer_count` ≈ 4, `multi_producer` true  
- [ ] Heights on all four nodes roughly aligned (a few blocks is fine)  
- [ ] After any pass/bond change: confirm **CanProduce** before expecting that key to seal  
- [ ] Keep at least one eligible producer — never leave the set empty  
- [ ] Fees: txs must meet **MinFeeV1** (0.001 SPLD); don’t expect staking “rewards” payouts yet  
- [ ] Don’t redeploy explorers or tear down instances unless that is the planned task  
- [ ] No secrets in chat/docs (pem, rpc tokens, seeds, passwords)

---

## 8. What this is NOT

- **Not mainnet** (and not production testnet/devnet). Lab chain id only: **`sqc-featval-1`**.  
- **Not full BFT** — no quorum votes / QC finality engine yet. PoA multi-producer is a lab harness on top of **admission gates**.  
- **Not a rewards program** — bonds escrow stake; reward issuance path is not fed yet.  
- **Not “anyone can produce”** — bonding without a pass is not enough.  
- **Not a place to paste keys or tokens** — treat this as a throwaway demo environment.

---

### Quick links

- Validators UI: https://quantum-validator.splendor.org/validators  
- Live validators JSON: https://quantum-validator.splendor.org/api/v1/validators  
- Master status / live tiers: internal runbook (`/status`, `/staking/tiers` on the lab's master node)  
- Deeper branch notes: `docs/FEAT_VALIDATOR.md` · staking module: `docs/modules/staking.md`

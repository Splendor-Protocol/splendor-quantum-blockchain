# AWS feat/validator PoA4 evidence (1 master + 3 validators)

Pointers only — no secrets (no pem, rpc.token, seeds, passwords, CF tokens).

| | |
|---|---|
| HEAD (feat/validator) | `fc945b1` (+ uncommitted PoA harness cherry-pick from harness/poa-multivalidator-proof) |
| chain id | `sqc-featval-1` |
| master RPC | `http://3.84.91.28:8080` |
| evidence written | 2026-09-07T11:06:49Z UTC |
| local state | `~/.sqc-feat-validator/` (instances.full.txt, keys/, chain/, gate-proof-logs/) |

## Topology

```
0 master i-025ee7ef5cbd315c7 3.84.91.28 10.120.8.110 master
1 validator i-0be8cb560d93676bd 54.243.5.2 10.120.11.92 v1
2 validator i-0005327cc0dc3b639 54.163.116.41 10.120.6.246 v2
3 validator i-072a401792bfc2089 3.89.125.204 10.120.7.6 v3
```

Roles: index0=master (genesis validator / admission admin); index1..3=validators v1..v3.
PoA harness: `--poa-producer` round-robin over chain-admitted keys (NOT multi_validator BFT).

## Gate proof log excerpts

See also: `~/.sqc-feat-validator/gate-proof-logs/`.


### baseline

```
{
  "action": "staking_info",
  "admin": "SQ:<redacted-hex>",
  "contract": "SQ:<redacted-hex>",
  "escrow_balance": "0",
  "genesis_anchor_known": true,
  "initialised": false,
  "max_passes": 64,
  "min_stake": "3947000000000000000000",
  "module": "staking",
  "pass_count": 0,
  "slashed_total": "0",
  "tiers": {
    "bronze": "3947000000000000000000",
    "gold": "394737000000000000000000",
    "platinum": "3947368000000000000000000",
    "silver": "39474000000000000000000"
  },
  "total_staked": "0",
  "unbonding_blocks": 302400,
  "validator_count": 0
}
-- validator_set
{
  "action": "validator_set",
  "genesis_anchor_known": true,
  "module": "staking",
  "validators": [
    {
      "address": "SQ:<redacted-hex>",
      "bond_exempt": true,
      "eligible": true,
      "passed": true,
      "source": "genesis",
      "stake": "0",
      "status": "inactive"
    }
  ]
}
-- can_produce v1
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": false,
  "genesis_anchor_known": true,
  "module": "staking",
  "reason": "validator has no admission pass: SQ:<redacted-hex> is not in the validator pass registry"
}
-- can_produce v2
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": false,
  "genesis_anchor_known": true,
  "module": "staking",
  "reason": "validator has no admission pass: SQ:<redacted-hex> is not in the validator pass registry"
}
-- can_produce v3
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": false,
  "genesis_anchor_known": true,
  "module": "staking",
  "reason": "validator has no admission pass: SQ:<redacted-hex> is not in the validator pass registry"
}
-- who has been producing (expect master alone)
   block  131  master  SQ:<redacted-hex>
   block  132  master  SQ:<redacted-hex>
   block  133  master  SQ:<redacted-hex>
   block  134  master  SQ:<redacted-hex>
   block  135  master  SQ:<redacted-hex>
   block  136  master  SQ:<redacted-hex>
   block  137  master  SQ:<redacted-hex>
   block  138  master  SQ:<redacted-hex>
   block  139  master  SQ:<redacted-hex>
   block  140  master  SQ:<redacted-hex>
   block  141  master  SQ:<redacted-hex>
   block  142  master  SQ:<redacted-hex>
   tally: {'master': 12}
```

### admit

```
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": true,
  "genesis_anchor_known": true,
  "module": "staking"
}
-- heights: master=159 v1=159 v2=159 v3=159

======== S1 RESULT — four admitted ========
-- 2026-09-07T11:07:13Z
-- validator_set
{
  "action": "validator_set",
  "genesis_anchor_known": true,
  "module": "staking",
  "validators": [
    {
      "address": "SQ:<redacted-hex>",
      "bond_exempt": true,
      "eligible": true,
      "passed": true,
      "source": "genesis",
      "stake": "0",
      "status": "inactive"
    },
    {
      "address": "SQ:<redacted-hex>",
      "bond_exempt": false,
      "eligible": true,
      "passed": true,
      "source": "admin",
      "stake": "5000000000000000000000",
      "status": "active",
      "tier": "Bronze"
    },
    {
      "address": "SQ:<redacted-hex>",
      "bond_exempt": false,
      "eligible": true,
      "passed": true,
      "source": "admin",
      "stake": "5000000000000000000000",
      "status": "active",
      "tier": "Bronze"
    },
    {
      "address": "SQ:<redacted-hex>",
      "bond_exempt": false,
      "eligible": true,
      "passed": true,
      "source": "admin",
      "stake": "5000000000000000000000",
      "status": "active",
      "tier": "Bronze"
    }
  ]
}
-- staking_info
{
  "action": "staking_info",
  "admin": "SQ:<redacted-hex>",
  "contract": "SQ:<redacted-hex>",
  "escrow_balance": "15000000000000000000000",
  "genesis_anchor_known": true,
  "initialised": true,
  "max_passes": 64,
  "min_stake": "3947000000000000000000",
  "module": "staking",
  "pass_count": 3,
  "slashed_total": "0",
  "tiers": {
    "bronze": "3947000000000000000000",
    "gold": "394737000000000000000000",
    "platinum": "3947368000000000000000000",
    "silver": "39474000000000000000000"
  },
  "total_staked": "15000000000000000000000",
  "unbonding_blocks": 302400,
  "validator_count": 3
}
```

### rotate

```

======== S2 ROTATION — sample 40s ========
-- 2026-09-07T11:07:13Z
-- heights: master=201 v1=201 v2=201 v3=201
   block  171  v3      SQ:<redacted-hex>
   block  172  master  SQ:<redacted-hex>
   block  173  v1      SQ:<redacted-hex>
   block  174  v2      SQ:<redacted-hex>
   block  175  v3      SQ:<redacted-hex>
   block  176  master  SQ:<redacted-hex>
   block  177  v1      SQ:<redacted-hex>
   block  178  v2      SQ:<redacted-hex>
   block  179  v3      SQ:<redacted-hex>
   block  180  master  SQ:<redacted-hex>
   block  181  v1      SQ:<redacted-hex>
   block  182  v2      SQ:<redacted-hex>
   block  183  v3      SQ:<redacted-hex>
   block  184  master  SQ:<redacted-hex>
   block  185  v1      SQ:<redacted-hex>
   block  186  v2      SQ:<redacted-hex>
   block  187  v3      SQ:<redacted-hex>
   block  188  master  SQ:<redacted-hex>
   block  189  v1      SQ:<redacted-hex>
   block  190  v2      SQ:<redacted-hex>
   block  191  v3      SQ:<redacted-hex>
   block  192  master  SQ:<redacted-hex>
   block  193  v1      SQ:<redacted-hex>
   block  194  v2      SQ:<redacted-hex>
   block  195  v3      SQ:<redacted-hex>
   block  196  master  SQ:<redacted-hex>
   block  197  v1      SQ:<redacted-hex>
   block  198  v2      SQ:<redacted-hex>
   block  199  v3      SQ:<redacted-hex>
   block  200  master  SQ:<redacted-hex>
   block  201  v1      SQ:<redacted-hex>
   block  202  v2      SQ:<redacted-hex>
   tally: {'v3': 8, 'master': 8, 'v1': 8, 'v2': 8}
```

### deny-pass

```

======== S3 DENY (no pass) — revoke v2 ========
-- 2026-09-07T11:08:05Z
submitted staking.set_pass from=SQ:<redacted-hex> nonce=3 tx=<redacted-hex>
receipt {"amount":0,"block_hash":[251,247,86,110,33,242,24,232,158,161,23,50,250,0,29,109,35,17,46,97,194,19,140,7,199,89,104,202,243,255,246,32],"block_height":216,"fee":100000,"from":[117,127,138,57,184,234,71,125,170,236,220,126,254,247,50,214,240,217,195,218,86,22,165,105,97,133,140,175,138,5,116,73],"gas_used":0,"logs":[{"address":[117,153,224,168,77,4,45,226,53,25,216,73,89,79,27,104,3,140,206,137,137,112,218,25,238,183,214,177,17,66,196,240],"block_height":216,"data":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","log_index":0,"topics":[[44,59,125,114,99,74,130,238,225,165,128,159,190,89,124,81,128,27,158,241,240,107,109,78,13,56,69,21,17,133,48,24],[240,158,158,72,238,168,28,91,7,123,168,170,33,19,9,235,27,83,48,146,215,54,81,201,157,132,7,246,194,167,14,113],[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]],"tx_hash":[62,171,127,221,235,222,96,251,204,41,118,34,200,159,136,227,227,202,42,182,193,224,23,70,126,162,42,138,245,200,0,155],"tx_index":0}],"status":1,"to":[176,112,67,244,239,116,153,81,137,57,133,214,100,148,208,244,228,76,136,18,5,51,97,192,126,162,75,199,10,68,77,205],"tx_hash":[62,171,127,221,235,222,96,251,204,41,118,34,200,159,136,227,227,202,42,182,193,224,23,70,126,162,42,138,245,200,0,155],"tx_index":0,"type":3}
-- can_produce v2
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": false,
  "genesis_anchor_known": true,
  "module": "staking",
  "reason": "validator has no admission pass: SQ:<redacted-hex> was admitted and then revoked"
}
-- rotation without v2
-- heights: master=254 v1=255 v2=256 v3=256
   block  238  v1      SQ:<redacted-hex>
   block  239  v3      SQ:<redacted-hex>
   block  240  master  SQ:<redacted-hex>
   block  241  v1      SQ:<redacted-hex>
   block  242  v3      SQ:<redacted-hex>
   block  243  master  SQ:<redacted-hex>
   block  244  v1      SQ:<redacted-hex>
   block  245  v3      SQ:<redacted-hex>
   block  246  master  SQ:<redacted-hex>
   block  247  v1      SQ:<redacted-hex>
   block  248  v3      SQ:<redacted-hex>
   block  249  master  SQ:<redacted-hex>
   block  250  v1      SQ:<redacted-hex>
   block  251  v3      SQ:<redacted-hex>
   block  252  master  SQ:<redacted-hex>
   block  253  v1      SQ:<redacted-hex>
   block  254  v3      SQ:<redacted-hex>
   block  255  master  SQ:<redacted-hex>
   block  256  v1      SQ:<redacted-hex>
   block  257  v3      SQ:<redacted-hex>
   tally: {'v1': 7, 'v3': 7, 'master': 6}

======== S3 RESTORE — grant v2 again ========
-- 2026-09-07T11:08:43Z
submitted staking.set_pass from=SQ:<redacted-hex> nonce=4 tx=<redacted-hex>
receipt {"amount":0,"block_hash":[170,193,171,32,244,160,179,158,7,33,242,203,186,83,37,15,100,159,106,220,14,72,7,39,17,168,254,46,58,167,202,20],"block_height":270,"fee":100000,"from":[117,127,138,57,184,234,71,125,170,236,220,126,254,247,50,214,240,217,195,218,86,22,165,105,97,133,140,175,138,5,116,73],"gas_used":0,"logs":[{"address":[117,153,224,168,77,4,45,226,53,25,216,73,89,79,27,104,3,140,206,137,137,112,218,25,238,183,214,177,17,66,196,240],"block_height":270,"data":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","log_index":0,"topics":[[44,59,125,114,99,74,130,238,225,165,128,159,190,89,124,81,128,27,158,241,240,107,109,78,13,56,69,21,17,133,48,24],[240,158,158,72,238,168,28,91,7,123,168,170,33,19,9,235,27,83,48,146,215,54,81,201,157,132,7,246,194,167,14,113],[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1]],"tx_hash":[31,251,192,41,88,88,7,82,49,167,249,7,202,62,192,144,221,183,74,80,14,17,34,55,79,38,193,202,241,233,227,230],"tx_index":0}],"status":1,"to":[176,112,67,244,239,116,153,81,137,57,133,214,100,148,208,244,228,76,136,18,5,51,97,192,126,162,75,199,10,68,77,205],"tx_hash":[31,251,192,41,88,88,7,82,49,167,249,7,202,62,192,144,221,183,74,80,14,17,34,55,79,38,193,202,241,233,227,230],"tx_index":0,"type":3}
-- can_produce v2
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": true,
  "genesis_anchor_known": true,
  "module": "staking"
}
-- heights: master=297 v1=297 v2=297 v3=298
   block  279  v3      SQ:<redacted-hex>
   block  280  master  SQ:<redacted-hex>
   block  281  v1      SQ:<redacted-hex>
   block  282  v2      SQ:<redacted-hex>
   block  283  v3      SQ:<redacted-hex>
   block  284  master  SQ:<redacted-hex>
   block  285  v1      SQ:<redacted-hex>
   block  286  v2      SQ:<redacted-hex>
   block  287  v3      SQ:<redacted-hex>
   block  288  master  SQ:<redacted-hex>
   block  289  v1      SQ:<redacted-hex>
   block  290  v2      SQ:<redacted-hex>
   block  291  v3      SQ:<redacted-hex>
   block  292  master  SQ:<redacted-hex>
   block  293  v1      SQ:<redacted-hex>
   block  294  v2      SQ:<redacted-hex>
   block  295  v3      SQ:<redacted-hex>
   block  296  master  SQ:<redacted-hex>
   block  297  v1      SQ:<redacted-hex>
   block  298  v2      SQ:<redacted-hex>
   tally: {'v3': 5, 'master': 5, 'v1': 5, 'v2': 5}
```

### deny-bond

```

======== S4 DENY (under-bonded) — v3 unstakes below floor ========
-- 2026-09-07T11:09:19Z
error: submit rejected (400): {"error":"rejected: fee too low: minimum 1000000000000000 at current pool load, got 100000"}
-- can_produce v3
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": true,
  "genesis_anchor_known": true,
  "module": "staking"
}
-- rotation without v3
-- heights: master=330 v1=330 v2=331 v3=331
   block  312  master  SQ:<redacted-hex>
   block  313  v1      SQ:<redacted-hex>
   block  314  v2      SQ:<redacted-hex>
   block  315  v3      SQ:<redacted-hex>
   block  316  master  SQ:<redacted-hex>
   block  317  v1      SQ:<redacted-hex>
   block  318  v2      SQ:<redacted-hex>
   block  319  v3      SQ:<redacted-hex>
   block  320  master  SQ:<redacted-hex>
   block  321  v1      SQ:<redacted-hex>
   block  322  v2      SQ:<redacted-hex>
   block  323  v3      SQ:<redacted-hex>
   block  324  master  SQ:<redacted-hex>
   block  325  v1      SQ:<redacted-hex>
   block  326  v2      SQ:<redacted-hex>
   block  327  v3      SQ:<redacted-hex>
   block  328  master  SQ:<redacted-hex>
   block  329  v1      SQ:<redacted-hex>
   block  330  v2      SQ:<redacted-hex>
   block  331  v3      SQ:<redacted-hex>
   tally: {'master': 5, 'v1': 5, 'v2': 5, 'v3': 5}

======== S4 RESTORE — v3 top-up ========
-- 2026-09-07T11:09:52Z
error: submit rejected (400): {"error":"rejected: fee too low: minimum 1000000000000000 at current pool load, got 100000"}
-- can_produce v3
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": true,
  "genesis_anchor_known": true,
  "module": "staking"
}
-- heights: master=363 v1=363 v2=364 v3=364
   block  346  v2      SQ:<redacted-hex>
   block  347  v3      SQ:<redacted-hex>
   block  348  master  SQ:<redacted-hex>
   block  349  v1      SQ:<redacted-hex>
   block  350  v2      SQ:<redacted-hex>
   block  351  v3      SQ:<redacted-hex>
   block  352  master  SQ:<redacted-hex>
   block  353  v1      SQ:<redacted-hex>
   block  354  v2      SQ:<redacted-hex>
   block  355  v3      SQ:<redacted-hex>
   block  356  master  SQ:<redacted-hex>
   block  357  v1      SQ:<redacted-hex>
   block  358  v2      SQ:<redacted-hex>
   block  359  v3      SQ:<redacted-hex>
   block  360  master  SQ:<redacted-hex>
   block  361  v1      SQ:<redacted-hex>
   block  362  v2      SQ:<redacted-hex>
   block  363  v3      SQ:<redacted-hex>
   block  364  master  SQ:<redacted-hex>
   block  365  v1      SQ:<redacted-hex>
   tally: {'v2': 5, 'v3': 5, 'master': 5, 'v1': 5}
```

### forge (best-effort)

```

======== S5 DENY — rogue forge to peers ========
-- 2026-09-07T11:10:25Z
-- can_produce rogue
{
  "action": "can_produce",
  "address": "SQ:<redacted-hex>",
  "eligible": false,
  "genesis_anchor_known": true,
  "module": "staking",
  "reason": "validator has no admission pass: SQ:<redacted-hex> is not in the validator pass registry"
}
-- forge push to v1 (54.243.5.2)
forged block height=371 producer=SQ:<redacted-hex> target=http://54.243.5.2:8080
HTTP 400 {"error":"block rejected: block application failed: block linkage rejected: non-contiguous height: got 371, expected 372"}
-- forge push to v2 (54.163.116.41)
forged block height=372 producer=SQ:<redacted-hex> target=http://54.163.116.41:8080
HTTP 400 {"error":"block rejected: block application failed: block 372 was sealed by a validator this chain does not admit: validator has no admission pass: SQ:<redacted-hex> is not in the validator pass registry"}
-- forge push to v3 (3.89.125.204)
forged block height=373 producer=SQ:<redacted-hex> target=http://3.89.125.204:8080
HTTP 400 {"error":"block rejected: block application failed: block linkage rejected: non-contiguous height: got 373, expected 374"}
```

## One-liners

- Who produces/seals: after admit+rotate, producers tally shows master+v1+v2+v3 (see rotate section).
- Pass revoke blocks production: after set_pass v2 false, v2 absent from producer tally; restore brings it back.
- Under-bond blocks production: after unstake below floor, v3 absent; add_stake restores.
- Restore works: both deny-pass and deny-bond restore sections show the key producing again.

## Teardown

```bash
cd ~/Workspace/splendor/splendor-quantum-blockchain
bash scripts/aws/feat-validator/90-teardown.sh          # instances+SG+keypair
bash scripts/aws/feat-validator/90-teardown.sh --purge-local  # also wipe ~/.sqc-feat-validator
# Explorer/DNS: wrangler delete --env validator (if deployed); cf-dns remove quantum-validator.splendor.org
```


## Under-bond retry (post min_fee_v1)

First `deny-bond` during `all` failed because height > 300 and txs used fee 100000.
Retried with `--fee 1000000000000000` (see `gate-proof-logs/deny-bond-retry.log`):

- After unstake: `can_produce v3` → **eligible:false** (not bonded / exiting)
- Producer tally without v3: `{master, v1, v2}` only
- After `add_stake` restore: **eligible:true**; tally again `{master,v1,v2,v3}` each ~5

## Explorer

- Live: https://quantum-validator.splendor.org (HTTP 200; label "Validator Demo (feat/validator)")
- Workers.dev backup: https://quantum-explorer-validator.cool-art-bfa3.workers.dev
- RPC base for Worker: `http://3.84.91.28:8080` (master public IP :8080; writes token-gated)
- Note: accidental `wrangler deploy` without env briefly touched Worker `quantum-explorer`; rolled back to version `8f398789-3f58-4031-b4fd-3856e56f2804` (2026-09-03). Confirm prod/testnet explorers with Niccolò if anything looks off.
- Local wrangler.jsonc has uncommitted `validator` env block (demo only).

## Uncommitted on feat/validator (blockchain repo)

- PoA harness cherry-pick (index): consensus/poa.go, cmd/node --poa-producer, cmd/sqc-modcall, network/*, etc.
- New scripts: `scripts/aws/feat-validator/11-launch-poa4.sh`, `25-gate-proof.sh` (not committed)
- HEAD: `fc945b1` + above local changes


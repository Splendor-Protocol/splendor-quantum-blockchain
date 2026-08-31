# QuantumMigrationBurn — legacy-chain burn gate

The EVM half of the SPLD/SPLV → Splendor Quantum migration bridge
(design: `docs/plans/2026-08-31-quantum-migration-bridge.md`).

Holders on the legacy Splendor mainnet (chain 2691) burn at least 1 whole
token here — native SPLD via `burnSpld{value}(quantumRecipient)`, ERC-20
SPLV via approve + `burnSplv(amount, quantumRecipient)` — naming their
quantum destination as the 32 raw bytes of the `SQ:` address. Every burn
forwards the full amount to `0x…dEaD` and emits one `Burned` event; the
bridge relayer reads the event after a confirmation depth and credits the
same amount on the quantum chain.

## Why non-upgradeable

The `SplvToSpldSwap` precedent is UUPS because it custodies a payout pool.
This contract custodies **nothing**, so upgradeability would only add attack
surface: there is nothing an upgrade could steal, and no "rescue" story
should exist to erode trust in the burn. Plain constructor, no proxy, no
initializer, no admin framework — two hand-rolled roles only:

- `owner` — sets the pauser, two-step ownership transfer (Ownable2Step-style).
- `pauser` — `pause()` / `unpause()`, the incident brake.

## Why it holds nothing

- SPLD is forwarded to the dead address inside the same call that received it.
- SPLV is pulled with `transferFrom(msg.sender, 0x…dEaD, amount)` — it never
  touches the contract's balance — with a dead-address balance snapshot
  before/after so fee-on-transfer tokens cannot under-deliver.
- There are **no** withdraw/rescue/sweep functions of any kind, and
  `receive()`/`fallback()` revert. Anything force-sent (selfdestruct) would
  be unreachable by design, exactly as intended.

A direct SPLV `transfer` to this contract (bypassing `burnSplv`) emits no
`Burned` event and is not creditable — the dapp warns about this; it is
user loss like any transfer to a wrong address.

## Layout

- `src/QuantumMigrationBurn.sol` — the contract (zero dependencies; a
  minimal local `IERC20` is declared in the same file).
- `test/QuantumMigrationBurn.t.sol` — forge tests, including fee-on-transfer,
  non-standard-return and reentrant token mocks.
- `script/DeployQuantumMigrationBurn.s.sol` — deploy script; reads
  `SPLV_TOKEN`, `BURN_PAUSER` and `MAX_BURN` from the environment (documented
  in the script header). The broadcaster becomes `owner`.

**`MAX_BURN` must equal the quantum bridge instance's `max_claim_amount`.**
The quantum module refuses to credit a claim larger than that cap, so a burn
above it would be destroyed here and never claimable. The contract enforces
the same ceiling (`maxBurn`, immutable) so an un-creditable burn is impossible;
a larger migration is done as several sub-cap burns. Deploy the bridge module
first, read its cap from `GET /bridge/{addr}` (`max_claim_amount`), and pass
the identical value here. Changing the cap later means redeploying both.
- `lib/forge-std/` — forge-std 1.15.0, vendored as a plain copy (no
  submodule, no network) so build + tests run fully offline.
- `foundry.toml` — solc 0.8.28, `evm_version = "london"` (the 2691 node is a
  pre-Merge geth fork; never compile with a Cancun/Shanghai default),
  optimizer at 200 runs.

## Running the tests

```sh
cd contracts/legacy-burn
forge build
forge test -vv
```

No network access, submodules or installs required.

## Deploying to mainnet

Do **not** deploy from this directory. Mainnet deploys run from
`wallet/contracts` — the established 2691 Foundry home with the
`splendor_mainnet` RPC endpoint and the explorer verify API already
configured — by copying `src/` and `script/` there:

```sh
cp src/QuantumMigrationBurn.sol            ../../../wallet/contracts/src/
cp script/DeployQuantumMigrationBurn.s.sol ../../../wallet/contracts/script/

cd ../../../wallet/contracts
SPLV_TOKEN=0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689 \
BURN_PAUSER=<pauser address> \
MAX_BURN=<the bridge instance's max_claim_amount, in wei> \
forge script script/DeployQuantumMigrationBurn.s.sol \
  --rpc-url splendor_mainnet --broadcast --verify
```

Keep `evm_version = "london"` when compiling there as well (it already is).

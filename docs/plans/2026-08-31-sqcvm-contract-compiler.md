# A contract compiler for SQC-VM — design + prototype

## The question

Features like the migration bridge ship today as **native Go modules compiled
into `sqc-node`** — consensus code every validator runs. Could they instead be
**deployed contracts**, the way tokens are on an EVM chain? That needs a
compiler: a high-level language that produces SQC-VM v4 bytecode a `TxDeploy`
can carry. This doc decides the approach and lands a working prototype.

## What already exists (verified in code)

- **A real bytecode VM** — `vm/engine.go` (SQC-VM v4): a 256-bit stack machine,
  SHA3-256 storage slots, gas metering, `CALL`/`DELEGATECALL`/`STATICCALL`,
  `LOG0-4`, `REVERT`/`REQUIRE`. `TxDeploy` stores `tx.Data` **verbatim as
  runtime code** (`vm/engine.go` `Deploy`, `core/block/executor.go`
  `execDeploy`) — there is no EVM-style constructor/init-code phase.
- **No compiler and no language.** `readme.md` §8 is explicit: QSol is "a
  specification and an intention… No parser, no compiler, no toolchain exists."
  The dialect notes amount to: `sqaddress` for 32-byte addresses, `sha3()` for
  `keccak256()`, a `verify_mldsa(...)` builtin, no `ecrecover()`.

## Where SQC-VM diverges from the EVM (the constraints a compiler must honor)

| Dimension | EVM | SQC-VM v4 | Consequence |
|---|---|---|---|
| Address width | 20 bytes | **32 bytes** (`SQ:` = SHA3-256(pubkey)) | An address is a full word; no masking, but every ABI/mapping layout that assumes 20 bytes is wrong. |
| Hash | keccak-256 | **SHA3-256** everywhere (`qcrypto.Hash32`) | Selectors, event topics and mapping slots must be SHA3-256. solc output (keccak) is layout-incompatible. |
| Opcode numbering | EVM table | **v4 relocations** (PUSH/DUP/SWAP at 0x60-0x9F; SSTORE 0x55, JUMP 0x56, etc.) | Raw EVM bytecode will not run; codegen targets the v4 table. |
| `SELECTOR`/`ABI_ENCODE`/`ABI_DECODE` (0x43-0x45) | n/a | **declared but NOT implemented** — they hit the `default: unknown opcode` path (confirmed: no handler in `engine.go`, only `disasm.go` names them) | The compiler must do selector extraction and ABI layout **itself**, with `CALLDATALOAD`+`SHR`, not these opcodes. |
| Several operand orders | — | `MSTORE`/`SSTORE` pop **value then offset/slot**; `JUMPI` pops **cond then dest**; `LOG*`/`RETURN`/`REVERT`/`SHA3_MEM` pop **size then offset**; `CREATE` pops offset-then-size | Codegen must emit pushes in the exact order each handler pops (encoded in `codegen.go` comments). |
| Top-level `REVERT` | rolls back the tx, tx included with failed receipt | **engine does not roll back entry-frame storage**; executor pre-fork treats the reverted call as UNAPPLIABLE (dropped by the producer, block carrying one rejected) | Verified: `TestTopLevelRevertDoesNotRollBackStorage` (engine level) and `core/block/vm_revert_test.go` (executor level). **Fixed by the `vm_revert_v1` fork**: the executor wraps the frame in a nested snapshot, rolls back writes+logs on revert, and includes the tx with a failed receipt (fee kept, nonce consumed) — EVM semantics. Compiled code still places every `require` before any storage write (checks-effects) as hygiene. |
| `ecrecover` / secp256k1 | precompile | **absent** — only `VERIFY_MLDSA` | No Solidity contract relying on ECDSA signatures can be honored; PQ signatures use `verify_mldsa`. |
| `SELFDESTRUCT`, `GAS`, `GASPRICE`, `BLOCKHASH`, `CHAINID`, `COINBASE` | present | mostly absent/different (`TOTALGAS` exists; no `BLOCKHASH`) | A subset language, not full Solidity. |

## Approaches considered

**(a) Reuse solc's frontend: Solidity → Yul IR → SQC-VM backend.** Rejected as
the first step. solc's Yul assumes keccak-256 for mapping/storage layout,
20-byte `address`, the EVM memory model (free-memory pointer at 0x40), EVM ABI
head/tail encoding, and a large builtin set (`keccak256`, `datacopy`,
`mload`/`mstore`, `call`, …). A Yul→SQC-VM backend means reimplementing a Yul
dialect + codegen + optimizer — months of work — for a language whose semantics
we are already breaking the moment `keccak256` becomes `sha3` (every existing
contract's storage layout changes, so "compatibility with real Solidity" is
illusory anyway). Worth revisiting **later** to reuse solc's mature parser and
optimizer, but not to bootstrap.

**(b) A Solidity-subset compiler written from scratch in Go — CHOSEN.** Smallest
honest surface: a lexer, parser and stack codegen targeting the v4 table
directly, in a **non-consensus** package. Honest about being a "Solidity-flavored
subset" (this *is* QSol, scoped to what works). No external dependency, no solc,
full control over the SHA3/32-byte/opcode-order divergences. It is the
pragmatic first rung and it produces bytecode the existing engine runs today.

**(c) Implement QSol per the repo's spec.** There is no spec to implement
against — only the dialect notes above. So (c) *is* (b) in practice; this
prototype is the first concrete QSol.

**Why not a plugin/WASM runtime** (recorded so it is not re-litigated):
`docs/plans/2026-08-27-blockpeak-settlement-module.md` rejected a WASM/plugin
contract runtime as a 6–12 month, CosmWasm-class determinism-and-gas risk. A
compiler to the *existing, audited* SQC-VM avoids introducing a second
execution engine entirely — it reuses the one the chain already runs.

## The prototype (this branch)

Package `compiler/` (non-consensus). Pipeline: **source → lexer → parser → AST
→ semantic check → stack codegen → assembler → SQC-VM v4 bytecode + ABI.**

- `lexer.go` — tokens, comments, `0x`/decimal numbers.
- `parser.go` — recursive-descent; `contract`, state vars (scalars + `mapping`),
  functions with params / `view` / `returns`, and statements
  (`=`, `require`, `return`, `emit`, `if`).
- `codegen.go` — the stack machine mapping, with every VM operand order
  documented at its use site.
- `asm.go` — a two-pass assembler; jump targets are fixed-width `PUSH2` so label
  addresses resolve in one pass (no push-width fixpoint).
- `compiler.go` — `Compile(src) (*Contract, error)`; the `Contract` carries the
  bytecode plus an ABI (function selectors, event topics) and `EncodeCall`.

### Calling convention (compiler-defined, since the ABI opcodes are unimplemented)

- **Selector**: top 4 bytes of `SHA3-256("name(type,type)")`. Runtime dispatch
  loads `CALLDATALOAD(0)`, `SHR 224`, and compares against each function's
  selector, jumping to its body; no match falls through to `REVERT`.
- **Arguments**: 32-byte big-endian words at calldata offset `4 + 32*i`.
  A 32-byte `address` argument is its raw word.
- **Return**: a single word via `MSTORE`+`RETURN` (offset 0, size 32).
- **Storage layout**: scalars occupy sequential slots `0,1,2,…`. A mapping at
  base slot `S` stores `m[k]` at `SHA3-256( pad32(k) ‖ pad32(S) )` (distinct
  per mapping because `S` differs; the two words are laid into scratch memory
  and hashed with `SHA3_MEM`). This is the SHA3 analogue of Solidity's
  keccak mapping layout.
- **Events**: `emit E(a,b)` → `LOG1` with `topic0 = SHA3-256("E(uint256,…)")`
  and the args as consecutive 32-byte data words — the shape the explorer and
  indexer already read (`vm/events.go`).

### What the prototype proves (all green — `go test ./compiler/`)

- `TestCounter` — storage read/write across calls (`inc`, `add(n)`, `get`).
- `TestToken` — a mapping, a **guarded owner action** (`require(msg.sender ==
  owner)`), balance transfer with a **revert path** (`require(bal >= amount)`),
  and **events** (`Mint`, `Transfer`) with SHA3 topics and correct data words.
- `TestUnknownSelectorReverts` — dispatcher fallthrough.
- `TestSelectorsAreSha3Based` — the ABI's selectors equal the runtime's.
- `TestCompileDeterministic` — identical source → byte-identical bytecode
  (consensus-critical: every node compiling the same source agrees).
- `TestTopLevelRevertDoesNotRollBackStorage` — the checks-effects rationale,
  proven against the real engine.

## Security / determinism model

- **The compiler is not consensus code.** It runs off-chain; only its emitted
  bytecode is deployed, and the node executes that bytecode exactly as it
  executes any `TxDeploy` payload. A compiler bug can produce a wrong contract,
  never a chain split.
- **Determinism** is the one consensus-relevant property, and it holds two ways:
  the VM is already deterministic (this is why the plugin/WASM path was
  rejected), and `Compile` is a pure function of its input (no clock, no map
  iteration in codegen order — states/functions are walked in source order),
  so the same source yields identical bytecode on every builder. A production
  toolchain should pin the compiler version in build metadata so a redeploy is
  reproducible.
- **Checks-effects ordering** is what the codegen emits (`require`s before
  writes). With the `vm_revert_v1` fork active the executor makes a top-level
  revert atomic, so the ordering is hygiene rather than a safety line — but it
  remains the only protection for embedders that run the engine without the
  executor's snapshots.

## Gap list: prototype → production

1. **Language coverage**: no internal function calls, no structs/arrays/strings,
   no `constructor` (deploy stores runtime code directly — needs an initializer
   convention or an init-code phase), no multiple return values, no explicit
   `event`/`error` declarations, no `modifier`s, no inheritance, no
   `break`/`continue`, one contract per source unit.
   Landed since: **local variables and `for`/`while` loops**. Locals are
   256-bit words in scratch memory above the mapping scratch, block-scoped,
   with slots allocated monotonically (no liveness analysis). Parameters are
   locals copied from calldata on entry, so a function can assign to its own
   parameters as in Solidity. An unbounded loop ends on the gas ceiling.
   Landed since the prototype: `/ %`, short-circuit `&& ||`, `!`, `else`,
   compound assignment, `require(cond, "reason")`, visibility modifiers
   (ignored), Solidity operator precedence, and **checked arithmetic**
   (overflow/underflow/div-by-zero revert — Solidity ≥0.8 parity, closing the
   wrap-vs-revert divergence the EVM differential test exposed).
2. **Engine gaps to close**: implement `SELECTOR`/`ABI_ENCODE`/`ABI_DECODE`
   (0x43-0x45) or delete them from the table. ~~Snapshot the top-level call
   frame so a bytecode `REVERT` is atomic~~ — **done**: the `vm_revert_v1`
   fork makes the executor snapshot the frame and include a reverted call with
   a failed receipt (`core/block/vm_revert_test.go`); schedule it in genesis
   documents alongside `module_revert_v1`.
3. **Deploy path**: a constructor/init-code convention so state can be set at
   deploy (today a separate guarded `init()` call is required, as in the token
   sample); deploy-time code-size/gas limits.
4. **Toolchain**: `cmd/sqc-solc` (source → hex + ABI JSON) and `cmd/sqc-deploy`
   (deploy / call / view / **verify** against a live node) exist. `verify` is
   the source-verification primitive: `Compile` is deterministic, so
   byte-equality against `GET /contract/{addr}/code` proves a source file
   matches a deployed contract. Still open: a node/explorer endpoint so the
   explorer UI can verify server-side (the explorer has no source-verify UI
   yet), and a disassembler round-trip test in CI.

### Live devnet deployment (splendor-quantum-devnet-1, node v0.5.2 / 65bbece)

Deployed with `sqc-deploy` and verified through the node's own
`POST /contract/verify`. All view answers match the local engine tests and the
anvil/EVM differential.

| contract | address |
|---|---|
| `examples/counter.qsol` | `SQ:99723ae98ce6aeb711194ca77cf4e326913d9d5872a4b86b533817df0a20eccf` |
| `examples/mathdemo.qsol` | `SQ:74faa98c07a5e3fc327aeb3cbb1d06de3f683bfe60dc03910a6f5348cc25cb2c` |
| `examples/token.qsol` | `SQ:b92f99b50ce70d8da77fcce32d3140e0174cb8b45ea29f1050503e9ffdf6a7bb` |
| `examples/loops.qsol` | `SQ:35b523370443270beefb5cf7ba703a23d3fd261d420db09a735e0efaf38c357e` |

Observed live: checked arithmetic (`add(2^256-1)` → `require: overflow`),
short-circuit `&&` (`safeDiv(10,0)` → `0`, no revert), require reasons in
`revert_reason`, SHA3 mapping balances after mint/transfer, and loops —
`sumTo(100)` → 5050 (12,134 gas), `halvings(1024)` → 10 via a `while` that
assigns to its own parameter, and `fill(6)` writing a mapping inside a `for`
(123,652 gas) with `squareOf(5)` → 25 and unwritten keys still 0.

`vm_revert_v1` is NOT yet scheduled on this tier, so a reverting TRANSACTION is
still dropped by the producer (no receipt, no charge) — the legacy behaviour the
fork fixes, and the reason the fork still has to be activated separately.

**An earlier set of addresses stopped verifying** when the locals/loops change
added a parameter-copy prologue to every function (counter: 111 → 117 bytes).
That is compiler-version drift, not a regression, and it is the concrete
argument for pinning the compiler version in build metadata: a verification
claim is only meaningful against a named compiler build. The addresses above
were redeployed with the current compiler and verify today.

### Fleet: 250 generated contracts, deployed and verified on the live chain

`sqc-deploy fleet -fleet-count 250 -fleet-seed 7` generated 250 distinct
contracts (varying scalars, mappings, function mixes, loops, branches, events,
guards), deployed each with a sequential nonce, and verified every one through
the node's endpoint:

    deployed 250/250 (0 submit failures)
    verified 250, mismatched 0, errored 0

That exercises what a handful of samples cannot: address derivation across 250
deploys, mempool admission and block inclusion under sustained load, and
agreement between the compiler and the node's verifier across 250 distinct
bytecodes. The in-repo `TestFleetCompilesDeploysAndVerifies` runs the same
pipeline against the engine and passes at 5000.

### Resolved: the devnet faucet jam

A `TxDeploy` from the faucet at nonce 9 was admitted to the mempool but skipped
by `ProduceBlock` every round, and could not be evicted — `PruneStale` only
drops nonces BELOW the committed one, and this one's nonce WAS the committed
one, with no replace-by-fee. The account, which is the devnet faucet's signer,
could send nothing. The shape is the one `mempool.PruneStale`'s own comment
describes ("observed live as a jammed heartbeat").

The v0.5.2 rollout cleared it, as predicted: the mempool is in memory, so the
restart dropped the stuck transaction. Worth remembering that a node restart is
the only remedy — and that `sqc-deploy`'s `awaitNonce` now reports a stalled
nonce as a jam rather than waiting on something that cannot resolve itself.
5. **Optimizer**: one pass so far — **mapping-slot caching**. A slot is
   `SHA3-256(pad32(key) ‖ pad32(baseSlot))` and depends only on the key and the
   base slot, never on what is stored there, so it is invariant for as long as
   the key is. Codegen now derives it once per straight-line region and reuses
   it from a memory word. Only provably stable keys qualify (`msg.sender`,
   `msg.value`, a literal, or a name never assigned in that function), and the
   live set is saved and restored around branches and loop bodies so a slot
   derived inside an `if` is never loaded by code that runs when the branch did
   not. Measured on devnet, same chain and same call: token `transfer`
   **44,618 → 43,351 gas** and the contract **445 → 413 bytes**; the sender's
   slot went from three derivations to one. Still naive elsewhere: redundant
   `PUSH`/`POP`, no constant folding, no common-subexpression elimination
   beyond mapping slots.

### Mapping access is dominated by SHA3, priced per byte

Worth a decision independent of the compiler. `vm/engine.go` charges
`SHA3PerByte * size` for `SHA3_MEM`, so deriving one mapping slot over 64 bytes
costs `30 + 64*6 = 414` gas. The EVM prices the same hash **per word**:
`30 + 6*ceil(64/32) = 42`. SHA3-256 and keccak-256 are the same permutation and
cost essentially the same to compute, so the 10x is a pricing choice rather than
a cost recovery.

The consequence is that on this chain a mapping read is mostly hashing: a
measured `balanceOf` costs 661 gas, of which 414 is the derivation and 100 the
`SLOAD`. Every token, registry and balance table pays it on every access. If the
per-byte rate is deliberate the caching above matters more, not less; if it is
an oversight, switching `SHA3_MEM` to per-word pricing is a one-line change with
a far larger effect than any compiler pass — but it IS a consensus change and
would need a fork gate.
6. **Safety analysis**: reentrancy is possible via `CALL` once external calls
   are added; add checks-effects enforcement and optional reentrancy guards.
7. **Revisit approach (a)** once the subset is proven in production: reusing
   solc's parser/optimizer via a Yul→SQC-VM backend could widen coverage without
   hand-writing a full frontend — but only after the SHA3/32-byte/opcode-order
   backend is battle-tested here first.

## Recommendation

Land the subset compiler as the first rung of QSol. It lets internal contracts
(a bridge, a token, a vesting vault) ship as **deployed, auditable-as-source
bytecode** on the existing VM instead of as consensus-code modules — reserving
native Go modules for things that genuinely must be in consensus. Close engine
gap #2 (entry-frame revert atomicity) early, because it is a latent hazard for
*any* contract, module-authored or compiled.

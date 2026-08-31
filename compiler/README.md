# compiler — a QSol-subset → SQC-VM v4 compiler (prototype)

A from-scratch compiler for a Solidity-flavored subset ("QSol") that emits
SQC-VM v4 bytecode. **It is not consensus code** — it runs off-chain, and only
the bytecode it produces is deployed (through an ordinary `TxDeploy`). The node
executes that bytecode exactly as it executes any other. A compiler bug can
produce a wrong contract, never a chain split.

Full rationale and the production gap list:
`docs/plans/2026-08-31-sqcvm-contract-compiler.md`.

## Use

```bash
go run ./cmd/sqc-solc compiler/examples/token.qsol      # hex + ABI JSON
go run ./cmd/sqc-solc -hex-only compiler/examples/token.qsol
```

Or as a library:

```go
c, err := compiler.Compile(src)   // c.Bytecode is the TxDeploy payload
data, _ := compiler.EncodeCall(c.Functions[0], arg0, arg1) // calldata
```

## Language (this subset)

- One `contract Name { … }` per source unit.
- State: scalar `uint256` / `address` / `bool`, and `mapping(K => V)`.
- Functions with value-type params, optional `view`, optional `returns (T)`.
- Statements: assignment (`x = e;`, `m[k] = e;`), `require(cond);`,
  `return e;`, `emit E(args);`, `if (cond) { … }`.
- Expressions: `+ - *`, comparisons (`== != < <= > >=`), literals (decimal /
  `0x`), `msg.sender`, `msg.value`, params, scalars, `m[key]`.

## Conventions the compiler defines

Because the VM's `SELECTOR`/`ABI_ENCODE`/`ABI_DECODE` opcodes are declared but
unimplemented, the compiler defines the calling convention itself:

- **Selector** = top 4 bytes of `SHA3-256("name(types)")`; runtime dispatch is
  `CALLDATALOAD(0)`, `SHR 224`, compare, jump. No match → `REVERT`.
- **Args**: 32-byte words at calldata `4 + 32*i` (a 32-byte `address` is its raw
  word).
- **Storage**: scalars at sequential slots; `m[k]` at
  `SHA3-256(pad32(k) ‖ pad32(baseSlot))`.
- **Events**: `emit E(a,b)` → `LOG1`, `topic0 = SHA3-256("E(uint256,…)")`, args
  as data words.

## Known limits

No loops, internal calls, arrays/structs/strings, constructor (use a guarded
`init()`), overflow checks, or optimizer. Integer math wraps at 2^256.
**`require` must precede any storage write**: a top-level `REVERT` does not roll
back entry-frame storage (proven in `compiler_test.go`), so codegen relies on
checks-effects ordering. See the design doc's gap list.

## Files

`lexer.go` · `parser.go` · `ast.go` · `codegen.go` · `asm.go` · `compiler.go`
· `compiler_test.go` (compile → deploy on the real engine → call → assert) ·
`examples/token.qsol`.

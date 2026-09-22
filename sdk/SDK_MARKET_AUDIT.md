# SDK market / architecture audit

**Date:** 2026-09-23  
**Branch:** `sdk/hardening-audit`  
**Companion:** [SDK_SCENARIO_AUDIT.md](./SDK_SCENARIO_AUDIT.md) (automated suite results)

## Verdict

Channels for future modules are correct: `Transport → Connection → Program → registerModule` plus ConnectKit UI `Connector`. Standard production features (seed, extension, QRC-20/1155, bridge, contract, RPC resilience, gateway custodial gates, WASM pins) are at or above first-party SDK practice vs ethers/viem/wagmi-class stacks. Live DEX/Exchange need **node routing** — SDK leftover clients are hidden from public DX until then.

## Scorecard vs market practice

| Area | Grade | Notes |
|------|-------|--------|
| Extensibility channels | At | Open registry + Program; typed Session hand-wires routed modules only |
| RPC failover / sticky / timeouts | At | No health-weighted quorum (deferred) |
| Connect races / epoch fencing | At | Mutex + softDisconnect + BroadcastChannel |
| Provider inject trust | At for first-party; below WC | Spoofable inject documented; secrets refused |
| Gateway custodial boundaries | Above typical bridge demos | Public = signedTx only; EVM unlock 410 |
| WASM / key pins | At / above WASM peers | Pin required for non-local browser |
| Cross-wallet / mobile (WC) | Deferred | Documented; UI Connector is the future plug-in point |
| Public DEX / Exchange DX | Hidden (this pass) | `/defi` leftover only; not Session / root / ConnectKit |

## Honesty changes (this pass)

- Root `@splendor/sqc-sdk` no longer re-exports DEX/Exchange.
- `modules` / `bindModules` / `createSession` expose qrc20, qrc1155, bridge, contract only.
- Connect-react no longer exports `useDex` / `useExchange` / deploy-pool hooks.
- Docs and web-dapp demo aligned; leftover adapters remain under `@splendor/sqc-sdk/defi`.

## Explicit limits

- No live RPC / real Chrome extension in automated audit.
- No on-chain DEX/Exchange routing in this workstream.
- Explorer remains link-only.

# SDK market / architecture audit

**Date:** 2026-09-23  
**Branch:** `sdk/hardening-audit`  

**Authoritative deep rubric:** [SDK_DEEP_AUDIT.md](./SDK_DEEP_AUDIT.md) (~100 parameters, evidence, P0/P1 fix log).  
**Companion scenario matrix:** [SDK_SCENARIO_AUDIT.md](./SDK_SCENARIO_AUDIT.md).

## Verdict (short)

Channels for future modules remain correct: `Transport → Connection → Program → registerModule` plus ConnectKit UI `Connector`. After the deep-audit fix pass, gateway error leakage, connect-mutex overlap, seed wipe-on-fail, RPC details/URL noise, userHints, SSRF denylist, and X402/DEX honesty gaps are addressed. Live DEX still needs node routing; WalletConnect remains deferred.

## Explicit limits

See [SDK_DEEP_AUDIT.md](./SDK_DEEP_AUDIT.md) — no live RPC, no real extension, no WC, no on-chain DEX, no CI load harness.

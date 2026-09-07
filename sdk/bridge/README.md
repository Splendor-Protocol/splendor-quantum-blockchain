# @splendor/pq-bridge (deprecated)

This package is a **shim** that re-exports [`@splendor/sqc-sdk`](../sqc-sdk).

**Migrate to:**

```js
import { connect, Wallet, deployQRC20, PROTOCOL } from '@splendor/sqc-sdk';
```

`SplendorPQBridge` remains available as an alias for `Wallet`.

Docs, WASM builds, gateway, and tests live under `sdk/sqc-sdk/`.

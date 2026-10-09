---
"@splendor/connect-react": patch
---

`mockWallet()` from `@splendor/connect-react/testing` now implements `getAddress`, `getNonce` and `getLatestBlock` like a real signer, so token deploys (`useDeployToken`, `DeployTokenWidget`) work under `MockSplendorProvider` instead of failing with "Signer is missing capability getNonce".

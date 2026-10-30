---
"@splendor/connect-react": patch
---

- Peer dependency on `@splendor/sqc-sdk` is now `>=3.0.0 <4`, so a future major of the SDK is not installed against this release.
- The `create-splendor-dapp` bin shipped inside this package is deprecated and prints a notice; use `npm create splendor-dapp@latest` (package `create-splendor-dapp`) for the full scaffolder.
- Removed an unused internal helper from the provider.

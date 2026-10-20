---
"@splendor/sqc-sdk": minor
"@splendor/connect-react": minor
"@splendor/dapp-spec": minor
"create-splendor-dapp": patch
---

- sqc-sdk: `modules`, `modules.bind()` and `bindModules()` get a `qrc721` slot, and QRC-721 is registered as a built-in module.
- connect-react: new `useQRC721` and `useDeployQRC721` hooks. `useDeployCollection`, `useDeployBytecode`, `useDeployBridge` and `useModuleCall` now refuse a second send while one is signing, record unconfirmed sends in the pending list, and return `explorerUrl` and `errorMessage`. `useTxAction` is memoized, so action callbacks keep their identity across renders. `ThemeOverrides` accepts `base` so light and dark survive overrides. `SignInButton` awaits an async `onSuccess` and reports its failure through `onError`. `TokenPanel` offers tokens deployed from this browser on the current network (`getRecentTokens`, `rememberDeployedToken`). Testing: `MockSplendorProvider` takes `theme`; `mockWallet` tracks the account nonce and accepts `confirm`, `receipt`, `call`, `onSend` and a hash function.
- dapp-spec: new `contract-call` block. `TipJar` shows send errors, `MembersArea` locks again when the wallet changes and adds sign-out, `NftDrop` shows the full collection address, the deploy transaction and safe error text. Generated apps paint the full page with the chosen theme.
- create-splendor-dapp: `--set` values follow the prop type (`--set deploy.decimals=6`, `--set deploy.review=false`).

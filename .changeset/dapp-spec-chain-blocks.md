---
"@splendor/dapp-spec": minor
---

- New blocks: `nft721-gallery` (QRC-721 tokens with owners, readable before connecting) and `x402-paywall` (requests a paid resource, reads the 402 requirements, pays with `payX402` under a reader price cap and retries with `X-Payment`; without a resource URL it runs a labelled client-only demo).
- Validation: token id lists for `nft721-gallery` / `nft-panel` accept numbers and ascending ranges; `x402-paywall.resourceUrl` and `activity-feed.indexerUrl` must be https (http only for localhost, never with credentials); `token-balances.tokens` takes up to 20 `SQ:` addresses.
- New template `nft-membership` (public gallery plus a wallet-only members page) and `DEMO_NFT_ADDRESS`.

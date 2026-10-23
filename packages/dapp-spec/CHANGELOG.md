# @splendor/dapp-spec

## Unreleased

- Codegen: unique page component names (`/` and `/home` no longer collide), Next exports get real
  route links via a shared `blocks/pageHref.ts`, `spec.nav` (brand, connect button, link order,
  footer) is honoured in both shells.
- Validation: one id namespace for containers and blocks, containers-only page roots, nav and
  contract checks, stricter CSS tokens, `integer` and `httpsOnly` prop formats (NFT ids, image and
  members `apiUrl`).
- `styleToCss(style, kind)`: column `width` becomes flex-basis, row `align` sets both axes.
- Blocks: newline-separated list props, no dead disabled CTA buttons, resynced form inputs,
  cancel-safe `ContractRead`, honest `TxStatusCard`, error states in `NftGallery`.

## 0.1.0

- First release: `DappSpec` v1, `validateSpec`, `migrateSpec`, `createCatalog`, five starter
  templates, blocks (`hero`, `balance-summary`, `tip-jar`, `members-area`, `nft-drop`) and
  `generateProject` for Vite and Next.js.

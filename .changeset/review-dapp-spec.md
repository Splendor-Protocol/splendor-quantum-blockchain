---
"@splendor/dapp-spec": minor
"create-splendor-dapp": patch
---

- `generateProject` validates the spec against `catalog` first and throws on an invalid spec, naming the failing paths. Pass `validate: false` only for a spec you have already validated.
- The duplicate `landing` template is no longer listed in `TEMPLATES`; `getTemplate('landing')` (and `create-splendor-dapp --template landing`) still resolve it to `token-project` through the new `TEMPLATE_ALIASES`, so old links keep working.
- Exported block sources open external links with `rel="noopener noreferrer"`.
- README template tables list every template, including the kits and `nft-membership`.

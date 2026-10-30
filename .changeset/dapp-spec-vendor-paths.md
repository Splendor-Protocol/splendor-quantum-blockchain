---
"@splendor/dapp-spec": patch
---

`generateProject` in vendor mode now throws when a `vendorFiles` key is not a relative path under `vendor/@splendor/` (traversal, absolute paths, drive letters or backslashes), instead of copying it into the generated project.

---
"@splendor/dapp-spec": minor
---

Exported projects deploy as they are:

- Generated apps call the RPC through `/rpc/<network>` on their own origin on every host, not only on localhost. Vite exports ship `vercel.json` and `netlify.toml` with that proxy and a strict CSP (`script-src 'self' 'wasm-unsafe-eval'`, `frame-ancestors 'none'`, `nosniff`, HSTS); Next.js exports keep their rewrites and gain HSTS. New `GenerateOptions.rpcTargets`, and exported `DEFAULT_RPC_TARGETS`, `EXPORT_CSP` and `EXPORT_SECURITY_HEADERS`.
- `VITE_SQC_NETWORK` / `NEXT_PUBLIC_SQC_NETWORK` choose the network at build time (`pickNetwork` in the generated `networks.ts`), and `.env.example` lists every variable.
- `lint` (ESLint flat config), `typecheck` and (Next.js) `preview` scripts, `engines.node >= 20`, `.github/workflows/ci.yml`, a README with per-host deploy steps, and a `.gitignore` entry for the decoded vendored signer.
- Layout and pages: column widths become flex shares, every leaf gets the same `.dapp-block` wrapper as the preview, rows stack below 720px; pages can have a `description` (meta tags), the spec a `favicon`, containers a `label`; exports render a 404 page and per-page titles.

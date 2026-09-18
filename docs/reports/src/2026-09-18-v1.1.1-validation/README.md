# Source of `docs/reports/2026-09-18-v1.1.1-validation.pdf`

`report.html` + `report.css` rendered with Google Chrome headless (`build.sh`,
`--headless=new --print-to-pdf`, A4). Charts under `charts/` are black-and-white
SVGs produced from the devnet stress-test exports (`scripts/aws/devnet-stress/`);
`charts.py` holds the plotting helpers. The body font is Manrope; copy the
`latin-400/500/600/700` woff2 files from `@fontsource/manrope` into `fonts/`
before rebuilding, or the page falls back to the system sans-serif.

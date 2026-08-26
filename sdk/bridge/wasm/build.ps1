# Build Splendor QC WASM signer for Node.js / browser
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Out = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

# Pin the toolchain. The artifact digest is Go-release-sensitive, so a rebuild
# under whatever Go happens to be installed invalidates the wasmSha256 pin in
# sqc.networks.json for everyone else. GOTOOLCHAIN makes any Go >= 1.21 fetch
# and use exactly this release. Keep in step with wasm/build.sh.
if (-not $env:SQC_GO_TOOLCHAIN) { $env:SQC_GO_TOOLCHAIN = "go1.25.0" }
$env:GOTOOLCHAIN = $env:SQC_GO_TOOLCHAIN

$env:GOOS = "js"
$env:GOARCH = "wasm"

Write-Host "Building sqc_signer.wasm..."
# -trimpath keeps the build byte-reproducible across machines, matching build.sh.
go build -trimpath -o (Join-Path $Out "sqc_signer.wasm") (Join-Path $Root "main.go")
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

$goroot = (go env GOROOT).Trim()
$candidates = @(
  (Join-Path $goroot "misc\wasm\wasm_exec.js"),
  (Join-Path $goroot "lib\wasm\wasm_exec.js")
)
$copied = $false
foreach ($c in $candidates) {
  if (Test-Path $c) {
    Copy-Item $c (Join-Path $Out "wasm_exec.js") -Force
    $copied = $true
    break
  }
}
if (-not $copied) { throw "wasm_exec.js not found under GOROOT=$goroot" }

# Clear GOOS/GOARCH/GOTOOLCHAIN so subsequent native builds aren't broken
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue

# Print the digests the pins must carry, so a rebuild is never left unsynced.
$shaWasm = (Get-FileHash (Join-Path $Out "sqc_signer.wasm") -Algorithm SHA256).Hash.ToLower()
$shaExec = (Get-FileHash (Join-Path $Out "wasm_exec.js") -Algorithm SHA256).Hash.ToLower()

Write-Host "Done: $Out\sqc_signer.wasm   sha256 $shaWasm"
Write-Host "      $Out\wasm_exec.js      sha256 $shaExec"
Write-Host ""
Write-Host "Next: npm run sync:config, then refresh the wallet-extension copy and its pins."

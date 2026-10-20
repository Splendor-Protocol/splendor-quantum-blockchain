# Build Splendor QC WASM signer for Node.js / browser
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Out = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

# Pin the toolchain. The artifact digest is Go-release-sensitive, so a rebuild
# under whatever Go happens to be installed invalidates the wasmSha256 pin in
# sqc.networks.json for everyone else. GOTOOLCHAIN makes any Go >= 1.21 fetch
# and use exactly this release. Keep in step with wasm/build.sh.
if (-not $env:SQC_GO_TOOLCHAIN) { $env:SQC_GO_TOOLCHAIN = "go1.26.10" }
$env:GOTOOLCHAIN = $env:SQC_GO_TOOLCHAIN

$env:GOOS = "js"
$env:GOARCH = "wasm"

# go build resolves the module from the working directory, not from the path of
# the file it is handed. Keep in step with wasm/build.sh.
Set-Location (Join-Path $Root "..\..\..")

Write-Host "Building sqc_signer.wasm..."
# -trimpath drops the filesystem path; -ldflags=-buildid= drops Go's build ID,
# whose action-ID half hashes the build environment rather than the source and
# so differs between two machines compiling identical source with the identical
# pinned toolchain. Without it the sha256 pin is not reproducible. Keep both
# flags in step with wasm/build.sh.
go build -trimpath -ldflags=-buildid= -o (Join-Path $Out "sqc_signer.wasm") (Join-Path $Root "main.go")
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
Write-Host "Next: npm run sync:config, then in splendor-quantum-wallet-extension: npm run sync:signer"

# Build Splendor QC WASM signer for Node.js / browser
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Out = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

$env:GOOS = "js"
$env:GOARCH = "wasm"

Write-Host "Building sqc_signer.wasm..."
go build -o (Join-Path $Out "sqc_signer.wasm") (Join-Path $Root "main.go")
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

# Clear GOOS/GOARCH so subsequent native builds aren't broken
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

Write-Host "Done: $Out\sqc_signer.wasm"
Write-Host "      $Out\wasm_exec.js"

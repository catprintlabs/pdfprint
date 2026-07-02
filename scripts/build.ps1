<#
.SYNOPSIS
  Build the pdfprint tools on Windows (where `make` usually isn't installed).

.DESCRIPTION
  Builds pdfprint.exe and stamp.exe. Locates the Go toolchain on PATH or at the
  default install dir (C:\Program Files\Go), so it works in a fresh shell where
  the installer's PATH entry hasn't been picked up yet.

.PARAMETER OutDir
  Where to write the executables (default: repo root).

.PARAMETER Clean
  Remove the built executables instead of building.

.EXAMPLE
  ./scripts/build.ps1
.EXAMPLE
  ./scripts/build.ps1 -OutDir dist
#>
[CmdletBinding()]
param(
  [string]$OutDir = ".",
  [switch]$Clean
)

$ErrorActionPreference = "Stop"

# Run from the repo root (this script lives in scripts/).
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

$targets = @(
  @{ Name = "pdfprint.exe"; Pkg = "./cmd/pdfprint" },
  @{ Name = "stamp.exe";    Pkg = "./cmd/stamp" }
)

if ($Clean) {
  foreach ($t in $targets) {
    $p = Join-Path $OutDir $t.Name
    if (Test-Path $p) { Remove-Item $p -Force; Write-Host "removed $p" }
  }
  return
}

# Locate Go: PATH first, then the default install location.
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) {
  $cand = Join-Path $env:ProgramFiles "Go\bin\go.exe"
  if (Test-Path $cand) { $go = $cand }
}
if (-not $go) {
  Write-Error "Go not found on PATH or in `"$env:ProgramFiles\Go`". Install Go 1.22+ (winget install GoLang.Go)."
  exit 1
}
Write-Host "go:     $go"
Write-Host ("version: " + (& $go version))

if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir | Out-Null }

foreach ($t in $targets) {
  $out = Join-Path $OutDir $t.Name
  Write-Host "build:  $out  <-  $($t.Pkg)"
  & $go build -o $out $t.Pkg
  if ($LASTEXITCODE -ne 0) { Write-Error "build failed for $($t.Pkg)"; exit $LASTEXITCODE }
}

Get-ChildItem ($targets | ForEach-Object { Join-Path $OutDir $_.Name }) |
  Select-Object Name, @{n = 'MB'; e = { [math]::Round($_.Length / 1MB, 2) } }, LastWriteTime |
  Format-Table -AutoSize

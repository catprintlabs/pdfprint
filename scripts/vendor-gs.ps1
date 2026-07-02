<#
.SYNOPSIS
  Assemble the minimal Ghostscript (2 files) for bundling next to pdfprint.exe.

.DESCRIPTION
  The Windows Ghostscript build ROM-embeds its resources into gsdll64.dll, so a
  working gs is just gswin64c.exe + gsdll64.dll (~24 MB). This copies those into
  an output folder (default vendor\gs\) that a packager (e.g. Electron's
  extraResources) can ship. pdfprint auto-detects gs at <exedir>\gs\gswin64c.exe.

.PARAMETER GsBin
  Path to an installed gs bin dir (...\gs\gs<ver>\bin). Auto-detected if omitted.

.PARAMETER OutDir
  Destination folder (default: vendor\gs under the repo root).

.EXAMPLE
  ./scripts/vendor-gs.ps1

.EXAMPLE
  ./scripts/vendor-gs.ps1 -GsBin 'C:\Program Files\gs\gs10.07.1\bin' -OutDir dist\gs
#>
[CmdletBinding()]
param(
  [string]$GsBin,
  [string]$OutDir
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $repo 'vendor\gs' }

# Locate an installed gs bin dir if not given.
if (-not $GsBin) {
  $pat = Join-Path $env:ProgramFiles 'gs\gs*\bin\gswin64c.exe'
  $exe = Get-ChildItem $pat -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -Last 1
  if (-not $exe) {
    throw 'No Ghostscript found under Program Files\gs. Install it or pass -GsBin.'
  }
  $GsBin = $exe.DirectoryName
}

$need = @('gswin64c.exe', 'gsdll64.dll')
foreach ($f in $need) {
  $src = Join-Path $GsBin $f
  if (-not (Test-Path $src)) { throw "Missing $f in $GsBin - not a valid gs bin dir." }
}

if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }
foreach ($f in $need) { Copy-Item (Join-Path $GsBin $f) (Join-Path $OutDir $f) -Force }

$sizeMB = [math]::Round(((Get-ChildItem $OutDir -File | Measure-Object Length -Sum).Sum) / 1MB, 1)
$ver = & (Join-Path $OutDir 'gswin64c.exe') '--version'
Write-Host ('vendored gs {0} to {1}  [{2} MB]' -f $ver, $OutDir, $sizeMB)
Get-ChildItem $OutDir | Select-Object Name, @{n = 'MB'; e = { [math]::Round($_.Length / 1MB, 2) } } | Format-Table -AutoSize

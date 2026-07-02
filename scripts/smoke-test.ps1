<#
.SYNOPSIS
  Windows smoke test: stamp the ruler fixture with the print command + timestamp,
  then print the stamped page so the paper documents exactly what produced it.

.DESCRIPTION
  The equivalent of `make print-test` for Windows, where make usually isn't
  installed. Requires Go and Ghostscript on PATH (pdfprint auto-detects gs).

.EXAMPLE
  ./scripts/smoke-test.ps1 -Printer "Xerox VersaLink C620 (FC:90:A7)"
  # Uses the existing printer name; auto-routes WSD/V4 queues to raw TCP.

.EXAMPLE
  ./scripts/smoke-test.ps1 -HostIp 10.0.1.151
  # Direct raw-TCP (AppSocket/9100); no queue needed.
#>
[CmdletBinding()]
param(
  [string]$Printer,
  [string]$HostIp,                       # note: $Host is reserved in PowerShell
  [string]$Device   = "pxlmono",
  [string]$PageSize = "Legal",
  [string]$Fixture  = "testdata\legal_ruler.pdf"
)

if (-not $Printer -and -not $HostIp) {
  Write-Error 'Pass -Printer "<name>" or -HostIp <ip>'; exit 2
}

# Build the target args and a human-readable command label for the stamp.
if ($Printer) {
  $target   = @('--printer', $Printer)
  $cmdLabel = "pdfprint --scale none --device $Device --page-size $PageSize --printer `"$Printer`" $Fixture"
} else {
  $target   = @('--host', $HostIp)
  $cmdLabel = "pdfprint --scale none --device $Device --page-size $PageSize --host $HostIp $Fixture"
}

$job = "testdata\smoke.out.pdf"   # gitignored (testdata/*.out.*)

# 1. Stamp the fixture (records command, timestamp, host, fixture name).
go run ./cmd/stamp --cmd $cmdLabel --line "fixture: $Fixture" -o $job $Fixture
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# 2. Print the stamped page.
Write-Host "+ $cmdLabel"
go run ./cmd/pdfprint --scale none --device $Device --page-size $PageSize @target $job
exit $LASTEXITCODE

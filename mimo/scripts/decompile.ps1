param(
    [Parameter(Mandatory=$true)][string]$DexName,
    [Parameter(Mandatory=$true)][string]$OutputName
)
$ErrorActionPreference = 'Stop'
$workspace = Split-Path -Parent $PSScriptRoot
$dexFile = Get-Item -LiteralPath (Join-Path $workspace "analysis/extent-dex/$DexName") -ErrorAction SilentlyContinue
if (-not $dexFile) {
    $dexFile = Get-ChildItem -LiteralPath (Join-Path $workspace 'analysis/runtime-dex-root') -Recurse -Filter $DexName | Select-Object -First 1
}
if (-not $dexFile) { throw "Missing DEX: $DexName" }
$env:JAVA_HOME = 'C:\Users\admin\.jdks\jbr-17.0.14'
$env:JAVA_OPTS = '-Xmx5g'
$jadx = Join-Path $workspace 'tools/jadx/bin/jadx.bat'
$outputDirectory = Join-Path $workspace "analysis/$OutputName"
$logFile = Join-Path $workspace "analysis/$OutputName.log"
& $jadx -d $outputDirectory --no-res '-Pdex-input.verify-checksum=no' -j 6 $dexFile.FullName *> $logFile
Get-Content -LiteralPath $logFile -Tail 8

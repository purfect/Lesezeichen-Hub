$ErrorActionPreference = 'Stop'

$appDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runtimeDir = Join-Path $appDir '.runtime'
$pidFile = Join-Path $runtimeDir 'lesezeichen.pid'
$addrFile = Join-Path $runtimeDir 'lesezeichen.addr'
$exeCandidates = @(
    (Join-Path $appDir 'Lesezeichen-Hub.exe'),
    (Join-Path $appDir 'lesezeichen.exe')
)

$legacyPidFile = Join-Path $appDir 'lesezeichen.pid'
$legacyAddrFile = Join-Path $appDir 'lesezeichen.addr'

if (-not (Test-Path $pidFile) -and (Test-Path $legacyPidFile)) {
    $pidFile = $legacyPidFile
    $addrFile = $legacyAddrFile
}

if (-not (Test-Path $pidFile)) {
    Write-Host 'Keine PID-Datei gefunden. Es scheint nichts zu laufen.'
    exit 0
}

$pidRaw = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1).Trim()
if ($pidRaw -notmatch '^\d+$') {
    Write-Host 'PID-Datei ist ungueltig.' -ForegroundColor Yellow
    Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
    Remove-Item $addrFile -Force -ErrorAction SilentlyContinue
    exit 1
}

$targetProcessId = [int]$pidRaw
$proc = Get-Process -Id $targetProcessId -ErrorAction SilentlyContinue
if ($null -ne $proc) {
    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $targetProcessId" -ErrorAction SilentlyContinue
    $processPath = if ($null -ne $processInfo) { [string]$processInfo.ExecutablePath } else { '' }
    $expectedProcess = $exeCandidates | Where-Object { (Test-Path $_) -and $processPath -and ($processPath.Trim() -ieq $_.Trim()) } | Select-Object -First 1

    if ($null -eq $expectedProcess) {
        Write-Host 'PID-Datei verweist nicht auf die erwartete Lesezeichen-Hub-EXE. Prozess wird nicht beendet.' -ForegroundColor Yellow
        Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
        Remove-Item $addrFile -Force -ErrorAction SilentlyContinue
        exit 1
    }

    Stop-Process -Id $targetProcessId -Force -ErrorAction SilentlyContinue
    Write-Host "Lesezeichen-Hub gestoppt. PID: $targetProcessId"
} else {
    Write-Host "Prozess mit PID $targetProcessId wurde nicht gefunden."
}

Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
Remove-Item $addrFile -Force -ErrorAction SilentlyContinue

# Clean up legacy root runtime files as well.
Remove-Item $legacyPidFile -Force -ErrorAction SilentlyContinue
Remove-Item $legacyAddrFile -Force -ErrorAction SilentlyContinue
exit 0

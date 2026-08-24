param(
    [int]$Port = 3233
)

$ErrorActionPreference = 'Stop'

$appDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runtimeDir = Join-Path $appDir '.runtime'
$exeCandidates = @(
    'Lesezeichen-Hub.exe',
    'lesezeichen.exe'
)
$pidFile = Join-Path $runtimeDir 'lesezeichen.pid'
$addrFile = Join-Path $runtimeDir 'lesezeichen.addr'
$logFile = Join-Path $runtimeDir 'lesezeichen.log'
$errLogFile = Join-Path $runtimeDir 'lesezeichen.err.log'
$addr = "127.0.0.1:$Port"

$exe = $null
foreach ($candidate in $exeCandidates) {
    $candidatePath = Join-Path $appDir $candidate
    if (Test-Path $candidatePath) {
        $exe = $candidatePath
        break
    }
}

if (-not (Test-Path $runtimeDir)) {
    New-Item -ItemType Directory -Path $runtimeDir -Force | Out-Null
}

function Test-HubReady {
    param([string]$Address)

    try {
        Invoke-WebRequest -UseBasicParsing -Uri "http://$Address/api/state" -TimeoutSec 1 | Out-Null
        return $true
    } catch {
        return $false
    }
}

function Test-ExpectedHubProcess {
    param(
        [int]$ProcessId,
        [string]$ExpectedPath
    )

    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
    if ($null -eq $processInfo -or [string]::IsNullOrWhiteSpace($processInfo.ExecutablePath)) {
        return $false
    }

    return $processInfo.ExecutablePath.Trim() -ieq $ExpectedPath.Trim()
}

if (-not (Test-Path $exe)) {
    Write-Host 'Keine EXE gefunden (erwartet: Lesezeichen-Hub.exe oder lesezeichen.exe).' -ForegroundColor Red
    Write-Host 'Bitte zuerst bauen, z.B.: go build -o Lesezeichen-Hub.exe .'
    exit 1
}

if (Test-Path $pidFile) {
    $oldPidRaw = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1).Trim()
    if ($oldPidRaw -match '^\d+$') {
        $oldPid = [int]$oldPidRaw
        $oldProcess = Get-Process -Id $oldPid -ErrorAction SilentlyContinue

        if ($null -ne $oldProcess -and (Test-ExpectedHubProcess -ProcessId $oldPid -ExpectedPath $exe)) {
            $oldAddr = ''
            if (Test-Path $addrFile) {
                $oldAddr = (Get-Content $addrFile -ErrorAction SilentlyContinue | Select-Object -First 1).Trim()
            }

            if ($oldAddr -ieq $addr -and (Test-HubReady -Address $addr)) {
                Write-Host "Lesezeichen-Hub laeuft bereits auf $addr (PID $oldPid)."
                Start-Process "http://$addr" | Out-Null
                exit 0
            }

            Write-Host "Stoppe alten Prozess PID $oldPid..."
            Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue
            Start-Sleep -Milliseconds 300
        } elseif ($null -ne $oldProcess) {
            Write-Host "PID-Datei verweist auf einen anderen Prozess. Dieser wird nicht beendet." -ForegroundColor Yellow
        }
    }

    Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
    Remove-Item $addrFile -Force -ErrorAction SilentlyContinue
}

# Legacy cleanup from old versions that wrote runtime files to project root.
Remove-Item (Join-Path $appDir 'lesezeichen.pid') -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $appDir 'lesezeichen.addr') -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $appDir 'lesezeichen.log') -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $appDir 'lesezeichen.err.log') -Force -ErrorAction SilentlyContinue

$env:ADDR = $addr
$process = Start-Process -FilePath $exe -WorkingDirectory $appDir -WindowStyle Hidden -PassThru -RedirectStandardOutput $logFile -RedirectStandardError $errLogFile

$process.Id | Set-Content $pidFile -Encoding ascii
$addr | Set-Content $addrFile -Encoding ascii

$ready = $false
for ($i = 0; $i -lt 50; $i++) {
    if (Test-HubReady -Address $addr) {
        $ready = $true
        break
    }

    if ($process.HasExited) {
        break
    }

    Start-Sleep -Milliseconds 200
}

if (-not $ready) {
    Write-Host "Start fehlgeschlagen oder Server nicht erreichbar auf $addr." -ForegroundColor Red
    if (Test-Path $logFile) {
        Write-Host '--- stdout (letzte Zeilen) ---'
        Get-Content $logFile -Tail 30
    }
    if (Test-Path $errLogFile) {
        Write-Host '--- stderr (letzte Zeilen) ---'
        Get-Content $errLogFile -Tail 30
    }
    exit 1
}

Write-Host "Lesezeichen-Hub gestartet auf $addr. PID: $($process.Id)"
Start-Process "http://$addr" | Out-Null
exit 0

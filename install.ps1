# Windows: build the bridge and register it to start at logon (Task Scheduler).
#   .\install.ps1              build + register + start
#   .\install.ps1 -Uninstall   stop + remove the task
# macOS: use install.sh.
#
# First time on a machine: after the build, run whatsapp-bridge\whatsapp-bridge.exe once
# in a terminal to scan the QR code, close it with Ctrl+C, then run this script again.
param([switch]$Uninstall)
$ErrorActionPreference = 'Stop'

$Dir    = $PSScriptRoot
$Bridge = Join-Path $Dir 'whatsapp-bridge'
$Exe    = Join-Path $Bridge 'whatsapp-bridge.exe'
$Task   = 'WhatsAppBridge'

function Stop-Bridge {
    Stop-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue
    Get-Process -Name 'whatsapp-bridge' -ErrorAction SilentlyContinue | Stop-Process -Force
}

if ($Uninstall) {
    Stop-Bridge
    Unregister-ScheduledTask -TaskName $Task -Confirm:$false -ErrorAction SilentlyContinue
    Write-Host "removed $Task"
    exit 0
}

$Config = Join-Path $Dir 'config.json'
if (-not (Test-Path $Config)) {
    $cfg = Get-Content (Join-Path $Dir 'config.example.json') -Raw | ConvertFrom-Json
    $claude = Get-Command claude -ErrorAction SilentlyContinue
    if ($claude) { $cfg.claude_bin = $claude.Source }
    $cfg | ConvertTo-Json | Set-Content $Config -Encoding UTF8
    Write-Host 'created config.json - set triage_dir (the repo Claude should investigate in) before relying on replies'
}

# build first: a compile error must not leave the running bridge stopped
Push-Location $Bridge
try {
    $env:CGO_ENABLED = '0'
    go build -o whatsapp-bridge.exe .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally { Pop-Location }

Stop-Bridge
New-Item -ItemType Directory -Force (Join-Path $Bridge 'store') | Out-Null

# cmd /c only to redirect the output to a log file; the task itself runs hidden
$Log     = Join-Path $Bridge 'store\bridge.log'
$Action  = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c `"`"$Exe`" >> `"$Log`" 2>&1`"" -WorkingDirectory $Bridge
$Trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$Settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -Hidden
Register-ScheduledTask -TaskName $Task -Action $Action -Trigger $Trigger -Settings $Settings -Force | Out-Null
Start-ScheduledTask -TaskName $Task
Write-Host "started $Task - admin page: http://127.0.0.1:8765 (log: $Log)"

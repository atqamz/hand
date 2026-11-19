[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Setup,
    [Parameter(Mandatory)][string]$Work,
    [switch]$Iroh
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true') { throw 'check-setup: this installs into the real profile; it runs only in CI' }

$app = Join-Path $Work 'app'
$bin = Join-Path $app 'bin'
$programs = [Environment]::GetFolderPath('Programs')
$lnk = Join-Path $programs 'Hand.lnk'
$deskLnk = Join-Path ([Environment]::GetFolderPath('Desktop')) 'Hand.lnk'
$description = 'Hand, the supervisor window for your agent fleets'
$icon = Join-Path $PSScriptRoot '..\..\packaging\windows\hand.ico'
$secondhand = Join-Path $env:USERPROFILE '.secondhand'
$drafts = Join-Path $env:LOCALAPPDATA 'hand-desktop\drafts'
$fleet = Join-Path $Work 'fleet'
$hadSecondhand = Test-Path -LiteralPath $secondhand
$keep = @(
    (Join-Path $secondhand 'setup-check\keep.txt'),
    (Join-Path $drafts 'setup-check.txt'),
    (Join-Path $fleet 'hand.db')
)
$uninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{3BB75498-076C-47AD-916A-C35EDB756592}_is1'

function Fail([string]$Message) { throw "check-setup: $Message" }
function Expect([bool]$Condition, [string]$Message) { if (-not $Condition) { Fail $Message } }

function Get-UserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    try { return [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) }
    finally { $key.Close() }
}
function Get-PathCount { @((Get-UserPath).Split(';', [StringSplitOptions]::RemoveEmptyEntries) | Where-Object { $_.TrimEnd('\') -eq $bin }).Count }
function Test-UninstallKey {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($uninstallKey)
    if ($key) { $key.Close() }
    return [bool]$key
}
function Read-Link([string]$Path) {
    $l = (New-Object -ComObject WScript.Shell).CreateShortcut($Path)
    return @{ Target = $l.TargetPath; Dir = $l.WorkingDirectory; Icon = $l.IconLocation; Description = $l.Description }
}
function Write-Link([string]$Path, [string]$Text) {
    $l = (New-Object -ComObject WScript.Shell).CreateShortcut($Path)
    $l.TargetPath = (Join-Path $env:SystemRoot 'System32\notepad.exe')
    $l.Description = $Text
    $l.Save()
}
function Invoke-Setup([string[]]$Extra, [string]$Log) {
    $p = Start-Process -FilePath $Setup -ArgumentList (@('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/DIR=`"$app`"", "/LOG=`"$Log`"") + $Extra) -Wait -PassThru
    return $p.ExitCode
}
function Invoke-Uninstall([string]$Log) {
    $p = Start-Process -FilePath (Join-Path $app 'unins000.exe') -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=`"$Log`"" -Wait -PassThru
    return $p.ExitCode
}
function Wait-Until([scriptblock]$Done, [int]$Seconds = 60) {
    $end = [DateTime]::UtcNow.AddSeconds($Seconds)
    while (-not (& $Done)) {
        if ([DateTime]::UtcNow -gt $end) { return $false }
        Start-Sleep -Milliseconds 500
    }
    return $true
}
function Expect-Installed([bool]$Desktop) {
    foreach ($n in 'hand.exe', 'hand-desktop.exe', 'hand.ico') { Expect (Test-Path -LiteralPath (Join-Path $bin $n)) "$n is not in $bin" }
    Expect ((Test-Path -LiteralPath (Join-Path $bin 'hand-iroh.exe')) -eq [bool]$Iroh) "hand-iroh.exe should $(if ($Iroh) { 'be' } else { 'not be' }) in $bin"
    Expect (Test-Path -LiteralPath (Join-Path $app 'unins000.exe')) 'no uninstaller'
    Expect ((Get-FileHash -LiteralPath (Join-Path $bin 'hand.ico')).Hash -eq (Get-FileHash -LiteralPath $icon).Hash) 'hand.ico is not the one in the repository'
    Expect (Test-UninstallKey) 'no uninstall entry in HKCU'
    Expect ((Get-PathCount) -eq 1) "the user PATH holds $(Get-PathCount) entries for $bin, want 1"
    $first = @(& (Join-Path $bin 'hand.exe') version)[0]
    Expect ($first -match '^version: \d+\.\d+\.\d+$') "hand.exe version printed '$first'"
    Expect ((Test-Path -LiteralPath $deskLnk) -eq $Desktop) "the desktop icon should $(if ($Desktop) { 'exist' } else { 'not exist' })"
}
function Expect-Removed {
    Expect (Wait-Until { -not (Test-Path -LiteralPath $app) }) "$app is still there"
    Expect (-not (Test-Path -LiteralPath $deskLnk)) 'the desktop icon is still there'
    Expect (-not (Test-UninstallKey)) 'the uninstall entry is still in HKCU'
    Expect ((Get-PathCount) -eq 0) "$bin is still on the user PATH"
}
function Expect-Kept {
    foreach ($k in $keep) { Expect (Test-Path -LiteralPath $k) "$k was removed" }
}

$logs = Join-Path $Work 'logs'
New-Item -ItemType Directory -Path $logs, (Split-Path $keep[0]), $drafts, $fleet -Force | Out-Null
foreach ($k in $keep) { Set-Content -LiteralPath $k -Value 'keep' }
Expect (-not (Test-Path -LiteralPath $lnk)) "$lnk exists before the install"
Expect ((Get-PathCount) -eq 0) "$bin is on the user PATH before the install"
$board = $null
$stand = $null
try {
    Write-Host '== silent install'
    Expect ((Invoke-Setup @() (Join-Path $logs 'install.log')) -eq 0) 'the silent install failed'
    Expect-Installed $false
    $l = Read-Link $lnk
    Expect ($l.Target -eq (Join-Path $bin 'hand-desktop.exe')) "the shortcut targets $($l.Target)"
    Expect ($l.Dir -eq $bin) "the shortcut starts in $($l.Dir)"
    Expect ($l.Icon -eq "$(Join-Path $bin 'hand.ico'),0") "the shortcut's icon is $($l.Icon)"
    Expect ($l.Description -eq $description) "the shortcut's description is '$($l.Description)'"
    $channel = ''
    foreach ($line in @(& (Join-Path $bin 'hand.exe') version)) { if ($line -match '^channel: (\S+)\s*$') { $channel = $Matches[1] } }
    if ($channel -eq 'edge') {
        $env:SECONDHAND_HOME = Join-Path $Work 'home'
        New-Item -ItemType Directory -Path $env:SECONDHAND_HOME, (Join-Path $Work 'cwd') -Force | Out-Null
        Push-Location (Join-Path $Work 'cwd')
        try { $doctor = (@(& (Join-Path $bin 'hand.exe') doctor 2>&1 | Out-String) -join '') }
        finally { Pop-Location }
        Expect ($doctor -match '(?m)^\s*start menu shortcut,ok,') "hand doctor does not call the shortcut ok:`n$doctor"
    }

    Write-Host '== silent install over the install'
    Expect ((Invoke-Setup @() (Join-Path $logs 'reinstall.log')) -eq 0) 'the second silent install failed'
    Expect-Installed $false

    Write-Host '== a Board of this install blocks the uninstall'
    $env:SECONDHAND_HOME = Join-Path $Work 'home'
    New-Item -ItemType Directory -Path $env:SECONDHAND_HOME -Force | Out-Null
    $board = Start-Process -FilePath (Join-Path $bin 'hand.exe') -ArgumentList 'board', '--addr', '127.0.0.1:0' -WorkingDirectory (Join-Path $Work 'cwd') -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 5
    Expect (-not $board.HasExited) 'the Board did not stay up'
    $code = Invoke-Uninstall (Join-Path $logs 'uninstall-blocked.log')
    Expect ($code -ne 0) 'the uninstall ran while a Board was up'
    Expect (Test-Path -LiteralPath (Join-Path $bin 'hand.exe')) 'the blocked uninstall removed hand.exe'
    Expect ((Get-Content -Raw -LiteralPath (Join-Path $logs 'uninstall-blocked.log')) -match 'hand board stop') 'the log does not name hand board stop'
    Expect ((Get-PathCount) -eq 1) 'the blocked uninstall changed the user PATH'
    & (Join-Path $bin 'hand.exe') board stop
    Expect (Wait-Until { $board.Refresh(); $board.HasExited } 30) 'hand board stop did not end the Board'
    $board = $null
    Remove-Item Env:SECONDHAND_HOME

    Write-Host '== an open window is closed, leftovers go, kept folders stay'
    Copy-Item -LiteralPath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -Destination (Join-Path $bin 'hand-desktop.exe') -Force
    $form = 'Add-Type -AssemblyName System.Windows.Forms; [Windows.Forms.Application]::Run((New-Object Windows.Forms.Form))'
    $stand = Start-Process -FilePath (Join-Path $bin 'hand-desktop.exe') -ArgumentList '-NoProfile', '-Command', $form -PassThru
    Expect (Wait-Until { $stand.Refresh(); $stand.MainWindowHandle -ne 0 } 30) 'the stand-in window did not open'
    Set-Content -LiteralPath (Join-Path $bin 'hand.exe.old') -Value 'old'
    Set-Content -LiteralPath (Join-Path $bin 'hand-iroh.exe.old-0123') -Value 'old'
    Expect ((Invoke-Uninstall (Join-Path $logs 'uninstall.log')) -eq 0) 'the silent uninstall failed'
    Expect (Wait-Until { $stand.Refresh(); $stand.HasExited } 30) 'the uninstall left the window open'
    $stand = $null
    Expect-Removed
    Expect (-not (Test-Path -LiteralPath $lnk)) 'the Start Menu shortcut is still there'
    Expect-Kept

    Write-Host '== a shortcut that is not Hand''s, and the desktop icon'
    Write-Link $lnk 'not Hand'
    Expect ((Invoke-Setup @('/TASKS="desktopicon"') (Join-Path $logs 'install-foreign.log')) -eq 0) 'the install beside a foreign shortcut failed'
    Expect-Installed $true
    Expect ((Read-Link $lnk).Description -eq 'not Hand') 'the install replaced a Hand.lnk that is not Hand''s'
    Expect ((Read-Link $deskLnk).Description -eq $description) 'the desktop icon is not Hand''s'
    Expect ((Invoke-Uninstall (Join-Path $logs 'uninstall-foreign.log')) -eq 0) 'the second silent uninstall failed'
    Expect-Removed
    Expect ((Test-Path -LiteralPath $lnk) -and (Read-Link $lnk).Description -eq 'not Hand') 'the uninstall removed a Hand.lnk that is not Hand''s'
    Expect-Kept
    Write-Host 'the wizard installs and removes what it should, and keeps what it should'
}
finally {
    foreach ($p in $board, $stand) { if ($p) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } }
    Remove-Item Env:SECONDHAND_HOME -ErrorAction SilentlyContinue
    if ((Test-Path -LiteralPath $lnk) -and (Read-Link $lnk).Description -eq 'not Hand') { Remove-Item -LiteralPath $lnk -Force }
    Remove-Item -LiteralPath (Split-Path $keep[0]) -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $keep[1] -Force -ErrorAction SilentlyContinue
    if (-not $hadSecondhand -and -not (Get-ChildItem -LiteralPath $secondhand -Force -ErrorAction SilentlyContinue)) {
        Remove-Item -LiteralPath $secondhand -Force -ErrorAction SilentlyContinue
    }
}

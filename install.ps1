[CmdletBinding()]
param(
    [string]$Version = $(if ($env:HAND_INSTALL_VERSION) { $env:HAND_INSTALL_VERSION } else { 'latest' }),
    [switch]$Edge,
    [string]$Dir = $(if ($env:HAND_INSTALL_DIR) { $env:HAND_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'hand\bin' }),
    [string]$Base = $(if ($env:HAND_INSTALL_BASE) { $env:HAND_INSTALL_BASE } else { 'https://github.com/atqamz/hand/releases' }),
    [ValidateRange(1, 86400)][int]$TimeoutSec = 300
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64' -and $env:PROCESSOR_ARCHITEW6432 -ne 'AMD64') {
    throw "install.ps1: Hand on Windows needs 64-bit x86 (amd64)"
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

if ($Edge) { $Version = 'edge' }
$Base = $Base.TrimEnd('/')
$loopback = '^http://(127\.0\.0\.1|localhost|\[::1\])(:[0-9]+)?(/|$)'
if ($Base -notmatch '^https://' -and $Base -notmatch $loopback) {
    throw "install.ps1: the base URL must use https, or http on this machine: $Base"
}
if ($Version -eq 'latest') { $url = "$Base/latest/download" } else { $url = "$Base/download/$Version" }
$stem = 'hand-windows-amd64'

function Get-Download([string]$Uri, [string]$OutFile) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    foreach ($hop in 0..5) {
        $request = [Net.HttpWebRequest]::Create($Uri)
        $request.AllowAutoRedirect = $false
        $request.Timeout = 300000
        $request.ReadWriteTimeout = 300000
        $request.UserAgent = 'install.ps1'
        $response = $request.GetResponse()
        try {
            $status = [int]$response.StatusCode
            if ($status -ge 300 -and $status -lt 400 -and $response.Headers['Location']) {
                $Uri = [Uri]::new([Uri]$Uri, $response.Headers['Location']).AbsoluteUri
                if ($Uri -notmatch '^https://' -and ($Base -match '^https://' -or $Uri -notmatch $loopback)) {
                    throw "install.ps1: refusing a redirect to a non-https URL: $Uri"
                }
                continue
            }
            if ($status -ne 200) { throw "install.ps1: HTTP $status fetching $Uri" }
            $file = [IO.File]::Create($OutFile)
            try {
                $stream = $response.GetResponseStream()
                $buffer = [byte[]]::new(81920)
                while ($true) {
                    $read = $stream.ReadAsync($buffer, 0, $buffer.Length)
                    if (-not $read.Wait([int][Math]::Max(0, $TimeoutSec * 1000 - $clock.ElapsedMilliseconds))) {
                        $request.Abort()
                        throw "install.ps1: timed out after $TimeoutSec seconds fetching $Uri"
                    }
                    if ($read.Result -eq 0) { break }
                    $file.Write($buffer, 0, $read.Result)
                }
            }
            finally { $file.Dispose() }
            return
        }
        finally {
            $response.Close()
        }
    }
    throw "install.ps1: too many redirects fetching $OutFile"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("hand-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$part = $null
try {
    $zip = Join-Path $tmp "$stem.zip"
    $sums = Join-Path $tmp "$stem.sha256"
    Get-Download "$url/$stem.zip" $zip
    Get-Download "$url/$stem.sha256" $sums

    $want = $null
    foreach ($line in Get-Content -LiteralPath $sums) {
        if ($line -match "^([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($stem)).zip\s*$") { $want = $Matches[1] }
    }
    $got = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    if (-not $want -or $got -ne $want) {
        throw "install.ps1: checksum mismatch for $stem.zip"
    }

    $unpacked = Join-Path $tmp 'unpacked'
    Expand-Archive -LiteralPath $zip -DestinationPath $unpacked
    $built = Join-Path $unpacked 'hand.exe'
    if (-not (Test-Path -LiteralPath $built)) { throw "install.ps1: $stem.zip holds no hand.exe" }

    New-Item -ItemType Directory -Path $Dir -Force | Out-Null
    $Dir = (Resolve-Path -LiteralPath $Dir).ProviderPath
    $target = Join-Path $Dir 'hand.exe'
    $part = Join-Path $Dir (".hand.exe." + [guid]::NewGuid().ToString('N') + '.part')
    Copy-Item -LiteralPath $built -Destination $part -Force
    $old = $null
    if (Test-Path -LiteralPath $target) {
        $old = "$target.old"
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $old) { $old = "$target.old-" + [guid]::NewGuid().ToString('N') }
        Move-Item -LiteralPath $target -Destination $old -Force
    }
    try {
        Move-Item -LiteralPath $part -Destination $target -Force
    }
    catch {
        if ($old) { Move-Item -LiteralPath $old -Destination $target -Force }
        throw
    }
}
finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    if ($part) { Remove-Item -LiteralPath $part -Force -ErrorAction SilentlyContinue }
}

$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
try {
    $path = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $listed = $path.Split(';', [StringSplitOptions]::RemoveEmptyEntries) | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') }
    if ($listed -notcontains $Dir.TrimEnd('\')) {
        $joined = if ($path) { $path.TrimEnd(';') + ';' + $Dir } else { $Dir }
        $key.SetValue('Path', $joined, [Microsoft.Win32.RegistryValueKind]::ExpandString)
        [Environment]::SetEnvironmentVariable('HAND_INSTALL_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('HAND_INSTALL_REFRESH', $null, 'User')
        Write-Output "added $Dir to your user PATH; open a new terminal to use it"
    }
}
finally {
    $key.Close()
}
if (($env:Path.Split(';') | ForEach-Object { $_.TrimEnd('\') }) -notcontains $Dir.TrimEnd('\')) {
    $env:Path = "$env:Path;$Dir"
}

$first = (& $target version | Select-Object -First 1)
Write-Output "installed hand $first to $target"

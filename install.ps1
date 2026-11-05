& {
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
    $limitMs = $TimeoutSec * 1000

    function Get-Download([string]$Uri, [string]$OutFile) {
        $clock = [Diagnostics.Stopwatch]::StartNew()
        foreach ($hop in 0..5) {
            $request = [Net.HttpWebRequest]::Create($Uri)
            $request.AllowAutoRedirect = $false
            $request.Timeout = [int][Math]::Min(300000, [Math]::Max(1, $limitMs - $clock.ElapsedMilliseconds))
            $request.ReadWriteTimeout = 300000
            $request.UserAgent = 'install.ps1'
            try { $response = $request.GetResponse() }
            catch [Net.WebException] {
                if ($_.Exception.Status -eq [Net.WebExceptionStatus]::Timeout) { throw "install.ps1: timed out after $TimeoutSec seconds fetching $Uri" }
                throw
            }
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
                        if (-not $read.Wait([int][Math]::Max(0, $limitMs - $clock.ElapsedMilliseconds))) {
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

    function Install-Binary([string]$Built, [string]$Name) {
        $target = Join-Path $Dir $Name
        $part = Join-Path $Dir (".$Name." + [guid]::NewGuid().ToString('N') + '.part')
        try {
            Copy-Item -LiteralPath $Built -Destination $part -Force
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
            Remove-Item -LiteralPath $part -Force -ErrorAction SilentlyContinue
        }
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("hand-install-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
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

        $want = $null
        foreach ($line in @(& $built version)) { if ($line -match '^iroh: (\S+)\s*$') { $want = $Matches[1] } }
        $irohBuilt = $null
        if ($want) {
            $irohStem = 'hand-iroh-windows-amd64'
            $irohZip = Join-Path $tmp "$irohStem.zip"
            $irohSums = Join-Path $tmp "$irohStem.sha256"
            $found = $true
            try { Get-Download "$url/$irohStem.zip" $irohZip }
            catch [Net.WebException] {
                if (-not $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne 404) { throw }
                $found = $false
            }
            if ($found) {
                Get-Download "$url/$irohStem.sha256" $irohSums
                $irohWant = $null
                foreach ($line in Get-Content -LiteralPath $irohSums) {
                    if ($line -match "^([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($irohStem)).zip\s*$") { $irohWant = $Matches[1] }
                }
                $irohGot = (Get-FileHash -LiteralPath $irohZip -Algorithm SHA256).Hash
                if (-not $irohWant -or $irohGot -ne $irohWant) {
                    throw "install.ps1: checksum mismatch for $irohStem.zip"
                }
                $irohUnpacked = Join-Path $tmp 'iroh-unpacked'
                Expand-Archive -LiteralPath $irohZip -DestinationPath $irohUnpacked
                $irohBuilt = Join-Path $irohUnpacked 'hand-iroh.exe'
                if (-not (Test-Path -LiteralPath $irohBuilt)) { throw "install.ps1: $irohStem.zip holds no hand-iroh.exe" }
                $irohVersion = $null
                $irohLines = @(& $irohBuilt --version)
                if ($LASTEXITCODE -eq 0 -and $irohLines.Count -gt 0) {
                    if ($irohLines[0].Trim() -cmatch '^hand-iroh ([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*) iroh \S+$') { $irohVersion = $Matches[1] }
                }
                if ($irohVersion -ne $want) {
                    $shown = if ($irohVersion) { $irohVersion } else { 'unknown' }
                    throw "install.ps1: $irohStem.zip is hand-iroh $shown, but this hand needs $want"
                }
            }
            else {
                Write-Output "this release has no $irohStem.zip; phone access over iroh is not installed"
            }
        }

        New-Item -ItemType Directory -Path $Dir -Force | Out-Null
        $Dir = (Resolve-Path -LiteralPath $Dir).ProviderPath
        if ($irohBuilt) { Install-Binary $irohBuilt 'hand-iroh.exe' }
        Install-Binary $built 'hand.exe'
        $target = Join-Path $Dir 'hand.exe'
    }
    finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
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
    if ($irohBuilt) { Write-Output "installed hand-iroh $irohVersion to $(Join-Path $Dir 'hand-iroh.exe')" }
} @args

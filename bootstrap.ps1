#Requires -Version 5.1
# Optional, explicitly opt-in Secondhand adoption for native Windows: acquires hand if missing,
# offers to install missing foundational dependencies (git, treehouse, herdr) with consent,
# reconciles a fleet home with `hand init`, reads readiness from `hand doctor`, and prints the
# exact next command. Never installs a coding-agent harness or no-mistakes; never reimplements
# `hand init` or `hand doctor` validation logic. No WSL: every step below runs as native
# PowerShell against native Windows tools.
[CmdletBinding()]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSReviewUnusedParameter', 'Yes', Justification = 'read inside Install-Hand and Install-FoundationalDep, which close over this script-scope parameter')]
param(
    [string]$Fleet = (Join-Path ([Environment]::GetFolderPath('UserProfile')) "secondhand-fleet"),
    [switch]$Yes,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

function Write-BootstrapLog {
    param([string]$Message)
    Write-Host $Message
}

function Fail {
    param([string]$Message)
    Write-BootstrapLog "bootstrap.ps1: $Message"
    exit 1
}

$interactive = (-not $Check) -and (-not [Console]::IsInputRedirected) -and (-not [Console]::IsOutputRedirected)

$scriptDir = $null
if ($PSCommandPath) {
    $scriptDir = Split-Path -Parent $PSCommandPath
}

# ---- step 1: acquire or verify hand ------------------------------------------------------------

$handCommand = Get-Command hand -ErrorAction SilentlyContinue
$handAvailable = [bool]$handCommand
$script:handCommandDir = if ($handCommand.Source) { Split-Path -Parent $handCommand.Source } else { $null }

# Install-Hand only ever runs when hand is missing. In check mode it reports and returns without
# mutating; otherwise it fails with an actionable message on every path that cannot end with hand
# on PATH, so callers never have to re-check its result.
function Install-Hand {
    if ($Check) {
        Write-BootstrapLog "hand: not installed (check mode: no changes made)"
        return
    }
    if (-not $Yes -and -not $interactive) {
        Fail "hand is not installed, and bootstrap is not running interactively without -Yes: refusing to install it"
    }
    if (-not $Yes) {
        $reply = Read-Host "hand is not installed. Install it now via install.ps1? [y/N]"
        if ($reply -notmatch '^(y|yes)$') {
            Fail "hand install declined; cannot continue"
        }
    }

    $installDir = if ($env:HAND_INSTALL_DIR) { $env:HAND_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "hand" }
    $script:handInstallDir = $installDir
    $sibling = if ($scriptDir) { Join-Path $scriptDir "install.ps1" } else { $null }
    if ($sibling -and (Test-Path -LiteralPath $sibling)) {
        Write-BootstrapLog "installing hand via $sibling"
        try {
            & $sibling
        } catch {
            Fail "install.ps1 failed: $($_.Exception.Message); recover by resolving the reported error and rerunning bootstrap.ps1"
        }
    } else {
        Write-BootstrapLog "installing hand from checksum-verified GitHub release"
        $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
        New-Item -ItemType Directory -Path $tmp | Out-Null
        try {
            $repo = "atqamz/hand"
            $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
            $tag = $release.tag_name
            if (-not $tag) {
                Fail "could not resolve the latest hand release tag"
            }
            $asset = "hand-windows-amd64.zip"
            $baseUrl = "https://github.com/$repo/releases/download/$tag"
            try {
                Invoke-WebRequest -Uri "$baseUrl/$asset" -OutFile (Join-Path $tmp $asset)
                Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")
            } catch {
                Fail "could not download the hand release or checksums: $($_.Exception.Message)"
            }
            if ((Get-Item -LiteralPath (Join-Path $tmp $asset)).Length -eq 0) {
                Fail "downloaded hand release archive is empty"
            }

            $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($asset) + '\s*$' }
            if (-not $line) {
                Fail "checksums.txt has no entry for $asset"
            }
            $want = (($line | Select-Object -First 1) -split '\s+')[0].ToLower()
            $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
            if ($got -ne $want) {
                Fail "checksum mismatch for ${asset}: want $want, got $got"
            }

            Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
            Copy-Item -Path (Join-Path $tmp "hand.exe") -Destination (Join-Path $installDir "hand.exe") -Force
        } finally {
            Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        }
    }

    $pathDirs = $env:PATH -split ';'
    if ($pathDirs -notcontains $installDir) {
        $env:PATH = "$installDir;$env:PATH"
    }
    if (-not (Get-Command hand -ErrorAction SilentlyContinue)) {
        Fail "hand was installed but is still not on PATH; add $installDir to PATH and rerun bootstrap.ps1"
    }
}

if (-not $handAvailable) {
    Install-Hand
    $handCommand = Get-Command hand -ErrorAction SilentlyContinue
    $script:handCommandDir = if ($handCommand.Source) { Split-Path -Parent $handCommand.Source } else { $null }
}

# ---- step 2: detect/install missing foundational dependencies ---------------------------------

function Update-ProcessPathFromRegistry {
    $machinePath = [System.Environment]::GetEnvironmentVariable('PATH', 'Machine')
    $userPath = [System.Environment]::GetEnvironmentVariable('PATH', 'User')
    $pathDirs = @($machinePath, $userPath) | ForEach-Object { if ($_){ $_ -split ';' } } | Where-Object { $_ }
    $handDirs = @($script:handCommandDir, $script:handInstallDir) | Where-Object { $_ }
    foreach ($handDir in $handDirs) {
        if (-not ($pathDirs | Where-Object { $_ -ieq $handDir })) {
            $pathDirs = @($handDir) + @($pathDirs)
        }
    }
    $env:PATH = $pathDirs -join ';'
}

# Get-DepSource is the source column the consent prompt shows for a missing foundational
# dependency.
function Get-DepSource {
    param([string]$Dep)
    switch ($Dep) {
        'git' { 'winget (or install Git for Windows manually)' }
        'treehouse' { 'kunchenguid/treehouse' }
        'herdr' { 'ogulcancelik/herdr' }
    }
}

# Resolve-DepAction returns @{ Kind = 'cmd'|'url'; Value = <winget args>|<installer url> } for
# $Dep. A url is never invoked directly with `irm ... | iex`: Invoke-DepAction downloads to a
# file first and only runs it once the download itself is confirmed to have succeeded, the same
# fetch-then-verify-then-run shape bootstrap.sh uses so a failed fetch can never be mistaken for
# an empty, successful script.
function Resolve-DepAction {
    param([string]$Dep)
    switch ($Dep) {
        'git' {
            if (Get-Command winget -ErrorAction SilentlyContinue) {
                @{ Kind = 'cmd'; Value = @('winget', 'install', '--id', 'Git.Git', '-e', '--source', 'winget', '--accept-package-agreements', '--accept-source-agreements') }
            } else {
                @{ Kind = 'cmd'; Value = $null }
            }
        }
        'treehouse' {
            @{ Kind = 'url'; Value = 'https://kunchenguid.github.io/treehouse/install.ps1' }
        }
        'herdr' {
            @{ Kind = 'url'; Value = 'https://herdr.dev/install.ps1' }
        }
        default {
            @{ Kind = 'cmd'; Value = $null }
        }
    }
}

function Get-DepActionDescription {
    param([string]$Dep)
    $action = Resolve-DepAction $Dep
    switch ($action.Kind) {
        'cmd' { if ($action.Value) { $action.Value -join ' ' } else { $null } }
        'url' { "download and verify $($action.Value), then run only the completed download" }
    }
}

function Invoke-DepAction {
    param([string]$Dep)
    $action = Resolve-DepAction $Dep
    switch ($action.Kind) {
        'cmd' {
            if (-not $action.Value) { return $false }
            & $action.Value[0] @($action.Value[1..($action.Value.Length - 1)])
            return $LASTEXITCODE -eq 0
        }
        'url' {
            $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName() + ".ps1")
            try {
                Invoke-WebRequest -Uri $action.Value -OutFile $tmp
            } catch {
                Remove-Item -Force $tmp -ErrorAction SilentlyContinue
                return $false
            }
            if (-not (Test-Path -LiteralPath $tmp) -or (Get-Item -LiteralPath $tmp).Length -eq 0) {
                Remove-Item -Force $tmp -ErrorAction SilentlyContinue
                return $false
            }
            $ok = $true
            try {
                & $tmp
            } catch {
                $ok = $false
            }
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            return $ok
        }
    }
}

# Get-MissingFoundationalDep lists, in the same order hand doctor's foundational tools table
# does, every dependency doctor would also report missing - never a schema bootstrap invents.
function Get-MissingFoundationalDep {
    @('git', 'treehouse', 'herdr') | Where-Object { -not (Get-Command $_ -ErrorAction SilentlyContinue) }
}

# Install-FoundationalDep returns $true once every foundational dependency is on PATH, and
# $false whenever one remains missing - declined, failed, unsupported, or check mode. It never
# treats that as fatal itself: `hand doctor` is the one place a remaining gap is judged blocking.
function Install-FoundationalDep {
    $missing = @(Get-MissingFoundationalDep)
    if ($missing.Count -eq 0) { return $true }

    if ($Check) {
        Write-BootstrapLog "missing foundational dependencies (check mode: no changes made):"
        foreach ($dep in $missing) { Write-BootstrapLog "  $dep" }
        return $false
    }

    Write-BootstrapLog "Missing foundational runtime dependencies:"
    Write-BootstrapLog ""
    foreach ($dep in $missing) {
        $desc = Get-DepActionDescription $dep
        Write-BootstrapLog "  $dep"
        Write-BootstrapLog "    source: $(Get-DepSource $dep)"
        if ($desc) {
            Write-BootstrapLog "    install method: $desc"
        } else {
            Write-BootstrapLog "    install method: none detected for this platform; install $dep manually, then rerun bootstrap.ps1"
        }
        Write-BootstrapLog ""
    }

    $proceed = [bool]$Yes
    if (-not $proceed) {
        if (-not $interactive) {
            Write-BootstrapLog "not running interactively and -Yes was not given: declining to install any of the above"
            return $false
        }
        $reply = Read-Host "Install these dependencies? [y/N]"
        $proceed = $reply -match '^(y|yes)$'
    }
    if (-not $proceed) {
        Write-BootstrapLog "declined: continuing without installing missing foundational dependencies"
        return $false
    }

    $installFailed = $false
    foreach ($dep in $missing) {
        $desc = Get-DepActionDescription $dep
        if (-not $desc) {
            Write-BootstrapLog "$dep`: no supported install method detected on this platform; skipping"
            $installFailed = $true
            continue
        }
        Write-BootstrapLog "installing $dep`: $desc"
        if (-not (Invoke-DepAction $dep)) {
            Write-BootstrapLog "$dep`: install action failed"
            $installFailed = $true
            continue
        }
        Update-ProcessPathFromRegistry
        if (-not (Get-Command $dep -ErrorAction SilentlyContinue)) {
            Write-BootstrapLog "$dep`: installed but still not on PATH"
            $installFailed = $true
        }
    }
    -not $installFailed
}

$null = Install-FoundationalDep

# ---- step 3: choose a safe fleet-home target ---------------------------------------------------

# Get-FleetState never duplicates hand doctor's or hand init's own validation: it only decides the
# one thing bootstrap alone is responsible for before ever invoking hand init - whether this
# target is safe to hand to it at all.
function Get-FleetState {
    if (-not (Test-Path -LiteralPath $Fleet)) {
        return 'absent'
    }
    if (-not (Test-Path -LiteralPath $Fleet -PathType Container)) {
        Fail "$Fleet exists and is not a directory"
    }
    if ((Test-Path -LiteralPath (Join-Path $Fleet "state\hand.db") -PathType Leaf)) {
        return 'fleet'
    }
    if ((Test-Path -LiteralPath (Join-Path $Fleet "data\projects.md") -PathType Leaf) -and (Test-Path -LiteralPath (Join-Path $Fleet "state") -PathType Container)) {
        return 'fleet'
    }
    if (-not (Get-ChildItem -LiteralPath $Fleet -Force -ErrorAction SilentlyContinue)) {
        return 'empty'
    }
    return 'foreign'
}

$state = Get-FleetState
if ($state -eq 'foreign') {
    Fail "$Fleet exists, is not empty, and is not a recognized Secondhand fleet; refusing to adopt it - pass -Fleet with an empty or already-initialized path"
}

if ($Check) {
    Write-BootstrapLog ""
    Write-BootstrapLog "fleet target: $Fleet ($state)"
    if ($state -ne 'fleet') {
        Write-BootstrapLog "hand init has not run here yet; check mode makes no changes, so readiness cannot be evaluated further"
        exit 0
    }
    if (-not $handAvailable) {
        Write-BootstrapLog "hand is not installed; readiness cannot be evaluated further"
        exit 0
    }
    $env:HAND_HOME = $Fleet
    $doctorOut = (& hand doctor 2>&1 | Out-String)
    Write-BootstrapLog ""
    Write-BootstrapLog $doctorOut
    exit 0
}

# ---- step 4: hand init, then hand doctor for the authoritative readiness result ----------------

if ($state -eq 'absent') {
    New-Item -ItemType Directory -Path $Fleet -Force | Out-Null
}

$initOut = (& hand init $Fleet 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0) {
    Write-BootstrapLog $initOut
    Fail "hand init failed against $Fleet; recover by resolving the reported error, then rerun: bootstrap.ps1 -Fleet $Fleet"
}
Write-BootstrapLog $initOut

$env:HAND_HOME = $Fleet
$doctorOut = (& hand doctor 2>&1 | Out-String)
Write-BootstrapLog ""
Write-BootstrapLog $doctorOut

# Get-DoctorField extracts a scalar TOON field ("key: value") from hand doctor's stdout.
function Get-DoctorField {
    param([string]$Name, [string]$Text)
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -match "^$Name`: (.*)$") { return $Matches[1] }
    }
    return $null
}

# Get-DoctorList extracts the "  - item" lines under one TOON list block ("name[N]:").
function Get-DoctorList {
    param([string]$Name, [string]$Text)
    $items = @()
    $inBlock = $false
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -like "$Name[[]*") { $inBlock = $true; continue }
        if ($inBlock -and $line -match '^  - (.*)$') { $items += $Matches[1]; continue }
        if ($inBlock) { $inBlock = $false }
    }
    return $items
}

# Get-InstalledHarness reads the harnesses[N]{name,installed} rows hand doctor already
# computed, in the order hand reports them, so bootstrap never re-detects harnesses on its own.
function Get-InstalledHarness {
    param([string]$Text)
    $names = @()
    $inBlock = $false
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -like "harnesses[[]*") { $inBlock = $true; continue }
        if ($inBlock -and $line -match '^  (\w+),(true|false)$') {
            if ($Matches[2] -eq 'true') { $names += $Matches[1] }
            continue
        }
        if ($inBlock) { $inBlock = $false }
    }
    return $names
}

$ready = Get-DoctorField 'ready' $doctorOut
if ($ready -ne 'true') {
    $blocking = Get-DoctorList 'blocking' $doctorOut
    Write-BootstrapLog ""
    Write-BootstrapLog "Secondhand is not ready yet. Blocking:"
    foreach ($item in $blocking) { Write-BootstrapLog "  - $item" }
    Fail "recover the items above, then rerun: `$env:HAND_HOME='$Fleet'; hand doctor"
}

$harnesses = @(Get-InstalledHarness $doctorOut)

Write-BootstrapLog ""
Write-BootstrapLog "Secondhand is ready."
Write-BootstrapLog ""
Write-BootstrapLog "Next:"
Write-BootstrapLog ""
Write-BootstrapLog "  cd $Fleet"
if ($harnesses.Count -eq 1) {
    Write-BootstrapLog "  $($harnesses[0])"
} elseif ($harnesses.Count -gt 1) {
    Write-BootstrapLog "  <choose one of the installed harnesses below>"
    foreach ($h in $harnesses) { Write-BootstrapLog "    $h" }
} else {
    Write-BootstrapLog "  <install and authenticate at least one supported coding-agent harness, then run hand doctor>"
}

#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Fleet = (Join-Path $env:USERPROFILE "secondhand-fleet"),
    [switch]$Yes,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

$HAND_RELEASE_TAG = '@HAND_RELEASE_TAG@'
$HAND_RELEASE_VERSION = '@HAND_RELEASE_VERSION@'
$HAND_RELEASE_COMMIT = '@HAND_RELEASE_COMMIT@'
$HAND_RELEASE_RUNTIME_ID = '@HAND_RELEASE_RUNTIME_ID@'
$HAND_RELEASE_CHECKSUMS_ASSET = 'checksums.txt'
$HAND_RELEASE_MANIFEST_ASSET = 'release-manifest.json'
$HAND_RELEASE_ASSET = 'hand-windows-amd64.zip'

function Write-BootstrapLog {
    param([string]$Message)
    Write-Host $Message
}

function Fail {
    param([string]$Message)
    Write-BootstrapLog "bootstrap.ps1: $Message"
    exit 1
}

$releasePlaceholderPrefix = '@HAND' + '_RELEASE_'
if ($HAND_RELEASE_TAG -like "$releasePlaceholderPrefix*" -or
    $HAND_RELEASE_VERSION -like "$releasePlaceholderPrefix*" -or
    $HAND_RELEASE_COMMIT -like "$releasePlaceholderPrefix*" -or
    $HAND_RELEASE_RUNTIME_ID -like "$releasePlaceholderPrefix*") {
    Fail 'this source template is not a release-bound bootstrap asset'
}

$handCommand = Get-Command hand -ErrorAction SilentlyContinue
$handAvailable = [bool]$handCommand

function Get-Sha256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Install-Hand {
    if ($Check) {
        Write-BootstrapLog 'hand: not installed (check mode: no changes made)'
        return
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        $baseUrl = "https://github.com/atqamz/hand/releases/download/$HAND_RELEASE_TAG"
        $archive = Join-Path $tmp $HAND_RELEASE_ASSET
        $checksums = Join-Path $tmp $HAND_RELEASE_CHECKSUMS_ASSET
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$HAND_RELEASE_ASSET" -OutFile $archive
            Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$HAND_RELEASE_CHECKSUMS_ASSET" -OutFile $checksums
            Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$HAND_RELEASE_MANIFEST_ASSET" -OutFile (Join-Path $tmp $HAND_RELEASE_MANIFEST_ASSET)
        } catch {
            Fail "download failed for the exact release $HAND_RELEASE_TAG`: $($_.Exception.Message)"
        }
        if (-not (Test-Path -LiteralPath $archive -PathType Leaf) -or (Get-Item -LiteralPath $archive).Length -eq 0) {
            Fail "downloaded hand release archive is empty"
        }

        $escapedAsset = [regex]::Escape($HAND_RELEASE_ASSET)
        $line = Get-Content -LiteralPath $checksums | Where-Object { $_ -match "^\s*([0-9a-fA-F]{64})\s+\*?$escapedAsset\s*$" } | Select-Object -First 1
        if (-not $line) {
            Fail "$HAND_RELEASE_CHECKSUMS_ASSET has no entry for $HAND_RELEASE_ASSET"
        }
        $want = (($line -split '\s+')[0]).ToLowerInvariant()
        $got = Get-Sha256 $archive
        if ($got -ne $want) {
            Fail "checksum mismatch for ${HAND_RELEASE_ASSET}: want $want, got $got"
        }
        $manifestPath = Join-Path $tmp $HAND_RELEASE_MANIFEST_ASSET
        $manifestLine = Get-Content -LiteralPath $checksums | Where-Object { $_ -match "^\s*([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($HAND_RELEASE_MANIFEST_ASSET))\s*$" } | Select-Object -First 1
        if (-not $manifestLine) {
            Fail "$HAND_RELEASE_CHECKSUMS_ASSET has no entry for $HAND_RELEASE_MANIFEST_ASSET"
        }
        $manifestWant = (($manifestLine -split '\s+')[0]).ToLowerInvariant()
        $manifestGot = Get-Sha256 $manifestPath
        if ($manifestGot -ne $manifestWant) {
            Fail "checksum mismatch for $HAND_RELEASE_MANIFEST_ASSET`: want $manifestWant, got $manifestGot"
        }
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
        if ($manifest.commit -ne $HAND_RELEASE_COMMIT) {
            Fail "release manifest commit does not match $HAND_RELEASE_COMMIT"
        }

        Expand-Archive -LiteralPath $archive -DestinationPath $tmp -Force
        $handSource = Join-Path $tmp 'hand.exe'
        if (-not (Test-Path -LiteralPath $handSource -PathType Leaf)) {
            Fail 'verified release archive does not contain hand.exe'
        }

        $installDir = if ($env:HAND_INSTALL_DIR) { $env:HAND_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'hand' }
        try {
            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
            Copy-Item -LiteralPath $handSource -Destination (Join-Path $installDir 'hand.exe') -Force
        } catch {
            Fail "could not install hand to $installDir; resolve permissions without sudo and rerun bootstrap.ps1: $($_.Exception.Message)"
        }
        $pathDirs = @($env:PATH -split ';')
        if ($pathDirs -notcontains $installDir) {
            $env:PATH = "$installDir;$env:PATH"
        }
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    if (-not (Get-Command hand -ErrorAction SilentlyContinue)) {
        Fail "hand was installed but is not on PATH; add $installDir to PATH and rerun bootstrap.ps1"
    }
}

if (-not $handAvailable) {
    Install-Hand
    $handCommand = Get-Command hand -ErrorAction SilentlyContinue
    $handAvailable = [bool]$handCommand
}

function Ensure-PrivateRuntime {
    if ($Check) {
        Write-BootstrapLog 'private runtime status (check mode: no changes made):'
        if (-not $handAvailable) {
            Write-BootstrapLog 'hand is not installed; private runtime status cannot be evaluated'
            return
        }
        & hand runtime status
        return
    }
    $runtimeStatus = (& hand runtime status 2>$null | Out-String)
    if ($runtimeStatus -match 'ready: true') {
        return
    }
    Write-BootstrapLog "ensuring private pinned Git, Treehouse, and Herdr runtime for $HAND_RELEASE_VERSION ($HAND_RELEASE_RUNTIME_ID)"
    & hand runtime ensure
    if ($LASTEXITCODE -ne 0) {
        Fail 'private runtime is not ready; repair with: hand runtime ensure'
    }
}

Ensure-PrivateRuntime

function Get-FleetState {
    if (-not (Test-Path -LiteralPath $Fleet)) {
        return 'absent'
    }
    if (-not (Test-Path -LiteralPath $Fleet -PathType Container)) {
        Fail "$Fleet exists and is not a directory"
    }
    if (Test-Path -LiteralPath (Join-Path $Fleet 'state\hand.db') -PathType Leaf) {
        return 'fleet'
    }
    if ((Test-Path -LiteralPath (Join-Path $Fleet 'data\projects.md') -PathType Leaf) -and
        (Test-Path -LiteralPath (Join-Path $Fleet 'state') -PathType Container)) {
        return 'fleet'
    }
    try {
        $children = @(Get-ChildItem -LiteralPath $Fleet -Force -ErrorAction Stop)
    } catch {
        Fail "cannot inspect fleet target $Fleet`: $($_.Exception.Message)"
    }
    if ($children.Count -eq 0) {
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
        Write-BootstrapLog 'hand init has not run here; check mode makes no changes, so readiness cannot be evaluated further'
        exit 0
    }
    if (-not $handAvailable) {
        Write-BootstrapLog 'hand is not installed; readiness cannot be evaluated further'
        exit 0
    }
    $env:HAND_HOME = $Fleet
    $doctorOut = (& hand doctor 2>&1 | Out-String)
    Write-BootstrapLog ""
    Write-BootstrapLog $doctorOut
    exit 0
}

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

function Get-DoctorField {
    param([string]$Name, [string]$Text)
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -match "^$Name`: (.*)$") { return $Matches[1] }
    }
    return $null
}

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

function Get-InstalledHarness {
    param([string]$Text)
    $names = @()
    $inBlock = $false
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -like 'harnesses[[]*') { $inBlock = $true; continue }
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
    Write-BootstrapLog 'Secondhand is not ready yet. Blocking:'
    foreach ($item in $blocking) { Write-BootstrapLog "  - $item" }
    Fail "recover the items above, then rerun: `$env:HAND_HOME='$Fleet'; hand doctor"
}

$harnesses = @(Get-InstalledHarness $doctorOut)
Write-BootstrapLog ""
Write-BootstrapLog 'Secondhand is ready.'
Write-BootstrapLog ""
Write-BootstrapLog 'Next:'
Write-BootstrapLog ""
Write-BootstrapLog "  cd $Fleet"
if ($harnesses.Count -eq 1) {
    Write-BootstrapLog "  $($harnesses[0])"
} elseif ($harnesses.Count -gt 1) {
    Write-BootstrapLog '  <choose one of the installed harnesses below>'
    foreach ($h in $harnesses) { Write-BootstrapLog "    $h" }
} else {
    Write-BootstrapLog '  <install and authenticate at least one supported coding-agent harness, then run hand doctor>'
}

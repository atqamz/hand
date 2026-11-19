[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Archives,
    [Parameter(Mandatory)][string]$Out,
    [Parameter(Mandatory)][string]$Work
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$innoVersion = '6.7.4'
$innoSha256 = 'a549929319f9120716677c685535ca4671e4b937b5fdc87e2585f32d8eb30e8c'
$innoUrl = "https://github.com/jrsoftware/issrc/releases/download/is-$($innoVersion.Replace('.', '_'))/innosetup-$innoVersion.exe"
$iss = Join-Path $PSScriptRoot '..\..\packaging\windows\hand.iss'

function Expand-Checked([string]$Stem, [string]$Exe) {
    $zip = Join-Path $Archives "$Stem.zip"
    $sums = Join-Path $Archives "$Stem.sha256"
    foreach ($f in $zip, $sums) { if (-not (Test-Path -LiteralPath $f)) { throw "build-setup: missing $f" } }
    $want = $null
    foreach ($line in Get-Content -LiteralPath $sums) {
        if ($line -match "^([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($Stem)).zip\s*$") { $want = $Matches[1] }
    }
    $got = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    if (-not $want -or $got -ne $want) { throw "build-setup: checksum mismatch for $Stem.zip" }
    $dir = Join-Path $Work $Stem
    Expand-Archive -LiteralPath $zip -DestinationPath $dir
    $names = @(Get-ChildItem -LiteralPath $dir -Name)
    if ($names.Count -ne 1 -or $names[0] -ne $Exe) { throw "build-setup: $Stem.zip holds '$($names -join ', ')', want $Exe" }
    Write-Host "checked $Stem.zip"
    return Join-Path $dir $Exe
}

New-Item -ItemType Directory -Path $Work, $Out -Force | Out-Null
$stage = Join-Path $Work 'stage'
New-Item -ItemType Directory -Path $stage | Out-Null

$hand = Expand-Checked 'hand-windows-amd64' 'hand.exe'
$desktop = Expand-Checked 'hand-desktop-windows-amd64' 'hand-desktop.exe'
$irohZip = Join-Path $Archives 'hand-iroh-windows-amd64.zip'
$iroh = if (Test-Path -LiteralPath $irohZip) { Expand-Checked 'hand-iroh-windows-amd64' 'hand-iroh.exe' }

$info = @{}
foreach ($line in @(& $hand version)) { if ($line -match '^(\w+): (\S+)\s*$') { $info[$Matches[1]] = $Matches[2] } }
$version = $info['version']
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw "build-setup: hand version printed '$version'" }
$want = "hand-desktop $version $($info['commit'])"
$got = (@(& $desktop --version | Out-String) -join '').Trim()
if ($got -ne $want) { throw "build-setup: hand-desktop.exe printed '$got', want '$want'" }
if ($iroh) {
    $first = @(& $iroh --version)[0].Trim()
    if ($first -cnotmatch "^hand-iroh $([regex]::Escape($info['iroh'])) iroh \S+$") { throw "build-setup: hand-iroh.exe printed '$first', hand needs $($info['iroh'])" }
}
foreach ($f in $hand, $desktop, $iroh) { if ($f) { Copy-Item -LiteralPath $f -Destination $stage } }

$setup = Join-Path $Work 'innosetup.exe'
Invoke-WebRequest -Uri $innoUrl -OutFile $setup -UseBasicParsing
$hash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash
if ($hash -ne $innoSha256) { throw "build-setup: innosetup-$innoVersion.exe is $hash, want $innoSha256" }
$inno = Join-Path $Work 'inno'
$run = Start-Process -FilePath $setup -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/CURRENTUSER', "/DIR=`"$inno`"" -Wait -PassThru
if ($run.ExitCode -ne 0) { throw "build-setup: the Inno Setup installer exited $($run.ExitCode)" }
$iscc = Join-Path $inno 'ISCC.exe'
$defs = @("/DVersion=$version", "/DSource=$stage")
if ($iroh) { $defs += '/DIroh' }
$log = @(& $iscc "/O$Out" @defs $iss 2>&1 | ForEach-Object { "$_" })
$log | ForEach-Object { Write-Host $_ }
if ($LASTEXITCODE -ne 0) { throw "build-setup: ISCC.exe exited $LASTEXITCODE" }
if (-not ($log -match "Inno Setup $([regex]::Escape($innoVersion))\b")) { throw "build-setup: the compiler does not report Inno Setup $innoVersion" }

$name = "Hand-Setup-$version-x64.exe"
$exe = Join-Path $Out $name
if (-not (Test-Path -LiteralPath $exe)) { throw "build-setup: $name was not built" }
$digest = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText((Join-Path $Out "Hand-Setup-$version-x64.sha256"), "$digest  $name`n", [Text.UTF8Encoding]::new($false))
Write-Output "built $name ($((Get-Item -LiteralPath $exe).Length) bytes) sha256 $digest"

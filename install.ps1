#Requires -Version 5.1
# Installs hand only: no herdr, treehouse, gh, agent harness, or no-mistakes.
# $env:HAND_INSTALL_DIR overrides the install directory; $env:HAND_INSTALL_VERSION pins a tag.
$ErrorActionPreference = 'Stop'

$repo = "atqamz/hand"
$installDir = if ($env:HAND_INSTALL_DIR) { $env:HAND_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "hand" }
$tag = $env:HAND_INSTALL_VERSION

if (-not $tag) {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $tag = $release.tag_name
}

$asset = "hand-windows-amd64.zip"
$baseUrl = "https://github.com/$repo/releases/download/$tag"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "downloading $asset ($tag)..."
    Invoke-WebRequest -Uri "$baseUrl/$asset" -OutFile (Join-Path $tmp $asset)
    Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")

    $line = Get-Content (Join-Path $tmp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($asset) + '\s*$' }
    if (-not $line) {
        throw "checksums.txt has no entry for $asset"
    }
    $want = (($line | Select-Object -First 1) -split '\s+')[0].ToLower()

    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
    if ($got -ne $want) {
        throw "checksum mismatch for ${asset}: want $want, got $got"
    }

    Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Copy-Item -Path (Join-Path $tmp "hand.exe") -Destination (Join-Path $installDir "hand.exe") -Force

    Write-Host "installed hand to $(Join-Path $installDir 'hand.exe')"
    try {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if (($userPath -split ';') -notcontains $installDir) {
            Write-Host "note: $installDir is not on your PATH"
        }
    } catch {
        # GetEnvironmentVariable's "User" scope is Windows-only; nothing to report elsewhere.
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

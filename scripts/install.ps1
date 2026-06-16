$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# cc-clip Windows installer
# Usage:
#   irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
#
# Optional environment overrides:
#   $env:CC_CLIP_VERSION = "v0.9.0"          # pin a release tag
#   $env:CC_CLIP_INSTALL_DIR = "$HOME\bin"   # install location

$Repo = "ShunmeiCho/cc-clip"
$InstallDir = if ($env:CC_CLIP_INSTALL_DIR) { $env:CC_CLIP_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }

function Get-CcClipArch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
    switch ($arch) {
        "x64" { return "amd64" }
        "arm64" { return "arm64" }
        default { throw "Unsupported architecture: $arch" }
    }
}

function Get-LatestVersion {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "cc-clip-installer" }
    if (-not $release.tag_name) {
        throw "Could not determine latest cc-clip version"
    }
    return [string]$release.tag_name
}

function Resolve-Version {
    if ($env:CC_CLIP_VERSION) {
        $ver = "v$($env:CC_CLIP_VERSION.TrimStart("v"))"
        if ($ver -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$') {
            throw "CC_CLIP_VERSION='$env:CC_CLIP_VERSION' is not a valid version tag (expected e.g. v0.9.0)"
        }
        Write-Host "Installing cc-clip $ver (pinned via CC_CLIP_VERSION)"
        return $ver
    }
    return Get-LatestVersion
}

function Download-File($Url, $Destination) {
    Invoke-WebRequest -Uri $Url -OutFile $Destination -Headers @{ "User-Agent" = "cc-clip-installer" }
}

function Verify-Checksum($ArchivePath, $ChecksumsPath, $ArchiveName) {
    $line = Get-Content -LiteralPath $ChecksumsPath | Where-Object {
        $fields = $_ -split '\s+'
        $fields.Count -ge 2 -and $fields[1] -eq $ArchiveName
    } | Select-Object -First 1

    if (-not $line) {
        throw "Checksum for $ArchiveName not found in checksums.txt"
    }

    $expected = (($line -split '\s+')[0]).ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "Checksum mismatch for ${ArchiveName}: expected $expected, got $actual"
    }
}

function Main {
    $arch = Get-CcClipArch
    $version = Resolve-Version
    $platform = "windows_$arch"
    $archiveName = "cc-clip_$($version.TrimStart("v"))_${platform}.zip"
    $downloadUrl = "https://github.com/$Repo/releases/download/$version/$archiveName"
    $checksumsUrl = "https://github.com/$Repo/releases/download/$version/checksums.txt"

    Write-Host "Installing cc-clip $version for $platform..."

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("cc-clip-install-" + [System.Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
    try {
        $archivePath = Join-Path $tmpDir $archiveName
        $checksumsPath = Join-Path $tmpDir "checksums.txt"
        $extractDir = Join-Path $tmpDir "extract"

        Write-Host "Downloading $downloadUrl..."
        Download-File $downloadUrl $archivePath

        Write-Host "Downloading checksums.txt..."
        Download-File $checksumsUrl $checksumsPath

        Write-Host "Verifying checksum..."
        Verify-Checksum $archivePath $checksumsPath $archiveName

        Write-Host "Extracting..."
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force

        $binary = Join-Path $extractDir "cc-clip.exe"
        if (-not (Test-Path -LiteralPath $binary)) {
            throw "cc-clip.exe not found in archive"
        }

        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        $dest = Join-Path $InstallDir "cc-clip.exe"
        try {
            Copy-Item -LiteralPath $binary -Destination $dest -Force
        } catch {
            throw "Failed to install cc-clip.exe to $dest. Stop any running cc-clip hotkey/tray process and retry. $($_.Exception.Message)"
        }

        Write-Host ""
        Write-Host "cc-clip $version installed to $dest"

        $pathEntries = $env:PATH -split ';' | ForEach-Object { $_.TrimEnd('\') }
        if ($pathEntries -notcontains $InstallDir.TrimEnd('\')) {
            Write-Host ""
            Write-Host "Add to your user PATH:"
            Write-Host "  $InstallDir"
        }

        Write-Host ""
        Write-Host "Quick start:"
        Write-Host "  cc-clip hotkey HOST --enable-autostart"
        Write-Host "  Alt+Shift+V in remote Claude Code"
    } finally {
        Remove-Item -LiteralPath $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Main

# Bunker installer for Windows.
#
#   irm https://raw.githubusercontent.com/kucukkanat/bunker/master/install.ps1 | iex
#
# Downloads bunker.exe from the latest GitHub release, verifies its SHA-256
# checksum, installs it to %LOCALAPPDATA%\Programs\Bunker and adds that folder
# to your user PATH. Set $env:BUNKER_VERSION = 'v1.2.3' first to pin a version.
# Set $env:BUNKER_NO_START = '1' to skip starting Bunker afterwards.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo = 'kucukkanat/bunker'
$Version = if ($env:BUNKER_VERSION) { $env:BUNKER_VERSION } else { 'latest' }
if ($Version -ne 'latest' -and -not $Version.StartsWith('v')) { $Version = "v$Version" }
$InstallDir = if ($env:BUNKER_INSTALL_DIR) { $env:BUNKER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\Bunker' }

$arch = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
switch ($arch) {
    'AMD64' { $Asset = 'bunker-windows-amd64.exe' }
    'ARM64' { $Asset = 'bunker-windows-amd64.exe' } # runs under x64 emulation
    default { throw "Unsupported CPU architecture: $arch" }
}

$Base = if ($Version -eq 'latest') { "https://github.com/$Repo/releases/latest/download" } else { "https://github.com/$Repo/releases/download/$Version" }
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("bunker-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
    Write-Host "==> Downloading $Asset ($Version)"
    $fromSource = $false
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "$Base/$Asset" -OutFile (Join-Path $Tmp 'bunker.exe')
    } catch {
        # No release published yet: build from source if Go is installed.
        if (Get-Command go -ErrorAction SilentlyContinue) {
            Write-Host "    no release binary yet; building from source with Go"
            $env:GOBIN = $Tmp
            go install "github.com/$Repo/cmd/bunker@latest"
            if ($LASTEXITCODE -ne 0) { throw "Building Bunker from source failed" }
            $fromSource = $true
        } else {
            throw "No Bunker release is published yet. Install Go (https://go.dev/dl/) and run this again, or run: go install github.com/$Repo/cmd/bunker@latest"
        }
    }

    if (-not $fromSource) {
        Invoke-WebRequest -UseBasicParsing -Uri "$Base/checksums.txt" -OutFile (Join-Path $Tmp 'checksums.txt')
        $want = $null
        foreach ($line in Get-Content (Join-Path $Tmp 'checksums.txt')) {
            $parts = $line -split '\s+'
            if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $Asset) { $want = $parts[0].ToLower() }
        }
        if (-not $want) { throw "No checksum for $Asset in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 (Join-Path $Tmp 'bunker.exe')).Hash.ToLower()
        if ($want -ne $got) { throw "Checksum mismatch for $Asset (expected $want, got $got)" }
        Write-Host "    sha256 $got ok"
    }

    Write-Host "==> Installing to $InstallDir\bunker.exe"
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force (Join-Path $Tmp 'bunker.exe') (Join-Path $InstallDir 'bunker.exe')
} finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($userPath -split ';' | Where-Object { $_ -eq $InstallDir })) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$InstallDir").TrimStart(';'), 'User')
    Write-Host "    added $InstallDir to your PATH (open a new terminal to use 'bunker')"
}
$env:Path = "$env:Path;$InstallDir"

$exe = Join-Path $InstallDir 'bunker.exe'
Write-Host ""
Write-Host "Bunker is installed: $(& $exe version)" -ForegroundColor Green
Write-Host ""
Write-Host "  Try it locally:   bunker dev"
Write-Host "  Run the server:   bunker serve   (prints a setup URL and code)"
Write-Host ""

if (-not $env:BUNKER_NO_START) {
    if (-not $env:BUNKER_DATA_DIR) { $env:BUNKER_DATA_DIR = Join-Path $env:LOCALAPPDATA 'Bunker\data' }
    Write-Host "Starting Bunker in dev mode at http://localhost:8080 (Ctrl+C to stop)..."
    Write-Host "  data: $env:BUNKER_DATA_DIR"
    Start-Process 'http://localhost:8080' -ErrorAction SilentlyContinue
    & $exe dev
}

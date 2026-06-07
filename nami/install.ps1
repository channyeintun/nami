$ErrorActionPreference = "Stop"

$Repo = "channyeintun/nami"
$BinaryName = "nami"

function Enable-Tls12OrHigher {
    try {
        $protocols = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        if ([Enum]::GetNames([Net.SecurityProtocolType]) -contains "Tls13") {
            $protocols = $protocols -bor [Net.SecurityProtocolType]::Tls13
        }

        [Net.ServicePointManager]::SecurityProtocol = $protocols
    } catch {
    }
}

function Get-WindowsArch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        "X64" { return "amd64" }
        "Arm64" { return "arm64" }
        default { throw "Unsupported Windows architecture: $arch" }
    }
}

function Add-ToUserPath {
    param([string]$PathEntry)

    $currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $normalizedEntry = $PathEntry.TrimEnd('\')
    if ([string]::IsNullOrWhiteSpace($currentPath)) {
        [Environment]::SetEnvironmentVariable("Path", $PathEntry, "User")
        return
    }

    $entries = $currentPath.Split(';', [System.StringSplitOptions]::RemoveEmptyEntries)
    foreach ($entry in $entries) {
        if ($entry.TrimEnd('\') -ieq $normalizedEntry) {
            return
        }
    }

    [Environment]::SetEnvironmentVariable("Path", "$PathEntry;$currentPath", "User")
}

function Add-ToCurrentProcessPath {
    param([string]$PathEntry)

    $normalizedEntry = $PathEntry.TrimEnd('\')
    if ([string]::IsNullOrWhiteSpace($env:Path)) {
        $env:Path = $PathEntry
        return
    }

    $entries = $env:Path.Split(';', [System.StringSplitOptions]::RemoveEmptyEntries)
    foreach ($entry in $entries) {
        if ($entry.TrimEnd('\') -ieq $normalizedEntry) {
            return
        }
    }

    $env:Path = "$PathEntry;$env:Path"
}

function Test-NamiInstall {
    param([string]$BinaryPath)

    Write-Host "Verifying nami..."
    & $BinaryPath --help *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Installed binary did not pass --help verification"
    }
}

Enable-Tls12OrHigher

$Arch = Get-WindowsArch
$Platform = "windows-$Arch"
$Archive = "$BinaryName-$Platform.zip"
$ArchiveUrl = "https://github.com/$Repo/releases/latest/download/$Archive"
$InstallRoot = if ($env:INSTALL_DIR) {
    Split-Path -Parent $env:INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "Programs\nami"
}
$InstallDir = if ($env:INSTALL_DIR) {
    $env:INSTALL_DIR
} else {
    Join-Path $InstallRoot "bin"
}
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("nami-install-" + [System.Guid]::NewGuid().ToString("N"))
$ArchivePath = Join-Path $TempDir $Archive

New-Item -ItemType Directory -Path $TempDir | Out-Null
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

try {
    Write-Host "Detected platform: $Platform"
    Write-Host "Downloading $Archive..."
    Invoke-WebRequest -Uri $ArchiveUrl -OutFile $ArchivePath

    Write-Host "Expanding release archive..."
    Expand-Archive -Path $ArchivePath -DestinationPath $TempDir -Force

    $ReleaseDir = Join-Path $TempDir "$BinaryName-$Platform"
    $BinaryPath = Join-Path $ReleaseDir "$BinaryName.exe"

    if (-not (Test-Path $BinaryPath)) {
        throw "Release archive is missing required file: $BinaryPath"
    }

    Write-Host "Installing to $InstallDir..."
    Copy-Item -Path $BinaryPath -Destination (Join-Path $InstallDir "$BinaryName.exe") -Force

    Add-ToUserPath -PathEntry $InstallDir
    Add-ToCurrentProcessPath -PathEntry $InstallDir

    Test-NamiInstall -BinaryPath (Join-Path $InstallDir "$BinaryName.exe")

    Write-Host ""
    Write-Host "nami installed successfully!"
    Write-Host "Installed to: $InstallDir"
    Write-Host ""
    Write-Host "If you ran this in your current PowerShell session, nami is ready now:"
    Write-Host "  nami --help"
    Write-Host "Otherwise, open a new terminal and run the same command."
    Write-Host ""
    Write-Host "If you use a model provider that needs an API key, set it before starting Nami."
} finally {
    if (Test-Path $TempDir) {
        Remove-Item -Path $TempDir -Recurse -Force
    }
}

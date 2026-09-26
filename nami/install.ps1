# `irm ... | iex` runs this script in the caller's own session. Running the
# body in a script block keeps its $ErrorActionPreference, functions and
# variables in that block's scope instead of leaving them in the session.
& {
    $ErrorActionPreference = "Stop"
    # Windows PowerShell 5.1's Invoke-WebRequest downloads many times slower
    # while it draws a progress bar.
    $ProgressPreference = "SilentlyContinue"

    $Repo = "channyeintun/nami"
    $BinaryName = "nami"
    $EngineName = "nami-engine"
    $LauncherJsName = "$BinaryName.js"
    # Silvery, the TUI renderer, needs Node.js 24 or newer. Older releases cannot
    # even parse the launcher bundle, and nami.cmd skips them the same way.
    $MinNodeMajor = 24

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

    function Get-NodeDistArch {
        param([string]$WindowsArch)

        switch ($WindowsArch) {
            "amd64" { return "x64" }
            "arm64" { return "arm64" }
            default { throw "Unsupported Windows architecture for Node.js runtime: $WindowsArch" }
        }
    }

    function Test-NodeSupported {
        param([string]$NodePath)

        try {
            $version = (& $NodePath --version 2>$null | Out-String).Trim()
        } catch {
            return $false
        }

        if ($version -match '^v(\d+)\.') {
            return [int]$Matches[1] -ge $MinNodeMajor
        }
        return $false
    }

    function Get-JavaScriptRuntimeFromPath {
        foreach ($runtime in @("node", "bun", "deno")) {
            $command = Get-Command $runtime -ErrorAction SilentlyContinue
            if (-not $command) {
                continue
            }
            if ($runtime -eq "node" -and -not (Test-NodeSupported -NodePath $command.Source)) {
                Write-Host "Skipping node on PATH: nami needs Node.js $MinNodeMajor or newer."
                continue
            }

            return @{
                Name = $runtime
                Source = "path"
                CommandPath = $command.Source
            }
        }

        return $null
    }

    function Get-PortableNodeRuntime {
        param([string]$PortableNodeExe)

        if (-not (Test-Path $PortableNodeExe)) {
            return $null
        }
        if (-not (Test-NodeSupported -NodePath $PortableNodeExe)) {
            Write-Host "The local Node.js runtime is older than $MinNodeMajor and will be replaced."
            return $null
        }

        return @{
            Name = "node"
            Source = "portable"
            CommandPath = $PortableNodeExe
        }
    }

    function Install-PortableNodeRuntime {
        param(
            [string]$WindowsArch,
            [string]$PortableNodeDir,
            [string]$PortableNodeExe,
            [string]$TempDir
        )

        $distArch = Get-NodeDistArch -WindowsArch $WindowsArch
        $archiveEntry = "win-$distArch-zip"

        Write-Host "No supported runtime detected. Downloading a local Node.js runtime..."

        $releases = Invoke-RestMethod -Uri "https://nodejs.org/dist/index.json"
        $release = $releases |
            Where-Object { $_.lts -and $_.files -contains $archiveEntry } |
            Select-Object -First 1

        if (-not $release) {
            throw "Could not find a downloadable Node.js LTS zip for $archiveEntry"
        }

        $version = $release.version
        $archiveName = "node-$version-win-$distArch.zip"
        $downloadUrl = "https://nodejs.org/dist/$version/$archiveName"
        $archivePath = Join-Path $TempDir $archiveName
        $extractDir = Join-Path $TempDir "node-runtime"

        Write-Host "Downloading $archiveName..."
        Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath

        if (Test-Path $extractDir) {
            Remove-Item -Path $extractDir -Recurse -Force
        }

        New-Item -ItemType Directory -Path $extractDir | Out-Null

        Write-Host "Expanding portable Node.js runtime..."
        Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

        $expandedRuntimeDir = Get-ChildItem -Path $extractDir -Directory | Select-Object -First 1
        if (-not $expandedRuntimeDir) {
            throw "Portable Node.js archive did not contain an extractable directory"
        }

        $portableNodeParent = Split-Path -Parent $PortableNodeDir
        New-Item -ItemType Directory -Path $portableNodeParent -Force | Out-Null

        if (Test-Path $PortableNodeDir) {
            Remove-Item -Path $PortableNodeDir -Recurse -Force
        }

        Move-Item -Path $expandedRuntimeDir.FullName -Destination $PortableNodeDir

        if (-not (Test-Path $PortableNodeExe)) {
            throw "Portable Node.js install is missing node.exe: $PortableNodeExe"
        }

        return @{
            Name = "node"
            Source = "portable"
            CommandPath = $PortableNodeExe
            Version = $version
        }
    }

    function Ensure-SupportedRuntimeAvailable {
        param(
            [string]$WindowsArch,
            [string]$PortableNodeDir,
            [string]$PortableNodeExe,
            [string]$TempDir
        )

        $portableRuntime = Get-PortableNodeRuntime -PortableNodeExe $PortableNodeExe
        if ($portableRuntime) {
            return $portableRuntime
        }

        $pathRuntime = Get-JavaScriptRuntimeFromPath
        if ($pathRuntime) {
            return $pathRuntime
        }

        return Install-PortableNodeRuntime `
            -WindowsArch $WindowsArch `
            -PortableNodeDir $PortableNodeDir `
            -PortableNodeExe $PortableNodeExe `
            -TempDir $TempDir
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
        param([string]$WrapperPath)

        Write-Host "Verifying launcher..."
        # Keep the launcher's stderr as text rather than a terminating error: it
        # is what explains a failed check.
        $ErrorActionPreference = "Continue"
        $output = (& $WrapperPath --help 2>&1 | ForEach-Object { "$_" }) -join "`n"
        if ($LASTEXITCODE -ne 0) {
            throw "Installed launcher did not pass --help verification:`n$output"
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
    # nami.cmd looks for this runtime at ..\runtime\node relative to itself, so it
    # has to live here rather than wherever the caller would like.
    $PortableNodeDir = Join-Path $InstallRoot "runtime\node"
    $PortableNodeExe = Join-Path $PortableNodeDir "node.exe"

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
        $LauncherPath = Join-Path $ReleaseDir $LauncherJsName
        $WrapperPath = Join-Path $ReleaseDir "$BinaryName.cmd"
        $EnginePath = Join-Path $ReleaseDir "$EngineName.exe"

        foreach ($required in @($LauncherPath, $WrapperPath, $EnginePath)) {
            if (-not (Test-Path $required)) {
                throw "Release archive is missing required file: $required"
            }
        }

        Write-Host "Installing to $InstallDir..."
        Copy-Item -Path $LauncherPath -Destination (Join-Path $InstallDir $LauncherJsName) -Force
        Copy-Item -Path $WrapperPath -Destination (Join-Path $InstallDir "$BinaryName.cmd") -Force
        Copy-Item -Path $EnginePath -Destination (Join-Path $InstallDir "$EngineName.exe") -Force

        $Runtime = Ensure-SupportedRuntimeAvailable `
            -WindowsArch $Arch `
            -PortableNodeDir $PortableNodeDir `
            -PortableNodeExe $PortableNodeExe `
            -TempDir $TempDir

        Add-ToUserPath -PathEntry $InstallDir
        Add-ToCurrentProcessPath -PathEntry $InstallDir

        Test-NamiInstall -WrapperPath (Join-Path $InstallDir "$BinaryName.cmd")

        Write-Host ""
        Write-Host "nami installed successfully!"
        Write-Host "Installed to: $InstallDir"
        if ($Runtime.Source -eq "portable") {
            Write-Host "Runtime: installed a local Node.js runtime at $PortableNodeDir"
        } else {
            Write-Host "Runtime: using $($Runtime.Name) from PATH"
        }
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
}

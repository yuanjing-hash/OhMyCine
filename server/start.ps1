[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [switch]$Help
)

if ($Help) {
    @'
OhMyCine Server Windows launcher

Usage:
  .\start.ps1 [-SkipBuild]
  .\start.ps1 -Help

The script uses compatible Go from PATH. If Go is missing or too old, it
installs the official system-wide GoLang.Go package with winget. Node/npm are
checked but never installed automatically. Runtime data stays under
.runtime\windows by default. Ctrl+C stops the foreground Server process.
'@
    exit 0
}

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'scripts\windows-common.ps1')

$runtimeOverride = [Environment]::GetEnvironmentVariable('OMC_RUNTIME_DIR', 'Process')
$binaryOverride = [Environment]::GetEnvironmentVariable('OMC_BINARY_PATH', 'Process')
$databaseOverride = [Environment]::GetEnvironmentVariable('OMC_DATABASE_PATH', 'Process')
$runtime = if ($runtimeOverride) { Resolve-ServerPath $runtimeOverride } else { $script:WindowsRuntimeDirectory }
$binary = if ($binaryOverride) { Resolve-ServerPath $binaryOverride } else { Join-Path $runtime 'bin\ohmycine-server.exe' }
$database = if ($databaseOverride) { Resolve-ServerPath $databaseOverride } else { Join-Path $runtime 'data\ohmycine.db' }

if (-not $SkipBuild) {
    $go = Get-CompatibleGo -InstallIfMissing
    $tools = Get-NodeTools
    Install-WebUiDependencies $tools.Npm
    Write-Step 'Building Web UI'
    Push-Location $script:WebUiDirectory
    try { Invoke-Checked $tools.Npm @('run', 'build') 'Web UI build failed' } finally { Pop-Location }
    Write-Step 'Building embedded Server executable'
    New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($binary)) | Out-Null
    $previousCgo = [Environment]::GetEnvironmentVariable('CGO_ENABLED', 'Process')
    Push-Location $script:ServerDirectory
    try {
        $env:CGO_ENABLED = '0'
        Invoke-Checked $go @('build', '-tags', 'webui', '-o', $binary, './cmd/server') 'Server build failed'
    } finally {
        [Environment]::SetEnvironmentVariable('CGO_ENABLED', $previousCgo, 'Process')
        Pop-Location
    }
} elseif (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "-SkipBuild requires an existing executable: $binary"
}

New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($database)) | Out-Null
if (-not [Environment]::GetEnvironmentVariable('OMC_ENV', 'Process')) { $env:OMC_ENV = 'production' }
if (-not [Environment]::GetEnvironmentVariable('OMC_SERVER_HOST', 'Process')) { $env:OMC_SERVER_HOST = '127.0.0.1' }
if (-not [Environment]::GetEnvironmentVariable('OMC_SERVER_PORT', 'Process')) { $env:OMC_SERVER_PORT = '3000' }
$listenHost = [Environment]::GetEnvironmentVariable('OMC_SERVER_HOST', 'Process')
if ($listenHost.Contains(':') -and -not $listenHost.StartsWith('[')) {
    $listenHost = "[$listenHost]"
    $env:OMC_SERVER_HOST = $listenHost
}
if (-not [Environment]::GetEnvironmentVariable('OMC_PUBLIC_ORIGIN', 'Process')) {
    $originHost = switch ($listenHost) {
        '0.0.0.0' { '127.0.0.1'; break }
        '[::]' { '[::1]'; break }
        default { $listenHost }
    }
    $env:OMC_PUBLIC_ORIGIN = "http://$originHost`:$($env:OMC_SERVER_PORT)"
}
$env:OMC_DATABASE_PATH = $database

Write-Step "Starting OhMyCine Server at $($env:OMC_PUBLIC_ORIGIN)"
Write-Host "    Database: $database"
Write-Host "    Executable: $binary"
& $binary
exit $LASTEXITCODE

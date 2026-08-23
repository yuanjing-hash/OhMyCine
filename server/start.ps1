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
Non-sensitive listen settings may be placed in
.runtime\windows\config\server.json; explicit OMC_* environment variables
override that file. The default listener is 0.0.0.0:3000 while the default
advertised browser/STRM origin remains http://127.0.0.1:3000.
OMC_TMDB_READ_ACCESS_TOKEN or OMC_TMDB_API_KEY configures one runtime credential;
OHMYCINE_TMDB_READ_ACCESS_TOKEN or OHMYCINE_TMDB_API_KEY is consumed only by
this build and removed before npm/Vite and the Server process are started.
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
$localConfigPath = Join-Path $runtime 'config\server.json'
$localConfig = Read-ServerLocalConfig $localConfigPath
$binary = if ($binaryOverride) { Resolve-ServerPath $binaryOverride } else { Join-Path $runtime 'bin\ohmycine-server.exe' }
$database = if ($databaseOverride) { Resolve-ServerPath $databaseOverride } else { Join-Path $runtime 'data\ohmycine.db' }
$logDirectoryOverride = [Environment]::GetEnvironmentVariable('OMC_LOG_DIR', 'Process')
$logDirectory = if ($logDirectoryOverride) { Resolve-ServerPath $logDirectoryOverride } else { Join-Path $runtime 'logs' }
$applicationToken = [Environment]::GetEnvironmentVariable('OHMYCINE_TMDB_READ_ACCESS_TOKEN', 'Process')
$applicationApiKey = [Environment]::GetEnvironmentVariable('OHMYCINE_TMDB_API_KEY', 'Process')
[Environment]::SetEnvironmentVariable('OHMYCINE_TMDB_READ_ACCESS_TOKEN', $null, 'Process')
[Environment]::SetEnvironmentVariable('OHMYCINE_TMDB_API_KEY', $null, 'Process')
if (-not [String]::IsNullOrWhiteSpace($applicationToken) -and -not [String]::IsNullOrWhiteSpace($applicationApiKey)) {
    throw 'Configure only one build-time TMDB credential.'
}
if (-not [String]::IsNullOrWhiteSpace($applicationToken) -and ($applicationToken.Length -gt 4096 -or $applicationToken -notmatch '^[A-Za-z0-9._~-]+$')) {
    throw 'OHMYCINE_TMDB_READ_ACCESS_TOKEN contains unsupported characters.'
}
if (-not [String]::IsNullOrWhiteSpace($applicationApiKey) -and ($applicationApiKey.Length -gt 4096 -or $applicationApiKey -notmatch '^[A-Za-z0-9._~-]+$')) {
    throw 'OHMYCINE_TMDB_API_KEY contains unsupported characters.'
}

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
        $buildArguments = @('build', '-tags', 'webui')
        if (-not [String]::IsNullOrWhiteSpace($applicationToken)) {
            $buildArguments += @('-ldflags', "-X=github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb.BuiltinReadAccessToken=$applicationToken")
        } elseif (-not [String]::IsNullOrWhiteSpace($applicationApiKey)) {
            $buildArguments += @('-ldflags', "-X=github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb.BuiltinAPIKey=$applicationApiKey")
        }
        $buildArguments += @('-o', $binary, './cmd/server')
        Invoke-Checked $go $buildArguments 'Server build failed'
    } finally {
        [Environment]::SetEnvironmentVariable('CGO_ENABLED', $previousCgo, 'Process')
        Pop-Location
    }
} elseif (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "-SkipBuild requires an existing executable: $binary"
}

New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($database)) | Out-Null
if (-not [Environment]::GetEnvironmentVariable('OMC_ENV', 'Process')) { $env:OMC_ENV = 'production' }
$listenSettings = Get-ServerListenSettings $localConfig
$env:OMC_SERVER_HOST = $listenSettings.Host
$env:OMC_SERVER_PORT = $listenSettings.Port
$env:OMC_PUBLIC_ORIGIN = $listenSettings.PublicOrigin
$env:OMC_DATABASE_PATH = $database
$env:OMC_LOG_DIR = $logDirectory
if (-not [Environment]::GetEnvironmentVariable('OMC_FFMPEG_PATH', 'Process')) {
    $env:OMC_FFMPEG_PATH = Join-Path $runtime 'tools\ffmpeg\bin\ffmpeg.exe'
}

Write-Step "Starting OhMyCine Server at $($env:OMC_PUBLIC_ORIGIN)"
Write-Host "    Database: $database"
Write-Host "    Executable: $binary"
Write-Host "    Logs: $logDirectory"
Write-Host "    Local config: $localConfigPath"
& $binary
exit $LASTEXITCODE

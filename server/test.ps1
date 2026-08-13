[CmdletBinding()]
param(
    [switch]$CheckDependenciesOnly,
    [switch]$SkipWebUi,
    [switch]$SkipGoQuality,
    [switch]$SkipHealthCheck,
    [switch]$Help
)

if ($Help) {
    @'
Usage: .\test.ps1 [-CheckDependenciesOnly] [-SkipWebUi] [-SkipGoQuality] [-SkipHealthCheck]

Runs permission drift, Web UI test/typecheck/lint/build, Go test/vet/build, and
an isolated real-process /api/v1/health check. Successful health-test data is
removed; failed diagnostics remain under .runtime\windows\tests.
'@
    exit 0
}

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'scripts\windows-common.ps1')

$go = Get-CompatibleGo
$tools = Get-NodeTools
if ($CheckDependenciesOnly) {
    Write-Host "Go: $(& $go version)"
    Write-Host "Node: $(& $tools.Node --version)"
    Write-Host "npm: $(& $tools.Npm --version)"
    exit 0
}

& (Join-Path $PSScriptRoot 'scripts\test-windows-common.ps1')
if ($LASTEXITCODE -ne 0) { throw "Windows script contract checks failed (exit code $LASTEXITCODE)" }

if (-not $SkipWebUi) {
    Install-WebUiDependencies $tools.Npm
    Push-Location $script:WebUiDirectory
    try {
        foreach ($command in @('permissions:check', 'test', 'typecheck', 'lint', 'build')) {
            Write-Step "Running Web UI $command"
            Invoke-Checked $tools.Npm @('run', $command) "Web UI $command failed"
        }
    } finally { Pop-Location }
}

$previousCgo = [Environment]::GetEnvironmentVariable('CGO_ENABLED', 'Process')
$env:CGO_ENABLED = '0'
try {
    if (-not $SkipGoQuality) {
        Push-Location $script:ServerDirectory
        try {
            Write-Step 'Running Go tests'; Invoke-Checked $go @('test', './...') 'go test failed'
            Write-Step 'Running Go vet'; Invoke-Checked $go @('vet', './...') 'go vet failed'
            $qualityBinary = Join-Path $script:WindowsRuntimeDirectory 'tests\quality\ohmycine-server.exe'
            New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($qualityBinary)) | Out-Null
            Write-Step 'Building Server'; Invoke-Checked $go @('build', '-o', $qualityBinary, './cmd/server') 'go build failed'
        } finally { Pop-Location }
    }

    if (-not $SkipHealthCheck) {
        $testsRoot = Join-Path $script:WindowsRuntimeDirectory 'tests'
        $testDirectory = Join-Path $testsRoot ([Guid]::NewGuid().ToString('N'))
        Assert-SafeTestDirectory $testDirectory $testsRoot
        $binary = Join-Path $testDirectory 'bin\ohmycine-server.exe'
        $database = Join-Path $testDirectory 'data\ohmycine.db'
        $stdout = Join-Path $testDirectory 'server.stdout.log'
        $stderr = Join-Path $testDirectory 'server.stderr.log'
        New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($binary)), ([IO.Path]::GetDirectoryName($database)) | Out-Null
        $process = $null; $healthy = $false; $old = $null
        try {
            Push-Location $script:ServerDirectory
            try { Invoke-Checked $go @('build', '-o', $binary, './cmd/server') 'health-check Server build failed' } finally { Pop-Location }

            $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
            $listener.Start(); $port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
            $old = @{}
            foreach ($name in @('OMC_ENV','OMC_SERVER_HOST','OMC_SERVER_PORT','OMC_PUBLIC_ORIGIN','OMC_DATABASE_PATH','OMC_LOG_DIR')) { $old[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
            $env:OMC_ENV = 'production'; $env:OMC_SERVER_HOST = '127.0.0.1'; $env:OMC_SERVER_PORT = "$port"
            $env:OMC_PUBLIC_ORIGIN = "http://127.0.0.1:$port"; $env:OMC_DATABASE_PATH = $database
            $env:OMC_LOG_DIR = Join-Path $testDirectory 'logs'
            $process = Start-Process -FilePath $binary -WorkingDirectory $testDirectory -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru -WindowStyle Hidden
            $deadline = [DateTime]::UtcNow.AddSeconds(30)
            do {
                if ($process.HasExited) { throw "Server exited before health check (code $($process.ExitCode))." }
                try {
                    $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/api/v1/health" -TimeoutSec 2
                    if ($response.StatusCode -eq 200) { $healthy = $true; break }
                } catch { Start-Sleep -Milliseconds 250 }
            } while ([DateTime]::UtcNow -lt $deadline)
            if (-not $healthy) { throw 'Health check timed out after 30 seconds.' }
        } catch {
            Write-Host "Health diagnostics retained at: $testDirectory"
            throw
        } finally {
            if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force; $process.WaitForExit() }
            if ($null -ne $old) {
                foreach ($name in $old.Keys) { [Environment]::SetEnvironmentVariable($name, $old[$name], 'Process') }
            }
        }
        if ($healthy) { Assert-SafeTestDirectory $testDirectory $testsRoot; Remove-Item -LiteralPath $testDirectory -Recurse -Force }
        Write-Step 'Isolated Server health check passed'
    }
} finally { [Environment]::SetEnvironmentVariable('CGO_ENABLED', $previousCgo, 'Process') }

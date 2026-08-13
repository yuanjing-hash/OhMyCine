[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'windows-common.ps1')

$go = Get-CompatibleGo
$testsRoot = [IO.Path]::GetFullPath((Join-Path $script:WindowsRuntimeDirectory 'tests'))
$testDirectory = [IO.Path]::GetFullPath((Join-Path $testsRoot ('directory-picker-' + [Guid]::NewGuid().ToString('N'))))
Assert-SafeTestDirectory $testDirectory $testsRoot
$binary = Join-Path $testDirectory 'bin\ohmycine-server.exe'
$database = Join-Path $testDirectory 'data\ohmycine.db'
$stdout = Join-Path $testDirectory 'server.stdout.log'
$stderr = Join-Path $testDirectory 'server.stderr.log'
New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($binary)), ([IO.Path]::GetDirectoryName($database)) | Out-Null

$process = $null
$old = @{}
try {
    Push-Location $script:ServerDirectory
    try { Invoke-Checked $go @('build', '-o', $binary, './cmd/server') 'directory picker smoke build failed' } finally { Pop-Location }
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
    foreach ($name in @('OMC_ENV','OMC_SERVER_HOST','OMC_SERVER_PORT','OMC_PUBLIC_ORIGIN','OMC_DATABASE_PATH')) { $old[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    $origin = "http://127.0.0.1:$port"
    $env:OMC_ENV = 'production'; $env:OMC_SERVER_HOST = '127.0.0.1'; $env:OMC_SERVER_PORT = "$port"
    $env:OMC_PUBLIC_ORIGIN = $origin; $env:OMC_DATABASE_PATH = $database
    $process = Start-Process -FilePath $binary -WorkingDirectory $testDirectory -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru -WindowStyle Hidden
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        if ($process.HasExited) { throw "Server exited before directory picker smoke (code $($process.ExitCode))." }
        try { $null = Invoke-RestMethod -Uri "$origin/api/v1/health" -TimeoutSec 2; break } catch { Start-Sleep -Milliseconds 200 }
    } while ([DateTime]::UtcNow -lt $deadline)

    $setupBody = @{ username = 'picker-smoke-owner'; display_name = 'Picker Smoke'; password = 'directory-picker-smoke-password' } | ConvertTo-Json
    $null = Invoke-RestMethod -Uri "$origin/api/v1/setup/owner" -Method Post -ContentType 'application/json' -Headers @{ Origin = $origin } -Body $setupBody -SessionVariable session
    $rootsResponse = Invoke-WebRequest -UseBasicParsing -Uri "$origin/api/v1/filesystem/roots" -WebSession $session
    $roots = $rootsResponse.Content | ConvertFrom-Json
    if ($rootsResponse.Headers['Cache-Control'] -ne 'no-store') { throw 'Roots response missing Cache-Control: no-store.' }
    $root = $roots.data.items | Where-Object { $_.enterable -and $_.token } | Select-Object -First 1
    if ($null -eq $root) { throw 'No enterable Windows root returned.' }
    $encodedToken = [Uri]::EscapeDataString($root.token)
    $listingResponse = Invoke-WebRequest -UseBasicParsing -Uri "$origin/api/v1/filesystem/directories?token=$encodedToken" -WebSession $session
    $listing = $listingResponse.Content | ConvertFrom-Json
    if ($listingResponse.Headers['Cache-Control'] -ne 'no-store' -or $listing.data.platform -ne 'windows') { throw 'Native Windows directory response contract failed.' }
    Write-Step "Windows directory picker smoke passed (roots=$($roots.data.items.Count), directories=$($listing.data.items.Count), truncated=$($listing.data.truncated))"
} catch {
    Write-Host "Directory picker smoke diagnostics retained at: $testDirectory"
    throw
} finally {
    if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force; $process.WaitForExit() }
    foreach ($name in $old.Keys) { [Environment]::SetEnvironmentVariable($name, $old[$name], 'Process') }
}
if (Test-Path -LiteralPath $testDirectory) { Assert-SafeTestDirectory $testDirectory $testsRoot; Remove-Item -LiteralPath $testDirectory -Recurse -Force }

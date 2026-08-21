Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'windows-common.ps1')

$expectedArguments = 'install --id GoLang.Go --exact --source winget --accept-source-agreements --accept-package-agreements'
$actualArguments = (Get-GoInstallArguments) -join ' '
if ($actualArguments -ne $expectedArguments) {
    throw "Unexpected GoLang.Go winget arguments: $actualArguments"
}

$testsRoot = Join-Path $script:WindowsRuntimeDirectory 'tests'
Assert-SafeTestDirectory (Join-Path $testsRoot 'contract-test') $testsRoot

$configTestDirectory = Join-Path $testsRoot ('local-config-' + [Guid]::NewGuid().ToString('N'))
Assert-SafeTestDirectory $configTestDirectory $testsRoot
try {
    New-Item -ItemType Directory -Force -Path $configTestDirectory | Out-Null
    $missingConfig = Read-ServerLocalConfig (Join-Path $configTestDirectory 'missing.json')
    if ($missingConfig.Count -ne 0) { throw 'A missing Windows local Server config did not produce defaults.' }
    $validConfigPath = Join-Path $configTestDirectory 'server.json'
    Set-Content -LiteralPath $validConfigPath -Encoding UTF8 -Value '{"listen_host":"0.0.0.0","port":3100,"public_origin":"http://192.0.2.10:3100"}'
    $loadedConfig = Read-ServerLocalConfig $validConfigPath
    if ($loadedConfig['listen_host'] -ne '0.0.0.0' -or $loadedConfig['port'] -ne 3100 -or $loadedConfig['public_origin'] -ne 'http://192.0.2.10:3100') { throw 'Windows local Server config was not parsed as expected.' }
    $unsafeConfigPath = Join-Path $configTestDirectory 'unsafe.json'
    Set-Content -LiteralPath $unsafeConfigPath -Encoding UTF8 -Value '{"api_key":"must-not-be-supported"}'
    $rejected = $false
    try { Read-ServerLocalConfig $unsafeConfigPath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted a credential field.' }
    foreach ($strictJSON in @('{"PORT":3000}', '{"listen_host":3000}', '{"port":"3000"}', '{"public_origin":3000}')) {
        Set-Content -LiteralPath $unsafeConfigPath -Encoding UTF8 -Value $strictJSON
        $rejected = $false
        try { Read-ServerLocalConfig $unsafeConfigPath | Out-Null } catch { $rejected = $true }
        if (-not $rejected) { throw "Windows local Server config accepted a non-strict field or value: $strictJSON" }
    }
    $wildcardOriginPath = Join-Path $configTestDirectory 'wildcard-origin.json'
    Set-Content -LiteralPath $wildcardOriginPath -Encoding UTF8 -Value '{"public_origin":"http://0.0.0.0:3000"}'
    $rejected = $false
    try { Read-ServerLocalConfig $wildcardOriginPath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted a wildcard public origin.' }
    Set-Content -LiteralPath $wildcardOriginPath -Encoding UTF8 -Value '{"public_origin":"http://[::]:3000"}'
    $rejected = $false
    try { Read-ServerLocalConfig $wildcardOriginPath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted an IPv6 wildcard public origin.' }
    Set-Content -LiteralPath $wildcardOriginPath -Encoding UTF8 -Value '{"public_origin":"http://0.0.0.0.:3000"}'
    $rejected = $false
    try { Read-ServerLocalConfig $wildcardOriginPath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted a dotted wildcard public origin.' }
    $badJsonPath = Join-Path $configTestDirectory 'bad.json'
    Set-Content -LiteralPath $badJsonPath -Encoding UTF8 -Value '{not-json}'
    $rejected = $false
    try { Read-ServerLocalConfig $badJsonPath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted invalid JSON.' }
    $oversizePath = Join-Path $configTestDirectory 'oversize.json'
    Set-Content -LiteralPath $oversizePath -Encoding ASCII -Value ('{' + (' ' * 65536) + '}')
    $rejected = $false
    try { Read-ServerLocalConfig $oversizePath | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Windows local Server config accepted a file larger than 64 KiB.' }

    $previousHost = [Environment]::GetEnvironmentVariable('OMC_SERVER_HOST', 'Process')
    $previousPort = [Environment]::GetEnvironmentVariable('OMC_SERVER_PORT', 'Process')
    $previousOrigin = [Environment]::GetEnvironmentVariable('OMC_PUBLIC_ORIGIN', 'Process')
    try {
        [Environment]::SetEnvironmentVariable('OMC_SERVER_HOST', $null, 'Process')
        [Environment]::SetEnvironmentVariable('OMC_SERVER_PORT', $null, 'Process')
        [Environment]::SetEnvironmentVariable('OMC_PUBLIC_ORIGIN', $null, 'Process')
        $fileSettings = Get-ServerListenSettings $loadedConfig
        if ($fileSettings.Host -ne '0.0.0.0' -or $fileSettings.Port -ne '3100' -or $fileSettings.PublicOrigin -ne 'http://192.0.2.10:3100') { throw 'Windows local config precedence is incorrect.' }
        $env:OMC_SERVER_HOST = '127.0.0.2'
        $env:OMC_SERVER_PORT = '3200'
        $env:OMC_PUBLIC_ORIGIN = 'http://198.51.100.20:3200'
        $environmentSettings = Get-ServerListenSettings $loadedConfig
        if ($environmentSettings.Host -ne '127.0.0.2' -or $environmentSettings.Port -ne '3200' -or $environmentSettings.PublicOrigin -ne 'http://198.51.100.20:3200') { throw 'OMC environment variables did not override Windows local config.' }
    } finally {
        [Environment]::SetEnvironmentVariable('OMC_SERVER_HOST', $previousHost, 'Process')
        [Environment]::SetEnvironmentVariable('OMC_SERVER_PORT', $previousPort, 'Process')
        [Environment]::SetEnvironmentVariable('OMC_PUBLIC_ORIGIN', $previousOrigin, 'Process')
    }
} finally {
    if (Test-Path -LiteralPath $configTestDirectory) { Remove-Item -LiteralPath $configTestDirectory -Recurse -Force }
}

foreach ($unsafePath in @($testsRoot, ($testsRoot + '-sibling\contract-test'))) {
    $rejected = $false
    try { Assert-SafeTestDirectory $unsafePath $testsRoot } catch { $rejected = $true }
    if (-not $rejected) { throw "Unsafe cleanup path was accepted: $unsafePath" }
}

$linkerTarget = 'github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb.BuiltinReadAccessToken'
$apiKeyLinkerTarget = 'github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb.BuiltinAPIKey'
$windowsStart = Get-Content -LiteralPath (Join-Path $script:ServerDirectory 'start.ps1') -Raw
$linuxStart = Get-Content -LiteralPath (Join-Path $script:ServerDirectory 'start.sh') -Raw
$windowsTest = Get-Content -LiteralPath (Join-Path $script:ServerDirectory 'test.ps1') -Raw
$manualBuild = Get-Content -LiteralPath (Join-Path (Split-Path $script:ServerDirectory -Parent) '.github\workflows\manual-build.yml') -Raw
foreach ($contract in @($windowsStart, $linuxStart, $manualBuild)) {
    if (-not $contract.Contains('OHMYCINE_TMDB_READ_ACCESS_TOKEN') -or -not $contract.Contains($linkerTarget) -or -not $contract.Contains('OHMYCINE_TMDB_API_KEY') -or -not $contract.Contains($apiKeyLinkerTarget)) {
        throw 'TMDB dual application credential linker injection contract is missing.'
    }
    if (-not $contract.Contains('A-Za-z0-9._~-') -or -not $contract.Contains('4096')) {
        throw 'TMDB linker credential length/character validation contract is missing.'
    }
}
if (-not $windowsStart.Contains("SetEnvironmentVariable('OHMYCINE_TMDB_READ_ACCESS_TOKEN', `$null, 'Process')") -or -not $windowsStart.Contains("SetEnvironmentVariable('OHMYCINE_TMDB_API_KEY', `$null, 'Process')") -or -not $linuxStart.Contains('unset OHMYCINE_TMDB_READ_ACCESS_TOKEN OHMYCINE_TMDB_API_KEY')) {
    throw 'Build-only TMDB credentials are not removed before Server runtime.'
}
foreach ($name in @('OHMYCINE_TMDB_READ_ACCESS_TOKEN', 'OHMYCINE_TMDB_API_KEY', 'OMC_TMDB_READ_ACCESS_TOKEN', 'OMC_TMDB_API_KEY')) {
    if (-not $windowsTest.Contains("SetEnvironmentVariable('$name', `$null, 'Process')")) {
        throw "Windows quality gate does not isolate $name from test subprocesses."
    }
}
if ($windowsStart.IndexOf("SetEnvironmentVariable('OHMYCINE_TMDB_READ_ACCESS_TOKEN', `$null, 'Process')") -gt $windowsStart.IndexOf('Install-WebUiDependencies') -or $windowsStart.IndexOf("SetEnvironmentVariable('OHMYCINE_TMDB_API_KEY', `$null, 'Process')") -gt $windowsStart.IndexOf('Install-WebUiDependencies') -or $linuxStart.IndexOf('unset OHMYCINE_TMDB_READ_ACCESS_TOKEN OHMYCINE_TMDB_API_KEY') -gt $linuxStart.IndexOf('NPM_BIN=')) {
    throw 'Build-only TMDB credential remains exported while Web UI dependencies or assets are built.'
}
foreach ($contract in @($windowsStart, $linuxStart, $manualBuild)) {
    if ($contract -match 'Builtin(ReadAccessToken|APIKey)[^\r\n]*OMC_TMDB_(READ_ACCESS_TOKEN|API_KEY)') {
        throw 'Runtime deployment TMDB credential was wired into linker injection.'
    }
}
if (-not $manualBuild.Contains('TMDB credential GitHub Secret is required for the official Server artifact')) {
    throw 'Official Server artifact does not fail closed when the TMDB Secret is absent.'
}

Write-Step 'Windows script contract checks passed'

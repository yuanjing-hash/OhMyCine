Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ServerDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:WebUiDirectory = Join-Path $script:ServerDirectory 'webui'
$script:WindowsRuntimeDirectory = Join-Path $script:ServerDirectory '.runtime\windows'

function Write-Step([string]$Message) {
    Write-Host "==> $Message"
}

function Resolve-ServerPath([string]$Path) {
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path $script:ServerDirectory $Path))
}

function Read-ServerLocalConfig([string]$Path) {
    $result = @{}
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $result }
    $file = Get-Item -LiteralPath $Path
    if ($file.Length -gt 65536) { throw 'Server local config exceeds 64 KiB.' }
    try { $parsed = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json } catch { throw 'Server local config is not valid JSON.' }
    if ($null -eq $parsed -or $parsed -isnot [PSCustomObject]) { throw 'Server local config must be a JSON object.' }
    $allowed = @('listen_host', 'port', 'public_origin')
    foreach ($property in $parsed.PSObject.Properties) {
        if ($allowed -cnotcontains $property.Name) { throw "Unsupported Server local config field: $($property.Name)" }
        $result[$property.Name] = $property.Value
    }
    if ($result.ContainsKey('listen_host')) {
        if ($result['listen_host'] -isnot [string]) { throw 'Server local config listen_host must be a string.' }
        $hostValue = [string]$result['listen_host']
        if ([String]::IsNullOrWhiteSpace($hostValue) -or $hostValue.Length -gt 255 -or $hostValue -match '[\s/\\]') { throw 'Server local config listen_host is invalid.' }
        $result['listen_host'] = $hostValue.Trim()
    }
    if ($result.ContainsKey('port')) {
        if ($result['port'] -isnot [int] -and $result['port'] -isnot [long]) { throw 'Server local config port must be an integer.' }
        $portValue = [long]$result['port']
        if ($portValue -lt 1 -or $portValue -gt 65535) { throw 'Server local config port must be between 1 and 65535.' }
        $result['port'] = $portValue
    }
    if ($result.ContainsKey('public_origin')) {
        if ($result['public_origin'] -isnot [string]) { throw 'Server local config public_origin must be a string.' }
        $originValue = [string]$result['public_origin']
        $originUri = $null
        if (-not [Uri]::TryCreate($originValue, [UriKind]::Absolute, [ref]$originUri) -or ($originUri.Scheme -ne 'http' -and $originUri.Scheme -ne 'https') -or -not [String]::IsNullOrEmpty($originUri.UserInfo) -or $originUri.AbsolutePath -ne '/' -or -not [String]::IsNullOrEmpty($originUri.Query) -or -not [String]::IsNullOrEmpty($originUri.Fragment)) { throw 'Server local config public_origin must be an HTTP(S) origin without path, credentials, query, or fragment.' }
        if ($originValue -match '^https?://(?:0\.0\.0\.0\.?|\[::\])(?::[0-9]+)?/?$') { throw 'Server local config public_origin cannot use a wildcard listen address.' }
        $result['public_origin'] = $originValue.TrimEnd('/')
    }
    return $result
}

function Get-ServerListenSettings([hashtable]$Config) {
    $hostValue = [Environment]::GetEnvironmentVariable('OMC_SERVER_HOST', 'Process')
    if ([String]::IsNullOrWhiteSpace($hostValue)) {
        $hostValue = if ($Config.ContainsKey('listen_host')) { [string]$Config['listen_host'] } else { '0.0.0.0' }
    }
    if ($hostValue.Contains(':') -and -not $hostValue.StartsWith('[')) { $hostValue = "[$hostValue]" }

    $portValue = [Environment]::GetEnvironmentVariable('OMC_SERVER_PORT', 'Process')
    if ([String]::IsNullOrWhiteSpace($portValue)) {
        $portValue = if ($Config.ContainsKey('port')) { [string]$Config['port'] } else { '3000' }
    }

    $originValue = [Environment]::GetEnvironmentVariable('OMC_PUBLIC_ORIGIN', 'Process')
    if ([String]::IsNullOrWhiteSpace($originValue)) {
        if ($Config.ContainsKey('public_origin')) {
            $originValue = [string]$Config['public_origin']
        } else {
            $originHost = switch ($hostValue) {
                '0.0.0.0' { '127.0.0.1'; break }
                '[::]' { '[::1]'; break }
                default { $hostValue }
            }
            $originValue = "http://$originHost`:$portValue"
        }
    }
    return @{ Host = $hostValue; Port = $portValue; PublicOrigin = $originValue }
}

function Invoke-Checked([string]$FilePath, [string[]]$Arguments, [string]$FailureMessage) {
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FailureMessage (exit code $LASTEXITCODE)"
    }
}

function Get-RequiredGoVersion {
    $match = Select-String -Path (Join-Path $script:ServerDirectory 'go.mod') -Pattern '^go\s+(\d+)\.(\d+)' | Select-Object -First 1
    if ($null -eq $match) { throw 'go.mod does not declare a Go version.' }
    return [version]("{0}.{1}" -f $match.Matches[0].Groups[1].Value, $match.Matches[0].Groups[2].Value)
}

function Get-GoVersion([string]$GoCommand) {
    $output = & $GoCommand version 2>&1
    if ($LASTEXITCODE -ne 0 -or "$output" -notmatch 'go(\d+)\.(\d+)') { return $null }
    return [version]("{0}.{1}" -f $Matches[1], $Matches[2])
}

function Refresh-ProcessPath {
    $current = [Environment]::GetEnvironmentVariable('Path', 'Process')
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $paths = @($current, $machine, $user, 'C:\Program Files\Go\bin') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    $env:Path = ($paths -join ';')
}

function Get-GoInstallArguments {
    return @('install', '--id', 'GoLang.Go', '--exact', '--source', 'winget', '--accept-source-agreements', '--accept-package-agreements')
}

function Get-CompatibleGo([switch]$InstallIfMissing) {
    $required = Get-RequiredGoVersion
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        $version = Get-GoVersion $command.Source
        if ($null -ne $version -and $version -ge $required) { return $command.Source }
    }
    if (-not $InstallIfMissing) {
        throw "Go $required or newer is required. Run start.ps1 to install the official system package, or install GoLang.Go with winget."
    }
    $winget = Get-Command winget.exe -ErrorAction SilentlyContinue
    if ($null -eq $winget) {
        throw "Go $required or newer is required and Windows Package Manager (winget) is unavailable. Install the official GoLang.Go package, then retry."
    }
    Write-Step "Installing official system Go package GoLang.Go (Windows may show a UAC prompt)"
    Invoke-Checked $winget.Source (Get-GoInstallArguments) 'winget failed to install GoLang.Go. Approve UAC and review the installer output'
    Refresh-ProcessPath
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -eq $command) { throw 'GoLang.Go installation completed, but go.exe could not be found after refreshing this process PATH.' }
    $version = Get-GoVersion $command.Source
    if ($null -eq $version -or $version -lt $required) { throw "Installed Go does not satisfy go.mod (required $required or newer)." }
    return $command.Source
}

function Get-NodeTools {
    $node = Get-Command node.exe -ErrorAction SilentlyContinue
    $npm = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($null -eq $node -or $null -eq $npm) {
        throw 'Node.js and npm are required on PATH. Install a supported Node.js release and retry.'
    }
    return @{ Node = $node.Source; Npm = $npm.Source }
}

function Install-WebUiDependencies([string]$NpmCommand) {
    $lockfile = Join-Path $script:WebUiDirectory 'package-lock.json'
    if (-not (Test-Path -LiteralPath $lockfile -PathType Leaf)) { throw "Missing Web UI lockfile: $lockfile" }
    $modules = Join-Path $script:WebUiDirectory 'node_modules'
    $stamp = Join-Path $modules '.ohmycine-package-lock.sha256'
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $lockfile).Hash
    $current = if (Test-Path -LiteralPath $stamp) { (Get-Content -Raw -LiteralPath $stamp).Trim() } else { '' }
    if (-not (Test-Path -LiteralPath $modules -PathType Container) -or $current -ne $hash) {
        Write-Step 'Installing Web UI dependencies from package-lock.json'
        Push-Location $script:WebUiDirectory
        try { Invoke-Checked $NpmCommand @('ci') 'npm ci failed' } finally { Pop-Location }
        Set-Content -LiteralPath $stamp -Value $hash -Encoding ASCII
    } else {
        Write-Step 'Reusing Web UI node_modules (lockfile unchanged)'
    }
}

function Assert-SafeTestDirectory([string]$Directory, [string]$TestsRoot) {
    $target = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\')
    $root = [System.IO.Path]::GetFullPath($TestsRoot).TrimEnd('\')
    if ($target -eq $root -or -not $target.StartsWith($root + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing test cleanup outside the test root: $target"
    }
}

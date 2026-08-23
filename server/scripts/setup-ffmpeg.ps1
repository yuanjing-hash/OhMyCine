[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# This is a deliberately pinned snapshot. The upstream convenience URL is
# rolling, but content is accepted only when it still matches this digest.
$archiveUrl = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip'
$archiveSha256 = 'fec81ae03971d9dd4be3ebe02e263bd2ec1d789483f931bdba5f5715e65da2e9'
$snapshot = '2026-08-23'

$serverRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$toolsRoot = [IO.Path]::GetFullPath((Join-Path $serverRoot '.runtime\windows\tools'))
$targetRoot = [IO.Path]::GetFullPath((Join-Path $toolsRoot 'ffmpeg'))
$tempRoot = [IO.Path]::GetFullPath((Join-Path $toolsRoot ('.ffmpeg-install-' + [Guid]::NewGuid().ToString('N'))))

if (-not $toolsRoot.EndsWith([IO.Path]::DirectorySeparatorChar)) {
    $toolsPrefix = $toolsRoot + [IO.Path]::DirectorySeparatorChar
} else {
    $toolsPrefix = $toolsRoot
}
if (-not $tempRoot.StartsWith($toolsPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'FFmpeg temporary path escaped the Server isolated tools directory.'
}
if (Test-Path -LiteralPath $targetRoot) {
    throw "FFmpeg is already present at $targetRoot. This safe installer will not overwrite it."
}

New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
$archivePath = Join-Path $tempRoot 'ffmpeg.zip'
$extractRoot = Join-Path $tempRoot 'extract'

try {
    Write-Host "Downloading pinned FFmpeg snapshot $snapshot into the Server isolated directory..."
    Invoke-WebRequest -UseBasicParsing -Uri $archiveUrl -OutFile $archivePath -MaximumRedirection 3
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actual -ne $archiveSha256) {
        throw "FFmpeg archive checksum mismatch. Expected $archiveSha256, got $actual. The upstream rolling asset may have changed; update the reviewed lock before installing."
    }
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractRoot
    $ffmpeg = Get-ChildItem -LiteralPath $extractRoot -Filter 'ffmpeg.exe' -File -Recurse | Select-Object -First 1
    $ffprobe = Get-ChildItem -LiteralPath $extractRoot -Filter 'ffprobe.exe' -File -Recurse | Select-Object -First 1
    if ($null -eq $ffmpeg -or $null -eq $ffprobe) {
        throw 'The verified archive does not contain the expected FFmpeg tools.'
    }
    $targetBin = Join-Path $targetRoot 'bin'
    New-Item -ItemType Directory -Path $targetBin -Force | Out-Null
    Copy-Item -LiteralPath $ffmpeg.FullName -Destination (Join-Path $targetBin 'ffmpeg.exe')
    Copy-Item -LiteralPath $ffprobe.FullName -Destination (Join-Path $targetBin 'ffprobe.exe')
    Set-Content -LiteralPath (Join-Path $targetRoot 'INSTALL-SOURCE.txt') -Encoding UTF8 -Value @(
        "snapshot=$snapshot"
        "url=$archiveUrl"
        "sha256=$archiveSha256"
        'distribution=https://www.gyan.dev/ffmpeg/builds/'
        'license=https://ffmpeg.org/legal.html'
    )
    & (Join-Path $targetBin 'ffmpeg.exe') -hide_banner -version | Select-Object -First 1
    Write-Host "FFmpeg installed without modifying the system PATH: $targetBin"
} catch {
    if (Test-Path -LiteralPath $targetRoot) {
        $resolvedTarget = [IO.Path]::GetFullPath($targetRoot)
        if ($resolvedTarget.StartsWith($toolsPrefix, [StringComparison]::OrdinalIgnoreCase) -and $resolvedTarget -ne $toolsRoot) {
            Remove-Item -LiteralPath $resolvedTarget -Recurse -Force
        }
    }
    throw
} finally {
    if (Test-Path -LiteralPath $tempRoot) {
        $resolvedTemp = [IO.Path]::GetFullPath($tempRoot)
        if ($resolvedTemp.StartsWith($toolsPrefix, [StringComparison]::OrdinalIgnoreCase) -and $resolvedTemp -ne $toolsRoot) {
            Remove-Item -LiteralPath $resolvedTemp -Recurse -Force
        }
    }
}

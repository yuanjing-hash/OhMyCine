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

foreach ($unsafePath in @($testsRoot, ($testsRoot + '-sibling\contract-test'))) {
    $rejected = $false
    try { Assert-SafeTestDirectory $unsafePath $testsRoot } catch { $rejected = $true }
    if (-not $rejected) { throw "Unsafe cleanup path was accepted: $unsafePath" }
}

Write-Step 'Windows script contract checks passed'

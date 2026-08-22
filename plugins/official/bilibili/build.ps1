$ErrorActionPreference = 'Stop'

$pluginRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repositoryRoot = Resolve-Path (Join-Path $pluginRoot '..\..\..')
$sdkRoot = Join-Path $repositoryRoot 'plugin-sdk'
$outputRoot = Join-Path $pluginRoot 'dist'

rustup target add wasm32-unknown-unknown
cargo build --manifest-path (Join-Path $pluginRoot 'Cargo.toml') --target wasm32-unknown-unknown --release
$wasmPath = Join-Path $pluginRoot 'target\wasm32-unknown-unknown\release\ohmycine_plugin_bilibili.wasm'
node (Join-Path $sdkRoot 'scripts\pack-plugin.mjs') --manifest (Join-Path $pluginRoot 'plugin.template.json') --wasm $wasmPath --out $outputRoot

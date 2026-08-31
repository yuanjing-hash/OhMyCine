import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { normalizePlayerInteractionSettings } from '../src/services/playerInteractionSettings'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const defaults = normalizePlayerInteractionSettings({})
assert.deepEqual({
  fsrMode: defaults.fsrMode,
  fsrSharpness: defaults.fsrSharpness,
  fsrDenoise: defaults.fsrDenoise,
  fsrTarget: defaults.fsrTarget,
}, {
  fsrMode: 'auto',
  fsrSharpness: 35,
  fsrDenoise: true,
  fsrTarget: 'auto',
})

const normalized = normalizePlayerInteractionSettings({
  fsrMode: 'force',
  fsrSharpness: 140.4,
  fsrDenoise: false,
  fsrTarget: '2160p',
})
assert.equal(normalized.fsrMode, 'force')
assert.equal(normalized.fsrSharpness, 100)
assert.equal(normalized.fsrDenoise, false)
assert.equal(normalized.fsrTarget, '2160p')
assert.equal(normalizePlayerInteractionSettings({ fsrSharpness: Number.NaN }).fsrSharpness, 35)

const useMpv = await source('src/composables/useMpv.ts')
for (const field of ['fsrMode', 'fsrSharpness', 'fsrDenoise', 'fsrTarget'])
  assert.match(useMpv, new RegExp(`${field}: settings\\.${field}`))
assert.match(useMpv, /playbackDiagnostics\.value = diagnostics/)

const shader = await source('src-tauri/resources/shaders/ohmycine-fsr-v1.glsl')
assert.match(shader, /FidelityFX Super Resolution v1\.0\.2 \(EASU\)/)
assert.match(shader, /FidelityFX Super Resolution v1\.0\.2 \(RCAS\)/)
assert.match(shader, /\/\/!PARAM OHMYCINE_SHARPNESS/)
assert.match(shader, /\/\/!PARAM OHMYCINE_DENOISE/)
assert.match(shader, /\/\/!PARAM OHMYCINE_TARGET_WIDTH/)
assert.match(shader, /\/\/!PARAM OHMYCINE_TARGET_HEIGHT/)
const shaderConditions = shader.match(/^\/\/!WHEN .+$/gm) ?? []
assert.equal(shaderConditions.length, 2)
for (const condition of shaderConditions) {
  assert.match(condition, /OHMYCINE_TARGET_WIDTH[\s\S]*LUMA\.w > /)
  assert.match(condition, /OHMYCINE_TARGET_HEIGHT[\s\S]*LUMA\.h > /)
}

const rustSettings = await source('src-tauri/src/commands/player_shared.rs')
assert.match(rustSettings, /2\.0 \* \(1\.0 - self\.fsr_sharpness \/ 100\.0\)/)
for (const [target, shortEdge] of [['1080p', 1080], ['1440p', 1440], ['2160p', 2160]] as const)
  assert.match(rustSettings, new RegExp(`"${target}" => Some\\(${shortEdge}\\)`))

const rustCommands = await source('src-tauri/src/commands/player.rs')
assert.match(rustCommands, /include_bytes!\("\.\.\/\.\.\/resources\/shaders\/ohmycine-fsr-v1\.glsl"\)/)
assert.match(rustCommands, /layout\.cache_dir\.join\("mpv"\)\.join\("shaders"\)/)
assert.match(rustCommands, /materialize_fsr_shader\(&app\)\.ok\(\)/)
assert.doesNotMatch(rustCommands, /shader_path:\s*String/)

const rustPlayer = await source('src-tauri/src/mpv/player.rs')
assert.match(rustPlayer, /\["change-list", "glsl-shaders", "clr", ""\]/)
assert.match(rustPlayer, /\["change-list", "glsl-shaders", "append", &shader_path\]/)
assert.match(rustPlayer, /record_fsr_fallback\("FSR Shader 编译失败，已恢复普通缩放。"\)/)
assert.match(rustPlayer, /refresh_fsr_target_parameters\(\)/)
assert.match(rustPlayer, /let scale = f64::from\(target_short_edge\) \/ short_edge/)
const storeSurfaceIndex = rustPlayer.indexOf('self.render_surface = Some(surface);')
const applyFsrIndex = rustPlayer.indexOf('self.apply_fsr_runtime_safely();', storeSurfaceIndex)
assert.ok(storeSurfaceIndex >= 0 && applyFsrIndex > storeSurfaceIndex, 'Windows auto mode must apply FSR after storing the initialized render surface')

const kotlinPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvPlugin.kt')
for (const field of ['fsrMode', 'fsrSharpness', 'fsrDenoise', 'fsrTarget'])
  assert.match(kotlinPlugin, new RegExp(field))

const kotlinHost = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt')
assert.match(kotlinHost, /FSR_SHADER_ASSET = "mpv\/ohmycine-fsr-v1\.glsl"/)
assert.match(kotlinHost, /File\(context\.filesDir, "mpv"\)[\s\S]*?File\(directory, "ohmycine-fsr-v1\.glsl"\)[\s\S]*?context\.assets\.open\(FSR_SHADER_ASSET\)/)
assert.match(kotlinHost, /command\(arrayOf\("change-list", "glsl-shaders", "clr", ""\)\)/)
assert.match(kotlinHost, /recordFsrFallback/)
assert.match(kotlinHost, /fsrTargetDimensions/)
assert.match(kotlinHost, /settings\.fsrSharpness\.takeIf \{ it\.isFinite\(\) \}[\s\S]*\?: 35\.0/)

const androidSetup = await source('scripts/setup-libmpv-android.mjs')
const syncIndex = androidSetup.indexOf('syncManagedAssets()')
const readyIndex = androidSetup.indexOf('if (runtimeReady())')
assert.ok(syncIndex >= 0 && readyIndex > syncIndex, 'managed FSR assets must sync before the runtime-ready early return')
assert.match(androidSetup, /FSR-NOTICE\.md/)

const windowsBundle = await source('src-tauri/tauri.windows.conf.json')
assert.match(windowsBundle, /"resources\/shaders\/NOTICE\.md": "FSR-NOTICE\.md"/)

const desktopPanel = await source('src/components/player/PlayerSettingsPanel.vue')
const mobileControls = await source('src/components/player/MobilePlayerControls.vue')
const playerView = await source('src/views/PlayerView.vue')
assert.match(desktopPanel, /FsrSettingsContent/)
assert.match(mobileControls, /openPanel\('fsr'\)/)
assert.match(mobileControls, /FSR 超分与锐化/)
assert.match(playerView, /@update-fsr-settings="handleUpdateFsrSettings"/)
assert.match(playerView, /await savePlayerInteractionSettings\(next\)[\s\S]*?await applyEngineSettings\(\)[\s\S]*?await refreshPlaybackDiagnostics\(\)/)

console.log(JSON.stringify({
  settingsNormalized: true,
  managedShaderOnly: true,
  easuAndRcasPresent: true,
  upscaleOnlyCondition: true,
  targetShortEdgeCap: true,
  windowsFallback: true,
  androidFallback: true,
  desktopAndMobileControls: true,
}, null, 2))

import { Buffer } from 'node:buffer'
import { createHash } from 'node:crypto'
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const rootDir = fileURLToPath(new URL('../..', import.meta.url))
const builderDir = fileURLToPath(new URL('.', import.meta.url))
const sourcesPath = join(builderDir, 'sources.json')
const expected = JSON.parse(readFileSync(sourcesPath, 'utf8'))
const sourceHash = sha256(sourcesPath)
const builderInputs = ['sources.json', 'build-runtime.py', 'shaderc.sh', 'Dockerfile', 'android-hdr-egl.c']
const builderHash = createHash('sha256')
for (const name of builderInputs)
  builderHash.update(name).update('\0').update(readFileSync(join(builderDir, name)))
const expectedBuilderHash = builderHash.digest('hex')
const androidRoot = resolve(rootDir, 'src-tauri', 'gen', 'android', 'app', 'src', 'main')
const nativeDir = join(androidRoot, 'jniLibs', 'arm64-v8a')
const assetDir = join(androidRoot, 'assets', 'mpv')
const markerPath = join(nativeDir, '.ohmycine-mpv-runtime')
const manifestPath = join(assetDir, 'runtime-manifest.json')
const requiredLibraries = [
  'libavcodec.so',
  'libavdevice.so',
  'libavfilter.so',
  'libavformat.so',
  'libavutil.so',
  'libc++_shared.so',
  'libmpv.so',
  'libplayer.so',
  'libswresample.so',
  'libswscale.so',
]

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex')
}

function validateManifest(manifest) {
  if (manifest.schemaVersion !== expected.schemaVersion || manifest.runtimeId !== expected.runtimeId
    || manifest.abi !== expected.abi || manifest.androidApi !== expected.androidApi
    || manifest.ndk?.version !== expected.ndk.version
    || manifest.sourceManifestSha256 !== sourceHash || manifest.builderInputsSha256 !== expectedBuilderHash) {
    throw new Error('Android mpv runtime provenance does not match the fixed builder')
  }
  for (const [name, source] of Object.entries(expected.sources)) {
    if (manifest.sources?.[name]?.commit !== source.commit || manifest.sources?.[name]?.url !== source.url)
      throw new Error(`Android runtime source mismatch: ${name}`)
  }
  for (const [name, enabled] of Object.entries(expected.features)) {
    if (manifest.features?.[name] !== enabled)
      throw new Error(`Android runtime capability missing: ${name}`)
  }
  if (!manifest.files || typeof manifest.files !== 'object')
    throw new Error('Android runtime file hashes are missing')
  for (const [name, hash] of Object.entries(manifest.files)) {
    if (!/^(?:lib\/arm64-v8a\/lib[\w+.-]+\.so|assets\/cacert\.pem|licenses\/[\w.-]+)$/.test(name)
      || name.includes('..') || !/^[a-f0-9]{64}$/.test(hash)) {
      throw new Error('Android runtime contains an invalid file reference')
    }
  }
  for (const name of requiredLibraries) {
    if (!manifest.files[`lib/arm64-v8a/${name}`])
      throw new Error(`Android runtime library hash missing: ${name}`)
  }
  if (!manifest.files['assets/cacert.pem'])
    throw new Error('Android runtime CA asset hash missing')
  if (!Array.isArray(manifest.elfDependencies?.['libmpv.so'])
    || !manifest.elfDependencies['libmpv.so'].includes('libvulkan.so')) {
    throw new Error('Android runtime does not link the platform Vulkan loader')
  }
}

function installedPath(name) {
  if (name.startsWith('lib/arm64-v8a/'))
    return join(nativeDir, name.slice('lib/arm64-v8a/'.length))
  if (name.startsWith('licenses/'))
    return join(assetDir, name)
  return join(assetDir, 'cacert.pem')
}

function verifyFiles(manifest, pathForFile) {
  for (const [name, expectedHash] of Object.entries(manifest.files)) {
    const path = pathForFile(name)
    if (!existsSync(path) || sha256(path) !== expectedHash)
      throw new Error(`Android runtime checksum mismatch: ${name}`)
  }
  const mpv = readFileSync(pathForFile('lib/arm64-v8a/libmpv.so'))
  const codec = readFileSync(pathForFile('lib/arm64-v8a/libavcodec.so'))
  const jni = readFileSync(pathForFile('lib/arm64-v8a/libplayer.so'))
  if (!mpv.includes(Buffer.from('androidvk\0')))
    throw new Error('Android runtime has no Vulkan androidvk context')
  if (!mpv.includes(Buffer.from('vkGetInstanceProcAddr\0')))
    throw new Error('Android runtime has no platform Vulkan loader import')
  if (!mpv.includes(Buffer.from('OHMYCINE_ANDROID_HDR_EGL_V1'))
    || !mpv.includes(Buffer.from('Android HDR requires high precision intermediate textures.'))
    || !mpv.includes(Buffer.from('Hardware decoding failed with software fallback disabled.'))) {
    throw new Error('Android runtime has no hardware HDR EGL or strict decoder patch')
  }
  if (!codec.includes(Buffer.from('RPU-preserving output is required.')))
    throw new Error('Android runtime has no known Dolby Vision hardware guard')
  if (!jni.includes(Buffer.from('eventEndFile\0')))
    throw new Error('Android JNI has no typed end-file callback')
  for (const symbol of ['attachSurface', 'setPropertyStringChecked', 'commandChecked', 'addLoadHook', 'continueHook', 'stopAsync']) {
    if (!jni.includes(Buffer.from(`Java_is_xyz_mpv_MPVLib_${symbol}\0`)))
      throw new Error(`Android JNI capability missing: ${symbol}`)
  }
}

function runtimeReady() {
  try {
    if (readFileSync(markerPath, 'utf8').trim() !== expectedBuilderHash)
      return false
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
    validateManifest(manifest)
    verifyFiles(manifest, installedPath)
    return true
  }
  catch {
    return false
  }
}

function syncManagedAssets() {
  mkdirSync(assetDir, { recursive: true })
  const shaderDir = resolve(rootDir, 'src-tauri', 'resources', 'shaders')
  copyFileSync(join(shaderDir, 'ohmycine-fsr-v1.glsl'), join(assetDir, 'ohmycine-fsr-v1.glsl'))
  copyFileSync(join(shaderDir, 'NOTICE.md'), join(assetDir, 'FSR-NOTICE.md'))
}

function setup() {
  const args = process.argv.slice(2)
  const runtimeIndex = args.indexOf('--runtime-dir')
  if (runtimeIndex >= 0 && !args[runtimeIndex + 1])
    throw new Error('--runtime-dir requires a directory')
  if (args.includes('--verify-only')) {
    if (!runtimeReady())
      throw new Error('The installed Android mpv runtime is missing or invalid')
    console.log(`verified ${expected.runtimeId}`)
    return
  }
  syncManagedAssets()
  if (runtimeReady()) {
    console.log(`${expected.runtimeId} already installed and verified`)
    return
  }
  const runtimeDir = resolve(runtimeIndex >= 0
    ? args[runtimeIndex + 1]
    : process.env.OHMYCINE_ANDROID_MPV_RUNTIME || join(rootDir, '.build', 'android-mpv-runtime', 'output'))
  const sourceManifest = join(runtimeDir, 'runtime-manifest.json')
  if (!existsSync(sourceManifest))
    throw new Error('A pinned Vulkan-capable Android mpv runtime is required. Build scripts/android-mpv-runtime and set OHMYCINE_ANDROID_MPV_RUNTIME to its output directory.')
  const manifest = JSON.parse(readFileSync(sourceManifest, 'utf8'))
  validateManifest(manifest)
  verifyFiles(manifest, name => join(runtimeDir, name))
  // Validate the complete native set before replacing managed build inputs.
  for (const name of Object.keys(manifest.files)) {
    const target = installedPath(name)
    mkdirSync(resolve(target, '..'), { recursive: true })
    copyFileSync(join(runtimeDir, name), target)
  }
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`)
  writeFileSync(markerPath, `${expectedBuilderHash}\n`)
  verifyFiles(manifest, installedPath)
  console.log(`installed and verified ${expected.runtimeId}`)
}

try {
  setup()
}
catch (error) {
  console.error(error)
  process.exitCode = 1
}

#!/usr/bin/env node
import { existsSync, mkdtempSync, rmSync, statSync } from 'node:fs'
import { homedir, platform, tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import process from 'node:process'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const ndkVersion = '27.2.12479018'
const sdkCandidates = [
  process.env.ANDROID_SDK_ROOT,
  process.env.ANDROID_HOME,
  platform() === 'win32' ? 'D:\\Software\\Android\\Sdk' : join(homedir(), 'Android', 'Sdk'),
  platform() === 'win32' ? join(process.env.LOCALAPPDATA || '', 'Android', 'Sdk') : null,
].filter(Boolean)
const sdkRoot = sdkCandidates.find(candidate => existsSync(join(candidate, 'ndk', ndkVersion)))
if (!sdkRoot)
  throw new Error(`Android NDK ${ndkVersion} is required; set ANDROID_SDK_ROOT.`)

const cppRoot = resolve(root, 'src-tauri', 'gen', 'android', 'app', 'src', 'main', 'cpp')
const ncnnRoot = join(cppRoot, 'third_party', 'ncnn')
const ncnnLibrary = join(ncnnRoot, 'lib', 'libncnn.a')
const executableSuffix = platform() === 'win32' ? '.exe' : ''
const cmakeCandidates = [
  process.env.CMAKE,
  join(sdkRoot, 'cmake', '3.22.1', 'bin', `cmake${executableSuffix}`),
  platform() === 'win32' ? 'D:\\Software\\CMake\\3.22.1\\bin\\cmake.exe' : null,
].filter(Boolean)
const cmake = cmakeCandidates.find(candidate => existsSync(candidate))
if (!cmake || !existsSync(ncnnLibrary))
  throw new Error('Pinned Android CMake or ncnn runtime is missing. Run npm run setup:libmpv:android first.')
const ninja = join(cmake, '..', `ninja${executableSuffix}`)
if (!existsSync(ninja))
  throw new Error(`Pinned Ninja executable is missing next to CMake: ${ninja}`)

const work = mkdtempSync(join(tmpdir(), 'ohmycine-framegen-native-'))
function run(program, args) {
  const result = spawnSync(program, args, { cwd: root, encoding: 'utf8' })
  if (result.status !== 0)
    throw new Error([result.stdout, result.stderr].filter(Boolean).join('\n'))
}

try {
  const toolchain = join(sdkRoot, 'ndk', ndkVersion, 'build', 'cmake', 'android.toolchain.cmake')
  const output = join(work, 'libohmycine_framegen.so')
  run(cmake, [
    '-S', cppRoot,
    '-B', work,
    '-G', 'Ninja',
    `-DCMAKE_TOOLCHAIN_FILE=${toolchain}`,
    '-DANDROID_ABI=arm64-v8a',
    '-DANDROID_PLATFORM=android-29',
    '-DANDROID_STL=c++_static',
    `-DCMAKE_MAKE_PROGRAM=${ninja}`,
    '-DCMAKE_BUILD_TYPE=RelWithDebInfo',
  ])
  run(cmake, ['--build', work, '--parallel'])
  const bytes = statSync(output).size
  if (bytes < 1_000_000)
    throw new Error(`ARM64 native output is unexpectedly small (${bytes} bytes)`)
  console.log(JSON.stringify({
    target: 'aarch64-linux-android29',
    ndkVersion,
    ncnnLinked: true,
    fullSessionLinked: true,
    warningsAsErrors: true,
    outputBytes: bytes,
  }, null, 2))
}
finally {
  rmSync(work, { recursive: true, force: true })
}

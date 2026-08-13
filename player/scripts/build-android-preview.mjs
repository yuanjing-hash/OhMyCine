#!/usr/bin/env node
import { spawnSync } from 'node:child_process'
import { existsSync, readdirSync, rmSync } from 'node:fs'
import { homedir, platform } from 'node:os'
import { delimiter, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const androidApi = '36'
const androidBuildTools = platform() === 'win32' ? ['35.0.0', '36.0.0'] : ['36.0.0']
const androidNdk = '27.2.12479018'
const playerRoot = fileURLToPath(new URL('..', import.meta.url))
const tauriCli = resolve(playerRoot, 'node_modules', '@tauri-apps', 'cli', 'tauri.js')
const setupLibmpv = resolve(playerRoot, 'scripts', 'setup-libmpv-android.mjs')
const apkPath = resolve(playerRoot, 'src-tauri', 'gen', 'android', 'app', 'build', 'outputs', 'apk', 'universal', 'debug', 'app-universal-debug.apk')

function firstExisting(paths, predicate = existsSync) {
  return paths.filter(Boolean).find(path => predicate(path))
}

function childDirectories(path) {
  if (!existsSync(path))
    return []
  return readdirSync(path, { withFileTypes: true })
    .filter(entry => entry.isDirectory())
    .map(entry => join(path, entry.name))
}

function resolveAndroidSdk() {
  const candidates = [process.env.ANDROID_SDK_ROOT, process.env.ANDROID_HOME]
  if (platform() === 'win32') {
    candidates.push(
      'D:\\Software\\Android\\Sdk',
      process.env.LOCALAPPDATA && join(process.env.LOCALAPPDATA, 'Android', 'Sdk'),
      join(homedir(), 'AppData', 'Local', 'Android', 'Sdk'),
    )
  }
  else {
    candidates.push(join(homedir(), 'Android', 'Sdk'))
  }
  return firstExisting(candidates, path => existsSync(join(path, 'platforms', `android-${androidApi}`)))
}

function resolveJavaHome() {
  const executable = platform() === 'win32' ? 'java.exe' : 'java'
  const candidates = [process.env.JAVA_HOME]
  if (platform() === 'win32') {
    const programFiles = process.env.ProgramFiles || 'C:\\Program Files'
    candidates.push('D:\\Software\\DevelopEnv\\Java\\Java21')
    candidates.push(join(programFiles, 'Android', 'Android Studio', 'jbr'))
    candidates.push(...childDirectories(join(programFiles, 'Eclipse Adoptium'))
      .filter(path => /jdk-17/i.test(path)))
  }
  else {
    candidates.push('/usr/lib/jvm/temurin-17-jdk-amd64', '/usr/lib/jvm/java-17-openjdk-amd64')
    candidates.push(join(homedir(), '.sdkman', 'candidates', 'java', 'current'))
  }
  return firstExisting(candidates, path => existsSync(join(path, 'bin', executable)))
}

function run(command, args, env = process.env) {
  const result = spawnSync(command, args, {
    cwd: playerRoot,
    env,
    stdio: 'inherit',
    shell: false,
  })
  if (result.error)
    throw result.error
  if (result.status !== 0)
    process.exit(result.status ?? 1)
}

function rustcPath() {
  const executable = platform() === 'win32' ? 'rustup.exe' : 'rustup'
  const result = spawnSync(executable, ['which', 'rustc'], { encoding: 'utf8', shell: false })
  return result.status === 0 ? result.stdout.trim() : undefined
}

const sdkRoot = resolveAndroidSdk()
if (!sdkRoot) {
  throw new Error(`Android SDK platform ${androidApi} was not found. Set ANDROID_SDK_ROOT or install it in the platform default SDK directory.`)
}

const ndkHome = join(sdkRoot, 'ndk', androidNdk)
const missingBuildTools = androidBuildTools.filter(version => !existsSync(join(sdkRoot, 'build-tools', version)))
if (!existsSync(ndkHome))
  throw new Error(`Android NDK ${androidNdk} was not found at ${ndkHome}.`)
if (missingBuildTools.length > 0)
  throw new Error(`Android Build Tools ${missingBuildTools.join(', ')} were not found under ${join(sdkRoot, 'build-tools')}.`)

const javaHome = resolveJavaHome()
if (!javaHome)
  throw new Error('A compatible JDK was not found. Set JAVA_HOME to JDK 17 or newer.')
if (!existsSync(tauriCli))
  throw new Error('Tauri CLI is missing. Run npm ci in player first.')

run(process.execPath, [setupLibmpv])
rmSync(apkPath, { force: true })

const env = { ...process.env }
for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy', 'no_proxy'])
  delete env[name]
env.ANDROID_HOME = sdkRoot
env.ANDROID_SDK_ROOT = sdkRoot
env.NDK_HOME = ndkHome
env.JAVA_HOME = javaHome
if (platform() === 'win32') {
  const localGradleHome = 'D:\\Software\\Android\\Gradle'
  env.GRADLE_USER_HOME = process.env.OHMYCINE_GRADLE_HOME
    || process.env.GRADLE_USER_HOME
    || (existsSync('D:\\Software\\Android') ? localGradleHome : join(homedir(), '.gradle'))
}
if (platform() === 'win32')
  env.GRADLE_OPTS = [env.GRADLE_OPTS, '-Dkotlin.incremental=false', '-Dkotlin.compiler.execution.strategy=in-process'].filter(Boolean).join(' ')
env.CARGO_PROFILE_DEV_DEBUG = '0'
env.CARGO_PROFILE_DEV_STRIP = 'debuginfo'
const pathKey = Object.keys(env).find(name => name.toLowerCase() === 'path') || 'PATH'
const cargoBin = join(homedir(), '.cargo', 'bin')
env[pathKey] = [join(javaHome, 'bin'), join(sdkRoot, 'platform-tools'), cargoBin, env[pathKey]].filter(Boolean).join(delimiter)
env.RUSTC ||= rustcPath()

console.log(`Android SDK: ${sdkRoot}`)
console.log(`Android NDK: ${ndkHome}`)
console.log(`JDK: ${javaHome}`)
if (env.GRADLE_USER_HOME)
  console.log(`Gradle cache: ${env.GRADLE_USER_HOME}`)
run(process.execPath, [tauriCli, 'android', 'build', '--debug', '--apk', '--target', 'aarch64', '--ci'], env)

if (!existsSync(apkPath))
  throw new Error(`Android build completed without producing ${apkPath}.`)
console.log(`Android ARM64 preview APK: ${apkPath}`)

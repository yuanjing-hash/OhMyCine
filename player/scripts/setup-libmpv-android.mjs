#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { createWriteStream, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, copyFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { pipeline } from 'node:stream/promises'
import { fileURLToPath } from 'node:url'
import SevenZip from '7z-wasm'

const releaseTag = '2026-04-25'
const apkName = 'app-default-arm64-v8a-release.apk'
const expectedSha256 = '4400bcba6be9cec1128e24d1eba153d8727384926b0639fa7fe44d4e36b04f81'
const releaseUrl = `https://github.com/mpv-android/mpv-android/releases/download/${releaseTag}/${apkName}`
const rootDir = fileURLToPath(new URL('..', import.meta.url))
const androidRoot = resolve(rootDir, 'src-tauri', 'gen', 'android', 'app', 'src', 'main')
const nativeDir = join(androidRoot, 'jniLibs', 'arm64-v8a')
const assetDir = join(androidRoot, 'assets', 'mpv')
const markerPath = join(nativeDir, '.ohmycine-mpv-runtime')
const maxDownloadAttempts = 3
const retryBaseDelayMs = 1500
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

function ensureDir(path) {
  mkdirSync(path, { recursive: true })
}

function runtimeReady() {
  if (!existsSync(markerPath) || !existsSync(join(assetDir, 'cacert.pem')))
    return false
  const marker = readFileSync(markerPath, 'utf8').trim()
  return marker === `${releaseTag} ${expectedSha256}`
    && requiredLibraries.every(name => existsSync(join(nativeDir, name)))
}

function wait(delayMs) {
  return new Promise(resolve => setTimeout(resolve, delayMs))
}

async function downloadFile(url, destPath) {
  let lastError
  for (let attempt = 1; attempt <= maxDownloadAttempts; attempt += 1) {
    rmSync(destPath, { force: true })
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(120_000) })
      if (!response.ok)
        throw new Error(`Failed to fetch ${url}: ${response.status} ${response.statusText}`)
      if (!response.body)
        throw new Error(`Failed to fetch ${url}: response body is empty`)
      await pipeline(response.body, createWriteStream(destPath))
      return
    }
    catch (error) {
      lastError = error
      rmSync(destPath, { force: true })
      if (attempt === maxDownloadAttempts)
        break
      const delayMs = retryBaseDelayMs * (2 ** (attempt - 1))
      console.warn(`mpv-android download attempt ${attempt}/${maxDownloadAttempts} failed; retrying in ${delayMs}ms`)
      await wait(delayMs)
    }
  }
  throw new Error(`Failed to download mpv-android after ${maxDownloadAttempts} attempts`, { cause: lastError })
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex')
}

async function extractArchive(archivePath, extractDir) {
  ensureDir(extractDir)
  const sevenZip = await SevenZip({ print: () => {}, printErr: () => {} })
  sevenZip.FS.mkdir('/archive_source')
  sevenZip.FS.mkdir('/archive_dest')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: dirname(archivePath) }, '/archive_source')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: extractDir }, '/archive_dest')

  try {
    sevenZip.callMain(['x', `/archive_source/${apkName}`, '-o/archive_dest', '-y'])
  }
  catch (error) {
    if (error?.status !== 0)
      throw error
  }
  finally {
    sevenZip.FS.unmount('/archive_source')
    sevenZip.FS.unmount('/archive_dest')
  }
}

async function setup() {
  if (runtimeReady()) {
    console.log(`mpv-android ${releaseTag} ARM64 runtime already installed`)
    return
  }

  const workDir = mkdtempSync(join(tmpdir(), 'ohmycine-mpv-android-'))
  try {
    const apkPath = join(workDir, apkName)
    const extractDir = join(workDir, 'extracted')
    console.log(`downloading mpv-android ${releaseTag} ARM64 runtime`)
    await downloadFile(releaseUrl, apkPath)
    const actualSha256 = sha256(apkPath)
    if (actualSha256 !== expectedSha256)
      throw new Error(`mpv-android checksum mismatch: expected ${expectedSha256}, got ${actualSha256}`)

    await extractArchive(apkPath, extractDir)
    ensureDir(nativeDir)
    ensureDir(assetDir)
    for (const name of requiredLibraries) {
      const source = join(extractDir, 'lib', 'arm64-v8a', name)
      if (!existsSync(source))
        throw new Error(`${name} missing from ${apkName}`)
      copyFileSync(source, join(nativeDir, name))
    }

    const caSource = join(extractDir, 'assets', 'cacert.pem')
    if (!existsSync(caSource))
      throw new Error(`cacert.pem missing from ${apkName}`)
    copyFileSync(caSource, join(assetDir, 'cacert.pem'))
    writeFileSync(markerPath, `${releaseTag} ${expectedSha256}\n`)
    console.log(`installed mpv-android ${releaseTag} ARM64 runtime`)
  }
  finally {
    rmSync(workDir, { recursive: true, force: true })
  }
}

setup().catch((error) => {
  console.error(error)
  process.exit(1)
})

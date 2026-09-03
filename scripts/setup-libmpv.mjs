#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { copyFileSync, createWriteStream, existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, statSync } from 'node:fs'
import { basename, dirname, join, relative, resolve } from 'node:path'
import { pipeline } from 'node:stream/promises'
import { fileURLToPath } from 'node:url'
import SevenZip from '7z-wasm'

const rootDir = fileURLToPath(new URL('..', import.meta.url))
const targetDir = resolve(rootDir, 'src-tauri', 'lib')
const tempDir = join(targetDir, 'temp')
const frameInterpolationRuntimeDir = join(targetDir, 'frame-interpolation')
const wrapperRelease = 'v0.1.1'
const wrapperBaseUrl = `https://github.com/nini22P/libmpv-wrapper/releases/download/${wrapperRelease}`
const mpvRelease = '2026-08-30-e8673660ab'
const mpvBaseUrl = `https://github.com/zhongfly/mpv-winbuild/releases/download/${mpvRelease}`
const windowsMpvArchive = {
  fileName: 'mpv-dev-lgpl-x86_64-20260830-git-e8673660ab.7z',
  sha256: '7659f968ccea69168aa8924ea1bf7c524e996946d184720d79f92241805f4724',
}
const windowsInferenceRuntime = {
  onnxRuntime: {
    version: '1.24.4',
    fileName: 'microsoft.ml.onnxruntime.directml.1.24.4.nupkg',
    url: 'https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime.directml/1.24.4/microsoft.ml.onnxruntime.directml.1.24.4.nupkg',
    sha256: '57e9f11b73437bef7a309496135d4c1f96b1a8e9ddba60013fa27bfc1d788681',
  },
  directMl: {
    version: '1.15.4',
    fileName: 'microsoft.ai.directml.1.15.4.nupkg',
    url: 'https://api.nuget.org/v3-flatcontainer/microsoft.ai.directml/1.15.4/microsoft.ai.directml.1.15.4.nupkg',
    sha256: '4e7cb7ddce8cf837a7a75dc029209b520ca0101470fcdf275c1f49736a3615b9',
  },
}
const wrapperArchives = {
  'linux-x86_64': ['libmpv-wrapper-linux-x86_64.zip', '1583564042f10be25166b52b6fe02db6d87cf9cef34985ba517048c044b7eee0'],
  'windows-x86_64': ['libmpv-wrapper-windows-x86_64.zip', 'd2ff8b2edcd34d2968e544adaa915e5e5c48eb1a0995945005269c2af119a492'],
  'macos-x86_64': ['libmpv-wrapper-macos-x86_64.zip', '99d65d7e368a456c883b4b3cf5d6384c9af64ec29dccaf4923548c2e3784b0e4'],
  'macos-aarch64': ['libmpv-wrapper-macos-aarch64.zip', '086dbf977fe5b51785048cbd190f1809186513688be0ecea1ad27bd766c55de8'],
}

const targets = {
  linux: {
    osName: 'linux',
    archName: 'x86_64',
    wrapperLibName: 'libmpv-wrapper.so',
    downloadsMpv: false,
  },
  windows: {
    osName: 'windows',
    archName: 'x86_64',
    wrapperLibName: 'libmpv-wrapper.dll',
    downloadsMpv: true,
  },
  macos: {
    osName: 'macos',
    archName: 'x86_64',
    wrapperLibName: 'libmpv-wrapper.dylib',
    downloadsMpv: false,
  },
  'macos-arm64': {
    osName: 'macos',
    archName: 'aarch64',
    wrapperLibName: 'libmpv-wrapper.dylib',
    downloadsMpv: false,
    outputName: 'libmpv-wrapper-aarch64.dylib',
  },
}

function ensureDir(path) {
  mkdirSync(path, { recursive: true })
}

async function downloadFile(url, destPath) {
  const res = await fetch(url)
  if (!res.ok)
    throw new Error(`Failed to fetch ${url}: ${res.status} ${res.statusText}`)
  await pipeline(res.body, createWriteStream(destPath))
}

function verifySha256(path, expectedSha256) {
  const actualSha256 = createHash('sha256').update(readFileSync(path)).digest('hex')
  if (actualSha256 !== expectedSha256)
    throw new Error(`Runtime checksum mismatch for ${basename(path)}: expected ${expectedSha256}, got ${actualSha256}`)
}

async function extractArchive(archivePath, extractDir) {
  rmSync(extractDir, { recursive: true, force: true })
  ensureDir(extractDir)

  const sevenZip = await SevenZip({ print: () => {}, printErr: () => {} })
  sevenZip.FS.mkdir('/archive_source')
  sevenZip.FS.mkdir('/archive_dest')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: dirname(archivePath) }, '/archive_source')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: extractDir }, '/archive_dest')

  try {
    sevenZip.callMain(['x', `/archive_source/${basename(archivePath)}`, '-o/archive_dest', '-y'])
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

function findFile(searchDir, fileName) {
  for (const entry of readdirSync(searchDir)) {
    if (entry.startsWith('.'))
      continue

    const fullPath = join(searchDir, entry)
    const stat = statSync(fullPath)
    if (stat.isDirectory()) {
      const found = findFile(fullPath, fileName)
      if (found)
        return found
    }
    else if (entry === fileName) {
      return fullPath
    }
  }

  return null
}

function moveExtractedFile(searchDir, fileName, outputName = fileName) {
  const found = findFile(searchDir, fileName)
  if (!found)
    throw new Error(`${fileName} not found after extraction`)

  const destPath = resolve(targetDir, outputName)
  const relativeDest = relative(targetDir, destPath)
  if (relativeDest === '' || relativeDest.startsWith('..') || relativeDest.includes(':'))
    throw new Error(`Refusing to install ${outputName} outside ${targetDir}`)

  rmSync(destPath, { force: true })
  renameSync(found, destPath)
  console.log(`installed ${outputName}`)
}

function copyInstalledFile(fileName, outputName) {
  const sourcePath = resolve(targetDir, fileName)
  const destPath = resolve(targetDir, outputName)
  const relativeDest = relative(targetDir, destPath)
  if (!existsSync(sourcePath))
    throw new Error(`${fileName} must be installed before creating ${outputName}`)
  if (relativeDest === '' || relativeDest.startsWith('..') || relativeDest.includes(':'))
    throw new Error(`Refusing to install ${outputName} outside ${targetDir}`)

  copyFileSync(sourcePath, destPath)
  console.log(`installed ${outputName}`)
}

function copyRuntimeFile(source, relativeDestination) {
  if (!existsSync(source))
    throw new Error(`Pinned inference runtime file not found: ${source}`)
  const destination = resolve(frameInterpolationRuntimeDir, relativeDestination)
  const relativeDest = relative(frameInterpolationRuntimeDir, destination)
  if (!relativeDest || relativeDest.startsWith('..') || relativeDest.includes(':'))
    throw new Error(`Refusing to install outside ${frameInterpolationRuntimeDir}: ${destination}`)
  ensureDir(dirname(destination))
  copyFileSync(source, destination)
  console.log(`installed frame-interpolation/${relativeDestination}`)
}

async function installWindowsFrameInterpolationRuntime(target) {
  if (target.archName !== 'x86_64')
    throw new Error(`No pinned Windows frame-interpolation runtime for ${target.archName}`)
  const ort = windowsInferenceRuntime.onnxRuntime
  const dml = windowsInferenceRuntime.directMl
  const ortArchive = join(tempDir, ort.fileName)
  const dmlArchive = join(tempDir, dml.fileName)
  const ortExtract = join(tempDir, 'onnxruntime-directml')
  const dmlExtract = join(tempDir, 'directml')
  await downloadFile(ort.url, ortArchive)
  verifySha256(ortArchive, ort.sha256)
  await extractArchive(ortArchive, ortExtract)
  await downloadFile(dml.url, dmlArchive)
  verifySha256(dmlArchive, dml.sha256)
  await extractArchive(dmlArchive, dmlExtract)

  const ortNative = join(ortExtract, 'runtimes', 'win-x64', 'native')
  copyRuntimeFile(join(ortNative, 'onnxruntime.dll'), 'onnxruntime.dll')
  copyRuntimeFile(join(ortNative, 'onnxruntime_providers_shared.dll'), 'onnxruntime_providers_shared.dll')
  copyRuntimeFile(join(ortNative, 'onnxruntime.lib'), 'onnxruntime.lib')
  for (const header of readdirSync(join(ortExtract, 'build', 'native', 'include')))
    copyRuntimeFile(join(ortExtract, 'build', 'native', 'include', header), join('include', header))
  copyRuntimeFile(join(ortExtract, 'LICENSE'), 'LICENSE-ONNX-RUNTIME')
  copyRuntimeFile(join(ortExtract, 'ThirdPartyNotices.txt'), 'THIRD-PARTY-ONNX-RUNTIME.txt')
  copyRuntimeFile(join(dmlExtract, 'bin', 'x64-win', 'DirectML.dll'), 'DirectML.dll')
  copyRuntimeFile(join(dmlExtract, 'bin', 'x64-win', 'DirectML.lib'), 'DirectML.lib')
  copyRuntimeFile(join(dmlExtract, 'include', 'DirectML.h'), join('include', 'DirectML.h'))
  copyRuntimeFile(join(dmlExtract, 'include', 'DirectMLConfig.h'), join('include', 'DirectMLConfig.h'))
  copyRuntimeFile(join(dmlExtract, 'LICENSE.txt'), 'LICENSE-DIRECTML')
  copyRuntimeFile(join(dmlExtract, 'LICENSE-CODE.txt'), 'LICENSE-DIRECTML-CODE')
  copyRuntimeFile(join(dmlExtract, 'ThirdPartyNotices.txt'), 'THIRD-PARTY-DIRECTML.txt')
}

async function installWrapper(target) {
  const archive = wrapperArchives[`${target.osName}-${target.archName}`]
  if (!archive)
    throw new Error(`No pinned wrapper archive for ${target.osName}-${target.archName}`)
  const [fileName, expectedSha256] = archive
  const archivePath = join(tempDir, fileName)
  const extractDir = join(tempDir, `wrapper-${target.osName}-${target.archName}`)

  console.log(`downloading ${fileName}`)
  await downloadFile(`${wrapperBaseUrl}/${fileName}`, archivePath)
  verifySha256(archivePath, expectedSha256)
  await extractArchive(archivePath, extractDir)
  moveExtractedFile(extractDir, target.wrapperLibName, target.outputName)
}

async function installWindowsMpv(target) {
  if (target.archName !== 'x86_64')
    throw new Error(`No pinned Windows mpv archive for ${target.archName}`)
  const { fileName, sha256: expectedSha256 } = windowsMpvArchive
  const archivePath = join(tempDir, fileName)
  const extractDir = join(tempDir, 'mpv-windows')

  console.log(`downloading ${fileName}`)
  await downloadFile(`${mpvBaseUrl}/${fileName}`, archivePath)
  verifySha256(archivePath, expectedSha256)
  await extractArchive(archivePath, extractDir)

  // Runtime DLL is shared by both Windows toolchains. The upstream GNU import
  // archive is COFF-compatible with MSVC link.exe, which resolves `-lmpv` as
  // mpv.lib, so keep both conventional file names without requiring VS tools.
  moveExtractedFile(extractDir, 'libmpv-2.dll')
  moveExtractedFile(extractDir, 'libmpv.dll.a')
  copyInstalledFile('libmpv.dll.a', 'mpv.lib')
}

async function setup(targetNames) {
  ensureDir(targetDir)
  ensureDir(tempDir)

  for (const targetName of targetNames) {
    const target = targets[targetName]
    if (!target)
      throw new Error(`Unknown target ${targetName}`)

    console.log(`setting up ${targetName}`)
    await installWrapper(target)
    if (target.downloadsMpv) {
      await installWindowsMpv(target)
      await installWindowsFrameInterpolationRuntime(target)
    }
  }

  rmSync(tempDir, { recursive: true, force: true })
}

const args = process.argv.slice(2)
const targetNames = args.length ? args : ['linux', 'windows', 'macos', 'macos-arm64']

setup(targetNames).catch((error) => {
  console.error(error)
  process.exit(1)
})

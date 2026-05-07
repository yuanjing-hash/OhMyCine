#!/usr/bin/env node
import { createWriteStream, existsSync, mkdirSync, readdirSync, renameSync, rmSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pipeline } from 'node:stream/promises'
import SevenZip from '7z-wasm'

const rootDir = new URL('..', import.meta.url).pathname
const targetDir = join(rootDir, 'src-tauri', 'lib')
const tempDir = join(targetDir, 'temp')
const wrapperBaseUrl = 'https://github.com/nini22P/libmpv-wrapper/releases/latest/download'
const mpvBaseUrl = 'https://github.com/zhongfly/mpv-winbuild/releases/latest/download'

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

async function extractArchive(archivePath, extractDir) {
  rmSync(extractDir, { recursive: true, force: true })
  ensureDir(extractDir)

  const sevenZip = await SevenZip({ print: () => {}, printErr: () => {} })
  sevenZip.FS.mkdir('/archive_source')
  sevenZip.FS.mkdir('/archive_dest')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: dirname(archivePath) }, '/archive_source')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: extractDir }, '/archive_dest')

  try {
    sevenZip.callMain(['x', `/archive_source/${archivePath.split('/').pop()}`, '-o/archive_dest', '-y'])
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

  const destPath = join(targetDir, outputName)
  rmSync(destPath, { force: true })
  renameSync(found, destPath)
  console.log(`installed ${outputName}`)
}

async function installWrapper(target) {
  const sha = await fetch(`${wrapperBaseUrl}/sha256.txt`).then(res => res.text())
  const searchKey = `libmpv-wrapper-${target.osName}-${target.archName}`
  const line = sha.split('\n').find(item => item.includes(searchKey))
  if (!line)
    throw new Error(`Could not find ${searchKey} in libmpv-wrapper sha256.txt`)

  const fileName = line.trim().split(/\s+/).pop()
  const archivePath = join(tempDir, fileName)
  const extractDir = join(tempDir, `wrapper-${target.osName}-${target.archName}`)

  console.log(`downloading ${fileName}`)
  await downloadFile(`${wrapperBaseUrl}/${fileName}`, archivePath)
  await extractArchive(archivePath, extractDir)
  moveExtractedFile(extractDir, target.wrapperLibName, target.outputName)
}

async function installWindowsMpv(target) {
  const sha = await fetch(`${mpvBaseUrl}/sha256.txt`).then(res => res.text())
  const searchKey = `mpv-dev-lgpl-${target.archName}`
  const line = sha.split('\n').find(item => item.includes(searchKey) && !item.includes('v3'))
  if (!line)
    throw new Error(`Could not find ${searchKey} in mpv-winbuild sha256.txt`)

  const fileName = line.trim().split(/\s+/).pop()
  const archivePath = join(tempDir, fileName)
  const extractDir = join(tempDir, 'mpv-windows')

  console.log(`downloading ${fileName}`)
  await downloadFile(`${mpvBaseUrl}/${fileName}`, archivePath)
  await extractArchive(archivePath, extractDir)
  moveExtractedFile(extractDir, 'libmpv-2.dll')
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
    if (target.downloadsMpv)
      await installWindowsMpv(target)
  }

  rmSync(tempDir, { recursive: true, force: true })
}

const args = process.argv.slice(2)
const targetNames = args.length ? args : ['linux', 'windows', 'macos', 'macos-arm64']

setup(targetNames).catch((error) => {
  console.error(error)
  process.exit(1)
})

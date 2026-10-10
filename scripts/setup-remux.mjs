#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import SevenZip from '7z-wasm'

const root = fileURLToPath(new URL('..', import.meta.url))
const temporary = resolve(root, '.tmp', 'remux')
const android = process.argv.includes('--android')
const commit = 'fc4960b155aa33b9a08cf26c5e0a0530f0545f24'
const windowsName = 'ffmpeg-n8.1.3-14-g330caae0c1-win64-lgpl-shared-8.1.zip'
const archive = android ? 'android-headers.tar.gz' : 'windows.zip'
const expected = android ? '10ff00bd0f4e2fff4e8a0c7ca59ed5b5cfe2091d607e324f941aacc8fba8156e' : '0314e211fbd06b92ffb529b96d3358a2c60c61d4ab0071695d940b20ce776fa5'
const url = android ? `https://codeload.github.com/FFmpeg/FFmpeg/tar.gz/${commit}` : `https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-10-06-13-06/${windowsName}`
const destination = resolve(root, 'src-tauri', android ? 'native/ffmpeg-headers' : 'resources/remux')
const marker = join(destination, '.runtime')
const sha = bytes => createHash('sha256').update(bytes).digest('hex')

async function extract(path, directory) {
  mkdirSync(directory, { recursive: true })
  const sevenZip = await SevenZip({ print: () => {}, printErr: () => {} })
  sevenZip.FS.mkdir('/source'); sevenZip.FS.mkdir('/target')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: temporary }, '/source')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: directory }, '/target')
  try { sevenZip.callMain(['x', `/source/${path}`, '-o/target', '-y']) }
  catch (error) { if (error?.status !== 0) throw error }
  finally { sevenZip.FS.unmount('/source'); sevenZip.FS.unmount('/target') }
}

function verifyRuntime() {
  if (android)
    return existsSync(join(destination, 'libavformat/avformat.h')) && existsSync(join(destination, 'libavutil/avconfig.h'))
  const manifest = readFileSync(resolve(root, 'src-tauri/native/remux-windows.sha256'), 'utf8')
  return existsSync(join(destination, 'LICENSE.txt')) && manifest.trim().split('\n').every((line) => {
    const [hash, name] = line.trim().split('  ')
    const path = join(destination, name)
    return existsSync(path) && sha(readFileSync(path)) === hash
  })
}

async function main() {
  if (existsSync(marker) && readFileSync(marker, 'utf8').trim() === expected && verifyRuntime()) {
    if (!android) copyFileSync(resolve(root, 'src-tauri/native/README.md'), join(destination, 'README.md'))
    console.log(`offline remux ${android ? 'Android headers' : 'Windows runtime'} already verified`)
    return
  }
  mkdirSync(temporary, { recursive: true })
  const path = join(temporary, archive)
  if (!existsSync(path) || sha(readFileSync(path)) !== expected) {
    const response = await fetch(url, { signal: AbortSignal.timeout(180_000) })
    if (!response.ok) throw new Error(`remux source download failed (${response.status})`)
    const bytes = Buffer.from(await response.arrayBuffer())
    if (sha(bytes) !== expected) throw new Error('remux archive checksum mismatch')
    writeFileSync(path, bytes)
  }
  const extractRoot = join(temporary, android ? 'android' : 'windows')
  await extract(archive, extractRoot)
  mkdirSync(destination, { recursive: true })
  if (android) {
    const sourceName = readdirSync(extractRoot).find(name => name.endsWith('.tar'))
    if (!sourceName) throw new Error('FFmpeg source tar is missing')
    copyFileSync(join(extractRoot, sourceName), join(temporary, 'headers.tar'))
    await extract('headers.tar', extractRoot)
    const source = join(extractRoot, `FFmpeg-${commit}`)
    for (const directory of ['libavformat', 'libavcodec', 'libavutil']) {
      mkdirSync(join(destination, directory), { recursive: true })
      for (const name of readdirSync(join(source, directory)).filter(name => name.endsWith('.h')))
        copyFileSync(join(source, directory, name), join(destination, directory, name))
    }
    // Public generated header for the vendored little-endian ARM64 runtime.
    writeFileSync(join(destination, 'libavutil/avconfig.h'), '#ifndef AVUTIL_AVCONFIG_H\n#define AVUTIL_AVCONFIG_H\n#define AV_HAVE_BIGENDIAN 0\n#define AV_HAVE_FAST_UNALIGNED 1\n#endif\n')
    copyFileSync(join(source, 'COPYING.LGPLv2.1'), join(destination, 'LICENSE'))
  }
  else {
    const source = join(extractRoot, windowsName.replace(/\.zip$/, ''))
    for (const name of readdirSync(join(source, 'bin')).filter(name => name === 'ffmpeg.exe' || name.endsWith('.dll')))
      copyFileSync(join(source, 'bin', name), join(destination, name))
    copyFileSync(join(source, 'LICENSE.txt'), join(destination, 'LICENSE.txt'))
    copyFileSync(resolve(root, 'src-tauri/native/README.md'), join(destination, 'README.md'))
    if (!verifyRuntime()) throw new Error('remux runtime files do not match the pinned manifest')
  }
  writeFileSync(marker, `${expected}\n`)
  console.log(`offline remux ${android ? 'matching FFmpeg headers' : 'Windows FFmpeg runtime'} installed and verified`)
}
main().catch((error) => { console.error(error); process.exitCode = 1 })

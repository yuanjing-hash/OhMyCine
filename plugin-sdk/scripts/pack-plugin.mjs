import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { basename, dirname, isAbsolute, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { zipSync } from 'fflate'

function option(name) {
  const index = process.argv.indexOf(`--${name}`)
  return index >= 0 ? process.argv[index + 1] : undefined
}

function safeAssetStem(pluginID, version) {
  const value = `${pluginID}-${version}`
  if (!/^[a-z0-9.-]+$/.test(value)) throw new Error('plugin id/version cannot form a safe release asset name')
  return value
}

function safeEntry(value) {
  return typeof value === 'string'
    && value.length > 0
    && value.length <= 240
    && value.endsWith('.wasm')
    && !value.startsWith('/')
    && !value.includes('\\')
    && !value.split('/').some(part => !part || part === '.' || part === '..')
}

function safeArtwork(value) {
  return typeof value === 'string'
    && value.length > 0
    && value.length <= 240
    && /\.(?:png|jpe?g|webp)$/i.test(value)
    && !value.startsWith('/')
    && !value.includes('\\')
    && !value.split('/').some(part => !part || part === '.' || part === '..')
}

function validArtworkBytes(path, bytes) {
  if (bytes.byteLength === 0 || bytes.byteLength > 4 * 1024 * 1024)
    return false
  const lower = path.toLowerCase()
  if (lower.endsWith('.png'))
    return bytes.byteLength >= 8 && Buffer.from(bytes.subarray(0, 8)).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))
  if (lower.endsWith('.jpg') || lower.endsWith('.jpeg'))
    return bytes.byteLength >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff
  return bytes.byteLength >= 12 && Buffer.from(bytes.subarray(0, 4)).toString('ascii') === 'RIFF' && Buffer.from(bytes.subarray(8, 12)).toString('ascii') === 'WEBP'
}

export async function packPlugin({ manifestPath, wasmPath, outputDirectory }) {
  const manifest = JSON.parse(await readFile(resolve(manifestPath), 'utf8'))
  if (manifest.schemaVersion !== 1 || manifest.runtime !== 'wasm' || !safeEntry(manifest.entry))
    throw new Error('manifest does not describe a safe v1 WASM entry')
  if (typeof manifest.id !== 'string' || typeof manifest.version !== 'string')
    throw new Error('manifest identity is missing')

  const wasm = new Uint8Array(await readFile(resolve(wasmPath)))
  if (wasm.byteLength === 0 || wasm.byteLength > 64 * 1024 * 1024)
    throw new Error('WASM entry size is invalid')
  await WebAssembly.compile(wasm)

  // Manifest is a separate Release asset. Keeping it outside .omcp avoids a
  // circular digest where the Manifest would need to contain the hash of a zip
  // that itself contains the Manifest.
  const archiveEntries = { [manifest.entry]: wasm }
  if (manifest.libraryArtwork != null) {
    if (!safeArtwork(manifest.libraryArtwork))
      throw new Error('manifest libraryArtwork is not a safe package-relative raster path')
    const manifestRoot = dirname(resolve(manifestPath))
    const artworkPath = resolve(manifestRoot, manifest.libraryArtwork)
    const relativeArtwork = relative(manifestRoot, artworkPath)
    if (!relativeArtwork || isAbsolute(relativeArtwork) || relativeArtwork === '..' || relativeArtwork.startsWith(`..${sep}`))
      throw new Error('manifest libraryArtwork escapes the plugin directory')
    const artwork = new Uint8Array(await readFile(artworkPath))
    if (!validArtworkBytes(manifest.libraryArtwork, artwork))
      throw new Error('manifest libraryArtwork has an invalid type or size')
    archiveEntries[manifest.libraryArtwork] = artwork
  }
  const archive = zipSync(archiveEntries, { level: 9, mtime: new Date('1980-01-01T00:00:00.000Z') })
  const digest = createHash('sha256').update(archive).digest('hex')
  manifest.packageSha256 = digest

  const outputRoot = resolve(outputDirectory)
  await mkdir(outputRoot, { recursive: true })
  const stem = safeAssetStem(manifest.id, manifest.version)
  const manifestOutput = resolve(outputRoot, `${stem}.manifest.json`)
  const packageOutput = resolve(outputRoot, `${stem}.omcp`)
  await Promise.all([
    writeFile(manifestOutput, `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o600 }),
    writeFile(packageOutput, archive, { mode: 0o600 }),
  ])
  return { digest, manifestOutput, packageOutput, packageName: basename(packageOutput) }
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  const manifestPath = option('manifest')
  const wasmPath = option('wasm')
  const outputDirectory = option('out')
  if (!manifestPath || !wasmPath || !outputDirectory) {
    console.error('usage: npm run pack -- --manifest <plugin.template.json> --wasm <plugin.wasm> --out <directory>')
    process.exitCode = 2
  } else {
    const result = await packPlugin({ manifestPath, wasmPath, outputDirectory })
    console.log(JSON.stringify(result, null, 2))
  }
}

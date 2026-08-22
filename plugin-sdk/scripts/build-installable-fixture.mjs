import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import wabtFactory from 'wabt'
import { packPlugin } from './pack-plugin.mjs'

const sdkRoot = fileURLToPath(new URL('..', import.meta.url))
const outputRoot = fileURLToPath(new URL('../dist/installable-fixture/', import.meta.url))
await mkdir(outputRoot, { recursive: true })

const contract = JSON.parse(await readFile(new URL('../fixtures/static-site/contract.v1.json', import.meta.url), 'utf8'))
const operationCodes = {
  'site.navigation': 1,
  'site.feed': 2,
  'site.search': 3,
  'site.detail': 4,
  'media.playback': 5,
  'media.download_plan': 6,
}

let offset = 1024
const responseLocations = []
const dataSegments = []
for (const [operation, code] of Object.entries(operationCodes)) {
  const bytes = Buffer.from(JSON.stringify(contract.responses[operation]), 'utf8')
  const pointer = offset
  offset += bytes.length + 16
  responseLocations.push({ code, pointer, length: bytes.length })
  dataSegments.push(`(data (i32.const ${pointer}) "${watEscape(bytes)}")`)
}

const cases = responseLocations.reduceRight(
  (otherwise, item) => `(if (result i64) (i32.eq (local.get $operation) (i32.const ${item.code})) (then ${packedResult(item.pointer, item.length)}) (else ${otherwise}))`,
  '(i64.const 0)',
)
const wat = `(module
  (memory (export "memory") 2)
  (global $heap (mut i32) (i32.const 65536))
  ${dataSegments.join('\n  ')}
  (func (export "omc_api_version") (result i32) (i32.const 1))
  (func (export "omc_start"))
  (func (export "omc_alloc") (param $size i32) (result i32)
    (local $pointer i32)
    (global.get $heap)
    (local.tee $pointer)
    (local.get $size)
    (i32.add)
    (global.set $heap)
    (local.get $pointer))
  (func (export "omc_invoke") (param $operation i32) (param $requestPointer i32) (param $requestLength i32) (result i64)
    ${cases}))`
const wabt = await wabtFactory()
const parsed = wabt.parseWat('static-site-fixture.wat', wat)
parsed.resolveNames()
parsed.validate()
const wasm = Uint8Array.from(parsed.toBinary({ canonicalize_lebs: true, write_debug_names: false }).buffer)
await WebAssembly.compile(wasm)
const wasmPath = fileURLToPath(new URL('../dist/installable-fixture/plugin.wasm', import.meta.url))
await writeFile(wasmPath, wasm, { mode: 0o600 })

const result = await packPlugin({
  manifestPath: fileURLToPath(new URL('../fixtures/static-site/plugin.json', import.meta.url)),
  wasmPath,
  outputDirectory: outputRoot,
})
console.log(JSON.stringify(result, null, 2))

function watEscape(bytes) {
  return [...bytes].map(byte => `\\${byte.toString(16).padStart(2, '0')}`).join('')
}

function packedResult(pointer, length) {
  return `(i64.or (i64.shl (i64.extend_i32_u (i32.const ${pointer})) (i64.const 32)) (i64.extend_i32_u (i32.const ${length})))`
}

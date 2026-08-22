import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'

const root = fileURLToPath(new URL('..', import.meta.url))
const schema = JSON.parse(await readFile(new URL('../schema/manifest-v1.schema.json', import.meta.url), 'utf8'))
const fixture = JSON.parse(await readFile(new URL('../fixtures/static-site/plugin.json', import.meta.url), 'utf8'))
const registrySchema = JSON.parse(await readFile(new URL('../schema/registry-v1.schema.json', import.meta.url), 'utf8'))
const registryFixture = JSON.parse(await readFile(new URL('../fixtures/repository/ohmycine-plugin-registry.v1.json', import.meta.url), 'utf8'))
const officialRegistry = JSON.parse(await readFile(new URL('../../ohmycine-plugin-registry.v1.json', import.meta.url), 'utf8'))
const ajv = new Ajv2020({ allErrors: true, strict: true })
addFormats(ajv)
const validate = ajv.compile(schema)
const validateRegistry = ajv.compile(registrySchema)

if (!validate(fixture))
  throw new Error(`valid fixture rejected: ${ajv.errorsText(validate.errors)}`)
if (!validateRegistry(registryFixture))
  throw new Error(`valid registry fixture rejected: ${ajv.errorsText(validateRegistry.errors)}`)
if (!validateRegistry(officialRegistry))
  throw new Error(`official registry rejected: ${ajv.errorsText(validateRegistry.errors)}`)

for (const [name, mutate] of [
  ['unknown capability', value => value.capabilities.push('pt.site')],
  ['path traversal', value => { value.entry = '../plugin.wasm' }],
  ['unknown field', value => { value.serverInternals = true }],
  ['invalid semver', value => { value.version = '1.0.0-beta..1' }],
  ['duplicate permission', value => { value.permissions = [{ kind: 'download.plan' }, { kind: 'download.plan' }] }],
  ['unbounded storage', value => { value.permissions = [{ kind: 'storage.private', maxBytes: 999_999_999 }] }],
]) {
  const invalid = structuredClone(fixture)
  mutate(invalid)
  if (validate(invalid))
    throw new Error(`invalid fixture accepted: ${name}`)
}

for (const [name, mutate] of [
  ['invalid registry semver', value => { value.plugins[0].version = '01.0.0' }],
  ['invalid release path', value => { value.plugins[0].packageUrl = 'https://github.com/ohmycine/example-plugins/releases/download/../plugin.omcp' }],
]) {
  const invalid = structuredClone(registryFixture)
  mutate(invalid)
  if (validateRegistry(invalid))
    throw new Error(`invalid registry accepted: ${name}`)
}

console.log(`plugin contract verified from ${root}`)

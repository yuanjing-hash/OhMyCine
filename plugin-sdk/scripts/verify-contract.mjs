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
const onlineMediaFixture = JSON.parse(await readFile(new URL('../fixtures/online-media.v1.json', import.meta.url), 'utf8'))
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

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const fixtureWork = onlineMediaFixture.feed?.[0]?.items?.[0]?.work
const fixtureVersion = fixtureWork?.segments?.[0]?.versions?.[0]
const fixtureAction = onlineMediaFixture.feed?.[0]?.items?.[0]?.actions?.[0]
const allowedActionIDs = new Set(['favorite.add', 'favorite.remove', 'watch-later.add', 'watch-later.remove', 'follow.add', 'follow.remove', 'like.add', 'like.remove', 'history.remove'])
const fixtureActions = onlineMediaFixture.feed?.[0]?.items?.[0]?.actions ?? []
const playback = onlineMediaFixture.playback
const assetRefs = [
  ...(playback?.assets ?? []),
  ...(playback?.subtitles ?? []),
  ...(playback?.danmaku ?? []),
].map(asset => asset.urlRef)
if (onlineMediaFixture.schemaVersion !== 1
  || fixtureWork?.identity?.value !== 'fixture-work'
  || fixtureVersion?.delivery !== 'online'
  || fixtureVersion?.variants?.[0]?.id !== 'qn:120'
  || fixtureAction?.id !== 'favorite.add'
  || fixtureActions.some(action => !allowedActionIDs.has(action.id))
  || playback?.selectionToken !== 'selection-fixture-1'
  || playback?.assets?.map(asset => asset.kind).join(',') !== 'dash-video,dash-audio'
  || assetRefs.length !== 4
  || assetRefs.some(reference => !uuidPattern.test(reference))) {
  throw new Error('online media cross-language fixture is invalid')
}

for (const [name, mutate] of [
  ['unknown capability', value => value.capabilities.push('pt.site')],
  ['path traversal', value => { value.entry = '../plugin.wasm' }],
  ['artwork path traversal', value => { value.libraryArtwork = '../cover.png' }],
  ['active artwork', value => { value.libraryArtwork = 'assets/cover.svg' }],
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

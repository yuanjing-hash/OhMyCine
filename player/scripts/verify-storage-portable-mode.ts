import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const restoreLocalStorage = installMockLocalStorage({
  'ohmycine-datasources': '[{"id":"local-one","password":"secret"}]',
  'ohmycine-theme': 'dark',
  'ohmycine-raw-source-index-schedule-v2:full:local:one:%2F': '{"version":2}',
  'unrelated-key': 'keep-me',
})

try {
  const settings = await import('../src/services/appSettings.ts')
  assert.equal(settings.shouldImportLegacyAppSettings('standard'), true)
  assert.equal(settings.shouldImportLegacyAppSettings('portable'), false)
  await settings.initializeAppSettings()

  assert.equal(settings.getAppSetting('ohmycine-theme'), 'dark')
  assert.equal(settings.getAppSetting('ohmycine-datasources'), '[{"id":"local-one"}]')
  assert.equal(settings.getAppSetting('ohmycine-datasources')?.includes('secret'), false)
  await settings.setAppSetting('ohmycine-theme', 'light')
  await settings.flushAppSettings()
  assert.equal(globalThis.localStorage.getItem('ohmycine-theme'), 'light')
  await settings.removeAppSetting('ohmycine-theme')
  await settings.flushAppSettings()
  assert.equal(globalThis.localStorage.getItem('ohmycine-theme'), null)
  assert.equal(globalThis.localStorage.getItem('unrelated-key'), 'keep-me')

  const sourceRoot = new URL('../src/', import.meta.url)
  for (const relativePath of [
    'stores/datasource.ts',
    'composables/useTheme.ts',
    'services/scraper/tmdb.ts',
    'services/scraper/classificationRules.ts',
    'services/scraper/rawSourceIndexScheduler.ts',
    'services/datasource/credentialStore.ts',
  ]) {
    const source = await readFile(fileURLToPath(new URL(relativePath, sourceRoot)), 'utf8')
    assert.doesNotMatch(source, /\blocalStorage\b/, `${relativePath} must use the shared settings boundary`)
  }

  const workflow = await readFile(fileURLToPath(new URL('../../.github/workflows/player-beta-release.yml', import.meta.url)), 'utf8')
  assert.match(workflow, /standard_asset="\$\{ASSET_PREFIX\}-standard\.zip"/)
  assert.match(workflow, /steps\.package\.outputs\.standard_asset/)
  assert.match(workflow, /portable_dir}\/portable\.flag/)

  const rustStorage = await readFile(fileURLToPath(new URL('../src-tauri/src/storage.rs', import.meta.url)), 'utf8')
  assert.match(rustStorage, /portable\.flag/)
  assert.match(rustStorage, /app_local_data_dir/)
  assert.match(rustStorage, /MIGRATION_FILES/)

  console.log(JSON.stringify({
    migratedKeys: 3,
    browserFallbackRoundTrip: true,
    directLocalStorageConsumers: 0,
    portableReleaseMarker: true,
    standardReleaseAsset: true,
    portableLegacyImportDisabled: true,
  }, null, 2))
}
finally {
  restoreLocalStorage()
}

function installMockLocalStorage(initial: Record<string, string>): () => void {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const values = new Map(Object.entries(initial))
  const storage: Storage = {
    get length() {
      return values.size
    },
    clear() {
      values.clear()
    },
    getItem(key: string) {
      return values.get(key) ?? null
    },
    key(index: number) {
      return [...values.keys()][index] ?? null
    },
    removeItem(key: string) {
      values.delete(key)
    },
    setItem(key: string, value: string) {
      values.set(key, value)
    },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })

  return () => {
    if (descriptor)
      Object.defineProperty(globalThis, 'localStorage', descriptor)
    else
      delete (globalThis as { localStorage?: Storage }).localStorage
  }
}

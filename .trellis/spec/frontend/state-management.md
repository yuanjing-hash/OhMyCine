# Frontend State Management

> State management rules for OhMyCine Player.

---

## Overview

Use Pinia for shared state. Keep component-local UI state in components and reusable reactive logic in composables. Server state is optional enhancement state and must not block Player independent use.

---

## Store Domains

Planned stores:

- `player`: playback status, current media, time, duration, volume, tracks, fullscreen/PiP state.
- `media`: cached media lists, selected item/detail, playback history, continue watching.
- `server`: optional Server URL/session/status, sync status, enhanced feature availability.
- `settings`: UI, language, DataSource configs, AI provider config references.
- `ui`: layout, theme, sidebar state, modals/toasts.

---

## State Categories

### Local component state

Use component refs for transient UI state such as hover state, menu open state, form draft fields, and drag interactions.

### Global app state

Use Pinia for state needed across routes/components: configured data sources, playback status, settings, Server connection, theme.

### Server/external state

Use services and stores to cache external data, but source-of-truth remains the DataSource or Server. Always handle offline/disconnected states.

Home aggregation must isolate external failures at the source/section level. A failed, disabled, misconfigured, or metadata-unavailable source must not replace or hide hero, continue-watching, recently-added, or library rows from other working sources; empty/error placeholders should not be inserted as normal `HomeSection` candidates.

### Raw Source Auto Indexing Contract

#### 1. Scope / Trigger
- Trigger: Player-side scraping/indexing for raw file sources such as OpenList/Alist, CloudDrive2, and local files.
- Applies to app startup, Home aggregation, raw source pages, scan cache reads/writes, scheduler state, and manual "scan now" controls.

#### 2. Signatures
- Scheduler factory: `createRawSourceIndexScheduler(options?: RawSourceIndexSchedulerOptions): RawSourceIndexScheduler`.
- Singleton: `rawSourceIndexScheduler`.
- Target builder: `createRawSourceAutoIndexTargets(configs, resolveSource): RawSourceIndexTarget[]`.
- Target shape: `{ sourceId, sourceType, rootPath, source: Pick<DataSource, 'list'> }`.
- Auto trigger: `triggerAutoIndexForTargets(targets, { intervalMs? }): Promise<RawSourceIndexTriggerResult[]>`.
- Manual trigger: `forceScan(target, { onLog? }): Promise<RawLocalScanCache>`.
- Status read: `getStatus({ sourceId, sourceType, rootPath? }): RawSourceIndexStatus`.

#### 3. Contracts
- App startup should call `startAutoIndexing()` once with current raw-source targets; it must not block first paint or Home loading.
- The default scheduler interval is `6 * 60 * 60 * 1000` ms unless a test or explicit caller overrides it.
- Cooldown keys are source-scoped and root-scoped: `sourceType + sourceId + normalized rootPath`.
- Auto indexing uses `runRawSourceLocalScan()` and the existing local raw scan cache; it must only read provider directory/file metadata and write local app data/cache.
- Auto indexing failures are background state only. They must not throw through `HomeView`, `DataSourceStore.loadHomeSections()`, or aggregated `HomeSection` collection.
- Manual scan controls may call `forceScan()` for immediate re-indexing, but manual scan is an advanced override, not the primary UX.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| One raw source scan fails | Mark that source status as `failed`; keep other source Home sections visible |
| Same source/root triggers during cooldown | Return `cooldown` with `skipped: true`; do not scan again |
| Another source/root triggers during cooldown | Treat independently and allow scan when its own cooldown permits |
| A scan is already in flight | Return `running` / skipped state and reuse the in-flight work |
| Source is disabled or unavailable | Do not create an auto-index target for it |
| Schedule/cache storage is unavailable | Continue in memory/best-effort mode; browsing and Home must still work |
| Manual scan fails | Show source-page error only; file browsing and other sources remain usable |

#### 5. Good/Base/Bad Cases
- Good: Emby Home sections render while OpenList auto indexing fails in the background.
- Base: OpenList has no cache yet, Home shows "waiting for auto index" status and the source page still offers folder browsing.
- Bad: Home displays an empty/error OpenList section that hides Emby hero/latest/continue rows.

#### 6. Tests Required
- Verify source cards show OpenList/Alist as waiting/pending when no local raw scan cache exists.
- Verify an OpenList indexing failure does not remove Emby hero, continue-watching, or recently-added sections.
- Verify scheduler cooldown suppresses repeated scans for the same source/root while allowing another source/root to scan.
- Verify TMDB credential auth routing remains separate from indexing scheduling: API Key uses `api_key`, Read Access Token uses `Authorization: Bearer`.

#### 7. Wrong vs Correct

Wrong:
```ts
await rawSourceIndexScheduler.forceScan(target)
await store.loadHomeSections()
```

Correct:
```ts
rawSourceIndexScheduler.startAutoIndexing({
  getTargets: async () => createRawSourceAutoIndexTargets(store.orderedConfigs, id => store.getSource(id)),
})
await store.loadHomeSections()
```

### URL state

Use Vue Router for route identity: current view, selected source ID, selected media ID, search query where appropriate.

---

## DataSource Configuration

- DataSource configs are ordered by `order` for the dynamic sidebar.
- User-facing display fields include name/displayName/iconUrl.
- Sensitive credentials should be stored through OS secure storage and referenced by `credentialRef` where possible.
- Ordinary config files must not contain API keys, cookies, passwords, AI keys, or server tokens in plaintext.
- Config import/export must redact sensitive values by default.

### Credential Reference Contract

Until OS secure storage is wired for every desktop target, a bounded MVP may use a Tauri command-backed persistent credential boundary for provider tokens, but it must still avoid plaintext DataSource config/localStorage and must not claim OS keychain/libsecret/DPAPI encryption.

#### 1. Scope / Trigger
- Trigger: adding or changing DataSource credentials for Emby/Jellyfin/OpenList/Alist/CloudDrive2/Server/AI providers.
- Applies to Settings forms, Pinia persistence, DataSource `init`, source export/import, sync, logs, and playback URL routing.

#### 2. Signatures
- Persisted config: `DataSourceConfig` stores non-sensitive fields plus `extra.credentialRef` or equivalent; it must not define raw `apiKey`, `username`, or `password` fields.
- Credential helper: stores/retrieves/removes structured secret values by `credentialRef`, such as Emby `accessToken`, `username`, and `password`.
- Provider credentials must use provider-specific structured envelopes, for example Emby `{ provider: 'emby', accessToken, username, password }` and OpenList/Alist `{ provider: 'alist', token, username, password }`; readers must reject envelopes for the wrong provider.
- When a successful re-login changes only the credential value while the visible config stays the same, update a non-sensitive field such as `extra.credentialVersion` so `DataSourceManager` sees a new config signature and reinitializes the source instead of reusing an old in-memory token.
- Tauri credential commands: `credential_set`, `credential_get`, and `credential_delete` persist/retrieve/delete secret values by credential reference.
- Add-source flow: generated source id, credential ref, and stored config id must match before the source is persisted.

#### 3. Contracts
- New API keys/tokens/passwords must not be written to `localStorage`, regular config JSON, or Pinia persistence snapshots.
- Desktop Player credentials should survive app restart through the Tauri credential boundary when available.
- The current SQLite credential boundary stores encrypted secret payloads in app data (`credentials.sqlite`) keyed by hashed credential references; the local master key is also app-data scoped, so document it as encrypted-at-rest with local-key limitations, not OS keychain/DPAPI/libsecret storage.
- Browser/Vite-only fallback may keep credentials in memory only and must show/carry a limitation state when persistence is unavailable.
- If config save or post-login validation fails after writing a credential, remove the newly written credential or restore the full previous structured credential for existing sources.
- If a stored source is missing its credential after restart, show a reconnect/re-enter-token state instead of treating the source as deleted or connected.
- Removing a source must delete the persistent SQLite credential row and any in-memory fallback for that `credentialRef`.
- Persistence sanitization must reconstruct safe config fields and drop sensitive `extra` keys before writing config to localStorage.
- Export/sync includes source structure by default, not the secret value.
- DataSource metadata caches may keep mapped or raw provider metadata in memory for faster repeat navigation, but must not persist or cache credentials, passwords, access tokens, raw stream URLs, or tokenized playback URLs.
- Source-scoped cache invalidation must clear only that source's media/list/detail/home metadata and must not remove config or credentials.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| User adds Emby source with account/password | Authenticate first, persist only non-sensitive config and credential reference, then discard password |
| User adds source with a manually pasted token | Reject for normal Emby UX unless explicitly implementing an advanced/import flow |
| Store generates a different id than the credential ref expects | Treat as a bug; source id and credential ref must be derived from the same id |
| Config save fails after credential write | Remove newly written credential |
| Post-login library validation fails after overwriting existing credential | Restore previous credential or remove new credential for a new source |
| Re-login succeeds but config signature does not change | Treat as a bug; bump a safe credential version/revision so the manager reloads the token |
| App restarts in Tauri desktop | Credential should load from persistent credential boundary |
| App runs in browser/Vite without Tauri commands | Use memory fallback only and show a persistence limitation warning |
| Source is disabled | Do not initialize it or allow browsing/playback until re-enabled |
| User clicks clear cache for a source | Clear only source-scoped metadata cache; keep config and credentials intact |
| Source is removed | Delete persistent SQLite credential row, memory fallback, and source-scoped metadata cache |
| Legacy plaintext credential file fallback exists | Remove it; credential reads/writes/deletes must use SQLite boundary only |
| Config includes sensitive top-level or `extra` keys | Drop them before persistence/export |
| Export config is requested | Redact or omit credential values |

#### 5. Good/Base/Bad Cases
- Good: OS secure storage stores the token and config stores only `credentialRef`.
- Base: session storage keeps the token for the current app session and config stores only `credentialRef`.
- Bad: `apiKey` is saved in localStorage because it is convenient for reloads.

#### 6. Tests Required
- Inspect persisted config after adding a source and verify no raw token/password is present.
- Verify add failure cleans up the temporary credential.
- Verify restart/missing credential state is user-safe.
- Verify source sidebar and local playback remain usable when one source lacks credentials.

#### 7. Wrong vs Correct

Wrong:
```ts
addConfig({ type: 'emby', url, apiKey: token })
```

Correct:
```ts
const auth = await EmbyDataSource.authenticate({ url, username, password })
const credentialRef = `datasource:${sourceId}:emby-token`
credentialStore.set(credentialRef, auth.accessToken)
addConfig({
  id: sourceId,
  type: 'emby',
  url,
  displayName: auth.serverName,
  enabled: true,
  extra: { credentialRef, userId: auth.userId },
})
```

---

## Server Connection State

- Server disconnected is a normal state.
- Server-only/enhanced pages show disabled/placeholder states with clear guidance.
- Do not hide local files, Emby/Jellyfin, OpenList/Alist, or CloudDrive2 functionality because Server is unavailable.
- Config sync defaults to structural sync; full credential sync requires explicit confirmation.

---

## AI State

- AI recommendations are Player-side by default.
- AI API keys belong in secure storage, not Pinia persistence or regular config.
- RAG context should include only library metadata needed for recommendations and avoid local absolute paths/credentials by default.
- Recommendations must be constrained to media the user already has indexed.

---

## Common Mistakes

- Persisting secrets in Pinia/localStorage/config JSON.
- Promoting every component field to a global store.
- Treating Server API results as required for the home page.
- Reordering data sources without persisting the order.
- Automatically overwriting local credentials during sync.

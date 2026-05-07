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

Until OS secure storage is wired for every desktop target, a bounded MVP may use session-only credential storage for provider tokens, but it must still avoid plaintext persistent config.

#### 1. Scope / Trigger
- Trigger: adding or changing DataSource credentials for Emby/Jellyfin/OpenList/Alist/CloudDrive2/Server/AI providers.
- Applies to Settings forms, Pinia persistence, DataSource `init`, source export/import, sync, logs, and playback URL routing.

#### 2. Signatures
- Persisted config: `DataSourceConfig` stores non-sensitive fields plus `extra.credentialRef` or equivalent.
- Credential helper: stores/retrieves/removes secret values by `credentialRef`.
- Add-source flow: generated source id, credential ref, and stored config id must match before the source is persisted.

#### 3. Contracts
- New API keys/tokens/passwords must not be written to `localStorage`, regular config JSON, or Pinia persistence snapshots.
- Session-only credential storage is acceptable only as a temporary MVP limitation and must be called out in UI/docs if it means credentials are lost after restart.
- If config save fails after writing a session credential, remove the orphan credential.
- If a stored source is missing its session credential after restart, show a reconnect/re-enter-token state instead of treating the source as deleted or connected.
- Export/sync includes source structure by default, not the secret value.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| User adds Emby source with account/password | Authenticate first, persist only non-sensitive config and credential reference, then discard password |
| User adds source with a manually pasted token | Reject for normal Emby UX unless explicitly implementing an advanced/import flow |
| Store generates a different id than the credential ref expects | Treat as a bug; source id and credential ref must be derived from the same id |
| Config save fails after credential write | Remove newly written credential |
| App restarts and session credential is gone | Show auth-required/reconnect state |
| Source is disabled | Do not initialize it or allow browsing/playback until re-enabled |
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
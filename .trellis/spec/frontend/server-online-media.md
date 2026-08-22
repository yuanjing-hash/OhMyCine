# Server Online Media

> Executable Player contracts for generic Server-hosted online libraries, paged history, provider progress, stream variants, and plugin danmaku.

## Scenario: Generic online playback and history

### 1. Scope / Trigger

- Trigger: changing `ServerDataSource`, online-library DTO parsing, HistoryView, playback progress synchronization, stream-quality selection, or plugin-provided danmaku.
- Server online media is an optional DataSource enhancement. Local and direct provider playback must remain available when Server is disconnected.

### 2. Signatures

```text
MediaWork -> MediaSegment -> MediaVersion -> StreamVariant
PlaybackPlan.danmaku[] -> Server same-origin asset track
player_list_playback_history(sourceId?, afterUpdatedAt?, afterIdentityKey?, limit)
DataSource.listPlaybackHistory?(cursor?, limit?)
DataSource.syncPlaybackProgress?(event)
DataSource.getDanmakuComments?(track)
```

Local history ordering is `updated_at DESC, identity_key ASC`; the next page uses both boundary values.

### 3. Contracts

- Parse Server responses from `unknown` at the DataSource boundary. Views do not call plugin/provider APIs or depend on provider-specific fields.
- The History page is distinct from Continue Watching. Local SQLite history uses stable bounded pages; Server online history keeps its opaque cursor and exposes concrete online libraries such as Bilibili as separate history sources.
- Playback always saves local history first. Provider progress sync runs through the DataSource boundary; failure shows a safe source-level diagnostic but never rolls back local history or interrupts playback.
- Online playback context preserves exact library/work/segment/version identity across detail, history, and direct-play entry points so progress cannot be written to the wrong provider item.
- A dedicated quality button switches only `StreamVariant`; it does not change segment or media version and hides when fewer than two usable variants exist. Replacement playback is prepared before switching, and failure preserves or restores the previous stream.
- When the exact playback plan includes plugin danmaku, Player loads that Server same-origin authenticated track first. Missing, empty, or failed plugin tracks fall back to the existing generic danmaku matcher; manual search remains available.
- Provider danmaku cache keys include source, item, media source/version, variant, and track ID. Never cache or persist upstream URLs, provider headers, Server Bearer tokens, or credential values.
- Server Bearer may be attached only to the configured Server origin. Cross-origin playback and subtitle/danmaku requests must drop private headers.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Server disconnected | Show an online-source unavailable state; local history and other DataSources continue |
| Local page boundary has duplicate timestamps | Use `identity_key` tie-breaker; no duplicate/omitted rows |
| Provider progress returns an error | Keep playback and local history; show safe sync diagnostic |
| Plugin danmaku is absent/empty/fails | Fall back to generic matching without blocking playback |
| Variant is unavailable or refresh fails | Keep/restore the current playable stream and report a safe error |
| Online DTO contains an unsafe URL/unknown shape | Reject at DataSource boundary |

### 5. Good / Base / Bad Cases

- Good: Player opens Bilibili history page 2 with the Server cursor, starts a segment, saves local progress, and then reports the same exact identity remotely.
- Good: a Bilibili playback plan supplies a Server danmaku asset; comments load directly and generic title matching is skipped.
- Base: Bilibili progress sync fails while offline; playback and local resume state still work.
- Bad: treat Continue Watching as complete history, paginate by timestamp alone, infer provider identity from title, or send Server Bearer to a provider/CDN URL.

### 6. Tests Required

- Rust tests cover stable local-history page boundaries, source filters, deletion isolation, and bounded limits.
- TypeScript verification covers generic online DTOs, same-origin gateway rules, online history, progress sync, plugin danmaku, variants, and fallback behavior.
- Run `verify:server-datasource`, `verify:server-online-library`, `verify:stream-quality`, `verify:danmaku`, typecheck, lint, build, Cargo tests, and strict all-target Clippy.

### 7. Wrong vs Correct

#### Wrong

```ts
await fetch(providerDanmakuUrl, { headers: { Cookie: providerCookie } })
```

#### Correct

```ts
const loaded = await source.getDanmakuComments?.(playbackPlan.danmaku[0])
if (!loaded?.length)
  await loadGenericDanmakuFallback(identity)
```

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
DataSource.refreshHomeSection?(refreshKey)
DataSource.performSiteAction?(itemId, actionId, value?, confirmed?)
MediaStreamRequest.audioUrl / audioHeaders
online-library | libraryId
online-node | libraryId | opaqueNodeToken
server-category | libraryId | mediaType | categoryName
```

Local history ordering is `updated_at DESC, identity_key ASC`; the next page uses both boundary values.

### 3. Contracts

- Parse Server responses from `unknown` at the DataSource boundary. Views do not call plugin/provider APIs or depend on provider-specific fields.
- A Server source root lists libraries only. A physical library lists Server-owned classification categories before catalog works; an online plugin library lists generic navigation nodes. Player never hard-codes a provider, region, channel name or plugin route.
- Nested plugin navigation remains inside `DataSource.list()`: branch folders retain the online library identity plus the Server-issued opaque node token, while leaf folders retain their generic route identity. Breadcrumb labels are presentation only and must never be used to reconstruct node keys.
- v1 flat navigation arrays remain accepted. v2 navigation must be a strict hierarchical envelope; malformed nodes must not become provider-specific fallback routes or leak raw node keys into Player state.
- The History page is distinct from Continue Watching. Local SQLite history uses stable bounded pages; Server online history keeps its opaque cursor and exposes concrete online libraries such as Bilibili as separate history sources.
- Playback always saves local history first. Provider progress sync runs through the DataSource boundary; failure shows a safe source-level diagnostic but never rolls back local history or interrupts playback.
- Online playback context preserves exact library/work/segment/version identity across detail, history, and direct-play entry points so progress cannot be written to the wrong provider item.
- A dedicated quality button switches only `StreamVariant`; it does not change segment or media version and hides when fewer than two usable variants exist. Replacement playback is prepared before switching, and failure preserves or restores the previous stream.
- When the exact playback plan includes plugin danmaku, Player loads that Server same-origin authenticated track first. Missing, empty, or failed plugin tracks fall back to the existing generic danmaku matcher; manual search remains available.
- Provider danmaku cache keys include source, item, media source/version, variant, and track ID. Never cache or persist upstream URLs, provider headers, Server Bearer tokens, or credential values.
- Home contributions are generic Server DTOs. Player stores only device-side `enabled`, `order`, and `placement` preferences, keeps provider identity visible, refreshes one contribution through its opaque refresh key, and renders one source failure without discarding healthy sources.
- Site actions use the exact allowlisted action descriptor supplied by Server. Confirm only when `requiresConfirmation` or `destructive` is true; destructive actions use the danger confirmation style. Never invent provider action IDs in a view.
- DASH video and audio are separate playback assets. Desktop and Android route both URLs and their independent headers through the native safe playback bridge; neither route may forward Server Bearer or provider-private headers after a cross-origin redirect.
- Server Bearer may be attached only to the configured Server origin. Cross-origin playback and subtitle/danmaku requests must drop private headers.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Server disconnected | Show an online-source unavailable state; local history and other DataSources continue |
| Local page boundary has duplicate timestamps | Use `identity_key` tie-breaker; no duplicate/omitted rows |
| Provider progress returns an error | Keep playback and local history; show safe sync diagnostic |
| Plugin danmaku is absent/empty/fails | Fall back to generic matching without blocking playback |
| Variant is unavailable or refresh fails | Keep/restore the current playable stream and report a safe error |
| One home contribution fails | Render its safe source-level error; keep other contributions and physical sources |
| Site action is destructive or requires confirmation | Require explicit user confirmation and pass `confirmed=true`; ordinary add actions execute without a redundant confirmation |
| DASH audio bridge fails | Fail the prepared replacement plan safely; do not leak credentials or silently play video-only |
| Online DTO contains an unsafe URL/unknown shape | Reject at DataSource boundary |
| Nested node token is malformed, expired, or belongs to another library | Surface a safe source error and keep the existing breadcrumb state |

### 5. Good / Base / Bad Cases

- Good: Player opens Bilibili history page 2 with the Server cursor, starts a segment, saves local progress, and then reports the same exact identity remotely.
- Good: a Bilibili playback plan supplies a Server danmaku asset; comments load directly and generic title matching is skipped.
- Good: a generic online source contributes one Hero and one row; Player applies device order/placement and refreshes only the selected row.
- Good: a destructive `follow.remove` action declares confirmation while `favorite.add` does not; both use the same generic DataSource method.
- Base: Bilibili progress sync fails while offline; playback and local resume state still work.
- Bad: treat Continue Watching as complete history, paginate by timestamp alone, infer provider identity from title, hard-code one provider action in a view, or send Server Bearer to a provider/CDN URL.

### 6. Tests Required

- Rust tests cover stable local-history page boundaries, source filters, deletion isolation, and bounded limits.
- TypeScript verification covers generic online DTOs, home contribution fault isolation and refresh, action descriptor filtering, same-origin gateway rules, online history, progress sync, plugin danmaku, variants, DASH audio, and fallback behavior.
- Navigation verification covers physical category ordering/filtering, v1 flat compatibility, v2 branches, opaque token preservation, multi-level back/refresh behavior, and the absence of provider-specific branches in `ServerDataSource`.
- Rust and Android bridge tests/assertions cover separate video/audio loopback routes, Range preservation, and cross-origin private-header stripping.
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

For site actions, do not branch on a provider name:

```ts
// Wrong: if (provider === 'bilibili') await callBilibiliFavorite()
// Correct: await source.performSiteAction?.(item.id, descriptor.id, nextState, confirmed)
```

## Scenario: Server library artwork and hierarchical back navigation

### 1. Scope / Trigger

- Trigger: changing Server physical/plugin library cards, Server navigation folders, or global desktop back controls.

### 2. Signatures

- `MediaLibrary.posterUrl/backdropUrl?: string` and folder `MediaItem.posterUrl/backdropUrl?: string` carry only safe display artwork.
- `registerLayoutBackHandler(owner, handler) -> unregister`; `handler() -> boolean | Promise<boolean>` returns true only when it consumed one internal level.

### 3. Contracts

- Resolve Server artwork against the configured Server origin only when the normalized path starts with `/api/v1/assets/`; reject userinfo, cross-origin URLs and other paths.
- Library and navigation-folder cards use landscape presentation; playable works retain poster presentation.
- A source view registers one layout back handler. Both desktop back controls invoke the shared dispatcher, which consumes category/node/library state before Router history.
- Unregister on unmount so a stale view cannot intercept another route. At source root, preserve the existing Router-history/home fallback.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing artwork | Render the established fallback without blocking navigation |
| Cross-origin or malformed artwork | Discard it at the DataSource boundary |
| Nested stack has two or more nodes | Load the previous node and keep the source route |
| Selected library is at its first node | Return to the Server library list |
| Source root has no internal state | Delegate to Router history/home |
| View unmounts | Remove its handler |

### 5. Good / Base / Bad Cases

- Good: `分类内容 → 分类列表 → Server 媒体库列表 → 上一页面` uses consecutive back presses.
- Base: an old Server omits artwork and the card fallback remains usable.
- Bad: every back click calls `router.back()` directly, or Player hard-codes Bilibili/115 cover URLs.

### 6. Tests Required

- DataSource verification covers physical, category and plugin same-origin artwork mapping.
- Navigation verification covers handler-first behavior, Router fallback and both desktop controls using the shared dispatcher.
- Run `verify:server-datasource`, `verify:server-online-library`, `verify:server-library-navigation`, typecheck, lint and build.

### 7. Wrong vs Correct

Wrong:

```ts
router.back()
```

Correct:

```ts
await navigateLayoutBack(router)
```

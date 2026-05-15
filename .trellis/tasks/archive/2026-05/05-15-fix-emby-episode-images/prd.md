# 修复 Emby 剧集图片映射

## Goal

修复 Emby 剧集/电视剧详情页中每一集缩略图都相同的问题，让 episode 卡片优先使用 Emby 返回的该集真实剧照/缩略图，而不是统一继承剧集或季的父级图片。

## What I already know

- 用户反馈：带剧集的电视剧/剧集详情页，每一集的图都一样，没有获取 Emby 真正的每集图片。
- `MediaDetailView.vue` 的分集卡片使用 `episode.backdropUrl ?? episode.posterUrl` 渲染 16:9 缩略图。
- `EmbyDataSource.mapItem()` 统一调用 `posterUrl(item)` / `backdropUrl(item)`。
- 当前 `backdropUrl(item)` 会先用 `item.BackdropImageTags`，没有时立刻 fallback 到 `ParentBackdropItemId`，导致 episode 缺少独立 backdrop 时大量继承同一父级图。
- 当前 `posterUrl(item)` 对所有类型先使用 `Primary` 的海报尺寸，再 fallback 到 `Thumb`；episode 卡片场景更适合优先使用 episode `Thumb` 或独立 still/backdrop。
- Emby fields 已请求 `ImageTags`, `BackdropImageTags`, `ParentBackdropItemId`, `ParentBackdropImageTags`, `ParentThumbItemId`, `ParentThumbImageTag`。
- `.trellis/spec/frontend/type-safety.md` 已有 Emby mapping contract，要求 episode/season mapping 保留层级并避免错误泛化。

## Requirements

- Episode 类型的 `MediaItem.backdropUrl` 必须优先使用该 episode 自己的 16:9 图片：`ImageTags.Thumb` 或自身 `BackdropImageTags`。
- Episode 缺少自身图片时才 fallback 到父级 thumb/backdrop。
- Episode 类型的 `posterUrl` 不应为了分集卡片优先使用剧集/季的父级海报；可使用自身 Primary/Thumb，缺失时再 fallback。
- Series/season/movie/folder 现有海报和背景映射不能回退。
- 分集列表 UI 不需要改数据结构；修复 DataSource 映射后 `MediaDetailView.vue` 继续使用 `episode.backdropUrl ?? episode.posterUrl`。
- 不能展示 Emby tokenized image URL；只传给 `img src`，错误/文本中不输出。

## Acceptance Criteria

- [x] Emby episode 卡片优先显示每集自己的 thumb/backdrop/still。
- [x] 多集拥有不同 episode image tag 时，详情页分集卡片显示不同图片。
- [x] Episode 没有自身图片时仍有合理 fallback，不出现破图。
- [x] Movie/series/season/folder 的现有 poster/backdrop 行为不明显回退。
- [x] `npm run typecheck --prefix player` / `npm run lint --prefix player` / `npm run build --prefix player` 通过。
- [x] `RUSTC="$(rustup which rustc)" npm run tauri:build:windows --prefix player` 通过。

## Definition of Done

- Emby episode image mapping 明确区分 episode 与非 episode。
- 规格或 PRD 记录 episode image priority，避免后续再次把 episode 统一 fallback 到父级图。
- 用户 Windows 宿主可验证剧集详情页每集图片不再全部相同。

## Technical Approach

1. 在 `EmbyDataSource` 中增加 episode-aware image helpers 或让 `posterUrl` / `backdropUrl` 对 `item.Type === 'Episode'` 使用不同优先级。
2. Episode thumbnail priority：own `Thumb` → own `Backdrop/0` → own `Primary` → parent thumb → parent backdrop。
3. Non-episode 保持现有 poster/backdrop 优先级，避免影响电影、剧集、季、文件夹。
4. 更新 Emby mapping spec，记录 episode image priority。

## Out of Scope

- 新增图片缓存/预加载系统。
- 从 TMDB 或其他 metadata provider 补剧照。
- 改造整套剧集详情布局。
- 修复非 Emby DataSource 的 episode 图片映射。

## Technical Notes

- Likely files:
  - `player/src/services/datasource/emby.ts`
  - `.trellis/spec/frontend/type-safety.md`
- Validation must include Player web checks and Windows package build per project preference.

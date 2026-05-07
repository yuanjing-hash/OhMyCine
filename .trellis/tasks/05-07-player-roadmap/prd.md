# 规划 Player roadmap 下一阶段任务

## Goal

根据 `docs/architecture/06-roadmap.md`、Player 设计文档和当前代码状态，规划下一阶段可执行的 Player 工作，并拆出一个优先级最高的 Trellis 实施任务。

## What I already know

- Player 当前已经具备 Tauri/Vue/Rust/libmpv 基础工程、路由、Cinema OS 方向 UI、播放页基础控制层。
- 本地文件选择和 Player 页拖放可以把视频路径加载到 libmpv 后端。
- 为避免未绑定渲染目标时弹出不可控外部 mpv 窗口，当前已抑制可见 mpv 视频输出。
- 因此当前状态是“后端加载/控制通路已通，但窗口内视频画面尚未完成”。
- 播放控制层已改为 liquid-glass 风格，并支持沉浸式自动隐藏；上一集/下一集只是禁用占位，播放列表/剧集队列未接入。
- Roadmap 中 Player Phase 1 仍有多个核心缺口：内嵌视频渲染、DataSource 具体实现、设置页数据源配置、LocalFileDataSource 浏览、播放历史/继续观看、字幕/音轨菜单、播放列表等。

## Current Player State

### Completed / Base Available

- Player app scaffold exists under `player/`.
- Vue Router / Pinia / UnoCSS / Tauri v2 foundation exists.
- Basic layout, home shell, source route placeholder, settings route placeholder, player route exist.
- Floating local file picker can select broad video formats and route to `/player`.
- Player route can react to query path changes and load selected local file.
- libmpv FFI owner/control path exists.
- Windows GNU cross-build libmpv link/runtime packaging path has been fixed.
- Playback chrome styling and auto-hide behavior are aligned with current Cinema OS direction.

### Not Yet Complete

- True embedded video rendering is missing: no native surface / `wid` / `MpvRenderContext` binding is implemented.
- Video display inside OhMyCine is not functional yet because visible mpv output is intentionally suppressed.
- DataSource interfaces/types exist, but concrete DataSource implementations and DataSourceManager/registry are missing.
- Settings page does not yet configure actual data sources.
- Source library browsing is placeholder-backed.
- Local folder/library browsing, file association, recent files, playback history, continue-watching are not implemented.
- Subtitle/audio track selection menus are not implemented.
- Playlist/episode queue is not implemented.

## Candidate Next Tasks

### Option A: Player Embedded Video Rendering MVP (Recommended)

Implement the first real in-window video rendering path for libmpv.

**Why first:** The user-facing playback loop is currently the biggest mismatch: local file selection can load/control, but the user still cannot watch video inside OhMyCine. DataSource and library work will feel incomplete until the playback surface works.

**Likely scope:**

- Design and implement a minimal desktop render bridge for the first supported runtime target.
- Wire `MpvRenderContext` / native surface or equivalent rendering path explicitly.
- Only enable visible mpv video output after a real embedded render target exists.
- Preserve current no-external-window safety behavior as the fallback.
- Keep UI truthful for unsupported/failed render initialization states.
- Validate compile/build and Windows package generation; Windows runtime visual verification remains user-owned.

### Option B: DataSource + Settings MVP

Implement actual configured data sources behind the existing DataSource abstractions.

**Why later:** This unlocks real browsing, but selecting media from sources still depends on a useful playback surface.

**Likely scope:**

- Add DataSourceManager/registry.
- Turn settings stub into add/edit/remove connection UI.
- Implement one or two first concrete sources, likely local folder and OpenList/Alist.
- Keep Server as optional placeholder/enhancement.

### Option C: Local Standalone Library MVP

Make Player better as an independent local-video app before cloud/server sources.

**Why later:** This is lower risk than embedded rendering and complements it well, but still benefits from first fixing the video surface.

**Likely scope:**

- Local folder DataSource.
- Recent files and playback history.
- Continue watching.
- Basic filename metadata fallback.
- Later TMDB scraping/cache.

## Requirements

- Preserve final roadmap scope; adjust ordering only, do not delete planned Player/Server/Hub/CLI capabilities.
- Prioritize work that makes current Player playback truthfully useful.
- Keep Player independent-first and not dependent on Server.
- Keep roadmap status honest: do not mark in-window video complete until actual embedded rendering is implemented.
- Create the next execution task around the highest-impact Player gap.

## Acceptance Criteria

- [ ] Current Player completed/incomplete state is summarized from roadmap/design/current implementation.
- [ ] 2–3 next-stage work options are identified with trade-offs.
- [ ] A recommended next task is selected.
- [ ] A concrete Trellis task is created for the selected next implementation slice.
- [ ] The created task has a PRD with goal, scope, acceptance criteria, out-of-scope, and validation expectations.

## Definition of Done

- Roadmap planning PRD exists in this task directory.
- Next implementation task exists under `.trellis/tasks/`.
- No product scope is removed from the roadmap.
- No code implementation is performed as part of this planning task.

## Decision (ADR-lite)

**Context:** Current local playback work exposed the most important Player gap: selected video can reach the backend, but the video frame is not embedded in OhMyCine because no native render target is bound.

**Decision:** Plan the next executable Player task as an embedded video rendering MVP before broad DataSource/library work.

**Consequences:** DataSource and local library features remain planned, but are sequenced after the playback surface becomes genuinely usable. The implementation must avoid regressing into external mpv windows and must keep unsupported states truthful.

## Out of Scope

- Implementing code in this planning task.
- Completing all roadmap Player Phase 1 items in one task.
- Implementing cloud/server DataSources before the playback surface works.
- Claiming Windows runtime playback is verified from WSL build output alone.

## Technical Notes

- Relevant specs:
  - `.trellis/spec/frontend/component-guidelines.md`
  - `.trellis/spec/frontend/directory-structure.md`
  - `.trellis/spec/frontend/quality-guidelines.md`
- Relevant docs:
  - `docs/architecture/03-player-design.md`
  - `docs/architecture/06-roadmap.md`
- Current known implementation constraints:
  - `player/src-tauri/src/mpv/render.rs` is still a placeholder/no-op area.
  - Visible libmpv output must not be enabled without a bound render target.
  - WSL cross-build success only proves executable/installer generation, not Windows runtime playback.

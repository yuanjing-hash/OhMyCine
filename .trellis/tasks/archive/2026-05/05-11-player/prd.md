# Player 播放页全屏沉浸式布局

## Goal

播放页面（`/player`）去掉顶部导航栏、左侧数据源菜单、返回按钮，只保留底部控制条和右下角隐藏菜单，视频画面全屏铺满整个窗口，控制元素浮在透明层上，实现真正的影院级沉浸体验。

## Background

当前 `AppLayout.vue` 无条件包裹所有路由，导致播放页也被以下 UI 元素覆盖：

- **WindowChrome**（顶部导航栏）：Home/Settings 按钮 + 窗口最小化/最大化/关闭按钮 + Tauri 拖拽区域
- **DataSourceSidebar**（左侧数据源菜单）：hover 触发的左侧数据源列表
- **BackButton**（返回按钮）：`left-6 top-3` 的返回导航
- **FloatingControls**（右下角浮动控件）：打开本地视频 + 主题切换

这些元素在播放页不必要，占据了屏幕空间，破坏沉浸感。

用户的需求是：
1. 视频画面必须全屏铺满整个播放窗口
2. 播放页不需要顶部导航栏（Home/Settings/窗口控制）—— 这些是其他页面才需要的
3. 播放页不需要左侧数据源菜单 —— 这是其他页面才需要的
4. 只需要底部控制条（播放/暂停/进度/音量）和右下角隐藏菜单
5. 控制元素通过透明/半透明 overlay 浮在视频上方，视频填满整个背景

## Requirements

### 布局隔离

- 播放页（`/player`）**不渲染** WindowChrome（顶部导航栏）
- 播放页**不渲染** DataSourceSidebar（左侧数据源菜单）
- 播放页**不渲染** BackButton（返回按钮）
- 播放页**保留**底部控制条（PlayerControls）和右下角隐藏菜单（FloatingControls 或等效入口）

### 实现方式

在 `AppLayout.vue` 中使用 `useRoute()` 检测当前路由，当 `route.name === 'player'` 时条件性隐藏不需要的布局元素。

具体做法：
- `AppLayout.vue` 添加 `useRoute()` 引入
- 计算属性 `isPlayerRoute = computed(() => route.name === 'player')`
- `<WindowChrome />` 加 `v-if="!isPlayerRoute"`
- `<DataSourceSidebar />` 加 `v-if="!isPlayerRoute"`
- `<BackButton />` 已有路由条件，但需排除 `/player` 路由

### 窗口拖拽

WindowChrome 被隐藏后，播放页失去了 Tauri 窗口拖拽区域。需要在播放页的顶部控制条（chrome visible 时）或整个播放页提供 `data-tauri-drag-region`，确保用户仍能拖动窗口。

### 视频全屏铺满

- 视频画面（mpv HWND）必须铺满整个播放窗口，不受其他 UI 元素的 bounds occlusion 限制
- 当控制条显示时，视频仍然全屏铺满，控制条浮在视频上方（半透明背景）
- 当控制条隐藏时，视频完全铺满，无任何 UI 遮挡

### 控制条行为

- 底部控制条保持现有的自动隐藏逻辑（鼠标静止 2.8s 后隐藏，鼠标移动/触摸重新显示）
- 控制条显示时：半透明渐变背景 + 控件，浮在全屏视频上方
- 控制条隐藏时：完全不可见，视频铺满整个窗口

### 右下角菜单

- 保留右下角 FloatingControls 或提供等效的隐藏菜单入口
- 该菜单用于：返回主页、打开设置、打开本地视频等导航功能（替代被隐藏的顶部栏和侧边栏的功能）

## Acceptance Criteria

- [x] 播放页不显示顶部导航栏（WindowChrome）
- [x] 播放页不显示左侧数据源菜单（DataSourceSidebar）
- [x] 播放页不显示返回按钮（BackButton）
- [x] 播放页显示底部控制条，支持自动隐藏和鼠标触发显示
- [x] 播放页右下角有隐藏菜单，包含返回主页、设置等导航入口
- [x] 视频画面全屏铺满整个播放窗口（topOcclusion 归零机制）
- [x] 控制条显示时浮在视频上方（半透明背景），不缩减视频区域
- [x] 控制条隐藏时视频完全铺满，无 UI 遮挡
- [x] 播放页仍可通过顶部区域或右下角菜单拖动窗口
- [x] 非播放页（首页、库、详情、设置）的布局不受影响
- [x] `npm run typecheck` / `lint` / `build` 全部通过

## Decision (ADR-lite)

**Context:** `AppLayout.vue` unconditionally wraps all routes with WindowChrome, DataSourceSidebar, BackButton, and FloatingControls. The player page needs a fullscreen immersive layout without these navigation elements.

**Decision:** Use route-aware conditional rendering in `AppLayout.vue` via `useRoute()`. When `route.name === 'player'`, hide WindowChrome, DataSourceSidebar, and BackButton with `v-if`. FloatingControls stays visible and gains Home/Settings navigation buttons on the player route. PlayerView gets `data-tauri-drag-region` on its top hover area for window dragging.

**Consequences:**
- Minimal change: 3 files edited, no new components, no layout restructuring
- Player page is fully immersive: video fills viewport, only bottom controls + bottom-right menu visible
- Non-player pages unaffected: all navigation elements render normally
- Window dragging preserved: top hover area in PlayerView doubles as Tauri drag region
- Component guidelines "back control on non-home routes" satisfied via FloatingControls Home button

## Out of Scope

- 字幕/音轨切换 UI
- 播放列表 / 剧集队列
- 倍速控制面板
- 视频比例/画面模式设置
- HDR / Dolby Vision 相关 UI

## Technical Notes

- 已改动文件：
  - `player/src/components/layout/AppLayout.vue` — 添加 `useRoute()` + `isPlayerRoute` computed，`v-if="!isPlayerRoute"` 隐藏 WindowChrome/DataSourceSidebar/BackButton
  - `player/src/components/layout/FloatingControls.vue` — 添加 `useRoute()` + `isPlayerRoute` computed，播放页显示 Home/Settings 导航按钮
  - `player/src/views/PlayerView.vue` — 顶部 hover 区域添加 `data-tauri-drag-region` 属性
- 视频全屏机制：WindowChrome 隐藏后 topOcclusion 自动归零，mpv HWND bounds 铺满整个窗口；控制条浮在视频上方（半透明 liquid-glass 背景）
- Spec 参考：
  - `.trellis/spec/frontend/component-guidelines.md` — Immersive Player Chrome Contract、BackButton 规则
  - `.trellis/spec/frontend/quality-guidelines.md` — 验证命令、禁止模式
  - `.trellis/spec/frontend/type-safety.md` — TypeScript 严格模式

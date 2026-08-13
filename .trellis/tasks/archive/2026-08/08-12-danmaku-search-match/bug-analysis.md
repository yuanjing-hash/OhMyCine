# Bug Analysis: 弹幕加载后播放器 UI 卡死

## 1. Root Cause Category

- **Category**: D — Test Coverage Gap / E — Implicit Assumption
- **Specific Cause**: 自动匹配修复后首次进入真实弹幕规模。Canvas overlay 每个动画帧从头扫描最多 50,000 条已排序弹幕，并重复做关键词转换与过滤，持续占满 WebView 主线程；libmpv 位于原生线程，因此视频继续播放但 Vue 控件、鼠标和键盘反馈卡死。

## 2. Why Earlier Checks Missed It

1. 原有自动匹配请求被官方参数校验拒绝，真实大弹幕数组从未到达渲染层。
2. parser、类型、Rust 网络和生产构建测试验证了正确性与安全边界，但没有覆盖最大允许弹幕量下的逐帧复杂度。
3. overlay 默认假定完整数组规模足够小，未利用 parser 已按时间排序这一契约。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | 对排序时间轴二分定位当前生命周期窗口 | DONE |
| P0 | Runtime bound | 单帧限制候选检查数与实际绘制数 | DONE |
| P0 | Test coverage | 用 50,000 条弹幕验证窗口选择与 overlay 限流契约 | DONE |
| P1 | Lifecycle | 暂停时仅在状态变化后重绘 | DONE |
| P1 | Documentation | 将大规模渲染复杂度写入 Danmaku Contract | DONE |

## 4. Systematic Expansion

- 任何允许大响应的 Canvas/动画功能都不能在 `requestAnimationFrame` 中全量遍历响应数组。
- 过滤后零结果也必须有候选检查上限，否则“全部被屏蔽”仍可能扫描整个同时间洪峰。
- 网络/API 修复应补一条真实上限数据进入最终消费者的性能检查，不能只验证协议层。

## 5. Knowledge Capture

- [x] 更新 `.trellis/spec/frontend/danmaku.md`
- [x] 更新 `verify-danmaku.ts`
- [x] 保存本任务根因复盘

## Follow-up: 阶梯动画与单集标题误用

- **Root cause**: overlay 直接使用低频 mpv `currentTime` 事件作为每帧坐标；弹幕身份直接复用 Player chrome 的单集标题，忽略了播放上下文已有的 `seriesName / seasonNumber / episodeNumber`。
- **Fix**: RAF 使用单调时钟在 mpv 锚点之间按倍速插值；统一身份解析优先结构化系列/季/集信息，文件 basename 作为 fallback，单集显示标题不再作为系列搜索词。
- **Prevention**: 50,000 条时间轴测试增加 144Hz 连续时钟断言，并用“莉可丽丝 / 慢慢的”回归样例固定身份优先级。

## Bug Analysis: 结构化搜索未自动选中唯一精确作品

### 1. Root Cause Category

- **Category**: E — Implicit Assumption / B — Cross-Layer Contract
- **Specific Cause**: composable 把所有作品组的集数扁平化后，以“全局候选数等于 1”作为自动选择条件。`/search/episodes` 本来就会返回模糊作品组，因此“莉可丽丝”的唯一精确组会被“莉可丽丝 新作”、“安琪莉可”等无关模糊组阻断。同时，设置页内嵌原生 match `<select>`，混淆了“播放参数设置”与“候选搜索/纠正”的 UI 归属。

### 2. Why Fixes Failed

1. **身份优先级修复**: 已确保查询输入使用 `seriesName + season + episode`，但只验证了“搜什么”，未验证“哪个结果可以自动选择”。
2. **全局数量假设**: 单结果样例让回归测试通过，但没有模拟官方搜索的多模糊作品组响应。
3. **错误 UI 归属**: 设置面板的下拉框掩盖了自动选择语义缺失，并与已有的专用搜索对话框重复。

### 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | 在 danmaku service/domain 边界提供纯函数，按唯一精确作品组 + 唯一过滤剧集判定 | DONE |
| P0 | Test coverage | 固定“莉可丽丝”与新作/包含词等混淆组的回归数据 | DONE |
| P0 | UI ownership | 候选和手动纠正仅在 `DanmakuSearchDialog` 呈现，移除设置内 match selector | DONE |
| P1 | Documentation | 在弹幕契约中记录保守 normalization 与歧义规则 | DONE |

### 4. Systematic Expansion

- **Similar Issues**: 任何分组搜索 API 都不应用扁平化总数替代领域唯一性。
- **Design Improvement**: 保留 provider 分组语义到选择边界，只将扁平化结果用于内部加载状态，不用于正确性判定。
- **Process Improvement**: 结构化身份回归测试必须同时覆盖查询输入和多组结果选择语义。

### 5. Knowledge Capture

- [x] 更新 `.trellis/spec/frontend/danmaku.md`
- [x] 更新 `verify-danmaku.ts`
- [x] 保存本次根因、早期修复不完整性和 UI 归属结论
## Bug Analysis: Danmaku canvas disappeared with Player chrome auto-hide

### 1. Root Cause Category

- **Category**: B - Cross-Layer Contract / D - Test Coverage Gap
- **Specific Cause**: The missing content was Vue Canvas danmaku, not native mpv subtitles. Although the overlay itself did not read `shouldShowChrome`, auto-hide was still promoted to a global `player-chrome-hidden` class on `html` and `body`, making the whole WebView tree share a chrome lifecycle marker. The fix removes that global/ancestor hidden state entirely: only explicit chrome nodes consume a narrow Pinia visibility state, while danmaku remains a persistent media sibling.

### 2. Why Earlier Checks Missed It

1. The initial report named subtitles, leading investigation into native `sid`, surface bounds, and WebView/native underlay behavior; the user correction established that mpv subtitles were unaffected.
2. Existing danmaku tests covered parsing, matching, timing, 50,000-comment performance, and controls, but not DOM ownership or compositor independence from chrome.
3. A z-index placed the Canvas visually between video and controls, but did not guarantee a stable composited surface when an animated sibling layer changed opacity.
4. The first correction added containment/isolation/transform promotion; the second added a low-alpha backplane. Both failed Windows runtime testing because neither removed the broad chrome lifecycle state from the overlay's ancestor tree.

### 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Scope correction | Fully revert the tentative subtitle/chrome workaround and preserve the existing desktop opacity fade | DONE |
| P0 | Architecture | Remove chrome-hidden state from `html`, `body`, and the Player root; expose a narrow store only to explicit chrome nodes | DONE |
| P0 | Test coverage | Assert no global/root chrome-hidden marker exists and danmaku has no chrome visibility/class/lifecycle dependency | DONE |
| P1 | Documentation | Record danmaku render-layer ownership and compositor contract | DONE |

### 4. Systematic Expansion

- **Similar Issues**: Any persistent media overlay can accidentally inherit toolbar lifecycle when a UI-specific state is written to a shared root ancestor.
- **Design Improvement**: Treat danmaku as media content. Only media presence, danmaku settings, playback time/state, and canvas sizing may govern it; chrome visibility is never an input.
- **Process Improvement**: Overlay tests must verify DOM placement, forbidden chrome dependencies, persistent RAF ownership, and compositor isolation in addition to data correctness.

### 5. Knowledge Capture

- [x] Reverted the mistaken subtitle workaround and its inaccurate spec/test assertions
- [x] Updated `.trellis/spec/frontend/danmaku.md`
- [x] Extended `verify-danmaku.ts` and auto-hide verification with forbidden global/root hidden-state assertions

---

## Bug Analysis: Danmaku loading raced Player chrome auto-hide

### 1. Root Cause Category

- **Category**: B - Cross-Layer Contract / D - Test Coverage Gap
- **Specific Cause**: `danmakuLoading` was rendered inside the controls but was absent from both `shouldShowChrome` and `canAutoHideChrome`. A pending inactivity timer could therefore hide the entire control surface while matching or comments were still loading, making the loading feedback and controls unavailable.

### 2. Why Earlier Checks Missed It

1. Danmaku lifecycle tests covered request cancellation and comment rendering, while chrome tests covered pointer/focus interaction; neither asserted the state handoff between them.
2. The loading indicator being a child of Player controls implicitly assumed the parent chrome would remain visible long enough to show it.

### 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Lifecycle | Treat `danmakuLoading` as a temporary chrome-visible and auto-hide-blocking state | DONE |
| P0 | Timer | Clear the pending hide timer on load start and restart normal inactivity timing on settle | DONE |
| P0 | Test coverage | Assert loading participates in both visibility and auto-hide predicates | DONE |

### 4. Systematic Expansion

- **Similar Issues**: Any status rendered only inside auto-hidden chrome needs an explicit parent visibility contract for its active lifetime.
- **Design Improvement**: Async feature state owns a bounded visibility lease; it does not permanently mutate the user's chrome preference.
- **Process Improvement**: Cross-component loading tests must cover both status production and visibility of its presentation surface.

### 5. Knowledge Capture

- [x] Updated `.trellis/spec/frontend/danmaku.md`
- [x] Extended Player chrome auto-hide verification

---

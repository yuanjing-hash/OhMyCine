# Bug Analysis: PT/Nyaa 发行名跨层失配与下载器能力边界

## 2026-08-25 Follow-up: 系列副标题与识别上下文丢失

### 1. Root Cause Category

- **Category**: B / C / D — 跨层契约、变更传播失败、测试覆盖缺口。
- **Specific Cause**: 站点 adapter 已返回标题、副标题，搜索请求也包含媒体类型，但 actor-bound claim 只保存发行标题。自动识别因而在进入共享 parser 前丢失 MoviePilot/Anitopy 风格解析所需的辅助标题事实，导致系列电影副标题、罗马音和错拼标题只能依赖人工介入。

### 2. Why Fixes Failed

1. 早期补词/正则只修 parser，无法恢复在 adapter → claim 边界已经丢弃的 subtitle/type facts。
2. 直接在 ranker 为具体作品补别名会让冻结样例通过，却无法覆盖其他系列作品，并增加静默错配风险。
3. 只测 parser 与 fake candidate 会跳过真实的 claim、查询预算和 TMDB 召回链。

### 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
| --- | --- | --- | --- |
| P0 | Architecture | claim 私有保存有界 subtitle 和 `movie|tv` 搜索提示，作为弱事实进入共享 recognizer | DONE |
| P0 | Security | URL/path/NUL/反斜杠/超长辅助值在任何元数据请求前丢弃 | DONE |
| P0 | Test Coverage | 使用本地 TMDB 假服务验证 subtitle 才能召回的完整 adapter → claim → service 流 | DONE |
| P0 | Change Propagation | Go `EngineVersion` 与 WebUI session 版本用跨层测试绑定 | DONE |
| P1 | Process | 每个生产失败原串进入冻结 corpus，并添加合法标题反例 | DONE |

### 4. Systematic Expansion

- **Similar Issues**: 本地/115/OpenList 的父目录与 manifest、站点 subtitle、搜索 media type、显式 TMDB ID 都是不同强度事实；任一层扁平化或丢失都会让后续 ranker 无法补救。
- **Design Improvement**: 保持 `结构解析 → 有界召回 → 统一排名 → 失败恢复`，不引入作品字典或 provider-specific parser。
- **Process Improvement**: 质量门同时覆盖 parser、共享 service、站点 claim、下载 override、benchmark 和 WebUI cache version。

### 5. Knowledge Capture

- [x] 更新 `.trellis/spec/backend/pt-discovery.md`。
- [x] 更新 `.trellis/spec/guides/cross-layer-thinking-guide.md`。
- [x] 仓库不存在 `src/templates/markdown/spec/` 镜像目录，无需同步模板。

## 1. Root Cause Category

- **Category**: B / D / E — 跨层契约、测试覆盖缺口、隐式假设。
- **Specific Cause**: 内置 `TV.txt`、`anime.txt` 一直存在且 322 条均被编译，但后续领域解析仍假设包名不能包含 `/`、类型可在季集解析前决定、点号清洗不会拆坏 `H.265/DDP5.1`，并只覆盖单一括号顺序。下载侧又把所有下载器都视为需要同一个 Server 资源锁，并假设发现层取得的 `.torrent` 可被 115 原生离线直接消费。

## 2. Why Fixes Failed

1. 早期修复只扩充技术词或字幕组正则，没有把截图中的完整发行名穿过 `Profile packs → parser → query budget → TMDB`，因此解析器局部改善仍会被输入校验或类型决策抵消。
2. 简化 fixture 丢失 `/`、括号顺序、整季区间、点号音轨和前置技术标签，无法复现真实站点失败。
3. 下载队列把“Server Worker 保护”和“qBittorrent 实际下载并发”混为一谈；115 则缺少 `.torrent bytes → magnet` 的能力适配层。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
| --- | --- | --- | --- |
| P0 | Test Coverage | 将完整 PT/Nyaa 标题同时放入 parser 与共享 `recognizeMedia` 回归 | DONE |
| P0 | Architecture | 解析与媒体类型决策保持在 provider-neutral 领域层；协议转换只在 downloader capability 边界 | DONE |
| P0 | Security | BTIH 对原始 `info` bencode 字节做 SHA-1，Tracker 有界，passkey 只留在 AES-GCM source envelope | DONE |
| P1 | Migration | v46 只提升未修改的旧默认队列策略，不覆盖用户自定义 revision | DONE |
| P1 | UI State | 搜索结果只用有界、短期、单标签页 sessionStorage 恢复并过滤过期 claim | DONE |

## 4. Systematic Expansion

- **Similar Issues**: 所有 PT/BT 站点、115/qBit/未来离线下载器都会经过同一识别和下载边界，不能在站点 adapter 内各写一套标题解析或协议猜测。
- **Design Improvement**: 保持 `站点结果 → 加密 source → downloader capability adaptation → provider` 与 `word packs → domain parser → metadata ranking` 两条明确管线。
- **Process Improvement**: 每个生产识别 bug 必须保存未清洗完整输入，并至少证明纯解析和正式服务入口都通过。

## 5. Knowledge Capture

- [x] 更新 `.trellis/spec/backend/media-classification-profiles.md`。
- [x] 更新 `.trellis/spec/backend/downloader-management.md`。
- [x] 更新 `.trellis/spec/backend/pt-discovery.md`。
- [x] 更新 `.trellis/spec/guides/cross-layer-thinking-guide.md`。
- [x] 仓库不存在 `src/templates/markdown/spec/` 镜像目录，无需同步模板。

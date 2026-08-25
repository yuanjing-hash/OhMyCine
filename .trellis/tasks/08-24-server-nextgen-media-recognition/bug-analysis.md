# Bug Analysis: PT/Nyaa 发行名跨层失配与下载器能力边界

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


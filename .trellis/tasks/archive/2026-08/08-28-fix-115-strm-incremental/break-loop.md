# Bug Analysis: STRM 每轮被误判为更新

## 1. Root Cause Category

- **Category**: B / D / E — Cross-Layer Contract, Test Coverage Gap, Implicit Assumption
- **Specific Cause**: 产物层把带 `now + 30 days` 的签名 URL bytes 当成普通确定性内容；fingerprint 层不知道签名租约会随时间变化，测试又只覆盖首次生成，没有覆盖相隔时间的第二个 generation。

## 2. Why Earlier Behavior Failed

1. 逐文件 SHA-256 对“已经生成的新 URL”做比较只能忠实发现 bytes 不同，无法判断变化只是续期时间。
2. generation/cleanup 安全模型是正确的，但每轮重新签名发生在 diff 之前，使本应 skipped 的项目全部进入写盘路径。
3. 首次生成和签名校验测试均会通过，缺少 bytes + mtime 的跨 generation 断言让回归长期不可见。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | 持久化私有 expiry/format lease facts；严格验证现有 URL 后再决定复用或续签 | DONE |
| P0 | Test | 两个相隔时间的 generation 断言 bytes/mtime 不变，续期窗口内只更新一次 | DONE |
| P0 | Security | 保留 exp/HMAC/opaque/active manifest，不改永久 URL、不写 provider identity | DONE |
| P1 | Performance | 一次加载 manifest、内存 diff、批量提交，移除逐项 SELECT + Save | DONE |
| P1 | Review contract | 将所有“含时间/nonce/key 的持久内容”加入 cross-layer checklist | DONE |

## 4. Systematic Expansion

- **Similar Issues**: 任何把临时 URL、签名时间、随机 nonce、rotating key ID 混入可重复生成 sidecar 的流程都可能产生假更新。
- **Design Improvement**: renderer 必须先检查可复用租约，再生成时间相关 bytes；fingerprint 始终表示真实落盘 bytes。
- **Process Improvement**: 对持久投影至少覆盖首次写入、跨时间 no-op、真实续期、key/origin 变化和升级旧行五类回归。

## 5. Knowledge Capture

- [x] 更新 `.trellis/spec/backend/media-library-foundation.md`
- [x] 更新 `.trellis/spec/guides/cross-layer-thinking-guide.md`
- [x] 更新 Server 与安全架构文档
- [x] 增加 v54 additive migration 和跨 generation 回归


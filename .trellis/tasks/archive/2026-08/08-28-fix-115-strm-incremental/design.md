# 技术设计

## 根因与边界

STRM 每轮调用 `SignArtifact(now + 30d)`，时间参数改变最终 bytes，导致现有 SHA-256 diff 永远判定为更新。修复必须继续满足签名 URL 有过期时间、opaque artifact、active manifest 和 provider identity 不落盘的安全契约。

## 设计

为 managed STRM 引入版本化的“签名内容租约”事实：artifact 记录当前持久内容的签名到期时间与签名格式版本。生成 desired map 时：

```text
existing file + managed manifest + matching opaque/source/path
  + signed URL strict verify
  + expiry outside renewal window
    => reuse exact bytes/fingerprint/mtime, update manifest binding only, skipped

missing/invalid/near-expiry/origin-key-format change
    => generate one new expiring signed URL, atomic replace, written|updated
```

签名 verifier 增加仅供内部使用的受控 inspect 结果（opaque、library、key、expiry、format），仍执行完整 HMAC/active-manifest 校验且不把 URL/provider 信息返回 DTO。续期窗口由 Server 常量控制并通过 injected clock 测试。

每轮一次加载现有 managed manifest 和一次形成 desired map，按 `(target_kind, normalized relative_path)` 分类。provider binding 可在 DB 中重新绑定而不写 STRM，因为解析时以 opaque artifact 查 active manifest。只有 complete non-partial generation 成功后，desired 缺失项才置 inactive 并交给现有 cleanup claim。

## Generation gate

- 真实 catalog/metadata/policy/目标缺失/签名续期/pending change barrier 任一成立才自动安排 artifact generation。
- 用户显式“立即增量/全量”允许创建验证 run，但 unchanged 必须全部 skip。
- pending media change 的 no-op carry-forward 继续遵守现有 `content_revision` 契约。

## 迁移

- Additive migration 为 `media_artifacts` 增加 nullable/defaulted `content_expires_at` 与 `content_format_version`（名称以实现时模型一致为准）。
- 旧行不批量改写；首次生成时严格检查现有 managed STRM，能验证的惰性回填并继续复用，不能验证的安全更新一次。
- 旧 v1 verifier 保留；签名 key 不因升级立即退役。

## 安全与删除

- 不采用无期限 v2 capability，不采用 MoviePilot pickcode URL。
- unmanaged collision 继续 skip，不收编。
- partial/failed/superseded 不停用 unseen manifest；cleanup 仍受 root identity、扩展名、reparse、ownership 与 per-file claim 保护。

## Rollback

新字段为 additive。回滚版本忽略字段并仍能验证原 v1 URL；升级实现不得创建旧代码无法解析的新永久 URL格式。

## Evidence

- `research/strm-incremental-diff.md`
- `.trellis/spec/backend/media-library-foundation.md`
- `.trellis/spec/backend/security-guidelines.md`

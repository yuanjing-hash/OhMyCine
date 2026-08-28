# 修复115 STRM真增量同步

## Goal

让 115 STRM 生成以真实期望内容和 manifest ownership 做差异同步：未变化条目跳过、新增条目写入、真实内容变化或签名续期才更新、云端已删除条目在完整扫描后受控清理。

## Background

- 当前每轮对已有 STRM 重新生成 `now + 30 days` 的签名 URL，`exp` 和 HMAC 每次变化，导致 bytes SHA-256 必变，因此约 76 个既有 STRM 每轮都被覆盖并计为更新。
- no-op 扫描仍推进 generation 并枚举所有 artifact，放大了 O(N) 数据库/文件 I/O，但它不是错误更新的直接根因。
- 现有 partial-scan、active manifest、root identity 和 cleanup claim 边界是正确安全机制，必须保留。

## Requirements

- 已有有效签名 STRM 在续期窗口之外必须复用原内容，不重新签名、不改写 bytes/mtime，并计为 skip。
- 签名仍必须包含过期时间；不得改成无期限静态 capability，也不得把 115 pickcode/provider ID 写入 STRM。
- 在签名临近到期、public origin/signing key/格式必须变化时进行一次真实更新并记录新的到期事实。
- 同路径 provider binding 更新只更新 active manifest；由于 STRM 指向稳定 opaque artifact，不应改写文件。
- 新路径写入新 STRM，旧路径只有在完整、成功、非 partial 权威扫描后进入 manifest cleanup。
- 一次加载当前 managed manifest 并形成 desired map，避免逐条 `SELECT + Save`；unchanged rebind 不计文件更新。
- no-op 周期扫描可不创建 artifact run；用户显式验证运行可以保留，但统计必须真实为 skip。
- 旧 v1 STRM 继续可播并惰性进入续期策略；升级不得一次性重写或误删全部投影。

## Acceptance Criteria

- [ ] 相隔数秒的两个相同 complete generations：第二轮 `write=0/update=0/cleanup=0`、`skip=N`，STRM bytes 与 mtime 不变。
- [ ] 同路径 provider item/binding 变化后 resolver 使用新 binding，STRM bytes 不变。
- [ ] 新增一个云端文件只写一个 STRM；改名/移动写新路径并在安全条件满足后清理旧路径。
- [ ] 完整扫描删除一个云端文件只删除对应 managed STRM；partial/failed/superseded 不删除 unseen 条目。
- [ ] 临近过期的 URL只续签一次，之后再次运行跳过；过期、篡改、inactive、unmanaged、wrong kind/target 均无法解析。
- [ ] 升级旧 manifest/STRM 不触发全量改写，旧未过期 URL继续有效。
- [ ] 数百条目回归证明 manifest SQL 不再按条目执行一组查询/保存，SQLite 事务时间有界。
- [ ] STRM、proxy、cleanup、migration 与 Web UI 统计测试及完整 Server 质量门通过。

## Out of Scope

- 无期限、无过期时间的静态 STRM 签名。
- 把 provider ID、pickcode、Cookie 或临时播放 URL写入投影文件。
- 删除 unmanaged 文件或放松 partial-scan 清理保护。

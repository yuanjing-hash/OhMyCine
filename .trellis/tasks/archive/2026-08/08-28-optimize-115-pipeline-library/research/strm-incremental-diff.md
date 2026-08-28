# Research: 115 STRM incremental diff

- Query: 定位 115 STRM 每次增量运行把大量正常条目标记为“更新”的根因；核对 manifest、内容指纹、签名 URL、扫描 generation、云端新增/删除同步与 MoviePilot P115StrmHelper 的实现。
- Scope: mixed
- Date: 2026-08-28

## Findings

### 1. 根因已定位：STRM 的内容指纹包含每次都变化的签名过期时间

当前数据流如下：

1. 每次媒体库 reconciliation 都令 `generation = dirty_generation + 1`，无论 catalog 是否真实变化，成功提交后都会写回新 generation 并调用 `ScheduleGeneration`（`server/internal/services/media_library.go:1615-1617`, `server/internal/services/media_library.go:1857-1864`, `server/internal/services/media_library.go:1908-1912`）。因此正常的周期扫描或生活事件唤醒也会留下一个 artifact run。
2. artifact worker 每轮加载该库的全部 Entry，逐条调用 `writeSTRM`（`server/internal/services/media_artifact.go:314-323`, `server/internal/services/media_artifact.go:339-375`）。
3. `writeSTRM` 对已有 artifact 仍重新调用 `SignArtifact(..., proxyDefaultTTL)`（`server/internal/services/media_artifact.go:788-799`）。
4. `SignArtifact` 把 `now + 30 days` 的 Unix 时间写入 `exp`，并把 `exp` 纳入 HMAC 和最终 URL（`server/internal/services/signed_proxy.go:27-32`, `server/internal/services/signed_proxy.go:137-158`, `server/internal/services/signed_proxy.go:381-386`）。两轮只要不在同一秒，STRM bytes 就不同。
5. `writeLocalArtifact` 对最终 bytes 做 SHA-256；只有 bytes 哈希与 manifest 的 `content_fingerprint` 完全相等才跳过，否则覆盖文件并对已有行返回 `updated`（`server/internal/services/media_artifact.go:811-867`）。

所以这不是 115 云端文件发生了变化，也不是 manifest 丢失，而是签名 URL 自身把“当前时间”带进了内容。截图中每轮约 76 个 `updated` 与库中约 76 个视频条目高度吻合；少量 `skipped` 很可能是内容确定的 NFO/JPG 等产物。现有测试只断言首次生成的 URL 不泄漏 provider ID/pickcode，并没有执行第二个 generation 来断言 unchanged STRM 被跳过（`server/internal/services/media_artifact_test.go:178-205`），因此该回归未被覆盖。

### 2. generation 频繁增长是独立的放大器，但不是“76 个更新”的直接原因

- reconciliation 的 catalog diff 本身会比较 provider ID/path/size/mtime 以及媒体投影字段，只有真实差异才增加 scan run 的 `updated`（`server/internal/services/media_library.go:1775-1827`, `server/internal/services/media_library.go:1930-1948`）。
- 但 scan 即使完全 no-op，也会推进 `dirty_generation` 并安排 artifact generation（`server/internal/services/media_library.go:1857-1912`）。这会产生大量历史记录和 O(N) artifact 检查。
- worker 每个 artifact 都单独 `SELECT`、`Stat`、`Save`（`server/internal/services/media_artifact.go:811-867`），因此频繁 no-op generation 仍有数据库和本地文件系统成本；签名时间 bug 又把这些本应为 skip 的检查升级成实际原子写盘。

建议把两层问题分开处理：先保证 artifact diff 正确（正常条目必须 skip），再对 no-op scan 做 schedule gate 或批量 manifest diff，减少历史噪声与 N+1 I/O。

### 3. 当前云端删除链路基本正确，应保留其安全边界

现有删除收敛路径为：

```text
完整且非 partial 的 115 枚举
  -> 删除本轮未见的 MediaLibraryEntry / recognition / source asset
  -> 生成当前 desired artifact 集
  -> generation 完成时将旧 run 的 active manifest 置为 false
  -> AutoCleanup 只删除 inactive + managed + allowlisted 的投影文件
```

证据：

- partial 枚举明确保留未见 Entry；只有完整结果才把 unseen Entry 计为 removed 并删除（`server/internal/services/media_library.go:1835-1855`）。
- 成功 artifact generation 在同一事务中停用其他 run 的 active manifest，再推进 applied generation（`server/internal/services/media_artifact.go:439-463`）。生成失败不会进入该分支，因此不会把旧 manifest 错误停用。
- 自动清理要求 run 已完成、policy/scan generation 匹配、scan 成功、非 partial、kind 允许、投影根未变，然后只选择 inactive managed manifest（`server/internal/services/strm_management.go:341-387`）。
- worker 在 artifact 完成后才执行 AutoCleanup；cleanup 失败会阻止媒体变更发布，但不回滚已经完成的 generation（`server/internal/services/media_artifact.go:470-491`）。
- full 与 incremental 的自动清理、重复执行幂等、partial/failed/superseded 保护已有测试（`server/internal/services/strm_management_test.go:382-450`）。

因此不应为了模仿外部插件而削弱 manifest ownership、partial 保护、root identity、symlink/reparse 或 per-file claim。需要修改的是“desired 与 existing 如何判定 unchanged”，不是删除安全模型。

### 4. MoviePilot P115StrmHelper 的可借鉴点

对照版本：DDSRem-Dev/MoviePilot-Plugins `P115StrmHelper 2.8.74`，commit [`574db20b03ec67d930a8753ca25c8695f3c3fe6f`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/tree/574db20b03ec67d930a8753ca25c8695f3c3fe6f)（commit time 2026-08-26T14:00:53Z）。

- 增量同步先分别生成本地树和网盘映射树，只遍历 `pan_to_local_tree - local_tree` 的行，然后才为缺失项生成 STRM；已存在路径不会重新写（[`increment.py:824-906`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/strm/increment.py#L824-L906)）。
- 新增项使用稳定 pickcode 构造 URL，内容没有每次运行生成的时间参数（[`strm.py:386-452`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/utils/strm.py#L386-L452)）；只有 tree diff 判定为缺失时才写入（[`increment.py:570-700`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/strm/increment.py#L570-L700)）。
- 全量模式也提供 `overwrite_mode == "never"`，已有 STRM 直接跳过（[`full/__init__.py:973-1005`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/strm/full/__init__.py#L973-L1005)）。
- 删除使用 `local - pan` tree diff；有生成失败、本地/云端任一树为空时不删。删除比例超过阈值时要求连续三个计数点通过稳定性检查后才删（[`increment.py:908-1000`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/strm/increment.py#L908-L1000)）。API 文档也明确推荐生成阶段缓存成功 STRM 路径集合，清理时复用该集合，避免重复云端扫描（[`API_STRM生成功能文档.md:415-492`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/docs/p115strmhelper/API_STRM%E7%94%9F%E6%88%90%E5%8A%9F%E8%83%BD%E6%96%87%E6%A1%A3.md#L415-L492), [`API_STRM生成功能文档.md:827-874`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/docs/p115strmhelper/API_STRM%E7%94%9F%E6%88%90%E5%8A%9F%E8%83%BD%E6%96%87%E6%A1%A3.md#L827-L874)）。
- 全量扫描的筛选计算用 Rust/Rayon 并行处理 batch（[`processor.rs:35-47`](https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/rust_utils/full_strm_sync/src/processor.rs#L35-L47)），本地写入另有有界 queue 和多 I/O worker。可借鉴其“批量枚举/一次 diff/有界并发写入”，不应照搬 pickcode 明文 URL。

MoviePilot 的核心语义正是用户描述的：云端新增 -> 写入；本地已有且云端未变 -> 跳过；云端缺失 -> 删除候选。但 OhMyCine 的 signed proxy、ownership manifest 和 partial-scan 安全模型更严格，应该保留并在其上实现同样的 diff 语义。

### 5. 期望 diff 算法

建议每轮先形成一个完整 `desired` 映射，并一次加载当前 manifest，而不是逐项查询：

```text
desired key = (target_kind, normalized relative_path)
desired value = kind, source identity, provider binding, deterministic render identity
existing key = current managed manifest 的同一唯一键
```

分类：

1. `desired` 有、`existing` 无，或 manifest 有但目标文件缺失：`written`。
2. 两者都有，目标存在，确定性 render identity 相同：`skipped`；不得改写文件 bytes/mtime。允许批量更新 run ownership/active/provider binding，但这类数据库重绑不应计成“文件更新”。
3. 两者都有，但 NFO/JPG/source asset 的确定性内容指纹变化，或 STRM 的 gateway origin/key/format 必须变化：`updated`。
4. `existing` 有、`desired` 无：先置 inactive；只有 authoritative complete non-partial run 成功后才进入 cleanup，最终计 `removed`。
5. partial/failed/superseded：只允许写入已确认新增/更新，不得把 unseen 视为删除。

对 STRM，`ProviderItemID` 变化不必导致文件改写：STRM 内容指向稳定 `OpaqueID`，resolver 在播放时从 active manifest 读取当前 provider binding。只要 gateway capability 本身没变，更新 manifest binding 后文件仍然有效。移动/重命名导致 relative path 改变时，应在新路径写入、旧路径进入受控 cleanup。

### 6. signed STRM 的修复选择

**推荐方案：增加稳定的 artifact-scoped signed URL v2。**

- STRM 持久内容使用 `opaque + library_id + kid + scope/version` 的 HMAC，不包含生成时刻；请求时仍必须验证 signature、key 状态以及 `MediaArtifact` 为当前 `managed + active + completed + strm + local_projection`。
- provider ID、pickcode、Cookie 和临时播放 URL仍不进入 STRM；删除/停用 artifact 会立即使 URL失效，key rotation 可整体撤销。
- 旧的含 `exp` v1 URL继续验证，首次遇到需要更新的旧 artifact 时惰性迁移；数据库 migration 不批量重写用户投影。
- `content_fingerprint` 继续表示真实 bytes SHA-256；同一 v2 URL跨 generation 完全稳定，现有 skip 判断即可成立。

这个方案最接近 MoviePilot 的稳定 URL，但仍保留 OhMyCine 的 opaque capability 和实时 active-manifest 撤销。风险是泄漏后的 token 生命周期从最多 30 天变为“artifact 停用或 signing key 退役前”；这是持久 STRM 本身的能力模型，需在安全设计中明确并以 key rotation、library deletion 和 manifest active 状态作为撤销机制。

**较小改动备选：复用现有有效 v1 URL并只在临近到期时续签。**

- 对已有 managed STRM 先严格读取/解析本地内容，验证 origin、opaque、library、signature 与 active manifest；剩余寿命高于 renewal window 时直接 skip，不再调用 `SignArtifact`。
- 到期前才续签并计一次 updated；需要独立、可靠的续签调度，否则一个长期没有扫描的库会在 30 天后全部不可播放。
- 该方案不会每轮更新，但仍会周期性批量改写，而且当前 `VerifyArtifactURL` 只返回 opaque/library，不暴露 expiry（`server/internal/services/signed_proxy.go:177-209`），需要增加内部 inspect 结果或持久 `content_expires_at`。

不建议只把 `content_fingerprint` 改成 provider/path 的“语义哈希”但仍每轮生成新 URL：那会让数据库声称 unchanged，却可能写入不同 bytes，破坏 manifest 与磁盘一致性。也不建议直接照搬 MoviePilot 的 `?pickcode=` 内容，因为这违反现有 provider identity 不落 STRM 的契约。

### 7. no-op generation 与性能优化

在 correctness 修复后，可进一步减少 run 数量和 I/O：

- scan transaction 计算 `catalog_changed`（added/updated/removed/metadata）、`artifact_policy_changed`、`managed_target_missing`、`signature_renewal_due`。只有至少一个为真，或存在等待该 generation 的 pending media change，才安排 artifact generation。
- 用户显式“立即增量/全量”可保留一个 verification run，但 unchanged 应全部计 skipped；周期 no-op 不需要生成 artifact history。
- 一次加载 `media_artifacts WHERE library_id=? AND target_kind=?` 构建 map；一次形成 desired map；批量更新 unchanged manifest 的 run/active/provider binding。文件写入保持有界并发，SQLite 状态提交保持短事务。
- 不应简单要求 `artifact_applied_generation == dirty_generation`；现有 media-change 逻辑允许无 change row 的新 generation承接旧 pending change（`.trellis/spec/backend/media-library-foundation.md:429-434`）。schedule gate 必须显式处理 pending change barrier。

### 8. 迁移与兼容风险

- 稳定 v2 URL若不新增字段，可复用现有 `OpaqueID`、`ContentFingerprint`、key 表和 active manifest，无 schema migration；若新增 `render_fingerprint/format_version/content_expires_at`，应使用下一版 additive migration（当前 migration 列表到 v53：`server/internal/database/migrations.go:87-88`），默认空值并惰性回填，升级时不得触发全库重写/删除。
- 旧 v1 URL必须继续可播至其原到期时间；不能在部署升级时立即 retire 旧 signing key。
- `publicOrigin`、签名格式或 active key的变化必须被视为 gateway capability变化；否则已有 STRM可能指向旧 origin或已退役 key。若采用 v2，应明确 previous-key 的可用期及再签策略。
- manifest 的唯一键目前是 `(library_id,target_kind,relative_path)`（`server/internal/models/models.go:753-771`）。批量 diff 必须保持该唯一性，并保留 unmanaged collision 直接 skip 的行为。
- 不能在 partial scan 中用 desired 集合停用 unseen manifest；否则 115 API partial result 会被误判为云端删除。
- 如果 no-op scan 不再安排 artifact generation，管理 UI 的“扫描 generation”和“artifact applied generation”可能长期不同；DTO/文案应说明两者分别是 source baseline 与最后实际投影版本，或引入独立 artifact revision。

### 9. 测试建议

至少新增以下回归：

1. 两个相隔数秒的 complete generations，源 Entry完全相同：第二轮 `written=0, updated=0, skipped=N, removed=0`，STRM bytes与mtime不变。
2. 同路径 provider binding 改变但 `OpaqueID`保留：manifest解析到新 provider item，STRM bytes不变；统计不冒充文件更新。
3. 新增一个云端文件：只写一个 STRM；已有条目全 skip。
4. complete scan删除一个云端文件：只停用并删除对应 managed STRM；同名 unmanaged 文件、NFO/JPG ownership边界和其他条目不变。
5. rename/move：新相对路径写入、旧路径清理，provider ID稳定；partial枚举时旧路径不删。
6. full/incremental no-op、生活事件重复/回放与周期扫描：不产生重复内容更新；若引入 schedule gate，周期 no-op不产生 artifact run。
7. v1 -> v2兼容：旧未过期 URL仍解析；惰性迁移只在受控更新发生；旧 key active/previous行为正确。
8. 签名安全：篡改 opaque/library/kid/signature失败；inactive、unmanaged、wrong kind/target、已删除 artifact失败；响应与日志仍无 provider ID/pickcode/Cookie。
9. public origin/key rotation：仅 gateway capability真正变化的条目更新一次，后续再次运行全部 skip。
10. 失败与崩溃恢复：写盘失败不停用旧 manifest；cleanup claim重放幂等；partial/failed/superseded不删。
11. 批量性能：数百/数千 manifest 的 SQL次数不随文件数线性执行 `SELECT + Save`；峰值内存、worker并发和 SQLite锁时间有界。

## Files Found

- `server/internal/services/media_library.go` — 115/本地 reconciliation、generation推进、Entry diff、完整扫描删除和 artifact scheduling。
- `server/internal/services/media_artifact.go` — desired产物枚举、STRM/NFO/source asset写入、bytes指纹、manifest active切换。
- `server/internal/services/signed_proxy.go` — signed STRM URL格式、30天TTL、HMAC验证和 active artifact解析。
- `server/internal/services/strm_management.go` — inactive manifest清理计划、generation/root/ownership guard和自动清理状态机。
- `server/internal/models/models.go` — MediaLibrary、MediaLibraryEntry、MediaArtifactRun、MediaArtifact持久模型与唯一键。
- `server/internal/services/media_artifact_test.go` — 首次生成、安全内容与coalescing测试；缺少跨generation unchanged回归。
- `server/internal/services/strm_management_test.go` — full/incremental cleanup、partial/failed/superseded保护和幂等测试。
- `server/internal/database/migrations.go` — 当前显式migration版本到v53，artifact/proxy最初由v27-v29引入。
- `.trellis/spec/backend/media-library-foundation.md` — 媒体库、signed STRM、manifest ownership、partial与cleanup的执行契约。

## External References

- DDSRem-Dev/MoviePilot-Plugins, P115StrmHelper 2.8.74, commit `574db20b03ec67d930a8753ca25c8695f3c3fe6f`。
- 增量 tree diff：`plugins.v2/p115strmhelper/helper/strm/increment.py`。
- 稳定 STRM URL：`plugins.v2/p115strmhelper/utils/strm.py`。
- 全量 skip/批量处理/删除保护：`plugins.v2/p115strmhelper/helper/strm/full/__init__.py` 与 Rust `processor.rs`。
- STRM生成/删除UUID缓存API：`docs/p115strmhelper/API_STRM生成功能文档.md`。

## Related Specs

- `.trellis/spec/backend/index.md` — backend pre-development与quality gate。
- `.trellis/spec/backend/media-library-foundation.md:305-384` — Metadata Snapshot and Managed Media Artifacts。
- `.trellis/spec/backend/media-library-foundation.md:336-350` — opaque signed URL、immutable policy、active manifest与cleanup契约。
- `.trellis/spec/backend/media-library-foundation.md:429-447` — no-op generation、pending media change与partial发布边界。
- `docs/architecture/02-server-design.md` — 115媒体库、生活事件、STRM/302与Notify总体数据流。

## Caveats / Not Found

- 未对用户运行中的SQLite做取证，也未读取Cookie、provider ID、STRM正文或绝对投影路径；“76个updated对应76个视频Entry”是截图统计与代码因果链的一致性判断，不是运行库数据导出。
- MoviePilot插件没有OhMyCine同等的opaque signed capability、active manifest和root ownership模型；其path-tree方案用于语义对照，不能原样复制。
- 本研究未修改产品代码，也未运行修复后的benchmark；批量manifest diff的具体并发数应在实现阶段以Windows本地SQLite和真实规模fixture测量。

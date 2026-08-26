# Technical Design

## Boundaries

本任务只改 Server：媒体库 reconcile 持久化、AI Provider URL 规范化、对应测试/日志及发布配置。不会修改 Player 或扩大 AI Provider 的网络权限。

## Media-library transaction design

### Reproduction first

新增跨层回归 fixture，模拟本地下载暂存目录完成 10 集 TV Transfer 后触发媒体库 reconcile。识别器使用确定性 fixture，避免测试依赖 TMDB 网络；数据库使用真实 SQLite migration 链和外键约束。

测试必须证明：枚举 10 个文件、归并为一个识别单元、保存一条 recognition、保存并关联 10 条 entries、更新 scan run/library generation，并在需要时写入一条 change outbox。

### Stage-aware commit

事务逻辑保持一个原子边界，但每个数据库阶段通过轻量 helper 包装内部错误：

1. `configuration_revalidate`
2. `load_existing_entries`
3. `persist_source_assets`
4. `persist_recognition`
5. `persist_entries`
6. `prune_stale_entries`
7. `advance_library_generation`
8. `persist_scan_run`
9. `record_media_change`

包装只携带内部 stage 和原始 cause，不改变 `errors.Is`/GORM 判断能力。事务外统一把 cause 分类为有限枚举；API 仍返回稳定 app error，不把原始数据库错误序列化给客户端。

### Root-cause correction

先用回归 fixture 重现，再根据失败 stage 修复实际 schema/query/model 问题。预期重点核查：recognition 外键、entry 唯一索引、change revision 唯一约束、generation 二次校验以及旧 migration 数据库的列/索引兼容性。

不得用以下方式“修复”：禁用外键、忽略 `Save` 错误、跳过 change outbox、把事务拆成可部分提交的多段、遇到唯一冲突就盲目删除用户数据。

## OpenAI-compatible URL design

将 Base URL 视为“API 根前缀”，而不是只允许 origin 或固定 `/v1`。规范化过程继续执行现有 scheme/host/port/userinfo/query/fragment/IP 校验，并额外保证 path：

- 使用规范化 escaped path；拒绝反斜杠、点段、重复分隔等歧义形式。
- 允许有界的安全 API path segments，例如 `/v1`、`/api/v1`。
- endpoint helper 只在 Base URL 尚未包含请求所需的版本尾段时追加 `/v1`；已以 `/v1` 结束的前缀直接追加资源路径。

示例：

```text
https://api.openai.com/v1 + /v1/models
  -> https://api.openai.com/v1/models

https://openrouter.ai/api/v1 + /v1/chat/completions
  -> https://openrouter.ai/api/v1/chat/completions
```

使用 mock transport 验证 models、probe 和 structured generation 的最终 URL 与 Authorization redaction，不对用户公开的真实 Key 发出请求。

## Compatibility

- 保留空 path 与 `/v1` 的现有行为。
- Google AI Studio 使用固定官方 Base URL，不受此修改影响。
- 不新增数据库破坏性 migration；若根因证明确需 additive migration，必须有旧版本升级与幂等测试。
- 现有错误码和响应 envelope 保持兼容，新增诊断字段只进入经过脱敏的 Server runtime log。

## Operations and rollback

- 修复先在隔离 SQLite 测试库验证，不操作用户本地数据库或媒体目录。
- 发布前运行 `server/test.ps1` 与组件级测试，确认无残留进程。
- 若 Beta 构建失败，不移动 tag 到其他 commit；修复后从最新 `origin/develop` 创建新的 Beta tag。
- 回滚代码不会删除已成功入库的数据；本任务不执行数据清理。

## Security considerations

- 自定义 AI Base URL 仍只允许公共 HTTPS/443，并经安全 dialer 检查全部解析 IP。
- 禁止环境代理和重定向的现有策略保持不变。
- 运行日志不记录原始数据库错误文本，因为其中可能包含 SQL、路径或媒体名称。
- 不读取、不显示、不测试用户曾粘贴的 API Key；提醒用户轮换。

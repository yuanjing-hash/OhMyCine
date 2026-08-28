# 技术设计

## 读模型和页面结构

以现有 `MediaCatalogService` 为唯一作品扫描事实源。扩展 catalog projection，补充安全同源海报/背景、简介、原名、recognition token/revision 和各库覆盖；新增后端 aggregate catalog endpoint 负责跨库稳定身份去重、筛选与分页。前端新增独立 `/discovery/library` 列表/详情路由，并将推荐与 catalog 共用的 hero、季集覆盖抽成展示组件，各自通过 adapter 输出规范化 DTO。

作品身份优先使用 `(media_type, tmdb_id)`；无 TMDB 匹配时退回稳定 work identity，并保留 per-library work token。聚合项永远携带各库投影，删除动作必须选择其中一个具体 library work，不能将聚合身份当成删除授权。

## 元数据动作

新增作品级 action resolver：服务端以 `(library_id, work_token)` 解析当前 entries、recognition token 和 revision，再委托现有 retry/candidates/override/clear 服务。普通重新刮削只更新识别和展示 artifact；需要移动文件时显式进入现有 reorganization preview/confirm 工作流。

## 破坏性删除

从 transfer deletion 抽取共享的短期确认令牌、root/provider validator、逐项 durable checkpoint 和安全审计原语，新增：

```text
POST /api/v1/media-libraries/:id/catalog/:work/deletion-preview
POST /api/v1/media-libraries/:id/catalog/:work/deletion-confirm
```

Preview 保存不可变的 entry/provider identity 快照和 work revision。Confirm 先重新对账，再创建持久删除执行记录；provider 调用在 DB 长事务外执行，每项完成后短事务 checkpoint，全部收敛后停用 catalog/managed facts、标记 library dirty 并调度 STRM/媒体服务器刷新。

本地路径从配置 Storage root + library relative root + entry relative path 重算，拒绝越界和 Reparse Point。115 先证明 library provider root 位于 Storage root，再证明每项仍在 library root 内，只调用对应连接的 `Recycle`/有界批量 recycle；missing 可幂等收敛，changed/ambiguous 失败关闭。

## 权限、兼容与回滚

- 新增专用 `media_libraries.media_delete` RBAC；浏览/元数据动作沿用并细化现有 read/update 权限。
- 新 API additive，不改变现有配置页和 catalog endpoint 语义。
- 新页面可独立关闭导航；删除能力可在 service 注册或 provider capability 不满足时返回受控阻断，不影响只读浏览。
- OpenAPI、权限 catalog、generated frontend permission 常量和安全文档同步更新。

## Evidence

- `../08-28-optimize-115-pipeline-library/research/web-detail-and-library.md`
- `.trellis/spec/backend/media-library-foundation.md`
- `.trellis/spec/backend/security-guidelines.md`
- `.trellis/spec/frontend/server-admin-ui.md`
- `.trellis/spec/guides/cross-layer-thinking-guide.md`

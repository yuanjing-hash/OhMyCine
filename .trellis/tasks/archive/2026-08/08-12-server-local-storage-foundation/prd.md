# Server 本地 Storage 与路径安全

## Goal

实现可在管理端注册、探测和管理的本地 Storage，并安全接入用户指定但不进入 Git 的本机媒体根，但不自动创建媒体库、不扫描媒体、不执行文件写操作。

## Requirements

1. 新增显式迁移、`Storage` 模型和 `storages` 表；首版只允许 `type=local`，未来保留 nullable Connection 引用和 capability snapshot。
2. 新增稳定 permission codes：`storages.read/create/update/delete/test`，同步 catalog、系统角色、API middleware 和 UI 控件。
3. API：`GET/POST /api/v1/storages`、`PATCH/DELETE /api/v1/storages/:id`、`POST /api/v1/storages/:id/test`。
4. 名称规范化且唯一；根路径必须是存在的绝对目录，支持 Windows 盘符和 UNC，拒绝相对路径、文件、缺失目录、根 Reparse Point 和边界逃逸。
5. 探测只执行安全枚举和磁盘容量查询，返回 exists/readable/available/free_bytes/total_bytes/last_checked_at/error_code 等受控摘要。
6. local driver 返回明确能力：非网盘、不支持原生离线下载/临时直链/signed 302/change cursor，支持本地目录枚举；watch 能力按当前 Windows 实现和运行环境探测。不得让用户手工打开不支持的能力。
7. 列表仅向有权限的管理用户显示配置所需根路径；仪表盘/普通日志/审计 metadata 不输出绝对路径或子文件名。
8. 创建、更新、测试、删除均审计；删除只删配置。未来有引用时必须由 FK/service 拒绝。
9. 管理端增加真实“存储”页面/标签：列表、添加、编辑、测试、删除确认、状态、容量和只读能力摘要；Connection/Destination 保持规划状态。
10. 真实路径不进入代码、seed、fixture；用户运行 Server 后通过 UI/API创建名为 `115 下载盘` 的 Storage。
11. 更新架构和路线图，明确 Storage 与 StorageDestination 的职责差异。

## Acceptance Criteria

- [ ] Windows 管理端可添加 `115 下载盘 -> <本机运行时媒体根>` 并显示在线、可读及容量摘要。
- [ ] Server 重启后 Storage 仍存在；创建 Storage 不扫描目录、不生成媒体记录。
- [ ] 路径错误、权限错误和 Reparse Point 有安全错误码与回归测试，不返回原始内部错误。
- [ ] 删除 Storage 后真实目录、2 个子目录和 4 个 MP4 完全不变。
- [ ] RBAC、审计、API envelope、前端 typecheck/lint/test/build、Go test/vet/build、隔离健康检查通过。

## Out of Scope

- MediaLibrary、扫描、刮削、分类 Profile。
- Connection、StorageDestination、CategoryRule。
- 任何真实文件写入、移动或删除。

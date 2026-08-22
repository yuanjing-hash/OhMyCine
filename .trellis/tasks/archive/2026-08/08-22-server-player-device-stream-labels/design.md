# Design — Player 设备管理与 Server 播放来源标识

## Architecture and boundaries

### Browser device management

保留现有 Player Bearer 路由：

```text
Player → DeviceAuth → /api/v1/player/devices → AuthService
```

新增浏览器管理路由：

```text
Server Web UI → Cookie Session + CSRF + permission → /api/v1/player-devices → AuthService
```

两条路由共用 `playerDeviceDTO`、`PlayerDevices` 和 `RevokePlayerDevice`，避免出现两套列表/撤销行为。管理端 GET 使用 `connections.read`，DELETE 使用 `connections.update`。`AuthService` 继续按当前 actor 用户 ID 查询和撤销，因此即使拥有连接权限也不能操作其它账号的设备。

所有设备响应保持 `Cache-Control: no-store`。DELETE 进入现有浏览器 mutation protection、Cookie Auth 和 CSRF 中间件；Player Bearer 不被浏览器 Auth 接受。

### Device UI state

`PlayersView.vue` 增加独立于 Emby connections 的状态：

- `devices`, `devicesLoading`, `devicesFailed`
- 单设备撤销 busy 状态
- 初次加载与手动刷新调用管理端 GET
- 撤销确认后调用 DELETE，成功重新加载设备列表

设备卡只展示 DTO 安全字段。最近活动是时间事实，不推断“在线”；过期时间使用绝对时间显示，避免伪造实时状态。Emby 加载失败与设备加载失败彼此隔离。

## Media delivery contract

`PlayerMediaVersion` 增加：

```text
delivery_kind: server_stream | server_redirect
```

赋值位置与现有 playability 判定在同一个 Server service 分支：

- local 普通文件验证成功 → `playable=true`, `delivery_kind=server_stream`
- pan115 active managed completed STRM artifact 存在 → `playable=true`, `delivery_kind=server_redirect`
- 不可播放 → `playable=false`, delivery kind 省略

该字段描述 Player 从 OhMyCine Server 获得媒体的 HTTP 交付方式，不描述 Server 内部是否借助 STRM artifact 建立映射。这样 115 可以继续以 artifact 作为安全播放前提，但 Player UI 不会误称自己在播放 STRM 文件。

## Player mapping and presentation

`ServerDataSource` 将 `delivery_kind` 解析为可选枚举，并为自己的 `MediaSourceOption` 设置：

- `sourceLabel`: 当前 Server 数据源显示名称
- `deliveryKind`: `server_stream | server_redirect`
- `isRemote: true`
- 不设置 `isStrm`

通用媒体详情展示顺序为：容器、大小、来源、交付方式，再在没有精确来源时显示现有 `STRM`/`远程` 标签。旧 Server 没有 `delivery_kind` 时只显示 `来自 <Server 名称>`。

新增字段保持可选，现有 Local/Emby/Jellyfin/OpenList 等 DataSource 不需要一次性迁移。真实 Emby STRM 仍通过既有 `isStrm` 显示。

## Compatibility

- 新 Server + 新 Player：显示准确来源和交付方式。
- 旧 Server + 新 Player：来源准确，交付方式省略，不再误标 STRM。
- 新 Server + 旧 Player：旧 Player 忽略新增 JSON 字段，但仍可能保留旧显示 bug；播放不受影响。
- 管理端接口为新增路由，不改变 Player Bearer API。

## Security and privacy

- 不把设备 token/hash、原始 device ID、IP、User-Agent 或绝对路径加入任何 DTO。
- 不把 115、artifact、签名地址或 CDN 地址作为展示字段。
- 撤销复用已有审计事件，设备名称之外不写敏感元数据。
- 管理端撤销不会删除 Player 本地数据，只使 device token 失效。

## Rollback

- 管理端路由/UI 可以独立撤回，不影响 Player Bearer 配对与媒体播放。
- `delivery_kind` 是向后兼容的可选字段，可保留在 Server DTO；Player 展示层可以单独回滚。
- 不涉及数据库迁移。

## Commit and release

代码、测试、契约文档和任务工件通过检查后提交到 `develop` 并推送。发布前重新 fetch 远端引用，确认本地提交已经成为最新 `origin/develop`。

Player 使用既有 Beta workflow 发布 `v1.1.11`，发布输入只能选择 `develop`/beta，并以远端 develop tip 为构建源。Server 代码参与普通 develop CI，但不创建 Server tag 或 Release。若 Player workflow 失败，保留已经推送的 develop 修复，诊断并重跑同一版本流程；不得把失败 tag 移到其它提交或改从功能分支发布。

## Risks

- 不能把管理端路由错误注册到 Player Bearer group，否则浏览器仍无法访问。
- 不能给设备状态标记“在线”，因为当前只有有界频率更新的 `last_seen_at`，没有实时心跳。
- 不能仅删除 `isStrm` 而继续显示泛化的“远程”，否则没有实现用户要求的“来自 Server”。
- 发布动作必须在远端 develop tip 校验和全量质量门禁之后执行，不能为了发版跳过 Server Web UI 或 Player DataSource 验证。

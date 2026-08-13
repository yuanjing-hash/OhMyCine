# Server 跨平台目录选择器

## Goal

在 Server 管理端提供跨平台、只读、受权限保护的目录树选择器，让用户无需登录 Server 主机或手工复制绝对路径，即可为本地 Storage 选择 Server 实际可访问的目录。选择器必须明确浏览的是 Server 文件系统，而不是打开管理端浏览器所在设备的文件系统。

## Background

- 当前 Storage 创建/编辑页使用自由文本输入根路径，后端已具备绝对路径、Windows/UNC、Reparse Point、只读探测和稳定错误校验。
- 浏览器原生 `<input type="file">` / File System Access API 只能访问浏览器所在设备，无法满足远程管理 Server 的语义。
- 目录选择应由 Server API 枚举 Server 进程可见的本地目录。Windows 需要识别盘符和当前进程可见的已映射/网络位置；Linux/NAS/Docker 需要从 Server 可见的文件系统根/挂载点进入。
- 任意目录枚举会暴露主机结构，必须与普通 Storage 读取权限分离，并保持 API/service 双重授权、限量、脱敏日志与 Reparse Point/symlink 防逃逸。

## Requirements

1. Storage 创建和编辑表单不再把自由文本路径作为主流程，改为“选择目录”控件；已选结果以只读路径摘要和面包屑展示，用户不能通过修改 DOM 提交未经服务端重新验证的路径。
2. 点击后打开 Server 目录树弹窗：展示当前位置、面包屑、返回上级、当前层文件夹列表、加载/空/无权限/失效状态，以及“选择当前目录”。只展示目录，不返回普通文件、文件内容、ACL、所有者或任意子树统计。
3. 目录按层懒加载，每次请求只枚举一个目录；服务端设置最大条目数、排序、超时/取消和稳定错误。不得递归扫描整个盘或挂载点。
4. Windows 根级展示 Server 进程可见的固定盘、可移动盘和已映射网络盘/共享；目录树支持盘符与 UNC。Linux/NAS/Docker 根级展示 Server 实际可见的根/挂载点；不得假装展示宿主机未挂载目录。
5. symlink、junction、mount-point Reparse Point 等跳转项默认不允许进入或选择，并以不可用原因标识；选择结果仍调用现有 Storage `CanonicalizeRoot` / probe 校验，浏览成功不等于注册必然成功。
6. 新增独立敏感权限 `storages.browse`。根列表和目录枚举 API 同时要求登录、`storages.browse` 和服务层 policy；`storages.create/update` 仍分别保护最终保存。Owner/administrator/operator 默认获得 browse，viewer 不获得。
7. API、普通日志和审计不得记录被浏览/选择的绝对路径或子目录名称；仅允许 request ID、actor、结果、稳定错误、平台和条目数等脱敏信息。响应只返回当前交互所需的路径/名称，并使用 `Cache-Control: no-store`。
8. 目录列表项至少包含 opaque selection token 或服务端重新校验所需的受控路径、显示名称、是否可进入/选择和不可用原因。客户端不得自行拼接 `..`、分隔符或盘符形成下一次请求。
9. 选择器可复用于后续 cloud MediaLibrary 的本地 STRM 输出目录、staging 和其它 Server 本地目录字段，但本任务只接入现有本地 Storage 创建/编辑页面。
10. 不提供创建目录、改名、移动、删除、上传、下载或文件预览；不自动注册 Storage，不扫描媒体。
11. 具备 `storages.browse` 的授权管理员默认可从 Server 进程可见的全部盘符/挂载点开始浏览，不要求运维预先配置 allowlist。系统保护目录、不可读目录和 symlink/junction/Reparse Point 仍由 adapter/policy 标记为不可进入或不返回；容器只暴露容器内实际挂载可见的文件系统。

## Acceptance Criteria

- [ ] 从另一台设备访问 Web 管理端时，选择器显示 Server 的目录树而不是客户端设备目录。
- [ ] Windows 可从根位置选择可见盘符并逐层进入目录；Linux/NAS/Docker fake/CI adapter 可验证挂载点语义和平台分隔符。
- [ ] 创建与编辑 Storage 都只能通过选择器更新根路径；保存时后端再次执行现有路径安全和只读探测。
- [ ] 每次枚举只读取当前层目录且不返回文件；大量目录、不可读目录、路径消失、取消和超时都有受控状态。
- [ ] symlink/junction/Reparse Point 无法进入或选择；伪造 token/path、`..`、跨盘/共享跳转和过期选择均被拒绝。
- [ ] 只有具备 `storages.browse` 的用户可以请求根和目录列表；五个现有 Storage CRUD/test 权限不被弱化。
- [ ] 授权管理员首次使用无需配置 allowlist，即可看到 Server 可见盘符/挂载点；未挂载的宿主机/NAS 路径不会被伪造展示。
- [ ] 日志、审计和错误响应不含绝对路径、子目录名或原始 OS 错误。
- [ ] `server/test.ps1`、Web UI typecheck/lint/test/build、Go test/vet/build、权限漂移和 Windows 真实目录选择 smoke 全部通过。

## Out of Scope

- 浏览 cloud provider 内部目录；未来由对应 Storage driver 使用另一套 provider-relative picker。
- 主机文件管理、目录创建、上传、移动、重命名、删除和权限修改。
- 自动发现未映射、未挂载、Server 进程不可见的 NAS/UNC 共享。
- MediaLibrary、STRM、staging 页面接入；只保留可复用组件/API边界。

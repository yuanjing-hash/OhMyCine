# Server STRM 与 signed 302 媒体库投影

## Goal

让具备稳定文件身份和临时直链能力的 cloud Storage 媒体库可选择启用 signed 302 与 STRM 投影；local Storage 不显示也不需要这些选项。

## Requirements

1. Storage driver capability 决定 MediaLibrary 是否可启用 signed proxy/STRM，禁止用户绕过能力检查。
2. `signed_proxy_enabled` 先于 `strm_enabled`；只有 cloud source 可以启用 STRM。启用时必须通过本地目录选择器直接选择该库本地 STRM 目录，后端将其作为 managed output root；无需预注册第二个 local Storage。缺少、相对/文件路径、Reparse Point、不可写或边界校验失败时拒绝保存/启动。STRM 内容只写 Server 生成的短标识/签名代理 URL，不含上游 token URL。
3. 签名覆盖 method、normalized library/file identity、exp 和可选 scope，拒绝过期、篡改、遍历与双重编码。
4. 302 请求每次仍验证外层签名；上游临时 URL 缓存 TTL 不超过真实过期时间，日志完全脱敏。
5. STRM 投影以 MediaLibrary 的持久文件树为唯一输入。首次/周期全量从当前树收敛；增量仅消费前后 generation diff，对 create/update/move/delete 做对应生成、替换、移动或受控清理。
6. 视频扩展名固定覆盖 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v`，在用户为该媒体库选择的受控本地 STRM 目录保留远端目录结构并把视频扩展名替换为 `.strm`。
7. 与媒体相关的 `srt,ssa,ass,jpg` 文件下载到相同相对目录和文件名。下载受 provider 限速、大小上限、临时文件 + 原子替换、路径边界和凭据脱敏保护；不得借伴随文件同步下载其它扩展名。
8. STRM/伴随文件同步属于对应 MediaLibrary supervisor 的 reconciliation，不进入全局持久任务队列，也不阻塞其它媒体库监听。失败保留可恢复状态并由该库下一次 reconciliation 重试。
9. 未启用 STRM 时不要求或保存本地 STRM 目录，不创建本地 projection root，不生成 `.strm`，也不下载伴随文件；媒体索引和对远端 Storage 的扫描/管理继续运行。关闭已启用的 STRM 时先提供受控差异预览，再只清理该媒体库 STRM 目录内由 Server 管理的投影文件。
10. local source 不生成 STRM。cloud Storage 的本地 mount 与本地 STRM 目录语义分离：mount 可用于刮削产物上传、transfer/import 或 driver 访问，但不能因存在 mount 而创建投影。

## Acceptance Criteria

- [ ] local 媒体库不显示 STRM/302 开关。
- [ ] cloud 媒体库勾选 STRM 后必须直接选择有效本地目录；无需第二个 local Storage，缺失、相对/文件路径、Reparse Point、不可写和路径越界均拒绝。未勾选时不显示/要求该目录。
- [ ] 模拟 115 capability 的 cloud 库可开启并生成 STRM，合法请求 302、非法/过期签名拒绝。
- [ ] API、日志、WebSocket、STRM 内容均不泄露上游 token URL 或凭据。
- [ ] fake cloud 文件树中的全部 17 种视频扩展名都生成同结构 `.strm`，`srt/ssa/ass/jpg` 同结构下载，其它扩展名不落地。
- [ ] 文件树新增、更新、移动和删除只改变对应 diff；全量重跑幂等，两个媒体库的同步可同时进行。
- [ ] STRM 关闭的 cloud 库没有本地 projection 产物，但仍正常维护远端媒体索引。
- [ ] fake cloud mount 的存在不会让未启用 STRM 的媒体库生成 `.strm`、字幕或图片；挂载写入仅由明确的上传/入库流程触发。

# Design: Library STRM and Signed Redirect Projection

STRM 是 MediaLibrary 的播放投影，不是 Storage 自身文件。Storage 提供 list/identity/direct-url capability，MediaLibrary 决定是否启用，proxy service 负责 HMAC 签名、缓存和 302。持久化只保存 storage/library/file opaque identity；请求时由 driver 解析临时直链。

`ProjectionReconciler` 由对应 `LibrarySupervisor` 调用，输入为 `FileTreeSnapshotDiff`，不创建 Queue Job。全量把文件树映射为期望 projection manifest 并收敛；增量将 create/update/move/delete 映射为最小文件操作。projection root 必须来自 MediaLibrary 显式保存并重新校验的 `strm_local_root`，不能从 source Storage 或其 mount 猜测。不同 library 使用独立受控 root 和锁，可同时执行；单库内按 generation 顺序串行。

视频映射集合为 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v`，输出同相对目录的 `.strm`。伴随集合仅为 `srt,ssa,ass,jpg`，保留原扩展名并通过受限 driver download 写入临时文件后原子替换。manifest 记录 provider identity、source generation、local relative path 和 Server-managed 标志，用于幂等更新和边界内清理。

`strm_enabled=false` 时本地 STRM 目录为空，reconciler 是零落地操作；不会为了索引或因为 cloud mount 存在而创建 projection root。local source 同样永远是零投影。关闭已有投影时只依据 manifest 和受控 root 生成清理预览，不跟随 symlink/reparse point，也不触碰用户自行放入的未管理文件。

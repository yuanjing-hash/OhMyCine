# Implementation Plan

1. 定义 capability gate 和 MediaLibrary proxy/STRM 条件配置：仅 cloud source 可开启，开启后安全校验的本地 `strm_local_root` 必填；无需第二个 local Storage，mount 不作为隐式输出目录。
2. 实现 HMAC signer、key rotation/renewal contract 和签名测试。
3. 实现 direct URL resolver/cache 与脱敏日志。
4. 实现文件树全量 manifest 收敛与 generation diff 增量 reconciler，接入每库 supervisor 而非持久任务队列。
5. 实现 17 种视频扩展名到 `.strm` 的同结构映射，以及 `srt/ssa/ass/jpg` 限速下载、原子写入和受控清理。
6. 实现 STRM 关闭/local source 时零落地、cloud mount 不触发投影、关闭已有投影的 preview/cleanup，以及并行库和幂等恢复测试。
7. 接入媒体库 UI、状态与 signed proxy 安全测试。

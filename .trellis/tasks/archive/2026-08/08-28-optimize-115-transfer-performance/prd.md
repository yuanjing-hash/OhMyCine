# 优化115整理转移性能

## Goal

把 115 同账号媒体整理从逐文件、逐祖先远程校验改为按共享来源树和目标目录批量验证/变更，在保留幂等、断点恢复和删除边界的前提下消除分钟级请求放大。

## Background

- 用户看到一个整理任务耗时十几分钟。运行证据显示该任务实际包含 28 个选中媒体文件和 10 个清理项；move 阶段耗时约 20 分 42 秒，清理再耗时约 175 秒。
- 主要放大来自每文件重复 `Stat` 整条祖先链、逐文件 Move/Stat/Rename、逐文件清理；健康 list/move/rename/recycle lane 均有 2 秒节流且 worker 串行。
- 本次任务未出现 405/429 风控重试，因此不能把固定慢速归因于实际风控。

## Requirements

- 115 来源边界只对 immutable package root 和唯一父目录证明一次，并复用同一次尝试内的受控树/目录缓存。
- 目标目录 DAG、存在性检查、冲突检查和操作后核对复用目标父目录 listing，不重复逐文件查询。
- 为 115 提供可选批量 mutation 能力，按目标父目录和有界 chunk 执行 move/copy/recycle；不改变其它驱动接口的最低能力。
- 批量调用前持久化私有 intent，调用后按稳定 ID、父目录、大小和可用 SHA1 对每项收敛；崩溃重试不得重复移动或错误完成。
- copy 继续使用任务专属临时目录并对零/一/多候选 fail-safe；不能因批量化放松歧义处理。
- 清理只处理完整 source manifest 与 verified selected manifest 的安全差集；视频和未匹配字幕仍受保护，候选必须位于 immutable package root 下。
- 仅 405/429 或明确频控/风控响应进入共享指数退避；正常操作不得伪装成风控等待。
- 日志只增加聚合调用数/耗时和安全阶段，不记录路径、文件名、provider ID、Cookie 或响应正文。
- 参考 MoviePilot 插件的批量分组与 mutation cache 形状，独立实现且不复制 GPLv3 源码。

## Acceptance Criteria

- [ ] 28 文件、4 个来源父目录、4 个目标父目录 fixture 的远端校验调用数与唯一父目录/chunk 数相关，不再与文件数×祖先深度相关。
- [ ] fixture 的 move batch 不超过目标父目录/chunk 数，健康路径没有逐文件固定等待。
- [ ] provider 成功但 checkpoint 前崩溃时，重试先核对 intent 并收敛，不重复提交已经完成的 move。
- [ ] 10 个清理候选复用共享来源证明并分批回收；任一身份变化只阻止对应安全 chunk，不扩大删除范围。
- [ ] 405/429 仍触发既有共享退避/熔断，普通成功不会清除更新一代的退避状态。
- [ ] 最终日志能区分 provider 等待、provider 调用、目标 listing、batch mutation 和 DB checkpoint 时间，且通过敏感数据边界测试。
- [ ] 相关 Go 测试、vet、lint/build 与 Windows Server 质量门通过。

## Out of Scope

- 取消 115 风控保护或无限并发。
- 跨账号 115 转移、本地下载后再上传。
- 复制 MoviePilot GPLv3 代码。

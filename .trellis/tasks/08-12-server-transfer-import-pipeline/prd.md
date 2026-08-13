# Server 跨存储传输与自动入库

## Goal

下载完成后将内容可靠地识别、规划、跨 Storage 传输并导入用户选择的 MediaLibrary，最终触发增量扫描、分类、STRM 投影和通知。

## Requirements

1. 用户提交资源时选择一条有效 DownloadRule（默认预选系统默认规则，可切换）；Server 保存规则版本快照、来源、下载输出和目标库，生成不可变 DownloadRoute。
2. 支持 direct-to-target、local-to-local、cloud-to-local、local-to-cloud、cloud-to-cloud 五类路线。
3. cloud-to-cloud 优先 provider server-side copy；不支持时只有在启用跨 Storage 且配置受控本地 staging 后才允许“下载到本地再上传”。
4. 下载完成后先解析/匹配足够元数据并生成可预览 ImportPlan（目标相对路径、命名、策略、冲突）；匹配不确定进入人工确认。
5. 分阶段以持久化队列 job 执行并保存 transfer/import progress、bytes、speed、重试、checkpoint、lease 和幂等 key。
6. 目标存在时使用任务快照中的冲突策略。`ask` 创建 ActionRequest 后进入 `waiting_user_action` 并释放执行槽，后续队列继续；响应后重新入队并重新校验目标。`overwrite` 直接替换且不暂停：本地旧目标直接永久移除，不使用隔离回收区；cloud provider 支持原生回收站时旧目标默认入云端回收站，否则永久移除后替换；覆盖前后均重验受控目标身份并审计。hardlink 失败不自动降级 copy；移动/跨盘传输均审计。除同路径冲突 overwrite 外，任何本地或云端删除都不作为规则的隐式完成动作，普通/递归/永久删除走独立预览和反复确认流程。
7. 传输完成后向目标 MediaLibrary supervisor 标记 dirty generation（不创建扫描队列任务），由其完成文件树 reconciliation、本地识别/分类和启用时的 STRM/伴随文件投影；需要网络请求或重试的 metadata scrape/海报处理仍创建独立持久 Queue Job。对应 generation 的必要步骤收敛后，再触发 Emby/Jellyfin refresh 与 Player 通知。
8. 网盘扫描、TMDB 刮削、上传/下载和 direct URL 分别限速，provider 风控错误退避并可取消。

## Acceptance Criteria

- [ ] fake local/cloud adapters 覆盖五类 route，错误路线在提交前拒绝。
- [ ] 下载、传输、入库阶段分别显示百分比与速度并可从失败点重试。
- [ ] 不确定匹配不会自动写入猜测目录；确认后幂等入库一次。
- [ ] 一个冲突任务等待用户时不占 worker slot、不阻塞其它下载/上传/扫描/刮削任务。
- [ ] overwrite 冲突不进入 waiting_user_action；本地旧目标直接永久删除，cloud 有回收站时入云端回收站，否则永久替换；三种路径都只影响已校验的同一目标且有审计。
- [ ] 完成后目标媒体库扫描确认新文件，源/目标文件策略符合用户选择。

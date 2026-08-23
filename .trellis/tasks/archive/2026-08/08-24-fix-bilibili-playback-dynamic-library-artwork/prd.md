# 修复 Bilibili 播放并统一动态媒体库封面

## Goal

修复 Player 通过 Server 播放 Bilibili DASH 视频时无法进入首帧的问题，并建立一致的动态媒体库封面能力：Server 管理的媒体库由 Server 生成，Player 独立管理的媒体库由 Player 本地生成，视觉规则与刷新语义保持一致。

## Background

- Bilibili 播放方案通常同时包含视频轨和音频轨。Player 当前 loopback 媒体桥只有一个活动目标，第二次 `prepare` 会覆盖第一次目标，导致视频轨 token 失效。
- 现有 Server 与 Bilibili 插件提供的是固定静态媒体库图片，只能作为缺少真实海报时的兜底。
- 参考 MoviePilot 媒体库封面生成思路，需要从库内真实媒体海报中筛选、去重并合成 16:9 库封面。
- Player 必须保持独立可用；不连接 Server 时，Player 自己加载的本地目录或直连数据源仍需具备封面生成能力。

## Requirements

### R1 Bilibili DASH 双轨播放

- Player loopback 媒体桥必须同时保存同一播放会话中的视频、音频等多个不透明 token，不能互相覆盖。
- 每个 token 只能访问自己绑定的 URL 与 Header；未知、过期 token 必须拒绝。
- 停止播放或切换媒体时应回收整个旧会话，避免旧 URL 长期可访问。
- Range、HEAD、跨源重定向移除敏感 Header 等现有安全行为必须保留。

### R2 通用媒体库封面语义

- 遵循“谁负责索引媒体库，谁负责生成封面”：Server 管理的数据源由 Server 生成；Player 独立管理的数据源由 Player 本地生成。
- Player 通过 ServerDataSource 浏览媒体库时直接消费 Server 封面，不重复生成。
- 封面来源具有 `generated | provider | custom | fallback` 语义；自定义封面优先于自动生成，固定静态图片只作为 fallback。
- 封面需要稳定 revision/内容摘要，消费者可据此刷新缓存。

### R3 Server 动态封面

- Server 从本地、115 和插件在线媒体库的真实媒体图片中收集候选，过滤无效项，并按媒体身份和图片身份去重。
- Server 使用统一 16:9 模板生成并缓存封面；内容未变化时不得重复生成。
- 首次扫描完成、有效增量变化、115 生活事件整理完成、插件 feed revision 变化和用户手动刷新时可触发更新，并对短时间重复事件去抖。
- 插件只提供受控的封面候选引用；Bilibili 与以后插件不得绕过 Server 安全图片网关暴露上游凭据或任意 URL。

### R4 Player 独立动态封面

- Player 从自己的媒体索引及本地图片缓存中选择候选，不写入用户媒体目录。
- Player 与 Server 使用一致的输出比例、候选数量、裁切、遮罩、标题布局和 fallback 规则。
- 本地库内容或候选海报变化后自动失效并重新生成；无变化时复用缓存。
- 直连 Emby/Jellyfin 等已有 provider 库封面时优先使用 provider 封面，缺失时才本地生成。

## Acceptance Criteria

- [ ] 任意返回独立 DASH 视频轨与音频轨的 Bilibili 媒体可在 Player 中加载并播放，双轨并发 Range 请求分别命中正确目标。
- [ ] 切换或停止播放后旧 loopback token 不再可用，敏感 Header 不会随跨源重定向泄漏。
- [ ] Server 本地媒体库和 115 媒体库可从库内真实海报生成非固定的 16:9 封面，并在内容变化后更新 revision。
- [ ] Bilibili 插件媒体库通过通用候选协议生成动态封面；上游图片 URL、Cookie、Token 不进入 Player DTO 或日志。
- [ ] Player 直接加载本地媒体目录时可在本地生成相同视觉风格的动态库封面，断开 Server 后仍正常工作。
- [ ] Player 浏览 Server 媒体库时只展示 Server 生成封面，并根据 revision 正确更新缓存。
- [ ] 候选不足、首次扫描未完成或上游暂不可用时显示对应静态 fallback，不出现空白卡片。
- [ ] Player typecheck/build、Rust 相关测试、Server Go 测试与插件契约测试通过。

## Out of Scope

- 不在本任务中增加任意 HTML/CSS 插件设置页面能力。
- 不把生成封面写回 Emby/Jellyfin 上游媒体库。
- 不改变媒体识别、TMDB 匹配或媒体文件整理规则。
- 不将 Server 管理媒体库的绝对路径或外部凭据同步到 Player。

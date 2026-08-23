# Player Server 嵌套媒体库导航与在线播放修复

## Goal

修复 Player 通过 Server 播放 Bilibili 在线媒体失败的问题，并把 Server 数据源改造成“来源 → 媒体库 → 分类/栏目 → 内容”的层级浏览体验。标准媒体库使用 Server 分类规则，非标准在线站点可由插件声明并提供任意深度的动态导航。

## Requirements

- Server 数据源首页只展示 Server 下可访问的媒体库，不把多个媒体库的分类或内容平铺混在首页。
- 点击本地、115 等标准媒体库后，先展示该媒体库关联分类规则产生的分类，例如华语电影、外语电影、国产剧、日番；再进入分类查看作品。
- 标准媒体库分类由 Server 自己管理，插件不能修改、替换或注入这些分类。
- 点击 Bilibili 等非标准插件媒体库后，由已安装且启用的插件提供栏目树。栏目可混合分支节点和内容叶节点，并支持 `Bilibili → 番剧 → 日本 → 内容` 这样的多层导航。
- Player 只理解通用导航节点、内容列表、分页、刷新和面包屑，不包含 Bilibili 或其他站点的硬编码。
- 插件必须显式声明层级导航模式；未声明的旧插件继续使用现有单层导航，不因协议升级失效。
- 动态导航深度、单层节点数、标识长度和分页大小必须受 Server 约束，并防止循环导航、跨媒体库标识复用和非法插件响应。
- Player 的返回、面包屑、刷新、搜索、详情、历史、下载和播放上下文必须保持当前来源及媒体库身份。
- 修复已登录 Bilibili 连接在详情成功后请求播放方案返回 503 的故障；不能用隐藏错误或静默降级伪装成功。
- 在线播放仍由 Server 网关保护上游地址、Cookie 和必要请求头，不向 Player 暴露站点凭据或原始受保护地址。
- 不停止、不清理用户当前运行的 Player、Server 进程和既有配置。

## Acceptance Criteria

- [ ] Server 来源首页依次显示本地媒体库、115 媒体库和 Bilibili 连接等媒体库卡片，不直接混入它们的下级分类。
- [ ] 进入标准媒体库可看到按其分类规则排序的分类节点，进入“外语电影”等节点只返回对应分类的作品。
- [ ] Bilibili 插件启用层级模式后可以展示至少两级导航，并且 Player 无任何 Bilibili 专用分类判断。
- [ ] 任意节点均可通过面包屑返回正确层级，重新进入或刷新时不会串到另一个媒体库或节点的缓存。
- [ ] 旧版单层 `site.navigation` 插件仍可打开和加载栏目。
- [ ] 超深、超量、循环、跨库或格式错误的插件导航响应被 Server 拒绝并记录可筛选的插件模块日志。
- [ ] 截图中的 Bilibili 视频可从详情页开始播放，DASH 视频和音频均经 Server 网关获取，清晰度切换仍可用。
- [ ] 上游鉴权失效、内容受限、清晰度不可用和插件响应错误向 Player 返回不同的稳定错误码与可读提示，日志不泄露 Cookie、URL token 或凭据。
- [ ] Player 类型检查、构建和相关单元测试通过；Server Go 测试通过；Bilibili Rust 插件测试及真实登录冒烟测试通过。

## Task Map

- `08-23-fix-bilibili-online-playback`：先修复可独立验证的 Bilibili 播放链路和诊断日志。
- `08-23-plugin-nested-catalog-navigation`：再实现标准分类入口和插件驱动层级导航协议。

## Out of Scope

- 本任务不让普通插件接管本地、115 等标准媒体库的分类规则。
- 本任务不重做 Player 全局首页聚合；插件首页贡献仍通过独立的 `home.contribution` 能力提供。
- 本任务不新增站点、下载器或跨设备同步功能。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.

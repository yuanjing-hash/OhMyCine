# 统一 Player 数据存储并支持便携模式

## Goal

建立 Player 唯一的运行时存储路径边界，把应用配置从 WebView localStorage 迁移到 Tauri SQLite，并同时支持 Windows 标准安装模式和真正可携带的显式便携模式。

## Requirements

- 默认模式使用 `%LOCALAPPDATA%/com.ohmycine.player/data` 保存 Player 自有数据库，使用同级 `cache` 和 `logs` 目录保存可清理数据。
- EXE 同目录存在 `portable.flag` 或启动参数包含 `--portable` 时启用便携模式，使用 EXE 同目录的 `data`、`cache` 和 `logs`。
- 创建共享 Rust storage layout 模块，credential、history、preferences、raw scan cache、settings 和渲染日志不得各自解析路径。
- 标准模式首次启动自动迁移旧 `%APPDATA%/com.ohmycine.player` 下的 credentials、history、preferences 和 scraper SQLite 数据，不覆盖已存在的新文件；便携模式不执行该迁移。
- 标准模式将数据源配置、主题、TMDB 非敏感设置、刮削分类规则和扫描计划从 WebView localStorage 迁入 `settings.sqlite`；迁移成功后删除对应旧 key。便携模式不得读取或删除共享 WebView localStorage 中的旧配置。
- WebView localStorage 仅作为旧版本迁移来源和浏览器/Vite fallback，不再作为 Tauri 桌面版配置源。
- Windows 默认模式使用 DPAPI 保护 AES 主密钥；旧明文 Base64 主密钥首次读取后原地升级为 DPAPI 包装。
- 便携模式使用随目录移动的文件主密钥，并在设置页明确提示便携凭据随文件夹可读的安全权衡。
- 全新便携目录必须作为空白独立配置启动，只复用自身已有 `data`；未来如需导入标准模式数据，必须由用户显式触发。
- 便携目录位于 UNC、WSL 映射或其他网络式路径时，设置诊断页提示 SQLite、日志和缓存性能风险，并建议移动到 Windows 本地磁盘。
- 设置页“关于 / 诊断”展示当前存储模式、数据目录、缓存目录、日志目录和凭据保护方式。
- GitHub beta portable ZIP 自动包含 `portable.flag`，安装包不得包含该标记。
- 更新 Player 设计、安全设计、路线图和 Trellis 可执行规范。
- 完成迁移回归测试、TypeScript/lint/Vite/Rust 检查和 Windows GNU release EXE 构建。

## Security Boundaries

- API Token、密码和 TMDB 凭据不得进入 settings.sqlite、localStorage、日志或迁移诊断。
- 默认模式 DPAPI 数据绑定当前 Windows 用户；便携模式必须明确降低保护等级，不得伪装成系统安全存储。
- 路径迁移只处理固定白名单文件名，不接受前端传入任意路径。
- settings key 仅允许 `ohmycine-` / `ohmycine:` 前缀，限制 key/value 大小并拒绝控制字符。
- 迁移不得覆盖已有新数据；失败时保留旧数据并让应用继续使用已有目标文件。

## Acceptance Criteria

- 默认启动后 SQLite 文件位于 LocalAppData 的统一 `data` 目录。
- 旧用户的数据源、播放历史、扫描缓存、凭据和偏好在升级后仍可读取。
- Tauri 桌面版清空 WebView cache 后，数据源配置、分类规则和扫描计划仍存在。
- 放置 `portable.flag` 后，设置页显示便携模式并把应用自有数据库写入 EXE 同目录 `data`。
- 新解压且没有 `data` 的便携目录首次启动不显示标准模式已有的数据源、历史或偏好；已有便携 `data` 继续保留。
- 从 WSL/UNC 路径启动便携版时显示性能提示，从 Windows 本地磁盘启动时不显示该提示。
- Windows 默认模式的 `master.key` 不再是可直接解码的裸 Base64 AES key。
- portable ZIP 解压后自带 `portable.flag`，setup.exe 仍使用默认模式。
- 所有自动化检查与 Windows GNU release 构建通过。

## Out Of Scope

- 海报/背景图片二进制缓存实现。
- 跨平台 Keychain/libsecret 集成。
- 便携凭据的用户口令加密。
- 自动删除整个 WebView2 profile。
- GitHub push、tag 或发布。

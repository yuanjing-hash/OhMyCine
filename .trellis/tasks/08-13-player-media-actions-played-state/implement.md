# 实施计划：Player 媒体操作菜单与已播放状态

## Preconditions

- 父任务只负责范围、共享契约、子任务依赖与最终集成验收。
- 每个子任务在实施前补齐自己的 PRD；复杂子任务补 `design.md` / `implement.md` 并单独获得实施批准。
- 现有 Server 工作目录和未提交改动不属于本任务，不得纳入 Player 提交。

## 子任务顺序

### Phase A：输入与基础契约

1. `08-13-player-media-action-capabilities`
   - 建立 target/action/capability/provider adapter/controller 契约。
   - 建立共享桌面 popover、移动 sheet 和确认入口。
   - 不显示未实现动作。
2. `08-13-player-action-input-menu`
   - 修复播放画面长按与 contextmenu 冲突。
   - 手机播放操作迁入右上工具面板。
   - MediaCard/MediaLibrary 接入鼠标右键与触摸长按；全局屏蔽原生菜单。

### Phase B：状态与日常组织

3. `08-13-player-played-state-continue-watching`
   - 扩展历史数据库和单项命令。
   - Emby/Jellyfin 标记接口与缓存刷新。
   - 完成态聚合规则、首页操作、海报勾选和详情状态。
4. `08-13-player-media-collections`
   - Player 本地收藏/播放列表/合集数据库与 UI。
   - Emby/Jellyfin 原生集合 adapter。

### Phase C：媒体管理与高风险操作

5. `08-13-player-media-maintenance-actions`
   - 迁移识别菜单。
   - 本地元数据/图像/字幕编辑、刷新刮削、媒体库重扫。
   - Emby/Jellyfin 原生维护接口。
6. `08-13-player-media-downloads`
   - 默认目录设置与一次性“下载到”。
   - 原生下载队列、302/STRM 原始响应解析、进度/取消/重试/续传。
   - Android SAF 与前台任务状态。
7. `08-13-player-media-delete`
   - 默认媒体库移除与 tombstone。
   - Emby/Jellyfin 原生删除。
   - 本地/网盘安全源删除、聚合清单、加强确认、部分失败。

### Phase D：父任务集成验收

8. 审核所有海报入口、首页分区、搜索、详情、来源媒体库和播放页动作一致性。
9. 验证 capability 不产生空按钮，provider/local 状态无双写，缓存失效完整。
10. 更新 Player 架构、roadmap 和 Trellis frontend specs。

## 共享验证矩阵

- 桌面鼠标：右键定位、左键关闭、滚动/缩放边界、键盘菜单导航。
- Windows 触屏：触摸长按菜单、滚动取消、播放长按倍速、不出现合成右键。
- Android：横竖屏工具菜单、播放长按、媒体卡长按、SAF 权限恢复、后台下载。
- Emby/Jellyfin：已播放、收藏、列表/合集、删除、下载、元数据刷新按服务端权限工作。
- 原始来源：本地状态/集合/刮削资产不写回源；下载解析原始媒体；源删除仅在显式勾选后执行。
- 聚合对象：电影多版本、季、系列完成态；整季/整剧下载与删除部分失败。
- 隐私：任务数据库、路由、日志和导出中无 URL query、Cookie、Authorization、Header 或凭据。

## 项目门禁

每个子任务运行其专项验证，并至少执行：

```powershell
cd player
npm run typecheck
npm run lint
npm run build
cargo test --manifest-path src-tauri/Cargo.toml
cargo check --manifest-path src-tauri/Cargo.toml
```

涉及 Android 时增加 Android 静态/Kotlin/APK 检查；涉及 Windows 文件系统、WebView 右键或目录选择时由项目所有者完成 Windows 原生运行验证；涉及 Linux 的共享 Rust/TypeScript 代码保持 CI 编译兼容。

## Rollback Points

- capability adapter 可逐个 provider 关闭，菜单自动隐藏动作。
- SQLite 迁移只新增表/列；回滚代码不得删除用户新数据。
- 下载 partial 文件有独立受控目录/目标命名，失败或取消不覆盖最终文件。
- 删除实现若任一 provider 未达到路径/权限安全要求，保持 `hidden/disabled`，不得用本地隐藏冒充源删除。

# 统一元数据图像字幕与媒体库维护操作

## Goal

将已有识别/刮削能力和提供方维护能力接入共享媒体菜单，统一元数据、图像、字幕、刷新和重扫入口。

## Requirements

- 非 Emby 的元数据、图像、字幕编辑只修改 Player 本地刮削数据库与受控缓存，不写回源目录。
- 复用现有 manual identification、图片编辑和扫描调度，不复制页面逻辑。
- 非 Emby 刷新元数据重新刮削选中目标，媒体库重扫调用当前库扫描调度器。
- Emby/Jellyfin 使用提供方原生元数据、图像、字幕编辑和刷新接口，按权限决定 capability。
- 现有 SourceLibrary 局部识别菜单迁移到共享 action controller。

## Acceptance Criteria

- [ ] 所有海报入口可到达一致的维护动作，来源不支持时不显示空入口。
- [ ] 非 Emby 编辑后源目录没有新增/覆盖 NFO、图片或字幕文件。
- [ ] Emby/Jellyfin 更新在服务端生效并刷新 Player 缓存。
- [ ] 媒体库重扫只作用于选中库且有进度/错误反馈。

## Out of Scope

- 非 Emby 元数据资产写回源目录。

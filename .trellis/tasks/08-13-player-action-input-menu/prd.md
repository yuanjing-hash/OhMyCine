# 修复移动播放长按并统一菜单输入

## Goal

让真实鼠标右键与触摸长按各自执行正确操作，并将所有可操作媒体对象接入统一响应式菜单入口。

## Requirements

- 播放画面触摸长按只执行右侧倍速/左侧连续后退，抑制系统合成 contextmenu。
- 桌面真实鼠标右键保留播放菜单；移动播放操作迁入右上角工具面板。
- MediaCard/MediaLibrary 鼠标右键和移动长按输出同一 `MediaActionTarget`。
- 长按在滚动、移动超阈值、取消或离开时终止，并抑制随后合成 click/contextmenu。
- App 非可操作区域统一阻止 WebView/浏览器原生菜单。

## Acceptance Criteria

- [ ] Android 和 Windows 触屏播放长按不再同时出现菜单与倍速。
- [ ] 桌面鼠标右键、移动媒体长按、键盘菜单导航均正常。
- [ ] 手机播放菜单动作在右上工具面板可访问。
- [ ] 页面滚动不会误开媒体菜单；其他区域无原生刷新菜单。

## Out of Scope

- 实现每个媒体动作的 provider API；由能力与领域子任务负责。

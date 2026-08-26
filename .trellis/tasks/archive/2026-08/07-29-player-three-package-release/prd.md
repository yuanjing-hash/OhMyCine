# Player Beta 发布三种 Windows 程序包

## Goal

让每个 Player GitHub Beta 同时发布安装包、标准免安装 ZIP 和便携 ZIP，用户可以按数据存储方式选择，不再需要自己从 release 目录整理程序文件。

## Requirements

- 发布 `setup.exe`，作为正常安装版本。
- 发布 `standard.zip`，包含干净的 Windows 运行文件但不包含 `portable.flag`，运行时使用标准 LocalAppData 配置档案。
- 发布 `portable.zip`，包含相同运行文件和 `portable.flag`，运行时使用 EXE 同目录的独立 data/cache/logs。
- 两种 ZIP 都只从 Windows GNU release 目录复制运行必需文件和许可证，不得打包构建中间产物。
- SHA-256 文件同时覆盖安装包、标准 ZIP 和便携 ZIP。
- GitHub Actions artifact、GitHub prerelease 上传列表和自动 Release notes 必须包含三种程序包。
- 更新 Player 发布质量规范和公开说明。
- 以 `v0.0.5` 从 `main` 最新提交触发下一次 Beta 发布。

## Acceptance Criteria

- `OhMyCine-Player-v0.0.5-windows-x64-setup.exe` 可由现有 NSIS 构建生成。
- `OhMyCine-Player-v0.0.5-windows-x64-standard.zip` 内没有 `portable.flag`、data、cache 或 logs。
- `OhMyCine-Player-v0.0.5-windows-x64-portable.zip` 内有 `portable.flag`，首次解压时没有 data、cache 或 logs。
- 两个 ZIP 都包含 `ohmycine-player.exe`、`WebView2Loader.dll`、`libmpv-wrapper.dll` 和 `libmpv-2.dll`。
- 校验文件包含三个程序包的 SHA-256。
- Release notes 明确说明标准 ZIP 使用用户目录，便携 ZIP 使用程序目录。
- Workflow dry-run、本地打包演练、Player 质量检查和 Windows GNU release 构建通过。

## Out Of Scope

- Windows 代码签名。
- macOS/Linux 发布包。
- 自动递增版本号。
- 改变标准模式或便携模式的数据存储实现。

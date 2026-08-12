# Design: Windows 原生 Server 启动与测试脚本

## Architecture

- `server/scripts/windows-common.ps1`：仅包含路径解析、工具链发现/下载、环境准备和安全目录断言等共享函数。
- `server/start.ps1`：生产式本地入口，构建内嵌 Web UI 的 EXE并以前台进程启动。
- `server/test.ps1`：质量门与真实健康探针入口，所有运行状态放入唯一测试目录。
- `server/.runtime/windows/`：唯一的 Windows 仓库本地状态根；内部区分 `cache/`、`bin/`、`data/`、`logs/`、`tests/`。

## Toolchain Contract

脚本先搜索 PATH 中满足 `go.mod` 版本要求的 Windows Go。不可用时要求 Windows Package Manager，并以精确 package ID `GoLang.Go` 调用 `winget install --exact`，接受源与包协议，等待官方安装完成。安装可能触发 UAC；脚本不会绕过授权。随后从机器/用户环境以及 Go 默认安装目录刷新当前进程 PATH，并重新验证版本。脚本不下载未校验的便携工具链，也不自行编辑持久 PATH。

Node/npm 不自动下载：用户已说明 Windows 有 Node，脚本只进行版本/存在性检查。Web UI 使用 `npm ci` 与 package-lock；依赖可复用时避免重复安装。

## SQLite Compatibility

GORM 仍使用 `gorm.io/driver/sqlite` 的 Dialector，但指定注册到 `database/sql` 的纯 Go SQLite driver，替换运行时对 `go-sqlite3`/CGO 的依赖。现有 DSN、迁移和 GORM 模型不变，并由现有 HTTP 集成测试与真实健康检查验证。

## Isolation and Safety

- 持久启动数据库固定在 `server/.runtime/windows/data/ohmycine.db`，除非用户显式设置 `OMC_DATABASE_PATH`。
- 测试数据库位于 `server/.runtime/windows/tests/<unique-id>/data/ohmycine.db`；健康检查进程日志也写入同一唯一目录。
- 测试清理函数先解析绝对路径，要求目标严格位于 tests 根目录且不是 tests 根本身；只自动删除本轮成功测试创建的目录。失败目录保留。
- 默认监听 `127.0.0.1`。用户显式覆盖监听地址时沿用 Server 现有安全提示和 origin 配置。

## Cross-platform Generated Files

权限生成器比较前把现有文件的 CRLF 规范化为 LF，只对换行符容忍；权限代码、顺序或内容变化仍会失败。生成时保留仓库规范的 LF，并通过 `.gitattributes` 固定该生成文件、JSON 和 shell 脚本的换行行为。

## Rollback

脚本和纯 Go SQLite 依赖可独立回退。运行状态全部位于已忽略的 `.runtime/windows`；删除该目录只会删除 Windows 隔离环境，但脚本自身不会提供或执行广泛清理操作。系统 Go 的卸载交由 Windows“已安装的应用”或 `winget uninstall --id GoLang.Go`，脚本不自动卸载。

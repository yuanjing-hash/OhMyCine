# 贡献指南

感谢你对 OhMyCine 的关注！本文档将帮助你了解如何参与项目开发。

## 目录

- [行为准则](#行为准则)
- [开始之前](#开始之前)
- [报告 Bug](#报告-bug)
- [提交功能建议](#提交功能建议)
- [贡献代码](#贡献代码)
- [开发环境](#开发环境)
- [代码规范](#代码规范)
- [Pull Request 流程](#pull-request-流程)
- [Issue 规范](#issue-规范)
- [组件指南](#组件指南)

## 行为准则

本项目遵循开源社区的基本礼仪：

- 尊重每一位参与者
- 就事论事，聚焦技术讨论
- 接受建设性批评
- 对他人表示友善和同理心

## 开始之前

1. **阅读项目文档**：
   - [README.md](README.md) — 项目概览
   - [DEVELOPMENT.md](DEVELOPMENT.md) — 编码规范
   - [docs/architecture/](docs/architecture/) — 架构设计文档

2. **了解项目结构**：OhMyCine 采用 Monorepo，包含四个组件：
   - `player/` — Tauri v2 + Vue 3 播放器
   - `server/` — Go + Gin 后端
   - `hub/` — VitePress 插件市场
   - `cli/` — Go + Cobra 命令行工具

3. **查看开发路线图**：[docs/architecture/06-roadmap.md](docs/architecture/06-roadmap.md) 了解当前开发阶段和优先级

## 报告 Bug

使用 [GitHub Issues](https://github.com/yuanjing-hash/OhMyCine/issues) 报告 Bug，请包含：

### Bug 报告模板

```markdown
**描述**
简要描述 Bug

**复现步骤**
1. 打开 '...'
2. 点击 '...'
3. 滚动到 '...'
4. 出现错误

**期望行为**
你期望发生什么

**实际行为**
实际发生了什么

**环境信息**
- OS: [e.g. Windows 11, macOS 15, Ubuntu 24.04]
- OhMyCine 版本: [e.g. v0.1.0]
- 组件: [Player / Server / CLI]
- 数据源: [Emby / Jellyfin / Alist / CloudDrive2]

**日志**
粘贴相关日志（如有）

**截图**
添加截图帮助说明问题（如有）
```

## 提交功能建议

使用 [GitHub Issues](https://github.com/yuanjing-hash/OhMyCine/issues) 提交功能建议，请包含：

```markdown
**功能描述**
简要描述你希望的功能

**使用场景**
解释为什么需要这个功能，解决什么问题

**期望方案**
描述你期望的实现方式（可选）

**替代方案**
你考虑过的其他方案（可选）
```

## 贡献代码

### 贡献类型

| 类型 | 说明 | 难度 |
|------|------|------|
| Bug 修复 | 修复已知问题 | 初级 |
| 文档改进 | 完善文档、修复错别字 | 初级 |
| 测试补充 | 补充单元测试、集成测试 | 中级 |
| 功能开发 | 实现路线图中的功能 | 中级-高级 |
| 架构优化 | 性能优化、代码重构 | 高级 |

### 工作流程

```
1. Fork 仓库
2. 创建功能分支
3. 编写代码
4. 编写/更新测试
5. 确保所有测试通过
6. 提交 Pull Request
7. 等待 Code Review
8. 合并到 develop
```

## 开发环境

### 前置依赖

| 工具 | 版本 | 说明 |
|------|------|------|
| Go | >= 1.22 | Server / CLI 开发 |
| Node.js | >= 20 | Player / Hub 开发 |
| Rust | latest stable | Player Tauri 后端 |
| Docker | latest | Server 本地开发 |
| Git | >= 2.30 | 版本控制 |

### Player 开发

```bash
cd player
npm install
npm run tauri dev
```

### Server 开发

```bash
cd server
go mod download
go run ./cmd/server

# 或使用 Docker
docker compose up -d
```

### CLI 开发

```bash
cd cli
go build -o omc ./cmd/omc
./omc --help
```

### Hub 开发

```bash
cd hub
npm install
npm run dev
```

## 代码规范

详细的编码规范请阅读 [DEVELOPMENT.md](DEVELOPMENT.md)，以下为关键要点：

### Commit 规范

采用 [Conventional Commits](https://www.conventionalcommits.org/) 格式：

```
<type>(<scope>): <description>
```

**Type**：`feat` / `fix` / `docs` / `style` / `refactor` / `perf` / `test` / `chore` / `ci`

**Scope**：`player` / `server` / `hub` / `cli` / `docs` / `api` / `db`

**示例**：

```bash
# 好
git commit -m "feat(server): add follow/subscribe engine for TV series"
git commit -m "fix(player): fix Emby playback timeout on slow network"
git commit -m "docs: update architecture overview"

# 不好
git commit -m "update code"
git commit -m "fix bug"
git commit -m "WIP"
```

### 代码风格

| 语言 | 格式化工具 | Lint 工具 |
|------|-----------|-----------|
| Go | `gofmt` / `goimports` | `golangci-lint` |
| TypeScript | Prettier | ESLint |
| Vue | Prettier | ESLint + vue-tsc |
| Rust | `rustfmt` | `clippy` |

### 提交前检查清单

- [ ] 代码符合 [DEVELOPMENT.md](DEVELOPMENT.md) 规范
- [ ] 新增代码有对应的测试
- [ ] 所有测试通过
- [ ] Lint 检查通过
- [ ] 公开 API 有文档注释
- [ ] 没有引入安全漏洞
- [ ] Commit message 符合 Conventional Commits 格式

## Pull Request 流程

### PR 标题

使用与 Commit 相同的格式：

```
feat(server): add follow/subscribe engine
```

### PR 描述模板

```markdown
## 变更说明
- 变更点1
- 变更点2

## 变更类型
- [ ] 新功能 (feat)
- [ ] Bug 修复 (fix)
- [ ] 文档更新 (docs)
- [ ] 代码重构 (refactor)
- [ ] 性能优化 (perf)
- [ ] 其他: ___

## 关联 Issue
Closes #xxx

## 测试
- [ ] 新增单元测试
- [ ] 所有测试通过
- [ ] 手动测试通过

## 截图 (UI 变更时)
添加截图或录屏

## Checklist
- [ ] 代码符合项目规范
- [ ] 已自测
- [ ] 无 breaking change / 已在描述中标注
- [ ] 文档已更新（如需要）
```

### Code Review 要点

Reviewer 会关注：

1. **正确性** — 代码逻辑是否正确，是否覆盖边界情况
2. **可读性** — 命名是否清晰，结构是否合理
3. **性能** — 是否有性能问题，是否需要优化
4. **安全** — 是否有安全漏洞（注入、XSS 等）
5. **测试** — 测试是否充分，是否覆盖关键路径
6. **规范** — 是否符合项目编码规范

### Review 流程

```
提交 PR → 自动 CI 检查 → 人工 Code Review → 修改反馈 → 批准 → 合并
```

- 至少需要 1 位维护者批准
- CI 检查必须通过
- 所有讨论必须解决

## Issue 规范

### Issue 标签

| 标签 | 说明 |
|------|------|
| `bug` | Bug 报告 |
| `enhancement` | 功能增强 |
| `documentation` | 文档相关 |
| `good first issue` | 适合新手 |
| `help wanted` | 需要帮助 |
| `player` | 播放器相关 |
| `server` | 后端相关 |
| `hub` | 插件市场相关 |
| `cli` | CLI 工具相关 |

### Issue 指派

- 未被指派的 Issue 可以自由认领
- 认领方式：在 Issue 下评论 "I'd like to work on this"
- 维护者会指派给你
- 如果无法完成，请及时取消指派

## 组件指南

### 贡献 Player

```
player/
├── src/                 # Vue 前端
│   ├── components/      # UI 组件
│   ├── views/           # 页面
│   ├── stores/          # Pinia 状态管理
│   ├── composables/     # 组合式函数
│   ├── services/        # 业务服务
│   └── styles/          # 全局样式
└── src-tauri/           # Rust 后端
    └── src/
        ├── commands/    # Tauri Commands
        └── mpv/         # libmpv 集成
```

**注意事项**：
- 新增 DataSource 需实现统一接口
- UI 组件使用 UnoCSS 原子类
- 播放器相关修改需测试多种格式

### 贡献 Server

```
server/
├── cmd/                 # 入口
├── internal/            # 私有代码
│   ├── handlers/        # HTTP 处理器 (薄层)
│   ├── services/        # 业务逻辑 (核心)
│   └── models/          # 数据模型
└── pkg/                 # 公共代码
    ├── cloud/           # 网盘驱动
    ├── downloader/      # 下载客户端
    └── metadata/        # 元数据刮削
```

**注意事项**：
- 新增网盘驱动需实现 `Driver` 接口
- 新增下载客户端需实现 `DownloadClient` 接口
- 所有 API 遵循 RESTful 规范

### 贡献 CLI

```
cli/
└── cmd/
    └── omc/
        ├── main.go      # 入口
        └── commands/    # 子命令
```

**注意事项**：
- 新增命令遵循 Cobra 规范
- 与 Server 共享 `pkg/` 代码

### 贡献文档

```
docs/
└── architecture/        # 架构设计文档
    ├── 00-index.md      # 索引
    ├── 01-overview.md   # 总览
    ├── 02-server-design.md
    ├── 03-player-design.md
    ├── 04-hub-design.md
    ├── 05-cli-design.md
    └── 06-roadmap.md    # 路线图
```

**注意事项**：
- 文档使用中文编写
- 保持与代码实现同步更新
- 架构变更需更新所有相关文档

## 发布周期

- **开发分支**：`develop`，日常开发
- **功能分支**：`feature/xxx`，从 `develop` 拉出
- **修复分支**：`fix/xxx`，从 `develop` 拉出
- **发布分支**：`release/x.x.x`，从 `develop` 拉出
- **生产分支**：`main`，始终可发布

版本号遵循 [Semantic Versioning](https://semver.org/)：

```
MAJOR.MINOR.PATCH

MAJOR: 不兼容的 API 变更
MINOR: 向后兼容的功能新增
PATCH: 向后兼容的 Bug 修复
```

## 许可证

贡献的代码将采用与项目相同的 [GPL-3.0 License](LICENSE) 开源。

## 获取帮助

- **GitHub Issues** — 报告 Bug、提交建议
- **GitHub Discussions** — 技术讨论、问题求助（即将开放）
- **Code Review** — 提交 PR 后获取反馈

---

<div align="center">

**感谢你的贡献！**

每一份贡献都让 OhMyCine 变得更好

</div>

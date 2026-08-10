# 修复分支拓扑并统一 Beta/Stable 发布规则

## Goal

在不丢失 Server 工作、不污染远端正式分支、不破坏现有运行数据的前提下，修复误提交到本地 `main` 的 Server 历史：将其重新作为功能工作集成到 `develop`，让本地 `main` 恢复与远端正式线一致；同时把 owner 最新确认的 Git/发布规则固化到项目规范和 GitHub Actions。

## Requirements

- 以 `ea5ecfe` 创建并验证只读恢复分支 `codex/server-admin-v0-2-recovery`，确保 8 个 Server 提交在移动本地 `main` 前始终可达。
- 以当前 `develop` 的 `5c4880b` 创建隔离集成分支 `codex/integrate-server-admin-v0-2`。
- 将恢复分支合入集成分支，冲突解决必须同时保留当前 `develop` 的 Player 弹幕/v1.0.1 修复和完整 Server v0.2 工作。
- 不删除、重置、迁移或提交 `server/.runtime/`、数据库、WAL/SHM、生成二进制、`node_modules` 或 `dist`。
- Server 和 Player 的功能/修复开发必须从 `develop` 拉出分支，验证后合回 `develop`。
- Beta 发布提交必须是远端 `develop` 的最新提交；不能从功能分支、历史提交或本地未推送提交发布。
- Stable 发布提交必须是远端 `main` 的最新提交；只有确认正式版后才能把 `develop` 合并到 `main`。
- tag push 保持 Beta 渠道，并验证 tag 提交等于远端 `develop`；Stable 通过显式 `workflow_dispatch channel=stable` 从远端 `main` 发布。
- 手动 Beta 发布必须从远端 `develop` 最新提交触发；手动 Stable 发布必须从远端 `main` 最新提交触发。
- 更新 `AGENTS.md`、`DEVELOPMENT.md`、Trellis Player Release Packaging 契约和 Release workflow 自校验，消除“Beta 必须来自 main”的旧规则。
- 集成和发布规则检查全部通过后，才允许把集成分支合入本地 `develop`。
- 在 Server 恢复分支已保护且 `develop` 合入成功后，将本地 `main` 引用对齐 `origin/main`；不得使用会改写当前工作树的 `git reset --hard`。
- 不 fetch、不 push、不创建/移动远端 tag；最终所有远端 refs 与任务开始时保持一致。

## Acceptance Criteria

- [x] `codex/server-admin-v0-2-recovery` 精确指向 `ea5ecfe`，并包含 `09806e5`、`adba2d8`、`857cfe8`、`ea5ecfe`。
- [x] 集成分支保留 `develop` 当前的 `b22c77c`、`ba22a35`/v1.0.1 修复历史，同时包含全部 Server 提交。
- [x] `server/start.sh`、Server Go 模块和 Web UI v0.2 在集成结果中均存在。
- [x] `server/.runtime/` 和 `server/server` 内容/身份未被删除或提交，并由恢复后的 `server/.gitignore` 正确忽略。
- [x] Release workflow 对 Beta 校验 `origin/develop`，对 Stable 校验 `origin/main`，且 tag push 默认 Beta。
- [x] workflow 自检覆盖合法 Beta、合法 Stable、错误分支、历史提交和本地未推送提交。
- [x] `AGENTS.md`、`DEVELOPMENT.md`、Trellis spec 与 workflow 对分支/渠道规则表述一致。
- [x] Player workflow/脚本静态检查、Player typecheck/lint/build、Server Go/Web UI 检查和一键启动脚本检查通过。
- [x] 本地 `develop` 最终包含集成结果；本地 `main` 最终精确等于 `origin/main`。
- [x] `origin/develop`、`origin/main` 和远端 tags 未被本任务修改。
- [x] 工作区仅保留原有运行产物，Git tracked worktree 干净。

## Definition of Done

- 所有分支移动前后都有精确 SHA 记录和可达性验证。
- Server 与 Player 当前能力均无回归，发布源校验可执行而非仅文档约定。
- 变更使用中文 Conventional Commit 提交，Trellis 任务归档。
- 不推送远端；最终向 owner 报告本地/远端每条关键分支的 SHA 和后续可执行命令。

## Technical Approach

1. 在隔离集成分支上以 `--no-ff` 合并 Server 恢复线，使用三方合并保留双方历史；逐文件审阅冲突和差异。
2. 将 release job 的 source-of-truth 校验改为 channel-aware：Beta → `origin/develop`，Stable → `origin/main`；tag push固定 Beta。
3. 扩充 workflow 内置 dry-run/guardrail，使分支来源规则本身可被 CI 验证。
4. 先完成 Player、Server、Git 拓扑和运行产物检查，再快进/合并本地 `develop`。
5. 当前工作树位于 `develop`/集成线时，用受保护分支兜底后执行 `git branch -f main origin/main`，只移动本地引用，不碰工作树和远端。

## Decision (ADR-lite)

**Context**: Server v0.2 被直接开发在本地 `main`，随后 Player v1.0.1 使用干净发布线完成远端正式发布，导致本地 `main` 同时含未发布 Server 工作且与 `origin/main` 分叉。

**Decision**: 把 `ea5ecfe` 视为可恢复的 Server 功能线，从当前 `develop` 建集成分支重新合入；远端正式线保持不变。以后 Beta 只取远端 `develop` 最新提交，Stable 只取远端 `main` 最新提交。

**Consequences**: Server 工作回到正确的集成分支，正式版历史保持纯净；发布 workflow 必须按渠道验证不同远端分支，Stable 使用显式渠道，tag push继续代表 Beta。

## Out of Scope

- 不向 GitHub 推送分支、提交或 tag。
- 不重新发布、删除或移动现有 `v1.0.1`。
- 不修改 Player/Server 产品功能，除合并冲突所需的最小兼容调整。
- 不删除本地恢复分支、旧修复分支或干净发布分支。

## Technical Notes

- 任务开始时：`develop=origin/develop=5c4880b`；`origin/main=ba22a35`；错误本地 `main=8f71efb`；Server 线末端 `ea5ecfe`。
- 远端 `v1.0.1` 精确指向 `ba22a35`，未包含 Server 提交。
- 原有运行产物：未跟踪 `server/.runtime/` 与 `server/server`；必须原样保留。
- 当前旧规则位于 `AGENTS.md`、`DEVELOPMENT.md`、`.github/workflows/player-beta-release.yml` 和 `.trellis/spec/frontend/quality-guidelines.md`。

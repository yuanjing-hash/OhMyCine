# 整理 Codex Trellis 集成

## Goal

让 Codex 可以稳定操作 OhMyCine 仓库：会话能读取 Trellis workflow 和项目规则，Codex agents/hooks/skills 文件可被版本管理，当前 Codex thread 能解析 active task，并避免明显的 Claude 遗留命名影响后续维护判断。

## What I Already Know

- 项目最初 Trellis init 主要落在 Claude Code 侧。
- 当前仓库已经存在 `.codex/`、`.agents/skills/` 和 `AGENTS.md`，但它们在 git 中仍是未跟踪文件。
- `.codex/hooks/*.py` 与 `.claude/hooks/*.py` 内容一致，手动运行 `session-start` 与 `inject-workflow-state` 可以输出 Trellis context。
- 当前 Codex 环境有 `CODEX_THREAD_ID`，创建本任务后 `task.py current --source` 已能解析到 Codex session。
- `.trellis/.template-hashes.json` 记录了 `AGENTS.md`，但未记录 `.codex/*` 或 `.agents/*`。

## Requirements

- 保留 Codex 平台文件：`.codex/hooks.json`、`.codex/config.toml`、`.codex/agents/*.toml`、`.codex/hooks/*.py`。
- 保留共享 skills：`.agents/skills/*`，让 Codex app 能发现项目本地 skills。
- 确认 hooks 能在当前仓库里运行，并能注入 workflow/spec/task 状态。
- 处理明显的命名或维护瑕疵，避免 Codex 配置看起来仍依赖 Claude 专属概念。
- 不改动播放器业务代码，不覆盖用户已有 WIP。

## Acceptance Criteria

- [x] `python3 ./.trellis/scripts/task.py current --source` 在当前 Codex 会话中返回本任务。
- [x] `.codex`、`.agents/skills`、`AGENTS.md` 在 `git status` 中不再只是意外的未知生成物，整理结果明确可提交。
- [x] Codex hook 脚本能通过最小 stdin 试跑。
- [x] 配置整理不破坏 Claude 侧已有 Trellis 文件。
- [x] 最终说明当前可用状态、仍需用户决定是否提交的文件范围。

## Out of Scope

- 不升级 Trellis CLI 或重新运行全量 `trellis init`。
- 不清理已有播放器功能 WIP。
- 不归档或完成其他 05-12 播放器任务。
- 不改变 Trellis 工作流的阶段规则。

## Technical Notes

- Codex platform map: `.codex/` stores hooks and agents; `.agents/skills/` stores shared skills.
- Current Trellis version: `0.5.4`.
- Current Codex session key: resolved from `CODEX_THREAD_ID`.
- `.codex/config.toml` now sets `TRELLIS_PLATFORM=codex` for shell commands.
- `.codex/hooks.json` uses repo-relative hook commands instead of a machine-specific absolute path.
- `.codex/hooks/inject-subagent-context.py` and `.claude/hooks/inject-subagent-context.py` recognize `.codex` script paths when detecting platform.

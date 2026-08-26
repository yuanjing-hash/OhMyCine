# 迁移未提交改动到 develop

## Goal

删除不再使用的 Claude 平台配置和废弃 onboarding 任务，将 `main` 工作区中其余未提交的 Trellis、Windows、Codex 与任务记录安全迁移到最新 `develop`，不改变任何已提交或已发布功能。

## Requirements

- 删除整个 `.claude/`。
- 删除 `.trellis/tasks/00-join-yuanjing/`。
- 保留其他未提交改动中的 Codex/Trellis 核心、Windows 工具链规则和其他任务记录。
- 项目仅适配 Codex：删除 Antigravity 的 `.agent/` 以及 GitHub Copilot 的 `.github/skills/`、`.github/copilot-instructions.md`；保留 Codex 使用的 `.codex/` 与共享 `.agents/skills/`。
- 以最新 `origin/develop` 为迁移基线；`develop` 当前包含 `main` 且领先 14 个提交。
- 对五个重叠文件合并双方内容，保留 `develop` 已有的新发布/弹幕配置，同时保留本地 Windows/Codex 改动。
- 不提交、不推送、不删除恢复用 stash，除非用户之后明确要求。

## Acceptance Criteria

- [ ] 当前分支是跟踪 `origin/develop` 的本地 `develop`。
- [ ] `main` 提交和远端状态未改变。
- [ ] `.claude/` 与 `.trellis/tasks/00-join-yuanjing/` 不存在。
- [ ] `.agent/`、`.github/skills/` 与 `.github/copilot-instructions.md` 不存在，`.codex/` 和 `.agents/skills/` 保留。
- [ ] 其余原有未提交工作出现在 `develop` 工作区。
- [ ] 五个重叠文件同时保留 `develop` 新内容和本地改动。
- [ ] 没有未解决冲突、冲突标记或意外暂存文件。
- [ ] 迁移前备份 stash 与迁移 stash 均保留，可恢复。
- [ ] Trellis CLI 与项目均为 `0.6.14`，`trellis update --dry-run --skip-all` 报告已是最新状态。

## Notes

本任务只整理工作区和分支归属，不改动已发布提交，不进行 commit、push、reset 或 stash drop。

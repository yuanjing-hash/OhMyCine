# 执行计划

1. 刷新远端引用并记录 `main`、`develop`、工作区和 stash 基线。
2. 创建完整备份 stash，并立即重新应用以继续整理。
3. 删除 `.claude/` 和 `.trellis/tasks/00-join-yuanjing/`。
4. 创建只含目标改动的迁移 stash。
5. 将本地 `develop` 对齐并跟踪最新 `origin/develop`。
6. 应用迁移 stash，解决重叠文件冲突并保留双方所需内容。
7. 按用户确认的 Codex-only 范围删除 Antigravity 与 GitHub Copilot 生成文件，保留 `.codex/` 和 `.agents/skills/`。
8. 完成 Trellis `0.6.14` 核心模板同步并使用官方 Codex 初始化重新登记 Codex 平台，清除已弃平台的模板清单项。
9. 验证分支关系、文件保留/删除、冲突、暂存区、Trellis dry-run 和 stash 恢复点。

# 技术设计

## 安全策略

先使用包含未跟踪文件的 stash 保存完整原始工作区，再重新应用该 stash并仅删除两个已授权目标。随后将过滤后的工作区保存为第二个迁移 stash，切换到最新 `origin/develop` 后用 `stash apply` 应用。两个 stash 全程保留。

## 冲突策略

若发生冲突，以 `origin/develop` 为已发布/已集成基线，并把本地 Windows/Codex 改动叠加进去。重点人工检查：

- `.github/workflows/player.yml`：保留 DandanPlay secrets。
- `.trellis/spec/backend/directory-structure.md`：保留 `server/webui` 独立 Go module 规则。
- `.trellis/spec/frontend/quality-guidelines.md`：保留 Beta/develop、Stable/main 规则。
- `AGENTS.md`：保留相同发布分支规则与 Windows/Codex 规则。
- `player/package.json`：保留版本 `1.1.0` 与 `verify:danmaku`。

## 恢复方案

迁移前完整 stash 是原始状态备份；迁移 stash 是删除 Claude/onboarding 后的目标状态备份。任何异常都停止继续修改并使用 stash 恢复，不使用破坏性 reset。

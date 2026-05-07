# 迁移设计文档到 Trellis 规范

## Goal

将现有 `docs/architecture/` 与 `CLAUDE.md` 中已经确定的产品设计、开发约束、安全要求和路线图，迁移为 Trellis 可持续使用的 `spec`、`task` 和工作规则；同时修复文档中的明显不一致，让后续 OhMyCine 开发统一走 Trellis 流程。

## What I already know

* 项目已经通过 `trellis init -u yuanjing` 初始化。
* 当前已有任务 `.trellis/tasks/00-bootstrap-guidelines/`，但本次会用 `.trellis/tasks/05-07-trellis/` 承接迁移工作。
* `docs/architecture/00-index.md` 到 `07-security-design.md` 是完整设计文档。
* `CLAUDE.md` 已包含项目定位、组件职责、安全要求、本地开发环境、提交规范等长期约束。
* `.trellis/spec/backend/` 与 `.trellis/spec/frontend/` 已有文件骨架，但还没有承载完整项目规范。
* 用户要求我直接推进，不重做已经开发的 Player 成果，而是把已完成状态接入 Trellis 管理。
* 数据库默认明确为 SQLite。
* 产品需要支持 OpenList，文档应使用 `OpenList/Alist` 或 `OpenList (Alist-compatible API)` 的一致表述。
* 已完成的功能/设计应同步更新 roadmap 勾选状态，避免 Trellis 任务状态与路线图脱节。

## Requirements

* 将长期、可执行的开发规范从 `docs/architecture/` 与 `CLAUDE.md` 提炼进 `.trellis/spec/`。
* 完善 backend spec：目录结构、数据库、错误处理、日志、安全、API、质量规范。
* 完善 frontend spec：目录结构、组件规范、组合式函数、状态管理、类型安全、质量规范。
* 将安全要求沉淀为 Trellis spec，覆盖凭据存储、代理安全、路径安全、日志脱敏、插件安全、AI 数据边界。
* 修复架构文档中明显不一致或重复内容。
* 明确数据库策略：SQLite 是默认本地/自托管数据库；其他数据库如出现，只能作为未来可选部署方向，不能改变默认开发目标。
* 明确 OpenList 支持策略：对外文档统一写 `OpenList/Alist` 或 `OpenList (Alist-compatible API)`；代码标识符可以在需要兼容生态时使用 `alist`。
* 建立 roadmap 更新规则：功能完成或设计状态变化后，应更新 `docs/architecture/06-roadmap.md` 的勾选/状态说明。
* 建立 Trellis 接管规则：已有 Player 代码不重做，通过文档、spec 和后续任务记录当前完成度；后续改动按 Trellis task 执行。
* 保持 Player 独立优先，不因为迁移 Trellis 改变产品架构。
* 保持 Server 作为增强/自动化层，不让 Player 基础播放依赖 Server。

## Acceptance Criteria

* [ ] `.trellis/spec/backend/` 中的规范文件不再是空壳，包含从设计文档提炼的可执行规则。
* [ ] `.trellis/spec/frontend/` 中的规范文件不再是空壳，包含 Player/Tauri/Vue/DataSource/Cinema OS 相关规则。
* [ ] 新增或补充安全/API 相关 spec，使后续实现 credential/proxy/sync/plugin/AI 时能被 Trellis 注入。
* [ ] `docs/architecture/00-index.md` 不再重复列出安全设计文档。
* [ ] 文档中数据库默认策略统一为 SQLite。
* [ ] 文档中 OpenList/Alist 表述一致，避免遗漏 OpenList 支持。
* [ ] `docs/architecture/06-roadmap.md` 表达清楚：已完成/部分完成的 Player 工作需要打勾或标记状态；后续 Trellis 任务完成后同步更新 roadmap。
* [ ] 已有 Player 开发成果被定义为“接管当前状态继续推进”，而不是要求迁移时重做。
* [ ] Trellis task 的 `implement.jsonl` 与 `check.jsonl` 指向必要 spec/docs 上下文，便于 sub-agent 获取正确背景。
* [ ] 迁移完成后运行必要的 Trellis 校验或文档一致性检查。

## Definition of Done

* 相关 Trellis spec 已补齐。
* 明显文档不一致已修复。
* Roadmap 更新规则已写入文档或 spec。
* 已有 Player 工作的接管策略已明确。
* Trellis check 通过或遗留问题被清楚记录。
* 变更按项目提交规范提交。

## Technical Approach

先以设计文档为源材料补齐 `.trellis/spec/`，再修复 docs 中影响后续开发判断的不一致，最后配置本任务的 implement/check 上下文并进入执行阶段。迁移只做“规范化和接管”，不重构或重写已有 Player 代码。

## Decision (ADR-lite)

**Context**: 项目已从设计文档阶段进入 Trellis 管理阶段，后续需要让 AI coding agents 自动获得稳定、可检查的项目规则。

**Decision**: `docs/architecture/` 继续作为完整产品设计与路线图；`.trellis/spec/` 承接长期开发规范；`.trellis/tasks/` 承接具体开发任务；roadmap 在任务完成后同步打勾或标记部分完成。

**Consequences**: 后续不会靠单次会话记忆推进项目，而是通过 Trellis spec/task 注入上下文；每次完成实现后需要同步检查是否更新 roadmap/spec。

## Out of Scope

* 不在本任务中重写或大改已有 Player 代码。
* 不在本任务中启动新的 Player/Server 功能开发。
* 不在本任务中改变产品最终范围，例如删除 PT、AI、Hub、插件、多用户权限等规划。
* 不把 Docker 作为本地开发前置条件。

## Technical Notes

* 关键源文档：`CLAUDE.md`、`docs/architecture/00-index.md` 到 `docs/architecture/07-security-design.md`。
* Trellis 工作流：`.trellis/workflow.md`。
* 当前 task：`.trellis/tasks/05-07-trellis/`。
* 迁移时应优先保留现有设计意图：Player independent-first、Server automation engine、Hub static registry、CLI operations interface。

# 实现 Server 插件驱动嵌套分类导航

## Goal

实现 Player 中来源到媒体库再到任意深度插件分类的导航能力，标准媒体库保持固定分类。

## Requirements

- Server 数据源根页只展示媒体库。
- 标准媒体库进入后展示 Server 分类规则分类；插件不可接管。
- 插件媒体库只有显式声明 `hierarchical` 时才能返回分支节点；旧插件保持单层导航。
- 插件可返回任意业务含义的多层节点，Player 不硬编码站点、番剧或地区。
- 支持分支与叶节点混排、面包屑、返回、刷新、分页、搜索和空状态。
- Server 约束最大深度 8、每层最多 100 节点，并防止循环、跨库 token 和无效响应。

## Acceptance Criteria

- [ ] 本地/115 标准媒体库按关联规则显示“外语电影”等分类，并正确过滤作品。
- [ ] Bilibili 至少可展开 `番剧 → 日本` 后显示内容。
- [ ] 第二个插件无需修改 Player 即可定义不同的两级或三级栏目树。
- [ ] v1 单层插件仍可浏览。
- [ ] 面包屑、返回和缓存不会在媒体库之间串联。
- [ ] 无效或恶意节点被拒绝且页面不白屏。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.

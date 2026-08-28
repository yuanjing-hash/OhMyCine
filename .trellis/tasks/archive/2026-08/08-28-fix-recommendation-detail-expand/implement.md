# 实施计划

1. 修正 coverage service 的 collection 初始化并增加 JSON 契约测试。
2. 在 discovery API client 增加集中 DTO 归一化和 null/缺失字段 fixture。
3. 让详情季集组件使用规范化数据，加入局部空态/错误态与重复展开回归。
4. 运行 focused Go/WebUI tests、typecheck、lint、build、完整 Server gate 和 `git diff --check`。

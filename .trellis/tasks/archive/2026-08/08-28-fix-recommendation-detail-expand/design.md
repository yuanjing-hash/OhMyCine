# 技术设计

白屏由 Go nil slice 编码为 `null`，展开后 Vue 模板执行 `episode.library_ids.length` 抛错。修复在两层收口：服务层构造所有 collection 为非 nil 空切片；WebUI discovery client 在 API 边界将旧版或异常 payload 的 null/缺失 collection、文本与日期规范化为安全 DTO。详情模板只读取规范化 DTO，并让季集区块在局部异常时显示空态而非击穿整个路由。

不改变覆盖率算法、订阅或搜索行为。回归直接使用截图对应的“总集数存在、已入库 0、每集 library_ids=null”生产形状。

## Evidence

- `../08-28-optimize-115-pipeline-library/research/web-detail-and-library.md`
- `.trellis/spec/frontend/type-safety.md`
- `.trellis/spec/frontend/server-admin-ui.md`
- `.trellis/spec/guides/cross-layer-thinking-guide.md`

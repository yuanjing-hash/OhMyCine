# Implementation Plan

- [x] 从全局搜索移除隐式 Server discovery，请求改到显式底部 CTA；整理单一清空/取消控件和 capability 状态。
- [x] 抽取共享 `MediaDetailHero`，迁移本地详情并用它重构 Server discovery 详情。
- [x] 增加分步 acquisition wizard、懒加载站点/目标选项、coverage/acquisition 独立状态。
- [x] 在 Tauri 实现安全 SSE command/channel 和取消；在 `ServerDataSource` 增加类型化流式 API。
- [x] 实现 progress reducer、进度条、站点状态、增量结果、稳定排序、取消和失败单站重试。
- [x] 拆分请求超时并删除重复 `source.test()`；增加权限/断连/超时错误映射。
- [x] 补 TypeScript/Rust 验证，运行 typecheck、lint、build、Cargo tests/Clippy，并同步相关 spec/架构状态。

## Validation

- `npm run typecheck`
- `npm run lint`
- `npm run build`
- `npm run verify:server-datasource`
- `cargo fmt --all -- --check`
- `cargo test`
- `cargo clippy --all-targets -- -D warnings`

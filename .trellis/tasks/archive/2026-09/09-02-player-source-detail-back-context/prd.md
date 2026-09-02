# 修复来源详情返回上下文

## Goal

Player 从 Server 媒体库分类进入影片详情后，返回必须恢复真实上一层的媒体库与分类上下文。

## Requirements

- Treat the source page immediately before opening a media detail as the real back destination.
- Preserve the selected data-source library, nested folder/category breadcrumb, raw-source media-library category, view mode, search context, and scroll position when applicable.
- Keep provider/local path identifiers out of the URL. The route may carry only an opaque, process-local context identifier.
- Restore only context owned by the current source. Missing, stale, deleted, or invalid context must safely fall back to the source landing page.
- Keep media detail navigation as a normal history push so the shared top/window/system back controls continue to use one navigation contract.
- Apply the behavior generically to Server and other DataSource implementations; do not special-case the reported library or category.

## Acceptance Criteria

- [x] From `Server 数据源首页 → 115测试盘 → 动画电影 → 影片详情`, Back returns to `115测试盘 → 动画电影` rather than the Server landing page.
- [x] Nested folder and raw scanned-category detail entries restore their own preceding source context.
- [x] Restored pages reload the current item list and restore the saved scroll position after rendering.
- [x] Invalid/cross-source/stale context does not expose paths, loop, or block source-root browsing.
- [x] Existing Home, Favorites, History, Downloads, and direct detail navigation retain normal browser-history behavior.
- [x] Player typecheck, lint, build, and focused source-navigation verification pass.

## Notes

- This is a lightweight Player frontend fix. The browsing snapshot is process-memory navigation state, not persisted provider data.

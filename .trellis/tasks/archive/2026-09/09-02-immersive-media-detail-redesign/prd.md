# 重设计沉浸式媒体详情页并发布 Player Beta

## Goal

隐藏破坏沉浸感的原生横向滚动条，参考 Emby 重构详情页信息层级与横向画廊，同时保留 OhMyCine 玻璃风格并发布 Player Beta。

## Requirements

- Remove all visible native horizontal scrollbars from media-detail rails without removing mouse-wheel, trackpad, drag/touch, or keyboard scrolling.
- Add discoverable glass previous/next controls and soft edge fades for desktop rails; keep touch scrolling direct on mobile.
- Rebalance the detail page using Emby's compact information hierarchy: hero identity/actions first, playback choices next, then large artwork and portrait cast rails, followed by compact technical metadata.
- Preserve OhMyCine's immersive backdrop, dark cinematic gradient, artwork-first layout, glass surfaces, responsive mobile behavior, and existing media actions.
- Reuse one typed horizontal-rail component for stills and people instead of duplicating scroll mechanics in the route view.
- Do not change playback, download, metadata, source, or history contracts.
- Include the previously completed source-detail Back-context fix in the released Player Beta.
- Publish a new Player Beta from the latest `develop` commit after all required checks pass.

## Acceptance Criteria

- [x] Stills and people rails show no operating-system scrollbar at any supported desktop width.
- [x] Desktop users can scroll both rails with visible-on-hover/focus glass arrow controls; disabled edges are represented correctly.
- [x] Cast is displayed as Emby-like portrait cards with names/roles, while stills are larger 16:9 artwork cards.
- [x] A single media version no longer occupies only one third of a mostly empty row; option cards adapt to available content.
- [x] Media information does not create a large empty glass block beside a cramped cast rail.
- [x] Mobile keeps direct touch scrolling and avoids hover-only navigation requirements.
- [x] Focused UI verification, typecheck, lint, build, and applicable Rust check pass.
- [x] A new `vMAJOR.MINOR.PATCH` Player Beta tag/release is published from remote `develop` with packaged assets.

## Notes

- Emby is a hierarchy/reference source, not a visual skin to copy. OhMyCine remains the visual system of record.

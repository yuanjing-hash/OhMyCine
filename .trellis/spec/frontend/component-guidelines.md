# Frontend Component Guidelines

> How Vue components are built in OhMyCine.

---

## Overview

Use Vue 3 Composition API with `<script setup>` and TypeScript. Components should support the Cinema OS design language: dark theme, liquid glass, immersive artwork-first layouts, smooth motion, and hover-revealed chrome where persistent UI would obstruct artwork.

---

## Component Structure

Preferred order:

```vue
<script setup lang="ts">
// imports, props/emits, refs/computed, methods
</script>

<template>
  <!-- markup -->
</template>

<style scoped>
/* only component-specific styles; prefer tokens/classes */
</style>
```

Rules:

- Use `defineProps` and `defineEmits` with explicit TypeScript types.
- Keep heavy logic in composables/services.
- Use computed values for derived display state.
- Clean up timers, listeners, and Tauri event subscriptions on unmount.
- Keep route-level data orchestration in views, not low-level components.

---

## Props and Events

- Props should represent component inputs, not global state copies.
- Use events for user actions: `play`, `select`, `open-detail`, `remove`.
- Do not mutate props directly.
- Prefer semantic prop names: `media`, `source`, `disabled`, `loading`, `progress`.
- Provide empty and loading states for data-source/server-dependent components.

---

## Styling Patterns

Use UnoCSS utilities and CSS variables from the Cinema OS design system.

Required principles:

- Use existing tokens for colors, spacing, radius, blur, shadow, and durations.
- Use liquid-glass classes for elevated chrome and cards.
- Do not rely on Iconify/UnoCSS icon preset classes unless the required icon collection is installed and configured; use explicit inline SVG for critical controls when availability is uncertain.
- Preserve artwork visibility: controls may be hover-revealed/auto-hidden in immersive contexts.
- Avoid hard-coded color values when a token exists.
- Keep desktop-first behavior explicit; mobile adaptations must be separately designed.

---

## Layout Components

- `AppLayout` owns the global shell: data-source sidebar, content area, window chrome.
- `DataSourceSidebar` renders home, ordered configured data sources, optional Server entry, and settings.
- The sidebar bottom add/plus affordance must navigate to data-source management, not a provider-specific setup shortcut.
- Disabled data sources may remain visible for management context, but must look disabled and must not be browsable until re-enabled.
- Server entries must not block local/Emby/Jellyfin/OpenList/Alist/CloudDrive2 browsing when disconnected.
- `WindowChrome` handles frameless window drag/control surfaces and must remain above route/loading content; loading skeletons, hero gradients, and decorative overlays should not intercept pointer events unless they contain real controls.
- Non-home routes must expose a visible back control in the global layout or route chrome. Prefer `router.back()` when `window.history.state?.back` exists; otherwise navigate to `/`.

### Back Navigation Control Contract

- Hide the back control on `/`.
- Show it on `/player`, `/source/:sourceId`, `/settings`, and future non-home views unless the view provides an equivalent route-level back affordance.
- Style it with existing Cinema OS / liquid-glass tokens and hover/active transitions; do not introduce a separate button language.
- Include `aria-label` and `title` so icon-only back controls remain accessible.
- Avoid relying on `window.history.length` alone in Tauri/WebView contexts; Vue Router's `history.state.back` is the signal for an in-app previous route.

### Data Source Management UI Contract

- Settings must expose a clear `管理数据源` entry or section for configured source management.
- The management view lists existing sources with delete, enable/disable, edit, and browse/open actions.
- Empty source state must include a clear add button.
- Add flow starts with a source type selector; unsupported/planned source types should be disabled or clearly unavailable.
- Provider-specific fields are shown dynamically after type selection. For Emby, normal setup uses server URL, account/username, and password fields; it must not ask for an access token as the primary UX.
- Add/Save must authenticate/test first, then persist non-sensitive config and credential references only after success.
- Forms must include Cancel and Add/Save actions and show loading/error states without leaking passwords, tokens, or tokenized URLs.

---

## Media Components

- `HeroCarousel` should prefer items with backdrop/title logo/overview and can be used at data-source roots to provide an artwork-first source landing page; hero backdrops must fill the full hero surface with cover behavior rather than leaving non-artwork gutters.
- `MediaCard` and poster components must handle missing posters gracefully, request/lazy-load appropriately sized images, and avoid showing raw provider type identifiers such as lowercase `folder` as user-facing subtitles.
- Data-source root pages should favor a hero/backdrop section followed by media libraries, continue-watching/latest/recommended rows where the DataSource exposes them.
- Media detail pages should keep OhMyCine styling while surfacing safe provider metadata: poster/backdrop, title, rating/year/runtime/genres, overview, play, neutral version labels, audio/subtitle tracks, stills, collections, similar content, and media info. Do not add provider-specific external-player or trailer actions unless explicitly requested.
- Detail layouts must differ by media type: movie/episode pages may show version/audio/subtitle controls when data exists, while series pages should show seasons and episode selection instead of movie-only playback selectors. Hide empty audio/subtitle/version panels when provider metadata is absent.
- Cloud/local data sources may lack metadata; components should use scraped metadata when available and clean file/folder fallback otherwise.
- Continue-watching components use local playback history first; Server state is enhancement only.

---

## Player Components

- Player controls should overlay video without breaking the immersive view.
- Persistent UI should not obstruct artwork/video unless necessary.
- Keyboard shortcuts must not conflict with text input focus.
- Subtitle/audio/danmaku menus should be accessible from both mouse and keyboard.
- Keep common playback actions on the bottom playback bar: speed, subtitles, audio tracks, queue/playlist, and fullscreen. Do not bury these primary actions inside a generic settings panel.
- When a playback queue has multiple items, the queue/playlist control should be actionable and open a lightweight read-only queue panel/popover with thumbnail, title, brief metadata/overview, current item state, and click-to-switch behavior. Hide or disable it only for no-queue/single-item playback.
- Reserve the playback settings panel for picture/display options such as aspect ratio, fit/fill mode, and future image-processing controls. Do not put recent-play/history, window always-on-top, fullscreen, speed, subtitle, audio, or queue actions in that panel.
- The player fullscreen affordance belongs at the far right of the playback bar and should toggle the whole Player window/fullscreen experience, not only a nested DOM panel.
- Render diagnostics must not appear as a persistent chip in normal playback UI; keep diagnostics behind explicit debug shortcuts or debug-only panels.

### Immersive Player Chrome Contract

- Show playback chrome when the mouse moves, controls receive focus, or the player is paused / has no loaded media.
- Hide playback chrome after about 2.5-3 seconds of pointer inactivity while media is playing and no control interaction is active.
- Keep chrome visible while the window is unfocused, while a control is hovered/focused, and while progress or volume controls are being dragged.
- Aggregate interaction state from parent and child controls; a child ending drag/hover must not hide chrome while the parent container is still hovered or focused.
- Use Cinema OS / liquid-glass tokens for the control bar, buttons, progress, and volume surfaces; do not fall back to native browser-style media controls.
- If embedded video rendering is not complete, show a truthful in-app placeholder instead of letting an external mpv window become the user-visible player.
- When embedded native video rendering is `ready`, do not keep centered placeholder/status panels over the video surface; only hover-revealed liquid-glass chrome should overlay active video.

---

## Accessibility

- Buttons and interactive elements need labels or visible text.
- Keyboard navigation must work for key player controls and dialogs.
- Maintain sufficient contrast on glass surfaces.
- Use semantic roles for dialogs/menus where custom components are used.

---

## Common Mistakes

- Duplicating fetch/playback logic across components.
- Using static sidebars instead of data-source-driven navigation.
- Assuming every media item has poster/backdrop/overview.
- Keeping controls permanently visible over poster art when hover reveal is more appropriate.
- Hiding Server disconnected state instead of showing clear disabled/placeholder UI.
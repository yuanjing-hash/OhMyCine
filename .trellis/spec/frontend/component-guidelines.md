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
- Preserve artwork visibility: controls may be hover-revealed/auto-hidden in immersive contexts.
- Avoid hard-coded color values when a token exists.
- Keep desktop-first behavior explicit; mobile adaptations must be separately designed.

---

## Layout Components

- `AppLayout` owns the global shell: data-source sidebar, content area, window chrome.
- `DataSourceSidebar` renders home, ordered configured data sources, optional Server entry, and settings.
- Server entries must not block local/Emby/Jellyfin/OpenList/Alist/CloudDrive2 browsing when disconnected.
- `WindowChrome` handles frameless window drag/control surfaces.
- Non-home routes must expose a visible back control in the global layout or route chrome. Prefer `router.back()` when `window.history.state?.back` exists; otherwise navigate to `/`.

### Back Navigation Control Contract

- Hide the back control on `/`.
- Show it on `/player`, `/source/:sourceId`, `/settings`, and future non-home views unless the view provides an equivalent route-level back affordance.
- Style it with existing Cinema OS / liquid-glass tokens and hover/active transitions; do not introduce a separate button language.
- Include `aria-label` and `title` so icon-only back controls remain accessible.
- Avoid relying on `window.history.length` alone in Tauri/WebView contexts; Vue Router's `history.state.back` is the signal for an in-app previous route.

---

## Media Components

- `HeroCarousel` should prefer items with backdrop/title logo/overview.
- `MediaCard` and poster components must handle missing posters gracefully.
- Cloud/local data sources may lack metadata; components should use scraped metadata when available and clean file/folder fallback otherwise.
- Continue-watching components use local playback history first; Server state is enhancement only.

---

## Player Components

- Player controls should overlay video without breaking the immersive view.
- Persistent UI should not obstruct artwork/video unless necessary.
- Keyboard shortcuts must not conflict with text input focus.
- Subtitle/audio/danmaku menus should be accessible from both mouse and keyboard.

### Immersive Player Chrome Contract

- Show playback chrome when the mouse moves, controls receive focus, or the player is paused / has no loaded media.
- Hide playback chrome after about 2.5-3 seconds of pointer inactivity while media is playing and no control interaction is active.
- Keep chrome visible while the window is unfocused, while a control is hovered/focused, and while progress or volume controls are being dragged.
- Aggregate interaction state from parent and child controls; a child ending drag/hover must not hide chrome while the parent container is still hovered or focused.
- Use Cinema OS / liquid-glass tokens for the control bar, buttons, progress, and volume surfaces; do not fall back to native browser-style media controls.
- If embedded video rendering is not complete, show a truthful in-app placeholder instead of letting an external mpv window become the user-visible player.

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
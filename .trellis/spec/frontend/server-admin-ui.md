# Server Admin Web UI Contract

> Executable visual and interaction rules for `server/webui`. The Server console is an information-dense administration surface and does not inherit the Player's Cinema OS presentation.

## Scope and Trigger

Apply this contract whenever a task adds or changes a view, layout, dialog, shared control, theme token, or browser-local presentation preference under `server/webui`.

## Theme Signatures

```ts
type Theme = 'light' | 'dark'

readStoredTheme(storage?: Storage): Theme
applyTheme(theme: Theme, root?: HTMLElement): void
setTheme(theme: Theme): void
toggleTheme(): void
```

- Storage key: `omc:server-theme`.
- DOM contract: `<html data-theme="light|dark">` and matching CSS `color-scheme`.
- Initialization occurs before `app.mount()` so the first Vue-rendered frame uses the selected theme.

## Contracts

- `light` is the deterministic first-use default. Do not let `prefers-color-scheme` override it.
- Only `light` and `dark` are valid stored values. Missing, malformed, blocked, or throwing storage falls back safely to `light`.
- Theme selection is browser-local, persists across login/logout and reload, and contains no sensitive data.
- Both authenticated and unauthenticated surfaces expose the same keyboard-accessible theme toggle with a label that states the current theme and target action.
- Colors and elevation use semantic CSS variables such as canvas, surface, border, text, accent, status, focus, and shadow tokens. Page templates may use layout utilities but must not assemble dark-only palettes from `bg-white/*`, `border-white/*`, `text-slate-*`, gradients, glow, or backdrop blur.
- Both palettes use a conventional administration style: opaque surfaces, clear 1px borders, compact spacing, small radii, restrained shadows, stable blue emphasis, and Chinese-first information hierarchy.
- Player-only Cinema OS rules (artwork-first layout, liquid glass, dark-only chrome) do not apply to `server/webui`.
- AI 模型列表使用页面内模态选择器：成功读取后才打开，搜索覆盖模型 ID/显示名称，整行选择只回填而不自动保存；失败保留当前模型。选择器必须使用语义主题 token，并覆盖加载、空列表、无匹配、当前选择、Escape/遮罩/关闭按钮、焦点约束和关闭后焦点恢复。
- Profile 命名编辑器把 `电影 /` 与 `电视剧 /` 显示为不可编辑固定根，只编辑根内模板，并明确自动分类按“媒体类型 → 类型内分类”组织。前端默认值包含完整固定根，但 Server 仍是规范化与权限边界。

## Validation and Error Matrix

| Condition | Required behavior |
| --- | --- |
| No stored value | Apply `light` before Vue mount |
| Stored `light` / `dark` | Apply exactly that theme and matching `color-scheme` |
| Unknown stored value | Ignore it and fall back to `light` |
| localStorage read/write throws | Keep the UI usable with in-memory `light` or current theme |
| Theme changes on a page with form controls/dialogs | Every surface and native control remains legible without a full-root transition flash |
| Small muted/status text | Meet WCAG AA contrast in both palettes |

## Good, Base, and Bad Cases

- Good: a new Storage dialog uses shared `panel`, input, button, status, border, and muted-text tokens and is visually checked in both themes.
- Base: a one-off layout retains utility classes for spacing/grid while every color and shadow comes from semantic tokens.
- Bad: a page looks correct only in dark mode because it uses `text-slate-300`, `bg-white/5`, a cyan glow, or a hard-coded black shadow.

## Tests Required

- Unit-test invalid/missing preference fallback, persistence, root attribute, and `color-scheme` application.
- Run `npm run test`, `npm run typecheck`, `npm run lint`, and `npm run build` from `server/webui`.
- For a visual-system change, browser-smoke an unauthenticated page and an authenticated page in both themes, reload after switching, and assert no console warning/error.
- Search the changed Server UI for accidental gradients, backdrop blur/glass, glow, and dark-only color utilities; review every intentional match.
- Check keyboard focus, accessible toggle labels, disabled/error/warning/success states, and responsive drawer/dialog presentation.

## Wrong vs Correct

Wrong:

```vue
<section class="rounded-5 border border-white/10 bg-white/5 text-slate-300 backdrop-blur-xl">
```

Correct:

```vue
<section class="panel text-muted">
```

Wrong:

```ts
const initialTheme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
```

Correct:

```ts
const initialTheme = readStoredTheme() // invalid or absent -> light
applyTheme(initialTheme)
```

## Scenario: Search Scope Memory and Bounded Transfer Deletion UI

### 1. Scope / Trigger

- Trigger: opening aggregate direct search or poster-detail resource search, restoring Site selections, rendering Transfer progress, or previewing deletion of terminal Transfer records/files.

### 2. Signatures

```ts
type SearchSiteOption = {
  id: number; name: string; site_type: 'pt' | 'bt';
  health_status: string; searchable: boolean; reason?: string
}

localStorage key: versioned search-site selection containing IDs only
previewTransferDeletion(id, scope, signal?: AbortSignal)
confirmTransferDeletion(id, token, signal?: AbortSignal)
```

### 3. Contracts

- Aggregate searches open one Site selector before their first request. Fixed `site_id` routes remain locked single-Site flows and do not show a redundant selector.
- First use selects every currently searchable option. Later visits intersect saved IDs with current options; newly added or re-enabled Sites are not silently selected. “全选” explicitly replaces the scope with every current searchable Site; zero selection disables confirmation.
- Persist only numeric Site IDs. Never persist safe-option reasons as authority, Site configuration, Base URLs, Cookie/passkey flags, solver settings, search claims or results in this preference.
- Retry, page changes, JSON/SSE fallback and TMDB multilingual search reuse the same in-memory selection. An old session without a stored explicit scope is discarded rather than replayed as all Sites.
- Transfer progress renders Server phases literally. Normal directory preparation/move/rename is not labeled as risk control; only `risk_backoff` displays the provider-risk message and retry countdown.
- Deletion preview has a close-bound AbortController and a 50-second client timeout around the Server's 45-second provider deadline. Timeout/cancel/error restores interactive controls, preserves the task and offers retry; it never changes status locally or consumes a token.
- Preview displays `source_missing`, `source_detached` and `library_missing` separately. Detached source items are described as retained, not failed deletion candidates.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| No saved selection | Select all currently searchable Sites |
| Saved selection plus newly added Site | Restore only saved IDs; leave the new Site unchecked until explicit full select |
| Saved IDs are all gone/disabled | Show zero selected, disable Search and ask the user to choose |
| Fixed single-Site route | Search that Site directly and keep the ID through retry/page changes |
| Phase is normal mkdir/move/rename | Show the real phase without risk-control wording |
| Phase is `risk_backoff` | Show bounded retry countdown and provider risk wording |
| Preview times out, dialog closes or navigation changes | Abort request, clear loading state and keep local/Server task state unchanged |
| Preview reports detached items | Show retained detached count and never imply they will be deleted |

### 5. Good / Base / Bad Cases

- Good: first search starts with all enabled searchable Sites checked; after the user unchecks two, refresh restores the exact remaining IDs and a later new Site stays unchecked.
- Base: a Site card opens a locked Mikan search without an extra dialog.
- Bad: automatically append newly added Sites to saved scope, drop scope on SSE retry, or leave “正在核对” spinning forever after Abort/timeout.

### 6. Tests Required

- Unit/component tests cover first-use all, persisted intersection, new-Site exclusion, explicit all/none, zero-selection disable, keyboard/Escape/focus behavior and locked single-Site bypass.
- Search integration tests assert the same `site_ids` on initial JSON/SSE, retry, pagination and TMDB multilingual requests and reject unscoped legacy restoration.
- Transfer UI tests cover every phase label, risk countdown only for `risk_backoff`, missing/detached counts, Abort on close/unmount, 50-second timeout and retryable error recovery.

### 7. Wrong vs Correct

#### Wrong

```ts
selected.value = options.value.map(item => item.id) // silently adds every new Site
await previewTransferDeletion(id, scope) // no abort or deadline
```

#### Correct

```ts
selected.value = firstUse ? selectableIDs : savedIDs.filter(id => selectable.has(id))
const controller = new AbortController()
await previewTransferDeletion(id, scope, controller.signal)
```

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

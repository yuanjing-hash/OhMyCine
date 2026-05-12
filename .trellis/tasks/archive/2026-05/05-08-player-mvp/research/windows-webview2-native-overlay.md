# Research: Windows WebView2 Native Video Surface and HTML Overlay Composition

- **Query**: Research how Windows WebView2/Tauri apps handle native child HWND or OpenGL video surfaces together with clickable HTML/WebView overlays. Focus on z-order, transparency, hit testing, child windows vs popup/top-level overlays, and known limitations.
- **Scope**: mixed
- **Date**: 2026-05-08

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src-tauri/src/mpv/surface.rs` | Cross-platform native render-surface facade; Windows delegates to `platform::windows::WindowsRenderSurface`; non-Windows currently returns unsupported render states. |
| `player/src-tauri/src/mpv/platform/windows.rs` | Current Windows implementation creates a child `HWND` under the Tauri window and renders libmpv through WGL/OpenGL into that child window. |
| `.trellis/spec/frontend/hook-guidelines.md` | Relevant frontend/Tauri IPC contract; says platform-specific assumptions must stay explicit. |

### Code Patterns

- Current implementation uses a native **child window** for video, not an HTML `<canvas>`/WebGL layer:

  ```rust
  // player/src-tauri/src/mpv/platform/windows.rs:421-425
  CreateWindowExW(
      WS_EX_NOACTIVATE | WS_EX_TRANSPARENT,
      class_name.as_ptr(),
      title.as_ptr(),
      WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS | WS_CLIPCHILDREN,
  ```

- The child surface is positioned above sibling child windows with `HWND_TOP`:

  ```rust
  // player/src-tauri/src/mpv/platform/windows.rs:455-462
  SetWindowPos(
      hwnd as HWND,
      HWND_TOP,
      x,
      y,
      width,
      height,
      SWP_NOACTIVATE | SWP_NOOWNERZORDER | SWP_SHOWWINDOW,
  )
  ```

- Mouse hit testing is set to pass through the native video child `HWND`:

  ```rust
  // player/src-tauri/src/mpv/platform/windows.rs:537-540
  if msg == WM_NCHITTEST {
      // Keep HTTRANSPARENT as a best-effort pass-through. The preferred composition keeps the
      // WebView above this full-bleed OpenGL child surface so Vue chrome receives input.
      return HTTRANSPARENT as LRESULT;
  }
  ```

- The render path is OpenGL/WGL with a swapchain-like child-window DC:
  - Pixel format: `PFD_DRAW_TO_WINDOW | PFD_SUPPORT_OPENGL | PFD_DOUBLEBUFFER` at `player/src-tauri/src/mpv/platform/windows.rs:599-604`.
  - Frame presentation: `SwapBuffers(handles.hdc)` at `player/src-tauri/src/mpv/platform/windows.rs:338-340`.

### Windows / WebView2 Composition Behavior

#### 1. Windowed WebView2 is itself an HWND, and HWND z-order is outside normal HTML/CSS stacking

Microsoft documents WebView2 **windowed hosting** as content hosted directly in a window, i.e. an `HWND`. In this mode the OS/framework handles much of input, focus, accessibility, and child-window behavior. This means the WebView participates in Win32 window ordering, not only CSS stacking.

Implication for OhMyCine: if the libmpv video is another sibling child `HWND`, then the visible ordering between video and WebView is governed by Win32 child-window z-order. CSS `z-index` inside the WebView can only order HTML elements relative to each other; it cannot make HTML controls draw above a separate native sibling `HWND` that the OS has placed above the WebView.

#### 2. WebView2 visual/composition hosting exists, but requires the host app to own rendering and input plumbing

Microsoft distinguishes:

- **Windowed hosting**: easiest; WebView2 owns its `HWND` behavior and input.
- **Window-to-Visual hosting**: uses windowed hosting plus visual integration.
- **Visual hosting**: the app takes spatial input and sends it to WebView2; provides more granular composition control but requires explicit rendering/window-management/input handling.

`ICoreWebView2CompositionController` is the Win32 interface for visual hosting. Its reference states it extends `ICoreWebView2Controller` to support visual hosting, exposes `RootVisualTarget`, and requires the host to forward input-related behavior such as cursor handling.

Implication: a robust “HTML overlay over native video” architecture on Windows generally needs a common compositor path, such as DirectComposition/visual hosting, rather than two unrelated sibling HWNDs. That is a larger integration than normal Tauri/Wry windowed WebView2 hosting.

#### 3. Transparent WebView2 background does not make a lower native child window automatically composited through in all hosting modes

Microsoft’s `ICoreWebView2Controller::IsVisible` docs note that setting `IsVisible = FALSE` makes WebView2 transparent/not rendered, but does not hide the containing `HWND`. WebView2 transparency/background handling is host-mode-specific and is not equivalent to CSS alpha blending over arbitrary sibling child HWNDs.

Implication: relying on WebView CSS `background: transparent` to show an OpenGL child `HWND` behind it is not a portable guarantee unless the WebView host has explicit transparent-background/composition support and the native surface is in the same composition pipeline.

#### 4. Win32 child windows, popups, and top-level/owned windows have separate z-order rules

Microsoft Win32 docs define:

- Child windows are clipped/ordered inside a parent and have child z-order relationships.
- Pop-up/overlapped windows are top-level windows with separate ordering rules.
- `WS_CHILD` cannot be combined with `WS_POPUP`.
- `WS_CLIPCHILDREN` excludes child areas from parent painting; `WS_CLIPSIBLINGS` clips child windows relative to each other.

Implication: a native video child `HWND` and WebView child `HWND` can fight for z-order. A top-level/owned transparent overlay window can be placed above the main window, but then it has separate focus, activation, DPI, move/resize, fullscreen, accessibility, and input-forwarding concerns.

#### 5. Hit testing can pass input through layered/native windows, but input pass-through does not solve draw ordering

Win32 layered-window docs state hit testing is based on shape/transparency; zero-alpha/color-keyed regions can let mouse messages through, and `WS_EX_TRANSPARENT` can pass mouse events to windows underneath. The current code also returns `HTTRANSPARENT` for `WM_NCHITTEST`.

Implication: `WS_EX_TRANSPARENT` / `HTTRANSPARENT` helps avoid the video child stealing clicks, but it does not make HTML draw over that video child if Win32 z-order places the video above the WebView.

#### 6. Known WebView2 framework limitation: native WebView often appears topmost over framework controls

A WebView2Feedback issue, “When using Webview2 in WPF, unable to overlay WPF controls on the Webview,” reports that WebView2 is topmost and WPF controls are hidden behind it; setting WPF ZIndex does not help. That issue is the inverse direction of OhMyCine’s current risk, but it demonstrates the same class of limitation: framework/UI-layer z-order does not necessarily overlay native WebView/child-window content.

### External References

- [Microsoft Learn: Windowed vs. Visual hosting of WebView2](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/windowed-vs-visual-hosting) — Explains windowed WebView2 as `HWND` hosting and visual hosting as app-managed input/composition.
- [Microsoft Learn: `ICoreWebView2CompositionController`](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2compositioncontroller) — Win32 API for WebView2 visual hosting; exposes `RootVisualTarget` and composition-controller behavior.
- [Microsoft Learn: `ICoreWebView2Controller`](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2controller) — Documents controller bounds, parent `HWND`, visibility, focus, and input behavior.
- [Microsoft Learn: Window Features](https://learn.microsoft.com/en-us/windows/win32/winmsg/window-features) — Describes child windows, top-level windows, z-order, layered windows, transparency, and hit testing.
- [Microsoft Learn: Window Styles](https://learn.microsoft.com/en-us/windows/win32/winmsg/window-styles) — Defines `WS_CHILD`, `WS_CLIPCHILDREN`, `WS_CLIPSIBLINGS`, and incompatibility of `WS_CHILD` with `WS_POPUP`.
- [MicrosoftEdge/WebView2Feedback#286](https://github.com/MicrosoftEdge/WebView2Feedback/issues/286) — User-reported limitation where WPF controls cannot overlay WebView2 despite XAML `ZIndex`, showing native WebView z-order constraints.
- [MicrosoftEdge/WebView2Feedback#3439](https://github.com/MicrosoftEdge/WebView2Feedback/issues/3439) — Request around using `CoreWebView2CompositionController` in WinUI 3; illustrates that composition-controller access is not uniformly surfaced by all frameworks.
- [Flutter issue #108486: PlatformViews on Windows using DirectComposition](https://github.com/flutter/flutter/issues/108486) — Cross-framework discussion of using DirectComposition to integrate HWND/native visuals into a retained UI tree; relevant as an architectural reference for native-video + UI composition.
- [mpv manual: Embedding into other applications](https://mpv.io/manual/master/#embedding-into-other-applications) — General reference for libmpv embedding; OhMyCine currently uses libmpv render API with an OpenGL child window.

### Related Specs

- `.trellis/spec/frontend/hook-guidelines.md` — Tauri IPC and composable guidance; explicitly requires platform-specific assumptions to remain explicit.

## Caveats / Not Found

- I did not find an existing OhMyCine spec dedicated to Windows WebView2/native HWND overlay composition.
- Tauri/Wry public docs were not found to provide a first-class “HTML overlay above arbitrary native child HWND video” contract. Tauri on Windows normally uses WebView2 windowed hosting through Wry; deeper WebView2 composition hosting would require lower-level Windows integration beyond ordinary Vue/CSS layering.
- The current code comment says the preferred composition keeps the WebView above the OpenGL child surface, but the code sets the video child `HWND` to `HWND_TOP`. Whether the WebView is actually above video at runtime depends on the exact Tauri/Wry WebView parent/child HWND hierarchy and subsequent z-order operations.

## Concise Recommendation Summary

For clickable Vue/HTML controls over Windows libmpv video, do not rely on CSS `z-index`, transparent WebView backgrounds, `WS_EX_TRANSPARENT`, or `HTTRANSPARENT` alone. Those affect HTML stacking or input routing, not reliable Win32 cross-HWND draw ordering. The most predictable short-term path is to keep the video child `HWND` below the WebView and verify z-order on real Windows WebView2/Tauri builds; if that cannot be made stable, use a separate owned/top-level transparent overlay window or move toward a shared DirectComposition/WebView2 visual-hosting design where video and WebView are composed in one native pipeline.
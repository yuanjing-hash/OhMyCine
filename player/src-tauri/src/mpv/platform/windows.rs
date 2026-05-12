//! Windows transparent-overlay mpv HWND backend.
//!
//! Creates a borderless `WS_POPUP` video underlay window for mpv and keeps the transparent
//! Tauri/WebView window above it. CSS transparency only works when the native Tauri/WebView window
//! itself is transparent, so the app window is configured as transparent and the Player route keeps
//! the video region's DOM background transparent. mpv renders into the underlay via its `wid` option
//! with `vo=gpu-next` + `hwdec=auto-safe`, owning its own D3D11 swap chain. No OpenGL, WGL, render
//! thread, or `mpv_render_context_*` is involved.
//!
//! The mpv underlay is deliberately not a Win32 owned window, because owned top-level windows are
//! always above their owner and would occlude the WebView controls. Instead, it is a no-activate
//! tool window (`WS_EX_TOOLWINDOW`, no `WS_EX_APPWINDOW`) that is pinned immediately behind the
//! Tauri HWND and explicitly follows owner move/resize/minimize/close events. This preserves the
//! single-window UX while allowing Vue controls to remain clickable in the transparent overlay.

use std::{
    ptr,
    sync::{Arc, Mutex},
};

use raw_window_handle::{HasWindowHandle, RawWindowHandle};
use windows_sys::Win32::{
    Foundation::{HWND, LPARAM, LRESULT, RECT, WPARAM},
    Graphics::Gdi::ClientToScreen,
    System::LibraryLoader::GetModuleHandleW,
    UI::WindowsAndMessaging::{
        CreateWindowExW, DefWindowProcW, DestroyWindow, GetClientRect, IsIconic, IsZoomed,
        LoadCursorW, RegisterClassW, SetWindowPos, ShowWindow, CS_HREDRAW, CS_VREDRAW, IDC_ARROW,
        SWP_NOACTIVATE, SWP_NOMOVE, SWP_NOSIZE, SWP_SHOWWINDOW, SW_HIDE, SW_SHOW, WNDCLASSW,
        WS_EX_NOACTIVATE, WS_EX_TOOLWINDOW, WS_POPUP,
    },
};

use crate::mpv::{
    render::{webview_background_transparency_applied, MpvRenderDiagnostics},
    surface::{
        OwnerWindowEvent, RenderSurfaceBounds, RenderSurfaceBoundsSnapshot, RenderSurfaceSnapshot,
        ZOrderStrategy,
    },
};

const CLASS_NAME: &str = "OhMyCineMpvSurface";

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

/// Diagnostic fields surfaced to the frontend diagnostic panel (Ctrl+Shift+D) and the log file.
type RenderDiagnostics = MpvRenderDiagnostics;

fn update_diagnostics(
    diagnostics: &Arc<Mutex<RenderDiagnostics>>,
    f: impl FnOnce(&mut RenderDiagnostics),
) {
    if let Ok(mut d) = diagnostics.lock() {
        f(&mut d);
    }
}

// ---------------------------------------------------------------------------
// WindowsRenderSurface
// ---------------------------------------------------------------------------

pub struct WindowsRenderSurface {
    /// Reference to the Tauri window; used for `run_on_main_thread` scheduling.
    window: tauri::Window,
    /// Tauri main HWND, used as the owner for the mpv HWND.
    owner: HWND,
    /// The video underlay HWND that mpv draws into via `wid`.
    mpv_hwnd: HWND,
    /// Last known bounds in physical screen pixels (x, y, w, h).
    bounds: Option<RenderSurfaceBoundsSnapshot>,
    /// Whether `mark_mpv_ready` has been called (mpv initialized with wid successfully).
    mpv_ready: bool,
    /// Shared diagnostic state.
    diagnostics: Arc<Mutex<RenderDiagnostics>>,
    /// Path to the log file, if resolved.
    log_path: Option<std::path::PathBuf>,
}

unsafe impl Send for WindowsRenderSurface {}

impl WindowsRenderSurface {
    pub fn create(window: &tauri::Window) -> Result<Self, String> {
        let owner = parent_hwnd(window)?;

        register_window_class()?;

        let log_path = resolve_log_path(window);
        if let Some(path) = log_path.as_ref() {
            append_log_line(
                path,
                "=== OhMyCine mpv transparent-overlay underlay session started ===",
            );
        }

        let webview_transparency_applied = webview_background_transparency_applied();
        let diagnostics = Arc::new(Mutex::new(RenderDiagnostics {
            owner_hwnd_attached: true,
            overlay_window_transparent: webview_transparency_applied,
            webview_background_transparent_applied: webview_transparency_applied,
            taskbar_ignored: true,
            log_file: log_path
                .as_ref()
                .and_then(|p| p.to_str().map(str::to_owned)),
            ..RenderDiagnostics::default()
        }));
        log_diagnostics(&log_path, &format!("owner hwnd={owner:?}"));

        // Create the mpv video underlay HWND on the main thread. Win32 window creation and
        // SetWindowPos must happen on the thread that owns the message pump.
        let mpv_hwnd = create_video_underlay_on_main_thread(window)?;
        update_diagnostics(&diagnostics, |d| {
            d.mpv_hwnd_created = true;
        });
        log_diagnostics(
            &log_path,
            &format!("mpv video underlay hwnd created: {mpv_hwnd:?}"),
        );

        Ok(Self {
            window: window.clone(),
            owner,
            mpv_hwnd,
            bounds: None,
            mpv_ready: false,
            diagnostics,
            log_path,
        })
    }

    /// Returns the HWND as an integer string for `mpv_set_option_string("wid", ...)`.
    pub fn mpv_wid(&self) -> String {
        (self.mpv_hwnd as isize).to_string()
    }

    /// Called by `MpvPlayer` after `mpv_initialize` succeeds with the `wid` option. Reveals the
    /// video underlay and places it immediately behind the transparent Tauri/WebView overlay.
    pub fn mark_mpv_ready(&mut self) {
        self.mpv_ready = true;
        let owner = self.owner as isize;
        let hwnd = self.mpv_hwnd as isize;
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();

        if let Err(err) = self.window.run_on_main_thread(move || {
            let owner = owner as HWND;
            let hwnd = hwnd as HWND;
            unsafe {
                ShowWindow(hwnd, SW_SHOW);
                // `hWndInsertAfter = owner` places the underlay directly below the Tauri overlay
                // in the regular top-level z-order. This is the key difference from the previous
                // HWND_TOPMOST/owned-window model, which necessarily covered the WebView controls.
                SetWindowPos(
                    hwnd,
                    owner,
                    0,
                    0,
                    0,
                    0,
                    SWP_NOACTIVATE | SWP_NOMOVE | SWP_NOSIZE,
                );
            }
            let webview_transparency_applied = webview_background_transparency_applied();
            update_diagnostics(&diagnostics, |d| {
                d.mpv_hwnd_shown = true;
                d.overlay_window_transparent = webview_transparency_applied;
                d.webview_background_transparent_applied = webview_transparency_applied;
                d.z_order_underlay_applied = true;
                d.mpv_wid_accepted = true;
                d.mpv_initialized = true;
                d.last_sync_result = "shown-underlay".to_string();
            });
            log_diagnostics(
                &log_path,
                "mpv HWND shown and placed directly behind transparent Tauri overlay",
            );
        }) {
            log::warn!("failed to schedule mark_mpv_ready on main thread: {err}");
        }
    }

    /// Convert frontend-reported CSS/logical bounds to physical screen coordinates and reposition
    /// the mpv video underlay behind the Tauri main window's client area.
    ///
    /// This method is fire-and-forget: it stores bounds eagerly and schedules the Win32
    /// `SetWindowPos` work on the main thread without blocking. The previous `sync_channel`
    /// pattern blocked the calling (async) thread while waiting for the main thread, which
    /// deadlocked when `on_window_event` (main thread) tried to acquire the `MpvState` mutex
    /// that the async thread already held.
    pub fn set_bounds(&mut self, bounds: RenderSurfaceBounds) -> Result<(), String> {
        if !self.mpv_ready {
            return Ok(());
        }

        let scale = bounds.scale_factor;
        let owner = self.owner as isize;
        let hwnd = self.mpv_hwnd as isize;
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();

        // Transparent-overlay model: keep video full-bleed behind the Vue/WebView overlay. Legacy
        // top/bottom occlusion values are intentionally ignored so controls are not protected by
        // shrinking video; they are protected because the WebView overlay is above the underlay.

        // Store bounds eagerly so `sync_geometry_from_owner` can use them even before the
        // async Win32 positioning executes on the main thread.
        self.bounds = Some(RenderSurfaceBoundsSnapshot {
            x: scaled_i32(bounds.x, scale),
            y: scaled_i32(bounds.y, scale),
            width: scaled_i32(bounds.width, scale),
            height: scaled_i32(bounds.height, scale),
            scale_factor: scale,
        });

        // Fire-and-forget: schedule Win32 positioning on the main thread without blocking.
        self.window
            .run_on_main_thread(move || {
                let owner = owner as HWND;
                let hwnd = hwnd as HWND;
                // Read the Tauri main window's client area origin in screen coordinates.
                let mut client_origin = RECT { left: 0, top: 0, right: 0, bottom: 0 };
                if unsafe { ClientToScreen(owner, &mut client_origin as *mut RECT as *mut _) } == 0 {
                    update_diagnostics(&diagnostics, |d| {
                        d.geometry_following = false;
                        d.last_sync_result = "ClientToScreen failed for Tauri owner HWND".to_string();
                    });
                    log_diagnostics(&log_path, "set_bounds: ClientToScreen failed");
                    return;
                }

                // ClientToScreen on a point (0,0) gives the client-area origin in screen coords.
                let screen_x = client_origin.left + scaled_i32(bounds.x, scale);
                let screen_y = client_origin.top + scaled_i32(bounds.y, scale);
                let width = scaled_i32(bounds.width, scale).max(1);
                let height = scaled_i32(bounds.height, scale).max(1);

                let ok = unsafe {
                    SetWindowPos(
                        hwnd,
                        owner,
                        screen_x,
                        screen_y,
                        width,
                        height,
                        SWP_NOACTIVATE,
                    )
                };

                if ok == 0 {
                    update_diagnostics(&diagnostics, |d| {
                        d.geometry_following = false;
                        d.last_sync_result = "SetWindowPos failed".to_string();
                    });
                    log_diagnostics(&log_path, "set_bounds: SetWindowPos failed");
                    return;
                }

                update_diagnostics(&diagnostics, |d| {
                    d.geometry_following = true;
                    d.z_order_underlay_applied = true;
                    d.last_sync_result = "ok".to_string();
                    d.scale = scale;
                    d.last_bounds = Some(format!("{width}x{height} at ({screen_x},{screen_y})"));
                    d.syncs = d.syncs.saturating_add(1);
                });
                log_diagnostics(
                    &log_path,
                    &format!(
                        "set_bounds: underlay screen=({screen_x},{screen_y}) size={width}x{height} scale={scale:.2}"
                    ),
                );
            })
            .map_err(|err| format!("failed to schedule set_bounds on main thread: {err}"))?;

        Ok(())
    }

    /// Accept the strategy for backwards compatibility. The transparent-overlay underlay is the
    /// only active model; all legacy variants are neutralized to it.
    pub fn set_strategy(&mut self, strategy: ZOrderStrategy) -> Result<(), String> {
        log_diagnostics(
            &self.log_path,
            &format!("set_strategy noted: {strategy:?} (transparent-overlay underlay is the only active backend)"),
        );
        Ok(())
    }

    pub fn snapshot(&self) -> RenderSurfaceSnapshot {
        RenderSurfaceSnapshot {
            bounds: self.bounds,
            clear_color: [0.0, 0.0, 0.0, 0.0],
            diagnostics: self.diagnostics.lock().ok().map(|d| d.clone()),
        }
    }

    /// Forward owner-window lifecycle events from Tauri's `on_window_event` to keep the mpv HWND
    /// synchronized with the Tauri main window.
    pub fn on_owner_window_event(&mut self, event: OwnerWindowEvent) {
        match event {
            OwnerWindowEvent::Destroyed => {
                self.hide_and_destroy();
                return;
            }
            OwnerWindowEvent::Minimized => {
                self.hide_mpv_hwnd();
                return;
            }
            OwnerWindowEvent::Restored => {
                self.show_mpv_hwnd();
            }
            OwnerWindowEvent::FocusChanged(focused) => {
                log_diagnostics(&self.log_path, &format!("owner focus changed: {focused}"));
            }
            _ => {}
        }

        // For Moved, Resized, ScaleFactorChanged, FocusChanged: re-sync geometry.
        self.sync_geometry_from_owner();
    }

    // -----------------------------------------------------------------------
    // Private helpers
    // -----------------------------------------------------------------------

    /// Re-read the Tauri owner's live state (iconic, zoomed, rect, DPI) and reposition the mpv
    /// HWND accordingly. This is the core of the geometry-following loop.
    fn sync_geometry_from_owner(&mut self) {
        if !self.mpv_ready {
            return;
        }

        let owner = self.owner as isize;
        let hwnd = self.mpv_hwnd as isize;
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();
        let current_bounds = self.bounds;

        if let Err(err) = self.window.run_on_main_thread(move || {
            let owner = owner as HWND;
            let hwnd = hwnd as HWND;
            // If the owner is minimized, hide the mpv HWND.
            if unsafe { IsIconic(owner) } != 0 {
                unsafe { ShowWindow(hwnd, SW_HIDE) };
                update_diagnostics(&diagnostics, |d| {
                    d.fullscreen_state = "minimized".to_string();
                    d.geometry_following = true;
                    d.last_sync_result = "hidden-minimized".to_string();
                });
                return;
            }

            let is_maximized = unsafe { IsZoomed(owner) } != 0;

            // Read the owner's client area in screen coordinates.
            let mut client_origin = RECT {
                left: 0,
                top: 0,
                right: 0,
                bottom: 0,
            };
            if unsafe { ClientToScreen(owner, &mut client_origin as *mut RECT as *mut _) } == 0 {
                update_diagnostics(&diagnostics, |d| {
                    d.geometry_following = false;
                    d.last_sync_result = "ClientToScreen failed".to_string();
                });
                return;
            }

            let mut client_rect = RECT {
                left: 0,
                top: 0,
                right: 0,
                bottom: 0,
            };
            if unsafe { GetClientRect(owner, &mut client_rect) } == 0 {
                update_diagnostics(&diagnostics, |d| {
                    d.geometry_following = false;
                    d.last_sync_result = "GetClientRect failed".to_string();
                });
                return;
            }

            let client_w = client_rect.right - client_rect.left;
            let client_h = client_rect.bottom - client_rect.top;

            // If we have bounds from the frontend, use them full-bleed; otherwise fill the client
            // area. The previous top/bottom occlusion model is intentionally not applied here.
            let (x, y, w, h, scale) = if let Some(b) = current_bounds {
                let screen_x = client_origin.left + b.x;
                let screen_y = client_origin.top + b.y;
                let w = b.width.max(1);
                let h = b.height.max(1);
                (screen_x, screen_y, w, h, b.scale_factor)
            } else {
                (
                    client_origin.left,
                    client_origin.top,
                    client_w.max(1),
                    client_h.max(1),
                    1.0,
                )
            };

            let ok =
                unsafe { SetWindowPos(hwnd, owner, x, y, w, h, SWP_NOACTIVATE | SWP_SHOWWINDOW) };

            let state_str = if is_maximized { "maximized" } else { "normal" };

            if ok == 0 {
                update_diagnostics(&diagnostics, |d| {
                    d.geometry_following = false;
                    d.fullscreen_state = state_str.to_string();
                    d.last_sync_result = "SetWindowPos failed".to_string();
                });
                log_diagnostics(&log_path, "sync_geometry: SetWindowPos failed");
            } else {
                update_diagnostics(&diagnostics, |d| {
                    d.geometry_following = true;
                    d.z_order_underlay_applied = true;
                    d.fullscreen_state = state_str.to_string();
                    d.last_sync_result = "ok".to_string();
                    d.scale = scale;
                    d.last_bounds = Some(format!("{w}x{h} at ({x},{y})"));
                    d.syncs = d.syncs.saturating_add(1);
                });
            }
        }) {
            log::warn!("failed to schedule sync_geometry on main thread: {err}");
        }
    }

    fn hide_mpv_hwnd(&mut self) {
        let hwnd = self.mpv_hwnd as isize;
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();
        if let Err(err) = self.window.run_on_main_thread(move || {
            let hwnd = hwnd as HWND;
            unsafe { ShowWindow(hwnd, SW_HIDE) };
            update_diagnostics(&diagnostics, |d| {
                d.last_sync_result = "hidden".to_string();
            });
            log_diagnostics(&log_path, "mpv HWND hidden");
        }) {
            log::warn!("failed to schedule hide_mpv_hwnd: {err}");
        }
    }

    fn show_mpv_hwnd(&mut self) {
        if !self.mpv_ready {
            return;
        }
        let owner = self.owner as isize;
        let hwnd = self.mpv_hwnd as isize;
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();
        if let Err(err) = self.window.run_on_main_thread(move || {
            let owner = owner as HWND;
            let hwnd = hwnd as HWND;
            unsafe {
                ShowWindow(hwnd, SW_SHOW);
                SetWindowPos(
                    hwnd,
                    owner,
                    0,
                    0,
                    0,
                    0,
                    SWP_NOACTIVATE | SWP_NOMOVE | SWP_NOSIZE,
                );
            }
            update_diagnostics(&diagnostics, |d| {
                d.mpv_hwnd_shown = true;
                d.z_order_underlay_applied = true;
                d.last_sync_result = "shown-underlay".to_string();
            });
            log_diagnostics(&log_path, "mpv HWND shown behind transparent Tauri overlay");
        }) {
            log::warn!("failed to schedule show_mpv_hwnd: {err}");
        }
    }

    /// Cleanup: hide, destroy, and unregister. Called on Drop and on `OwnerWindowEvent::Destroyed`.
    fn hide_and_destroy(&mut self) {
        if self.mpv_hwnd.is_null() {
            return;
        }
        let hwnd = std::mem::replace(&mut self.mpv_hwnd, ptr::null_mut()) as isize;
        let window = self.window.clone();
        let diagnostics = Arc::clone(&self.diagnostics);
        let log_path = self.log_path.clone();

        if let Err(err) = window.run_on_main_thread(move || {
            let hwnd = hwnd as HWND;
            unsafe {
                ShowWindow(hwnd, SW_HIDE);
                DestroyWindow(hwnd);
            }
            update_diagnostics(&diagnostics, |d| {
                d.mpv_hwnd_created = false;
                d.mpv_hwnd_shown = false;
                d.last_sync_result = "destroyed".to_string();
            });
            log_diagnostics(&log_path, "mpv HWND destroyed");
        }) {
            log::warn!("failed to schedule mpv HWND destruction: {err}");
            // Best-effort: try direct destroy anyway.
            let hwnd = hwnd as HWND;
            unsafe {
                ShowWindow(hwnd, SW_HIDE);
                DestroyWindow(hwnd);
            }
        }
    }
}

impl Drop for WindowsRenderSurface {
    fn drop(&mut self) {
        self.hide_and_destroy();
    }
}

// ---------------------------------------------------------------------------
// Win32 helpers
// ---------------------------------------------------------------------------

/// Get the Tauri main window HWND as the owner for our mpv surface.
fn parent_hwnd(window: &tauri::Window) -> Result<HWND, String> {
    let handle = window
        .window_handle()
        .map_err(|err| format!("failed to get Tauri window handle: {err}"))?;

    match handle.as_raw() {
        RawWindowHandle::Win32(handle) => Ok(handle.hwnd.get() as HWND),
        _ => Err("current Tauri window is not backed by a Win32 HWND".to_string()),
    }
}

/// Register the window class once. The `WndProc` just calls `DefWindowProcW`; no custom hit-test
/// is needed because the mpv HWND is placed behind the transparent Tauri/WebView overlay.
fn register_window_class() -> Result<(), String> {
    static REGISTER_RESULT: std::sync::OnceLock<Result<(), String>> = std::sync::OnceLock::new();
    REGISTER_RESULT
        .get_or_init(|| {
            let class_name = wide_null(CLASS_NAME);
            let hinstance = unsafe { GetModuleHandleW(ptr::null()) };
            let wnd_class = WNDCLASSW {
                style: CS_HREDRAW | CS_VREDRAW,
                lpfnWndProc: Some(mpv_surface_wnd_proc),
                cbClsExtra: 0,
                cbWndExtra: 0,
                hInstance: hinstance,
                hIcon: ptr::null_mut(),
                hCursor: unsafe { LoadCursorW(ptr::null_mut(), IDC_ARROW) },
                hbrBackground: ptr::null_mut(),
                lpszMenuName: ptr::null(),
                lpszClassName: class_name.as_ptr(),
            };

            let atom = unsafe { RegisterClassW(&wnd_class) };
            if atom == 0 {
                Err("failed to register OhMyCineMpvSurface window class".to_string())
            } else {
                Ok(())
            }
        })
        .clone()
}

/// Minimal WndProc. mpv handles its own rendering inside the `wid` window; we just forward to
/// `DefWindowProcW`.
unsafe extern "system" fn mpv_surface_wnd_proc(
    hwnd: HWND,
    msg: u32,
    wparam: WPARAM,
    lparam: LPARAM,
) -> LRESULT {
    unsafe { DefWindowProcW(hwnd, msg, wparam, lparam) }
}

/// Create the mpv video underlay HWND on the Tauri main thread. The window is `WS_POPUP` (no
/// caption, no border), `WS_EX_NOACTIVATE` (does not steal focus), and `WS_EX_TOOLWINDOW` (no
/// taskbar entry and no Alt-Tab entry). It is intentionally not a Win32 owned window because owned
/// top-level windows are always above their owner and would occlude the transparent WebView overlay.
///
/// Uses a timeout on the sync channel to prevent indefinite blocking if the main thread message
/// loop is stalled. The `try_lock` in `on_window_event` prevents the primary deadlock, but the
/// timeout is a safety net.
fn create_video_underlay_on_main_thread(window: &tauri::Window) -> Result<HWND, String> {
    let (tx, rx) = std::sync::mpsc::sync_channel(1);

    window
        .run_on_main_thread(move || {
            let result = create_video_underlay_window().map(|hwnd| hwnd as isize);
            let _ = tx.send(result);
        })
        .map_err(|err| format!("failed to schedule HWND creation on main thread: {err}"))?;

    match rx.recv_timeout(std::time::Duration::from_secs(5)) {
        Ok(result) => result.map(|v| v as HWND),
        Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {
            Err("timed out waiting for HWND creation on main thread (5s)".to_string())
        }
        Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => Err(
            "HWND creation callback channel disconnected before result was available".to_string(),
        ),
    }
}

fn create_video_underlay_window() -> Result<HWND, String> {
    let class_name = wide_null(CLASS_NAME);
    let title = wide_null("OhMyCine mpv video underlay");

    let hwnd = unsafe {
        CreateWindowExW(
            WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW,
            class_name.as_ptr(),
            title.as_ptr(),
            WS_POPUP,
            0,
            0,
            16,
            16,
            ptr::null_mut(),
            ptr::null_mut(),
            GetModuleHandleW(ptr::null()),
            ptr::null_mut(),
        )
    };

    if hwnd.is_null() {
        return Err("failed to create mpv video underlay HWND".to_string());
    }

    // Do NOT show the window yet. `mark_mpv_ready` will reveal it after mpv_initialize succeeds.
    Ok(hwnd)
}

// ---------------------------------------------------------------------------
// Utility
// ///

fn scaled_i32(value: f64, scale_factor: f64) -> i32 {
    (value * scale_factor)
        .round()
        .clamp(i32::MIN as f64, i32::MAX as f64) as i32
}

fn wide_null(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0)).collect()
}

// ---------------------------------------------------------------------------
// Log file diagnostics
// ---------------------------------------------------------------------------

fn resolve_log_path(window: &tauri::Window) -> Option<std::path::PathBuf> {
    use tauri::Manager;

    let app = window.app_handle();
    let path_api = app.path();

    let base = path_api
        .app_log_dir()
        .or_else(|_| path_api.app_config_dir())
        .ok()?;

    if std::fs::create_dir_all(&base).is_err() {
        return None;
    }
    Some(base.join("render-diagnostics.log"))
}

fn append_log_line(path: &std::path::Path, line: &str) {
    use std::io::Write;

    let Ok(mut file) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
    else {
        return;
    };
    let _ = writeln!(file, "[{}] {}", current_timestamp(), line);
}

fn log_diagnostics(path: &Option<std::path::PathBuf>, line: &str) {
    if let Some(path) = path.as_ref() {
        append_log_line(path, line);
    }
    log::info!("{line}");
}

fn current_timestamp() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};

    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    format!("{secs}")
}

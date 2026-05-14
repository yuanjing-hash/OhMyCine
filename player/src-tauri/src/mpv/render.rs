use std::sync::atomic::{AtomicBool, Ordering};

use serde::Serialize;

static WEBVIEW_BACKGROUND_TRANSPARENT_APPLIED: AtomicBool = AtomicBool::new(false);

/// Declares which concrete render backend the Player is using on the current platform.
///
/// The current Windows backend uses a transparent Tauri/WebView overlay above a separate mpv video
/// underlay window. mpv still receives a real HWND through `wid` and renders with
/// `vo=gpu-next` + `hwdec=auto-safe`, while Vue controls remain in the transparent overlay.
/// Future True-render-API or WebView2 Composition Hosting backends would appear here as new
/// variants without breaking the frontend render state shape.
#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum RenderBackendKind {
    WindowsTransparentOverlay,
    LinuxFuture,
    MacosFuture,
    MobileFuture,
    Unsupported,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum RenderStatus {
    Idle,
    Initializing,
    Ready,
    Unsupported,
    Error,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvRenderDiagnostics {
    pub owner_hwnd_attached: bool,
    pub mpv_hwnd_created: bool,
    pub mpv_hwnd_shown: bool,
    pub overlay_window_transparent: bool,
    pub webview_background_transparent_applied: bool,
    pub z_order_underlay_applied: bool,
    pub geometry_following: bool,
    pub taskbar_ignored: bool,
    pub fullscreen_state: String,
    pub last_sync_result: String,
    pub mpv_wid_accepted: bool,
    pub mpv_initialized: bool,
    pub last_bounds: Option<String>,
    pub scale: f64,
    pub syncs: u64,
    pub log_file: Option<String>,
}

impl MpvRenderDiagnostics {
    pub fn summary(&self) -> String {
        let log_suffix = self
            .log_file
            .as_ref()
            .map(|path| format!(" logFile={path}"))
            .unwrap_or_default();
        let bounds_str = self.last_bounds.as_deref().unwrap_or("none");

        format!(
            "Diagnostics: ownerHwndAttached={} mpvHwndCreated={} mpvHwndShown={} \
             overlayWindowTransparent={} webviewBackgroundTransparentApplied={} \
             zOrderUnderlayApplied={} geometryFollowing={} taskbarIgnored={} fullscreenState={} \
             lastSyncResult={} mpvWidAccepted={} mpvInitialized={} lastBounds={bounds_str} \
             scale={:.2} syncs={} {log_suffix}",
            bool_yes_no(self.owner_hwnd_attached),
            bool_yes_no(self.mpv_hwnd_created),
            bool_yes_no(self.mpv_hwnd_shown),
            bool_yes_no(self.overlay_window_transparent),
            bool_yes_no(self.webview_background_transparent_applied),
            bool_yes_no(self.z_order_underlay_applied),
            bool_yes_no(self.geometry_following),
            bool_yes_no(self.taskbar_ignored),
            self.fullscreen_state,
            self.last_sync_result,
            bool_yes_no(self.mpv_wid_accepted),
            bool_yes_no(self.mpv_initialized),
            self.scale,
            self.syncs,
        )
    }
}

impl Default for MpvRenderDiagnostics {
    fn default() -> Self {
        Self {
            owner_hwnd_attached: false,
            mpv_hwnd_created: false,
            mpv_hwnd_shown: false,
            overlay_window_transparent: false,
            webview_background_transparent_applied: false,
            z_order_underlay_applied: false,
            geometry_following: false,
            taskbar_ignored: false,
            fullscreen_state: "unknown".to_string(),
            last_sync_result: "pending".to_string(),
            mpv_wid_accepted: false,
            mpv_initialized: false,
            last_bounds: None,
            scale: 1.0,
            syncs: 0,
            log_file: None,
        }
    }
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvRenderState {
    pub status: RenderStatus,
    pub backend: RenderBackendKind,
    pub message: Option<String>,
    pub diagnostics: Option<MpvRenderDiagnostics>,
}

fn bool_yes_no(value: bool) -> &'static str {
    if value {
        "yes"
    } else {
        "no"
    }
}

pub fn set_webview_background_transparency_applied(applied: bool) {
    WEBVIEW_BACKGROUND_TRANSPARENT_APPLIED.store(applied, Ordering::Relaxed);
}

#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub fn webview_background_transparency_applied() -> bool {
    WEBVIEW_BACKGROUND_TRANSPARENT_APPLIED.load(Ordering::Relaxed)
}

pub fn current_render_state() -> MpvRenderState {
    MpvRenderState {
        status: current_backend_initial_status(),
        backend: current_backend_kind(),
        message: Some(current_backend_message().to_string()),
        diagnostics: None,
    }
}

#[cfg(target_os = "windows")]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::WindowsTransparentOverlay
}

#[cfg(target_os = "windows")]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Idle
}

#[cfg(target_os = "windows")]
fn current_backend_message() -> &'static str {
    "Windows transparent overlay backend is compiled; call mpv_init_render_surface to create the mpv video underlay window, hand its HWND to libmpv through wid + vo=gpu-next + hwdec=auto-safe, and keep the Tauri/WebView window transparent above it."
}

#[cfg(target_os = "linux")]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::LinuxFuture
}

#[cfg(target_os = "linux")]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Unsupported
}

#[cfg(target_os = "linux")]
fn current_backend_message() -> &'static str {
    "Linux native render backend is planned but not implemented in this slice; visible video remains safely suppressed."
}

#[cfg(target_os = "macos")]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::MacosFuture
}

#[cfg(target_os = "macos")]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Unsupported
}

#[cfg(target_os = "macos")]
fn current_backend_message() -> &'static str {
    "macOS native render backend is planned but not implemented in this slice; visible video remains safely suppressed."
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::MobileFuture
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Unsupported
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn current_backend_message() -> &'static str {
    "Mobile native render backends are planned after desktop rendering matures."
}

#[cfg(not(any(
    target_os = "windows",
    target_os = "linux",
    target_os = "macos",
    target_os = "android",
    target_os = "ios"
)))]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::Unsupported
}

#[cfg(not(any(
    target_os = "windows",
    target_os = "linux",
    target_os = "macos",
    target_os = "android",
    target_os = "ios"
)))]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Unsupported
}

#[cfg(not(any(
    target_os = "windows",
    target_os = "linux",
    target_os = "macos",
    target_os = "android",
    target_os = "ios"
)))]
fn current_backend_message() -> &'static str {
    "This platform does not have a planned native render backend yet."
}

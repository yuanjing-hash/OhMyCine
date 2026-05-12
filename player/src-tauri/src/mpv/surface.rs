use serde::{Deserialize, Serialize};

use super::render::MpvRenderDiagnostics;

#[cfg(not(target_os = "windows"))]
use super::render::{MpvRenderState, RenderBackendKind, RenderStatus};

/// Frontend-reported logical bounds for the mpv render surface.
///
/// On Windows, Rust converts these CSS/logical coordinates to physical pixels in screen
/// coordinates so the mpv video underlay window aligns with the Tauri main window client area.
/// The current transparent-overlay model keeps the video full-bleed behind the WebView; legacy
/// top/bottom occlusion values are accepted for IPC compatibility but ignored by the Windows
/// backend.
#[derive(Debug, Clone, Copy, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct RenderSurfaceBounds {
    /// X coordinate of the player container relative to the Tauri window client area, in
    /// CSS/logical pixels.
    pub x: f64,
    /// Y coordinate of the player container relative to the Tauri window client area, in
    /// CSS/logical pixels.
    pub y: f64,
    /// Width of the player container in CSS/logical pixels.
    pub width: f64,
    /// Height of the player container in CSS/logical pixels.
    pub height: f64,
    /// Frontend-observed device pixel ratio. Windows uses this to convert CSS rects to physical
    /// pixels before positioning the mpv video underlay HWND in screen coordinates.
    pub scale_factor: f64,
    /// Legacy top occlusion band in CSS/logical pixels. Accepted for backwards compatibility;
    /// ignored by the Windows transparent-overlay backend because Vue controls sit above video.
    #[serde(default)]
    pub top_occlusion: f64,
    /// Legacy bottom occlusion band in CSS/logical pixels. Accepted for backwards compatibility;
    /// ignored by the Windows transparent-overlay backend because video is full-bleed.
    #[serde(default)]
    pub bottom_occlusion: f64,
}

impl RenderSurfaceBounds {
    pub fn sanitized(self) -> Self {
        Self {
            x: finite_or_zero(self.x),
            y: finite_or_zero(self.y),
            width: finite_or_zero(self.width).max(0.0),
            height: finite_or_zero(self.height).max(0.0),
            scale_factor: finite_or_zero(self.scale_factor).max(0.25),
            top_occlusion: finite_or_zero(self.top_occlusion).max(0.0),
            bottom_occlusion: finite_or_zero(self.bottom_occlusion).max(0.0),
        }
    }
}

/// Render backend selector surfaced to the frontend diagnostic panel.
///
/// Windows currently has a single active strategy: a full-bleed mpv video underlay beneath a
/// transparent Tauri/WebView overlay. Legacy variants are accepted but treated as synonyms so older
/// frontends can still call `mpv_set_render_strategy` without erroring.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub enum ZOrderStrategy {
    /// Full-bleed mpv video underlay behind the transparent Tauri/WebView overlay.
    #[default]
    TransparentOverlay,
    /// Legacy: owned top-level HWND above the owner. Accepted for backwards compatibility and
    /// neutralized to `TransparentOverlay`.
    OwnedTopLevel,
    /// Legacy: render HWND at `HWND_BOTTOM` beneath WebView. Accepted for backwards compatibility
    /// and neutralized to `TransparentOverlay`.
    BottomTransparentHole,
    /// Legacy: render HWND at `HWND_TOP` with top/bottom occlusion. Accepted for backwards
    /// compatibility and neutralized to `TransparentOverlay`.
    TopDisabledFallback,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RenderSurfaceSnapshot {
    pub bounds: Option<RenderSurfaceBoundsSnapshot>,
    pub clear_color: [f32; 4],
    pub diagnostics: Option<MpvRenderDiagnostics>,
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RenderSurfaceBoundsSnapshot {
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
    pub scale_factor: f64,
}

pub struct NativeRenderSurface {
    inner: platform::RenderSurface,
}

impl NativeRenderSurface {
    pub fn create(window: &tauri::Window) -> Result<Self, String> {
        platform::RenderSurface::create(window).map(|inner| Self { inner })
    }

    /// Returns the HWND-as-integer string mpv consumes through `mpv_set_option_string("wid", ...)`
    /// before `mpv_initialize`. On non-Windows the surface layer cannot exist, so callers of this
    /// method never reach it.
    #[allow(dead_code)]
    pub fn mpv_wid(&self) -> String {
        self.inner.mpv_wid()
    }

    /// Called by `MpvPlayer` once mpv has successfully processed the `wid` option and
    /// `mpv_initialize` returned without creating its own window. The surface records the fact
    /// and, on Windows, reveals the video underlay behind the transparent Tauri overlay.
    #[allow(dead_code)]
    pub fn mark_mpv_ready(&mut self) {
        self.inner.mark_mpv_ready();
    }

    pub fn set_bounds(&mut self, bounds: RenderSurfaceBounds) -> Result<(), String> {
        self.inner.set_bounds(bounds.sanitized())
    }

    #[allow(dead_code)]
    pub fn set_strategy(&mut self, strategy: ZOrderStrategy) -> Result<(), String> {
        self.inner.set_strategy(strategy)
    }

    pub fn snapshot(&self) -> RenderSurfaceSnapshot {
        self.inner.snapshot()
    }

    pub fn on_owner_window_event(&mut self, event: OwnerWindowEvent) {
        self.inner.on_owner_window_event(event);
    }
}

/// Owner-window lifecycle signals the Windows backend uses to keep its mpv video underlay aligned
/// with the Tauri main window. On non-Windows platforms these events are ignored.
#[allow(dead_code)]
#[derive(Debug, Clone, Copy)]
pub enum OwnerWindowEvent {
    Moved,
    Resized,
    ScaleFactorChanged,
    FocusChanged(bool),
    Minimized,
    Restored,
    Destroyed,
}

#[cfg(not(target_os = "windows"))]
pub fn unsupported_state_for_current_platform() -> MpvRenderState {
    MpvRenderState {
        status: RenderStatus::Unsupported,
        backend: unsupported_backend_kind(),
        message: Some(unsupported_message().to_string()),
        diagnostics: None,
    }
}

fn finite_or_zero(value: f64) -> f64 {
    if value.is_finite() {
        value
    } else {
        0.0
    }
}

#[cfg(target_os = "linux")]
fn unsupported_backend_kind() -> RenderBackendKind {
    RenderBackendKind::LinuxFuture
}

#[cfg(target_os = "macos")]
fn unsupported_backend_kind() -> RenderBackendKind {
    RenderBackendKind::MacosFuture
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn unsupported_backend_kind() -> RenderBackendKind {
    RenderBackendKind::MobileFuture
}

#[cfg(not(any(
    target_os = "windows",
    target_os = "linux",
    target_os = "macos",
    target_os = "android",
    target_os = "ios"
)))]
fn unsupported_backend_kind() -> RenderBackendKind {
    RenderBackendKind::Unsupported
}

#[cfg(target_os = "linux")]
fn unsupported_message() -> &'static str {
    "Linux native render backend is planned but not implemented in this slice; visible video remains safely suppressed."
}

#[cfg(target_os = "macos")]
fn unsupported_message() -> &'static str {
    "macOS native render backend is planned but not implemented in this slice; visible video remains safely suppressed."
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn unsupported_message() -> &'static str {
    "Mobile native render backends are planned after desktop rendering matures; visible video remains safely suppressed."
}

#[cfg(not(any(
    target_os = "windows",
    target_os = "linux",
    target_os = "macos",
    target_os = "android",
    target_os = "ios"
)))]
fn unsupported_message() -> &'static str {
    "This platform does not have a planned native render backend yet; visible video remains safely suppressed."
}

#[cfg(target_os = "windows")]
mod platform {
    pub use super::super::platform::windows::WindowsRenderSurface as RenderSurface;
}

#[cfg(not(target_os = "windows"))]
mod platform {
    use super::{
        unsupported_message, OwnerWindowEvent, RenderSurfaceBounds, RenderSurfaceSnapshot,
        ZOrderStrategy,
    };

    pub struct RenderSurface;

    impl RenderSurface {
        pub fn create(_window: &tauri::Window) -> Result<Self, String> {
            Err(unsupported_message().to_string())
        }

        #[allow(dead_code)]
        pub fn mpv_wid(&self) -> String {
            String::new()
        }

        #[allow(dead_code)]
        pub fn mark_mpv_ready(&mut self) {}

        pub fn set_bounds(&mut self, _bounds: RenderSurfaceBounds) -> Result<(), String> {
            Err(unsupported_message().to_string())
        }

        pub fn set_strategy(&mut self, _strategy: ZOrderStrategy) -> Result<(), String> {
            Err(unsupported_message().to_string())
        }

        pub fn snapshot(&self) -> RenderSurfaceSnapshot {
            RenderSurfaceSnapshot {
                bounds: None,
                clear_color: [0.0, 0.0, 0.0, 1.0],
                diagnostics: None,
            }
        }

        pub fn on_owner_window_event(&mut self, _event: OwnerWindowEvent) {}
    }
}

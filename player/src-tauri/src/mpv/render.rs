use std::{
    ffi::CStr,
    os::raw::{c_char, c_int, c_void},
    ptr,
    ptr::NonNull,
};

use libmpv_sys::{
    mpv_error_string, mpv_handle, mpv_opengl_fbo, mpv_opengl_init_params, mpv_render_context,
    mpv_render_context_create, mpv_render_context_free, mpv_render_context_render,
    mpv_render_context_set_update_callback, mpv_render_param,
    mpv_render_param_type_MPV_RENDER_PARAM_API_TYPE,
    mpv_render_param_type_MPV_RENDER_PARAM_FLIP_Y,
    mpv_render_param_type_MPV_RENDER_PARAM_INVALID,
    mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_FBO,
    mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, MPV_RENDER_API_TYPE_OPENGL,
};
use serde::Serialize;

/// libmpv-sys 3.1.0 exposes OpenGL as a generated constant, but does not expose
/// an `MPV_RENDER_API_TYPE_SW` binding. Keep the software API spelling local so
/// future diagnostic backends can use the same narrow render boundary instead of
/// scattering raw C string literals through platform code.
#[allow(dead_code)]
pub const MPV_RENDER_API_TYPE_SW_FALLBACK: &[u8; 3] = b"sw\0";

#[allow(dead_code)]
pub type GlGetProcAddress = unsafe extern "C" fn(
    ctx: *mut c_void,
    name: *const c_char,
) -> *mut c_void;

#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum RenderBackendKind {
    WindowsOpenGl,
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
pub struct MpvRenderState {
    pub status: RenderStatus,
    pub backend: RenderBackendKind,
    pub message: Option<String>,
}

#[allow(dead_code)]
pub struct MpvRenderContext {
    ctx: NonNull<mpv_render_context>,
    backend: RenderBackendKind,
    width: c_int,
    height: c_int,
}

impl Drop for MpvRenderContext {
    fn drop(&mut self) {
        unsafe { mpv_render_context_free(self.ctx.as_ptr()) };
    }
}

#[allow(dead_code)]
impl MpvRenderContext {
    /// Create a true libmpv OpenGL render context.
    ///
    /// Safety:
    /// - `mpv` must be a valid initialized `mpv_handle` owned by `MpvPlayer`.
    /// - An app-owned OpenGL context must be current on the calling render thread.
    /// - `get_proc_address` must remain valid for libmpv's OpenGL symbol lookup.
    /// - The returned context must be dropped before the GL surface/context is destroyed.
    pub unsafe fn create_opengl(
        mpv: *mut mpv_handle,
        get_proc_address: GlGetProcAddress,
        get_proc_address_ctx: *mut c_void,
        update_callback: Option<unsafe extern "C" fn(*mut c_void)>,
        update_callback_ctx: *mut c_void,
    ) -> Result<Self, String> {
        if mpv.is_null() {
            return Err("libmpv handle is not available for render context creation".to_string());
        }

        let mut init_params = mpv_opengl_init_params {
            get_proc_address: Some(get_proc_address),
            get_proc_address_ctx,
            extra_exts: ptr::null(),
        };
        let mut params = [
            mpv_render_param {
                type_: mpv_render_param_type_MPV_RENDER_PARAM_API_TYPE,
                data: MPV_RENDER_API_TYPE_OPENGL.as_ptr().cast::<c_void>().cast_mut(),
            },
            mpv_render_param {
                type_: mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_INIT_PARAMS,
                data: (&mut init_params as *mut mpv_opengl_init_params).cast::<c_void>(),
            },
            invalid_param(),
        ];

        let mut raw = ptr::null_mut();
        check_error(unsafe { mpv_render_context_create(&mut raw, mpv, params.as_mut_ptr()) })?;
        let ctx = NonNull::new(raw)
            .ok_or_else(|| "libmpv returned a null render context".to_string())?;

        unsafe {
            mpv_render_context_set_update_callback(
                ctx.as_ptr(),
                update_callback,
                update_callback_ctx,
            )
        };

        Ok(Self {
            ctx,
            backend: RenderBackendKind::WindowsOpenGl,
            width: 0,
            height: 0,
        })
    }

    pub fn backend(&self) -> RenderBackendKind {
        self.backend
    }

    pub fn resize(&mut self, width: c_int, height: c_int) {
        self.width = width.max(0);
        self.height = height.max(0);
    }

    /// Render the next libmpv frame into the current OpenGL default framebuffer.
    ///
    /// Safety: the same OpenGL context used to create this render context must be
    /// current on the render thread, and the framebuffer dimensions must be in
    /// physical pixels.
    pub unsafe fn render_default_fbo(&mut self, flip_y: bool) -> Result<(), String> {
        if self.width <= 0 || self.height <= 0 {
            return Ok(());
        }

        let mut fbo = mpv_opengl_fbo {
            fbo: 0,
            w: self.width,
            h: self.height,
            internal_format: 0,
        };
        let mut flip = i32::from(flip_y);
        let mut params = [
            mpv_render_param {
                type_: mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_FBO,
                data: (&mut fbo as *mut mpv_opengl_fbo).cast::<c_void>(),
            },
            mpv_render_param {
                type_: mpv_render_param_type_MPV_RENDER_PARAM_FLIP_Y,
                data: (&mut flip as *mut i32).cast::<c_void>(),
            },
            invalid_param(),
        ];

        check_error(unsafe { mpv_render_context_render(self.ctx.as_ptr(), params.as_mut_ptr()) })
    }
}

pub fn current_render_state() -> MpvRenderState {
    MpvRenderState {
        status: current_backend_initial_status(),
        backend: current_backend_kind(),
        message: Some(current_backend_message().to_string()),
    }
}

#[allow(dead_code)]
pub fn compile_proof_summary() -> &'static [&'static str] {
    &[
        "mpv_render_context_create",
        "mpv_render_context_set_update_callback",
        "mpv_render_context_render",
        "mpv_render_context_free",
        "mpv_render_param",
        "mpv_opengl_init_params",
        "mpv_opengl_fbo",
        "MPV_RENDER_API_TYPE_OPENGL",
        "MPV_RENDER_API_TYPE_SW_FALLBACK",
    ]
}

#[allow(dead_code)]
fn invalid_param() -> mpv_render_param {
    mpv_render_param {
        type_: mpv_render_param_type_MPV_RENDER_PARAM_INVALID,
        data: ptr::null_mut(),
    }
}

#[allow(dead_code)]
fn check_error(code: i32) -> Result<(), String> {
    if code >= 0 {
        return Ok(());
    }

    let message = unsafe { CStr::from_ptr(mpv_error_string(code)) }
        .to_string_lossy()
        .into_owned();
    Err(message)
}

#[cfg(target_os = "windows")]
fn current_backend_kind() -> RenderBackendKind {
    RenderBackendKind::WindowsOpenGl
}

#[cfg(target_os = "windows")]
fn current_backend_initial_status() -> RenderStatus {
    RenderStatus::Idle
}

#[cfg(target_os = "windows")]
fn current_backend_message() -> &'static str {
    "Windows OpenGL true-render backend scaffold is compiled; native child surface initialization is the next milestone."
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
    "Linux true-render backend is planned but not implemented in this slice; visible video remains safely suppressed."
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
    "macOS true-render backend is planned but not implemented in this slice; visible video remains safely suppressed."
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
    "Mobile true-render backends are planned after desktop rendering matures."
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
    "This platform does not have a planned true-render backend yet."
}

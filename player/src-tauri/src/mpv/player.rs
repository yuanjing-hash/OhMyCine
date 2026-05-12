use std::{
    ffi::{CStr, CString},
    os::raw::{c_char, c_int, c_void},
    ptr,
    sync::{Arc, Mutex},
};

use libmpv_sys::{
    mpv_command, mpv_create, mpv_error_string, mpv_format_MPV_FORMAT_DOUBLE,
    mpv_format_MPV_FORMAT_FLAG, mpv_format_MPV_FORMAT_INT64, mpv_free, mpv_get_property,
    mpv_get_property_string, mpv_handle, mpv_initialize, mpv_set_option_string, mpv_set_property,
    mpv_set_property_string, mpv_terminate_destroy,
};

use super::{
    render::{current_render_state, MpvRenderState, RenderStatus},
    surface::{NativeRenderSurface, OwnerWindowEvent, RenderSurfaceBounds, ZOrderStrategy},
};

pub type MpvState = Arc<Mutex<MpvPlayer>>;

pub struct MpvPlayer {
    ctx: *mut mpv_handle,
    /// True once `mpv_initialize` has succeeded on the current `ctx`.
    initialized: bool,
    render_surface: Option<NativeRenderSurface>,
    render_state: MpvRenderState,
}

// MpvPlayer is only accessed through Arc<Mutex<_>> in Tauri state. libmpv handles are designed
// to be controlled from multiple threads as long as individual calls are synchronized.
unsafe impl Send for MpvPlayer {}

impl Drop for MpvPlayer {
    fn drop(&mut self) {
        // Cleanup order must match the transparent-overlay `wid` contract on Windows:
        //   1. Hide the mpv video underlay and stop forwarding geometry updates.
        //   2. `mpv_terminate_destroy` frees libmpv's D3D resources attached to the wid.
        //   3. Dropping the render surface then calls `DestroyWindow` and unregisters the class.
        // Doing it in this order prevents libmpv from writing to a destroyed HWND and prevents a
        // residual ghost window from flashing after mpv shuts down.
        if let Some(surface) = self.render_surface.as_mut() {
            surface.on_owner_window_event(OwnerWindowEvent::Destroyed);
        }

        if !self.ctx.is_null() {
            unsafe { mpv_terminate_destroy(self.ctx) };
            self.ctx = ptr::null_mut();
            self.initialized = false;
        }

        self.render_surface.take();
    }
}

impl MpvPlayer {
    pub fn new() -> Result<Self, String> {
        let ctx = unsafe { mpv_create() };
        if ctx.is_null() {
            return Err("failed to create libmpv context".to_string());
        }

        let player = Self {
            ctx,
            initialized: false,
            render_surface: None,
            render_state: current_render_state(),
        };

        // Non-Windows: initialize immediately in the no-visible-video safety mode. Visible video
        // is not supported on these platforms in this slice, and the control path must still be
        // usable so audio/property commands work.
        //
        // Windows: defer `mpv_initialize` until `init_render_surface` has created the owned
        // top-level HWND and passed it to libmpv via the `wid` option. If the Player view is
        // never opened, `mpv_initialize` is called on first use (e.g. load_file) with a safe
        // `vo=null` fallback so control commands still return a sensible error instead of UB.
        #[cfg(not(target_os = "windows"))]
        {
            let mut player = player;
            player.apply_safe_fallback_options()?;
            player.finish_initialize()?;
            return Ok(player);
        }

        #[cfg(target_os = "windows")]
        Ok(player)
    }

    pub fn load_file(&mut self, path: &str) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.command(&["loadfile", path, "replace"])
    }

    pub fn render_state(&self) -> MpvRenderState {
        let mut state = self.render_state.clone();
        if let Some(surface) = self.render_surface.as_ref() {
            let snapshot = surface.snapshot();
            if let Some(diagnostics) = snapshot.diagnostics {
                let summary = diagnostics.summary();
                state.diagnostics = Some(diagnostics);
                state.message = Some(match state.message {
                    Some(message) if message.contains("Diagnostics:") => {
                        let prefix = message
                            .split(" Diagnostics:")
                            .next()
                            .unwrap_or(message.as_str());
                        format!("{prefix} {summary}")
                    }
                    Some(message) => format!("{message} {summary}"),
                    None => summary,
                });
            }
        }
        state
    }

    pub fn init_render_surface(&mut self, window: &tauri::Window) -> MpvRenderState {
        if self.render_surface.is_some() {
            return self.render_state.clone();
        }

        // On non-Windows platforms the surface layer returns `unsupported` via
        // `NativeRenderSurface::create`. mpv was already initialized in safe mode in
        // `MpvPlayer::new`, so we only need to reflect the unsupported state here.
        #[cfg(not(target_os = "windows"))]
        {
            match NativeRenderSurface::create(window) {
                Ok(surface) => {
                    // Should not happen on non-Windows targets; keep the state truthful anyway.
                    self.render_surface = Some(surface);
                    self.render_state.status = RenderStatus::Ready;
                }
                Err(err) => {
                    self.render_surface = None;
                    self.render_state = failed_surface_state(err);
                }
            }
            return self.render_state.clone();
        }

        #[cfg(target_os = "windows")]
        {
            if self.initialized {
                self.render_state = failed_surface_state(
                    "mpv was already initialized in safe vo=null/video=no fallback mode before the Windows render surface was created; refusing to claim wid video readiness for this handle. Restart the Player app so the underlay HWND can be attached before mpv_initialize."
                        .to_string(),
                );
                return self.render_state.clone();
            }

            self.render_state.status = RenderStatus::Initializing;
            self.render_state.message = Some(
                "正在创建 Windows mpv 视频底层窗口，并通过 wid + vo=gpu-next + hwdec=auto-safe 调用 mpv_initialize；Tauri/WebView 透明叠层保持在其上方。"
                    .to_string(),
            );

            let mut surface = match NativeRenderSurface::create(window) {
                Ok(surface) => surface,
                Err(err) => {
                    self.render_surface = None;
                    // Owned top-level creation failed: fall back to the safe
                    // `vo=null`/`video=no` mode so mpv cannot pop its own window, then initialize.
                    if let Err(fallback_err) = self.ensure_initialized_fallback() {
                        self.render_state = failed_surface_state(format!(
                            "{err}; additional fallback failure: {fallback_err}"
                        ));
                    } else {
                        self.render_state = failed_surface_state(err);
                    }
                    return self.render_state.clone();
                }
            };

            // Configure the Windows VO pipeline and attach the video underlay HWND via `wid`
            // before calling `mpv_initialize`. Order is load-bearing: mpv locks the `wid` option
            // at initialize time.
            let configure_result = (|| -> Result<(), String> {
                self.set_option("force-window", "no")?;
                self.set_option("vo", "gpu-next")?;
                self.set_option("hwdec", "auto-safe")?;
                self.set_option("keep-open", "yes")?;
                self.set_option("osc", "no")?;
                let wid = surface.mpv_wid();
                if wid.is_empty() {
                    return Err(
                        "mpv video underlay HWND did not yield a wid value for libmpv".to_string(),
                    );
                }
                self.set_option("wid", &wid)?;
                self.finish_initialize()?;
                Ok(())
            })();

            match configure_result {
                Ok(()) => {
                    // Surface can now reveal the mpv video underlay behind the transparent Tauri overlay.
                    surface.mark_mpv_ready();
                    let snapshot = surface.snapshot();
                    self.render_surface = Some(surface);
                    self.render_state.status = RenderStatus::Ready;
                    let diagnostics = snapshot.diagnostics;
                    let diagnostics_summary = diagnostics
                        .as_ref()
                        .map(|d| d.summary())
                        .unwrap_or_else(|| "Diagnostics unavailable.".to_string());
                    self.render_state.diagnostics = diagnostics;
                    self.render_state.message = Some(format!(
                        "Windows mpv video underlay is attached via wid + vo=gpu-next + hwdec=auto-safe; transparent Tauri/WebView overlay remains above it for Vue controls. {diagnostics_summary}"
                    ));
                }
                Err(err) => {
                    // Dispose the video underlay window explicitly; we will not hand its HWND
                    // to mpv. Then fall back to the safe `vo=null`/`video=no` mode so mpv cannot
                    // create its own visible window.
                    drop(surface);
                    self.render_surface = None;
                    if let Err(fallback_err) = self.ensure_initialized_fallback() {
                        self.render_state = failed_surface_state(format!(
                            "{err}; additional fallback failure: {fallback_err}"
                        ));
                    } else {
                        self.render_state = failed_surface_state(err);
                    }
                }
            }

            self.render_state.clone()
        }
    }

    pub fn update_render_surface_bounds(&mut self, bounds: RenderSurfaceBounds) -> MpvRenderState {
        let Some(surface) = self.render_surface.as_mut() else {
            if matches!(
                self.render_state.status,
                RenderStatus::Idle | RenderStatus::Initializing
            ) {
                self.render_state.message = Some(
                    "Render surface has not been initialized yet; bounds update was recorded by the UI but native video remains suppressed."
                        .to_string(),
                );
            }
            return self.render_state.clone();
        };

        match surface.set_bounds(bounds) {
            Ok(()) => {
                let snapshot = surface.snapshot();
                self.render_state.status = RenderStatus::Ready;
                let diagnostics = snapshot.diagnostics;
                let diagnostics_summary = diagnostics
                    .as_ref()
                    .map(|d| d.summary())
                    .unwrap_or_else(|| "Diagnostics unavailable.".to_string());
                self.render_state.diagnostics = diagnostics;
                self.render_state.message = snapshot.bounds.map(|bounds| {
                    format!(
                        "Windows mpv video underlay aligned behind the transparent Tauri overlay: {}x{} physical pixels at ({}, {}) with scale {:.2}. {diagnostics_summary}",
                        bounds.width, bounds.height, bounds.x, bounds.y, bounds.scale_factor,
                    )
                });
            }
            Err(err) => {
                self.render_state.status = RenderStatus::Error;
                self.render_state.message = Some(err);
            }
        }

        self.render_state.clone()
    }

    pub fn set_render_strategy(&mut self, strategy: ZOrderStrategy) -> MpvRenderState {
        let Some(surface) = self.render_surface.as_mut() else {
            if matches!(
                self.render_state.status,
                RenderStatus::Idle | RenderStatus::Initializing
            ) {
                self.render_state.message = Some(format!(
                    "Render surface is not initialized yet; strategy change to {strategy:?} was noted but no window exists to apply it."
                ));
            }
            return self.render_state.clone();
        };

        match surface.set_strategy(strategy) {
            Ok(()) => {
                let snapshot = surface.snapshot();
                let diagnostics = snapshot.diagnostics;
                let diagnostics_summary = diagnostics
                    .as_ref()
                    .map(|d| d.summary())
                    .unwrap_or_else(|| "Diagnostics unavailable.".to_string());
                self.render_state.status = RenderStatus::Ready;
                self.render_state.diagnostics = diagnostics;
                self.render_state.message = Some(format!(
                    "Windows render strategy noted as {strategy:?}; legacy occlusion/topmost modes are neutralized to the transparent-overlay underlay model. {diagnostics_summary}"
                ));
            }
            Err(err) => {
                self.render_state.status = RenderStatus::Error;
                self.render_state.message = Some(err);
            }
        }

        self.render_state.clone()
    }

    /// Forward owner-window lifecycle signals to the render surface. On Windows this keeps the
    /// mpv video underlay synchronized with the Tauri main window across move, resize,
    /// scale-factor change, focus change, minimize/restore, and close. On other platforms it is
    /// a no-op.
    pub fn on_owner_window_event(&mut self, event: OwnerWindowEvent) {
        if let Some(surface) = self.render_surface.as_mut() {
            surface.on_owner_window_event(event);
        }
    }

    pub fn pause(&mut self) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.set_property("pause", "true")
    }

    pub fn resume(&mut self) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.set_property("pause", "false")
    }

    pub fn seek(&mut self, position: f64) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.command(&["seek", &position.to_string(), "absolute"])
    }

    fn set_option(&self, option: &str, value: &str) -> Result<(), String> {
        let option = CString::new(option).map_err(|err| err.to_string())?;
        let value = CString::new(value).map_err(|err| err.to_string())?;
        self.check_error(unsafe {
            mpv_set_option_string(self.ctx, option.as_ptr(), value.as_ptr())
        })
    }

    pub fn set_property(&self, prop: &str, value: &str) -> Result<(), String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;

        match prop.to_str().unwrap_or_default() {
            "pause" => {
                let mut value = if value == "true" || value == "1" {
                    1
                } else {
                    0
                };
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_FLAG,
                        (&mut value as *mut c_int).cast::<c_void>(),
                    )
                })
            }
            "volume" | "time-pos" | "duration" | "speed" => {
                let mut value = value.parse::<f64>().map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_DOUBLE,
                        (&mut value as *mut f64).cast::<c_void>(),
                    )
                })
            }
            "sid" | "aid" => {
                let mut value = value.parse::<i64>().map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_INT64,
                        (&mut value as *mut i64).cast::<c_void>(),
                    )
                })
            }
            _ => {
                let value = CString::new(value).map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property_string(self.ctx, prop.as_ptr(), value.as_ptr())
                })
            }
        }
    }

    pub fn get_property(&self, prop: &str) -> Result<String, String> {
        if let Some(value) = self.get_property_string(prop)? {
            return Ok(value);
        }
        if let Ok(value) = self.get_property_double(prop) {
            return Ok(value.to_string());
        }
        if let Ok(value) = self.get_property_int64(prop) {
            return Ok(value.to_string());
        }
        if let Ok(value) = self.get_property_flag(prop) {
            return Ok((value != 0).to_string());
        }
        Err(format!("failed to get mpv property: {prop}"))
    }

    pub fn time_pos(&self) -> f64 {
        self.get_property_double("time-pos").unwrap_or(0.0)
    }

    pub fn duration(&self) -> f64 {
        self.get_property_double("duration").unwrap_or(0.0)
    }

    pub fn paused(&self) -> bool {
        self.get_property_flag("pause").unwrap_or(1) != 0
    }

    /// Apply the no-visible-video safety options. mpv may not render to its own window under this
    /// configuration, so the control path still works while visible video remains suppressed.
    fn apply_safe_fallback_options(&self) -> Result<(), String> {
        self.set_option("force-window", "no")?;
        self.set_option("vo", "null")?;
        self.set_option("video", "no")?;
        self.set_option("hwdec", "no")?;
        self.set_option("keep-open", "yes")?;
        self.set_option("osc", "no")?;
        Ok(())
    }

    /// Finalize `mpv_initialize`. Only call once per handle lifetime.
    fn finish_initialize(&mut self) -> Result<(), String> {
        if self.initialized {
            return Ok(());
        }
        self.check_error(unsafe { mpv_initialize(self.ctx) })?;
        self.initialized = true;
        Ok(())
    }

    /// If mpv has not been initialized yet (Windows path where `init_render_surface` was never
    /// called), initialize it in the safe `vo=null`/`video=no` fallback so control commands still
    /// work. No-op after the first successful initialize.
    fn ensure_initialized_fallback(&mut self) -> Result<(), String> {
        if self.initialized {
            return Ok(());
        }
        self.apply_safe_fallback_options()?;
        self.finish_initialize()
    }

    fn command(&self, args: &[&str]) -> Result<(), String> {
        let c_args = args
            .iter()
            .map(|arg| CString::new(*arg).map_err(|err| err.to_string()))
            .collect::<Result<Vec<_>, _>>()?;
        let mut raw_args = c_args
            .iter()
            .map(|arg| arg.as_ptr())
            .chain(std::iter::once(ptr::null()))
            .collect::<Vec<*const c_char>>();

        self.check_error(unsafe { mpv_command(self.ctx, raw_args.as_mut_ptr()) })
    }

    fn get_property_string(&self, prop: &str) -> Result<Option<String>, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let value = unsafe { mpv_get_property_string(self.ctx, prop.as_ptr()) };
        if value.is_null() {
            return Ok(None);
        }

        let result = unsafe { CStr::from_ptr(value) }
            .to_string_lossy()
            .into_owned();
        unsafe { mpv_free(value.cast::<c_void>()) };
        Ok(Some(result))
    }

    fn get_property_double(&self, prop: &str) -> Result<f64, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value = 0.0_f64;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_DOUBLE,
                (&mut value as *mut f64).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn get_property_int64(&self, prop: &str) -> Result<i64, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value = 0_i64;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_INT64,
                (&mut value as *mut i64).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn get_property_flag(&self, prop: &str) -> Result<c_int, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value: c_int = 0;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_FLAG,
                (&mut value as *mut c_int).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn check_error(&self, code: i32) -> Result<(), String> {
        if code >= 0 {
            return Ok(());
        }

        let message = unsafe { CStr::from_ptr(mpv_error_string(code)) }
            .to_string_lossy()
            .into_owned();
        Err(message)
    }
}

#[cfg(target_os = "windows")]
fn failed_surface_state(message: String) -> MpvRenderState {
    let mut state = current_render_state();
    state.status = RenderStatus::Error;
    state.message = Some(message);
    state.diagnostics = None;
    state
}

#[cfg(not(target_os = "windows"))]
fn failed_surface_state(message: String) -> MpvRenderState {
    let mut state = super::surface::unsupported_state_for_current_platform();
    state.message = Some(message);
    state
}

pub fn create_state() -> Result<MpvState, String> {
    Ok(Arc::new(Mutex::new(MpvPlayer::new()?)))
}

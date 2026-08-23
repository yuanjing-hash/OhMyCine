use std::{
    ffi::{CStr, CString},
    os::raw::{c_char, c_int, c_void},
    path::PathBuf,
    ptr,
    sync::{Arc, Mutex},
};

use libmpv_sys::{
    mpv_command, mpv_create, mpv_error_string, mpv_event_id_MPV_EVENT_FILE_LOADED,
    mpv_event_id_MPV_EVENT_LOG_MESSAGE, mpv_event_id_MPV_EVENT_NONE,
    mpv_event_id_MPV_EVENT_VIDEO_RECONFIG, mpv_event_log_message, mpv_format_MPV_FORMAT_DOUBLE,
    mpv_format_MPV_FORMAT_FLAG, mpv_format_MPV_FORMAT_INT64, mpv_format_MPV_FORMAT_NODE,
    mpv_format_MPV_FORMAT_NODE_ARRAY, mpv_format_MPV_FORMAT_NODE_MAP, mpv_format_MPV_FORMAT_STRING,
    mpv_free, mpv_free_node_contents, mpv_get_property, mpv_get_property_string, mpv_handle,
    mpv_initialize, mpv_node, mpv_node_list, mpv_request_log_messages, mpv_set_option_string,
    mpv_set_property, mpv_set_property_string, mpv_terminate_destroy, mpv_wait_event,
};

use super::{
    render::{current_render_state, MpvRenderState, RenderStatus},
    surface::{NativeRenderSurface, OwnerWindowEvent, RenderSurfaceBounds, ZOrderStrategy},
};
use crate::commands::player_shared::MpvEngineSettings;

pub type MpvState = Arc<Mutex<MpvPlayer>>;

#[derive(Debug, Default)]
pub struct MpvEventBatch {
    pub file_loaded: bool,
    pub video_ready: bool,
    pub reached_limit: bool,
    pub fsr_fallback: bool,
}

#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvTrack {
    pub id: i64,
    pub kind: String,
    pub language: Option<String>,
    pub title: Option<String>,
    pub codec: Option<String>,
    pub channels: Option<i64>,
    pub is_default: bool,
    pub selected: bool,
}

#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvTrackState {
    pub tracks: Vec<MpvTrack>,
    pub current_subtitle: Option<i64>,
    pub current_audio: Option<i64>,
}

pub struct MpvPlayer {
    ctx: *mut mpv_handle,
    /// True once `mpv_initialize` has succeeded on the current `ctx`.
    initialized: bool,
    render_surface: Option<NativeRenderSurface>,
    render_state: MpvRenderState,
    engine_settings: MpvEngineSettings,
    fsr_shader_path: Option<PathBuf>,
    fsr_status: String,
    fsr_reason: Option<String>,
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
            engine_settings: MpvEngineSettings::default(),
            fsr_shader_path: None,
            fsr_status: "not-configured".to_string(),
            fsr_reason: None,
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
            Ok(player)
        }

        #[cfg(target_os = "windows")]
        Ok(player)
    }

    pub fn load_file_with_headers(
        &mut self,
        path: &str,
        headers: &[(String, String)],
        audio_path: Option<&str>,
    ) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.apply_http_headers(headers)?;
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        let audio_option = audio_path.map(|value| format!("audio-file={value}"));
        let result = if let Some(option) = audio_option.as_deref() {
            self.command(&["loadfile", path, "replace", "-1", option])
        } else {
            self.command(&["loadfile", path, "replace"])
        };
        if result.is_ok() {
            if let Some(surface) = self.render_surface.as_mut() {
                surface.set_playback_active(true);
            }
        }
        result
    }

    pub fn apply_engine_settings(
        &mut self,
        settings: MpvEngineSettings,
        shader_path: Option<PathBuf>,
    ) -> Result<(), String> {
        let settings = settings.validated()?;
        if self.initialized {
            self.set_property("vo", &settings.video_output)?;
            self.set_property("hwdec", settings.desktop_hwdec())?;
            self.set_property("cache", settings.cache_value())?;
            self.set_property(
                "demuxer-max-bytes",
                &settings.demuxer_max_bytes().to_string(),
            )?;
            self.set_property("video-sync", &settings.video_sync)?;
        }
        self.fsr_shader_path = shader_path;
        self.engine_settings = settings;
        if self.initialized {
            self.apply_fsr_runtime_safely();
        } else if self.engine_settings.fsr_mode == "off" {
            self.fsr_status = "disabled".to_string();
            self.fsr_reason = None;
        } else {
            self.fsr_status = "pending-render-init".to_string();
            self.fsr_reason = None;
        }
        Ok(())
    }

    pub fn fsr_diagnostics(&self) -> (String, Option<String>) {
        (self.fsr_status.clone(), self.fsr_reason.clone())
    }

    pub fn add_subtitle(
        &mut self,
        path: &str,
        title: Option<&str>,
        language: Option<&str>,
    ) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        let title = title.unwrap_or("外部字幕");
        let language = language.unwrap_or("");
        self.command(&["sub-add", path, "select", title, language])
            .map_err(|error| format!("外部字幕暂时无法加载：{error}"))
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
            self.render_state.clone()
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
            self.render_state.message = Some(format!(
                "正在创建 Windows mpv 视频底层窗口，并通过 wid + vo={} + hwdec={} 调用 mpv_initialize；Tauri/WebView 透明叠层保持在其上方。",
                self.engine_settings.video_output,
                self.engine_settings.desktop_hwdec(),
            ));

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
            let video_output = self.engine_settings.video_output.clone();
            let hardware_decoder = self.engine_settings.desktop_hwdec().to_string();
            let cache = self.engine_settings.cache_value().to_string();
            let demuxer_max_bytes = self.engine_settings.demuxer_max_bytes().to_string();
            let video_sync = self.engine_settings.video_sync.clone();
            let configure_result = (|| -> Result<(), String> {
                self.set_option("force-window", "no")?;
                self.set_option("vo", &video_output)?;
                self.set_option("hwdec", &hardware_decoder)?;
                self.set_option("cache", &cache)?;
                self.set_option("demuxer-max-bytes", &demuxer_max_bytes)?;
                self.set_option("video-sync", &video_sync)?;
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
                    // Automatic mode requires the managed Windows surface to be present. Apply
                    // FSR only after moving the successfully initialized surface into player
                    // state; applying it inside the initialization closure would always observe
                    // `render_surface == None` and incorrectly disable auto mode.
                    self.apply_fsr_runtime_safely();
                    self.render_state.status = RenderStatus::Ready;
                    let diagnostics = snapshot.diagnostics;
                    let diagnostics_summary = diagnostics
                        .as_ref()
                        .map(|d| d.summary())
                        .unwrap_or_else(|| "Diagnostics unavailable.".to_string());
                    self.render_state.diagnostics = diagnostics;
                    self.render_state.message = Some(format!(
                        "Windows mpv video underlay is attached via wid + vo={video_output} + hwdec={hardware_decoder}; transparent Tauri/WebView overlay remains above it for Vue controls. {diagnostics_summary}"
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
                self.refresh_fsr_target_parameters();
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

    pub fn stop(&mut self) -> Result<(), String> {
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        if !self.initialized {
            return Ok(());
        }
        self.command(&["stop"])
    }

    pub fn seek(&mut self, position: f64) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.command(&["seek", &position.to_string(), "absolute"])
    }

    pub fn track_state(&mut self) -> Result<MpvTrackState, String> {
        self.ensure_initialized_fallback()?;
        let tracks = self.track_list()?;
        let current_subtitle = tracks
            .iter()
            .find(|track| track.kind == "sub" && track.selected)
            .map(|track| track.id);
        let current_audio = tracks
            .iter()
            .find(|track| track.kind == "audio" && track.selected)
            .map(|track| track.id);

        Ok(MpvTrackState {
            tracks,
            current_subtitle,
            current_audio,
        })
    }

    fn track_list(&self) -> Result<Vec<MpvTrack>, String> {
        let prop = CString::new("track-list").map_err(|_| "Invalid mpv property".to_string())?;
        let mut node = std::mem::MaybeUninit::<mpv_node>::zeroed();
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_NODE,
                node.as_mut_ptr().cast::<c_void>(),
            )
        })?;

        let mut node = unsafe { node.assume_init() };
        let tracks = unsafe { parse_track_list_node(&node) };
        unsafe { mpv_free_node_contents(&mut node) };
        Ok(tracks)
    }

    fn set_option(&self, option: &str, value: &str) -> Result<(), String> {
        let option = CString::new(option).map_err(|err| err.to_string())?;
        let value = CString::new(value).map_err(|err| err.to_string())?;
        self.check_error(unsafe {
            mpv_set_option_string(self.ctx, option.as_ptr(), value.as_ptr())
        })
    }

    pub fn set_property(&self, prop: &str, value: &str) -> Result<(), String> {
        let property_name = prop;
        let prop = CString::new(property_name).map_err(|err| err.to_string())?;

        match property_name {
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
            "volume" | "time-pos" | "duration" | "speed" | "panscan" | "video-zoom"
            | "brightness" => {
                let mut value = value
                    .parse::<f64>()
                    .map_err(|_| "Invalid numeric mpv value".to_string())?;
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_DOUBLE,
                        (&mut value as *mut f64).cast::<c_void>(),
                    )
                })
            }
            "sid" | "aid" => self.command(&["set", property_name, value]),
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

    fn apply_http_headers(&self, headers: &[(String, String)]) -> Result<(), String> {
        let header_fields = headers
            .iter()
            .map(|(name, value)| format!("{name}: {value}"))
            .collect::<Vec<_>>()
            .join(",");
        self.set_property("http-header-fields", &header_fields)
            .map_err(|_| "播放请求头设置失败。".to_string())
    }

    fn apply_fsr_runtime_safely(&mut self) {
        #[cfg(not(target_os = "windows"))]
        {
            let _ = self.command(&["change-list", "glsl-shaders", "clr", ""]);
            self.fsr_status = "unsupported-platform".to_string();
            self.fsr_reason = Some("FSR 首发仅支持 Windows 与 Android。".to_string());
        }

        #[cfg(target_os = "windows")]
        {
            if self.engine_settings.fsr_mode == "off" {
                let _ = self.command(&["change-list", "glsl-shaders", "clr", ""]);
                self.fsr_status = "disabled".to_string();
                self.fsr_reason = None;
                return;
            }

            if self.render_surface.is_none() && self.engine_settings.fsr_mode != "force" {
                let _ = self.command(&["change-list", "glsl-shaders", "clr", ""]);
                self.fsr_status = "unavailable".to_string();
                self.fsr_reason = Some("当前没有可用的 Windows GPU 视频表面。".to_string());
                return;
            }

            let Some(shader_path) = self.fsr_shader_path.as_ref() else {
                self.record_fsr_fallback("应用内置 FSR Shader 尚未安装。");
                return;
            };
            if !shader_path.is_file() {
                self.record_fsr_fallback("应用内置 FSR Shader 文件不可用。");
                return;
            }

            let shader_path = shader_path.to_string_lossy().into_owned();
            let options = self.fsr_shader_options();
            let result = (|| -> Result<(), String> {
                self.command(&["change-list", "glsl-shaders", "clr", ""])?;
                self.set_property("glsl-shader-opts", &options)?;
                self.command(&["change-list", "glsl-shaders", "append", &shader_path])?;
                Ok(())
            })();

            if result.is_err() {
                self.record_fsr_fallback("FSR Shader 加载失败，已恢复普通缩放。")
            } else {
                self.fsr_status = if self.engine_settings.fsr_mode == "force" {
                    "armed-force"
                } else {
                    "armed-auto"
                }
                .to_string();
                self.fsr_reason = Some("仅在输出尺寸大于源画面时触发。".to_string());
            }
        }
    }

    fn refresh_fsr_target_parameters(&mut self) {
        if !self.initialized
            || self.engine_settings.fsr_mode == "off"
            || !self.fsr_status.starts_with("armed")
        {
            return;
        }
        let options = self.fsr_shader_options();
        if self.set_property("glsl-shader-opts", &options).is_err() {
            self.record_fsr_fallback("FSR 目标分辨率更新失败，已恢复普通缩放。")
        }
    }

    fn fsr_shader_options(&self) -> String {
        let (target_width, target_height) = self.fsr_target_dimensions();
        format!(
            "OHMYCINE_SHARPNESS={:.3},OHMYCINE_DENOISE={},OHMYCINE_TARGET_WIDTH={},OHMYCINE_TARGET_HEIGHT={}",
            self.engine_settings.fsr_sharpness_stops(),
            i32::from(self.engine_settings.fsr_denoise),
            target_width,
            target_height,
        )
    }

    fn fsr_target_dimensions(&self) -> (u32, u32) {
        let Some(target_short_edge) = self.engine_settings.fsr_target_short_edge() else {
            return (16_384, 16_384);
        };
        let Some(bounds) = self
            .render_surface
            .as_ref()
            .and_then(|surface| surface.snapshot().bounds)
        else {
            return (16_384, 16_384);
        };
        if bounds.width <= 0 || bounds.height <= 0 {
            return (16_384, 16_384);
        }

        let width = f64::from(bounds.width);
        let height = f64::from(bounds.height);
        let short_edge = width.min(height);
        if short_edge <= f64::from(target_short_edge) {
            return (width.round() as u32, height.round() as u32);
        }
        let scale = f64::from(target_short_edge) / short_edge;
        (
            (width * scale).round().max(1.0) as u32,
            (height * scale).round().max(1.0) as u32,
        )
    }

    fn record_fsr_fallback(&mut self, reason: &str) {
        let _ = self.command(&["change-list", "glsl-shaders", "clr", ""]);
        self.fsr_status = "fallback".to_string();
        self.fsr_reason = Some(reason.to_string());
    }

    /// Finalize `mpv_initialize`. Only call once per handle lifetime.
    fn finish_initialize(&mut self) -> Result<(), String> {
        if self.initialized {
            return Ok(());
        }
        self.check_error(unsafe { mpv_initialize(self.ctx) })?;
        self.initialized = true;
        if let Ok(level) = CString::new("warn") {
            let _ = unsafe { mpv_request_log_messages(self.ctx, level.as_ptr()) };
        }
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

    pub fn drain_events(&mut self, max_events: usize) -> MpvEventBatch {
        let mut batch = MpvEventBatch::default();
        for index in 0..max_events {
            let event = unsafe { mpv_wait_event(self.ctx, 0.0) };
            if event.is_null() || unsafe { (*event).event_id } == mpv_event_id_MPV_EVENT_NONE {
                break;
            }
            let event_id = unsafe { (*event).event_id };
            if event_id == mpv_event_id_MPV_EVENT_LOG_MESSAGE {
                let data = unsafe { (*event).data };
                if !data.is_null() {
                    let message = unsafe { &*(data.cast::<mpv_event_log_message>()) };
                    let prefix = unsafe { optional_c_string(message.prefix) };
                    let text = unsafe { optional_c_string(message.text) };
                    if self.handle_fsr_log_message(&prefix, &text) {
                        batch.fsr_fallback = true;
                    }
                }
            }
            batch.file_loaded |= event_id == mpv_event_id_MPV_EVENT_FILE_LOADED;
            batch.video_ready |= event_id == mpv_event_id_MPV_EVENT_VIDEO_RECONFIG;
            batch.reached_limit = index + 1 == max_events;
        }
        batch
    }

    fn handle_fsr_log_message(&mut self, prefix: &str, text: &str) -> bool {
        if !self.fsr_status.starts_with("armed") {
            return false;
        }
        let line = format!("{prefix} {text}").to_ascii_lowercase();
        let names_shader =
            line.contains("shader") || line.contains("glsl") || line.contains("hook");
        let reports_failure = line.contains("error")
            || line.contains("failed")
            || line.contains("compile")
            || line.contains("invalid");
        if !names_shader || !reports_failure {
            return false;
        }
        self.record_fsr_fallback("FSR Shader 编译失败，已恢复普通缩放。");
        true
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

unsafe fn optional_c_string(value: *const c_char) -> String {
    if value.is_null() {
        return String::new();
    }
    CStr::from_ptr(value).to_string_lossy().into_owned()
}

unsafe fn parse_track_list_node(node: &mpv_node) -> Vec<MpvTrack> {
    if node.format != mpv_format_MPV_FORMAT_NODE_ARRAY {
        return Vec::new();
    }

    let Some(list) = node_list(node) else {
        return Vec::new();
    };

    let count = list.num.max(0) as usize;
    if count == 0 || list.values.is_null() {
        return Vec::new();
    }

    std::slice::from_raw_parts(list.values, count)
        .iter()
        .filter_map(|track| parse_track_node(track))
        .collect()
}

unsafe fn parse_track_node(node: &mpv_node) -> Option<MpvTrack> {
    let id = map_i64(node, "id")?;
    let kind = map_string(node, "type")?;
    if kind != "sub" && kind != "audio" {
        return None;
    }

    Some(MpvTrack {
        id,
        kind,
        language: map_string(node, "lang"),
        title: map_string(node, "title"),
        codec: map_string(node, "codec"),
        channels: map_i64(node, "demux-channel-count"),
        is_default: map_bool(node, "default"),
        selected: map_bool(node, "selected"),
    })
}

unsafe fn node_list(node: &mpv_node) -> Option<&mpv_node_list> {
    if node.format != mpv_format_MPV_FORMAT_NODE_ARRAY
        && node.format != mpv_format_MPV_FORMAT_NODE_MAP
    {
        return None;
    }

    let list = node.u.list;
    if list.is_null() {
        return None;
    }

    Some(&*list)
}

unsafe fn map_value<'a>(node: &'a mpv_node, key: &str) -> Option<&'a mpv_node> {
    if node.format != mpv_format_MPV_FORMAT_NODE_MAP {
        return None;
    }

    let list = node_list(node)?;
    let count = list.num.max(0) as usize;
    if count == 0 || list.values.is_null() || list.keys.is_null() {
        return None;
    }

    let values = std::slice::from_raw_parts(list.values, count);
    let keys = std::slice::from_raw_parts(list.keys, count);
    for (index, raw_key) in keys.iter().enumerate() {
        if raw_key.is_null() {
            continue;
        }
        let current_key = CStr::from_ptr(*raw_key).to_string_lossy();
        if current_key == key {
            return values.get(index);
        }
    }

    None
}

unsafe fn map_string(node: &mpv_node, key: &str) -> Option<String> {
    let value = map_value(node, key)?;
    if value.format != mpv_format_MPV_FORMAT_STRING {
        return None;
    }

    let raw = value.u.string;
    if raw.is_null() {
        return None;
    }

    let text = CStr::from_ptr(raw).to_string_lossy().trim().to_string();
    if text.is_empty() {
        None
    } else {
        Some(text)
    }
}

unsafe fn map_i64(node: &mpv_node, key: &str) -> Option<i64> {
    let value = map_value(node, key)?;
    if value.format == mpv_format_MPV_FORMAT_INT64 {
        Some(value.u.int64)
    } else if value.format == mpv_format_MPV_FORMAT_DOUBLE {
        Some(value.u.double_ as i64)
    } else {
        None
    }
}

unsafe fn map_bool(node: &mpv_node, key: &str) -> bool {
    map_value(node, key)
        .map(|value| {
            if value.format == mpv_format_MPV_FORMAT_FLAG {
                value.u.flag != 0
            } else if value.format == mpv_format_MPV_FORMAT_INT64 {
                value.u.int64 != 0
            } else {
                false
            }
        })
        .unwrap_or(false)
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

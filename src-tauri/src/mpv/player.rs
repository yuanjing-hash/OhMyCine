use std::{
    ffi::{CStr, CString},
    os::raw::{c_char, c_int, c_void},
    path::PathBuf,
    ptr,
    sync::{Arc, Mutex},
};

use libmpv_sys::{
    mpv_command, mpv_create, mpv_error_string, mpv_event_id_MPV_EVENT_END_FILE,
    mpv_event_id_MPV_EVENT_FILE_LOADED, mpv_event_id_MPV_EVENT_LOG_MESSAGE,
    mpv_event_id_MPV_EVENT_NONE, mpv_event_id_MPV_EVENT_START_FILE,
    mpv_event_id_MPV_EVENT_VIDEO_RECONFIG, mpv_event_log_message, mpv_format_MPV_FORMAT_DOUBLE,
    mpv_format_MPV_FORMAT_FLAG, mpv_format_MPV_FORMAT_INT64, mpv_format_MPV_FORMAT_NODE,
    mpv_format_MPV_FORMAT_NODE_ARRAY, mpv_format_MPV_FORMAT_NODE_MAP, mpv_format_MPV_FORMAT_STRING,
    mpv_free, mpv_free_node_contents, mpv_get_property, mpv_get_property_string, mpv_handle,
    mpv_initialize, mpv_node, mpv_node_list, mpv_request_log_messages, mpv_set_option_string,
    mpv_set_property, mpv_set_property_string, mpv_terminate_destroy, mpv_wait_event,
};

use super::{
    frame_interpolation::{FrameGenerationController, MediaEvent},
    render::{current_render_state, MpvRenderState, RenderStatus},
    surface::{NativeRenderSurface, OwnerWindowEvent, RenderSurfaceBounds, ZOrderStrategy},
};
use crate::commands::player_shared::{
    FrameInterpolationCapability, FrameInterpolationDiagnostics, MpvEngineSettings,
};

pub type MpvState = Arc<Mutex<MpvPlayer>>;

#[cfg(target_os = "windows")]
fn is_windows_frame_interpolation_hwdec(value: &str) -> bool {
    let value = value.to_ascii_lowercase();
    value.starts_with("d3d11va")
        || value.starts_with("dxva2")
        || value.starts_with("nvdec")
        || value.starts_with("vulkan")
}

#[cfg(target_os = "windows")]
fn has_reliable_windows_cfr_timing(media_pts: f64, container_fps: f64, estimated_fps: f64) -> bool {
    media_pts.is_finite()
        && container_fps.is_finite()
        && estimated_fps.is_finite()
        && container_fps > 0.0
        && estimated_fps > 0.0
        && ((container_fps - estimated_fps).abs() / container_fps) <= 0.002
}

#[cfg(target_os = "windows")]
fn source_needs_windows_frame_interpolation(source_fps: f64, target_fps: f64) -> bool {
    source_fps.is_finite()
        && target_fps.is_finite()
        && source_fps > 0.0
        && target_fps > 0.0
        && source_fps < target_fps - 0.01
}

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

#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DesktopPlaybackDiagnostics {
    pub state: String,
    pub last_event: String,
    pub last_error: Option<String>,
    pub file_loaded: bool,
    pub video_format: Option<String>,
    pub audio_codec: Option<String>,
    pub vo_configured: bool,
    pub hardware_decoder: Option<String>,
    pub video_output: String,
    pub video_output_fallback_used: bool,
    pub playback_transport: String,
    pub fsr_status: String,
    pub fsr_reason: Option<String>,
    #[serde(flatten)]
    pub frame_interpolation: FrameInterpolationDiagnostics,
    pub logs: Vec<String>,
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
    playback_state: String,
    last_playback_event: String,
    last_playback_error: Option<String>,
    file_loaded: bool,
    stop_requested: bool,
    playback_transport: String,
    frame_interpolation_controller: FrameGenerationController,
    #[cfg(target_os = "windows")]
    windows_frame_interpolation_session:
        Option<super::windows_frame_interpolation_assets::WindowsFrameInterpolationSession>,
    #[cfg(target_os = "windows")]
    windows_frame_interpolation_reason: Option<String>,
    #[cfg(target_os = "windows")]
    windows_frame_interpolation_audio_delay_original: Option<f64>,
    #[cfg(target_os = "windows")]
    windows_frame_interpolation_seen_drops: u64,
    #[cfg(target_os = "windows")]
    windows_frame_interpolation_seen_inferences: u64,
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
        #[cfg(target_os = "windows")]
        self.stop_windows_frame_interpolation_session();
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
            playback_state: "idle".to_string(),
            last_playback_event: "not-started".to_string(),
            last_playback_error: None,
            file_loaded: false,
            stop_requested: false,
            playback_transport: "none".to_string(),
            frame_interpolation_controller: FrameGenerationController::default(),
            #[cfg(target_os = "windows")]
            windows_frame_interpolation_session: None,
            #[cfg(target_os = "windows")]
            windows_frame_interpolation_reason: None,
            #[cfg(target_os = "windows")]
            windows_frame_interpolation_audio_delay_original: None,
            #[cfg(target_os = "windows")]
            windows_frame_interpolation_seen_drops: 0,
            #[cfg(target_os = "windows")]
            windows_frame_interpolation_seen_inferences: 0,
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
        #[cfg(target_os = "windows")]
        self.stop_windows_frame_interpolation_session();
        self.apply_http_headers(headers)?;
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        let audio_option = audio_path.map(|value| format!("audio-file={value}"));
        self.playback_state = "loading".to_string();
        self.last_playback_event = "load-command".to_string();
        self.last_playback_error = None;
        self.file_loaded = false;
        self.stop_requested = false;
        self.playback_transport = if path.starts_with("http://127.0.0.1:") {
            "rust-loopback"
        } else {
            "direct"
        }
        .to_string();
        let result = if let Some(option) = audio_option.as_deref() {
            self.command(&["loadfile", path, "replace", "-1", option])
        } else {
            self.command(&["loadfile", path, "replace"])
        };
        if result.is_ok() {
            if let Some(surface) = self.render_surface.as_mut() {
                surface.set_playback_active(true);
            }
        } else if let Err(error) = result.as_ref() {
            self.playback_state = "error".to_string();
            self.last_playback_event = "load-command-error".to_string();
            self.last_playback_error = Some(error.clone());
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
        self.frame_interpolation_controller
            .set_requested(settings.frame_interpolation_mode == "auto");
        self.frame_interpolation_controller
            .set_quality(&settings.frame_interpolation_quality);
        #[cfg(target_os = "windows")]
        if settings.frame_interpolation_mode != self.engine_settings.frame_interpolation_mode
            || settings.frame_interpolation_target
                != self.engine_settings.frame_interpolation_target
            || settings.frame_interpolation_quality
                != self.engine_settings.frame_interpolation_quality
        {
            self.stop_windows_frame_interpolation_session();
            if settings.frame_interpolation_mode != "auto" {
                self.windows_frame_interpolation_reason = None;
            }
        }
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

    pub fn playback_diagnostics(&self) -> DesktopPlaybackDiagnostics {
        let hardware_decoder = if self.initialized {
            self.get_property_string("hwdec-current").ok().flatten()
        } else {
            None
        };
        let video_output = if self.initialized {
            self.get_property_string("current-vo")
                .ok()
                .flatten()
                .unwrap_or_else(|| self.engine_settings.video_output.clone())
        } else {
            self.engine_settings.video_output.clone()
        };
        let frame_interpolation_hwdec = hardware_decoder.as_deref().filter(|value| {
            let value = value.to_ascii_lowercase();
            value.starts_with("d3d11va")
                || value.starts_with("dxva2")
                || value.starts_with("nvdec")
                || value.starts_with("vulkan")
        });
        #[cfg(target_os = "windows")]
        let frame_interpolation_backend_reason = self
            .windows_frame_interpolation_reason
            .as_deref()
            .unwrap_or_else(|| {
                super::windows_frame_interpolation_assets::probe()
                    .reason
                    .as_str()
            });
        #[cfg(not(target_os = "windows"))]
        let frame_interpolation_backend_reason = "Windows DirectML 视频插帧后端仅支持 Windows。";
        #[cfg(target_os = "windows")]
        let mut frame_interpolation = {
            let asset_probe = super::windows_frame_interpolation_assets::probe();
            let has_hwdec = frame_interpolation_hwdec.is_some();
            let controller_state = self.frame_interpolation_controller.state().as_str();
            let effective_state = if self.engine_settings.frame_interpolation_mode == "off" {
                "disabled"
            } else if !has_hwdec {
                "unavailable-no-hwdec"
            } else {
                controller_state
            };
            let effective_reason = if effective_state == "disabled" {
                None
            } else if effective_state == "unavailable-no-hwdec" {
                Some("当前媒体未使用硬件解码，视频插帧已自动关闭。".to_string())
            } else {
                self.windows_frame_interpolation_reason
                    .clone()
                    .or_else(|| {
                        self.frame_interpolation_controller
                            .reason()
                            .map(str::to_string)
                    })
                    .or_else(|| Some(frame_interpolation_backend_reason.to_string()))
            };
            let mut diagnostics = self
                .engine_settings
                .unavailable_frame_interpolation_diagnostics(
                    frame_interpolation_hwdec,
                    frame_interpolation_backend_reason,
                    None,
                );
            diagnostics.frame_interpolation_effective_state = effective_state.to_string();
            diagnostics.frame_interpolation_reason = effective_reason;
            diagnostics.frame_interpolation_backend = asset_probe
                .directml_flow_mask_ready
                .then(|| "windows-directml".to_string());
            diagnostics.frame_interpolation_output_hdr_mode = if effective_state == "active" {
                "scrgb".to_string()
            } else {
                "unknown".to_string()
            };
            diagnostics.frame_interpolation_capability = FrameInterpolationCapability {
                supported: asset_probe.directml_flow_mask_ready,
                backend: asset_probe
                    .directml_flow_mask_ready
                    .then(|| "windows-directml".to_string()),
                reason: (!asset_probe.directml_flow_mask_ready).then(|| asset_probe.reason.clone()),
                api_level: None,
                gpu_name: None,
                gpu_adapter_id: None,
                fp16: asset_probe.directml_flow_mask_ready,
                hdr_kinds: if asset_probe.directml_flow_mask_ready {
                    ["sdr", "pq", "hlg", "hdr10plus", "dolby-vision"]
                        .into_iter()
                        .map(str::to_string)
                        .collect()
                } else {
                    Vec::new()
                },
                max_target_fps: asset_probe.directml_flow_mask_ready.then_some(120),
            };
            diagnostics
        };
        #[cfg(not(target_os = "windows"))]
        let mut frame_interpolation = self
            .engine_settings
            .unavailable_frame_interpolation_diagnostics(
                frame_interpolation_hwdec,
                frame_interpolation_backend_reason,
                None,
            );
        frame_interpolation.frame_interpolation_input_hdr_kind = self.input_hdr_kind();
        frame_interpolation.frame_interpolation_flow_scale =
            Some(self.frame_interpolation_controller.flow_scale());
        let (p50, p95) = self.frame_interpolation_controller.model_time_percentiles();
        frame_interpolation.frame_interpolation_model_time_p50_ms = p50;
        frame_interpolation.frame_interpolation_model_time_p95_ms = p95;
        frame_interpolation.frame_interpolation_dropped_frames =
            self.frame_interpolation_controller.dropped_frames();

        DesktopPlaybackDiagnostics {
            state: self.playback_state.clone(),
            last_event: self.last_playback_event.clone(),
            last_error: self.last_playback_error.clone(),
            file_loaded: self.file_loaded,
            video_format: self
                .initialized
                .then(|| self.get_property_string("video-format").ok().flatten())
                .flatten(),
            audio_codec: self
                .initialized
                .then(|| self.get_property_string("audio-codec-name").ok().flatten())
                .flatten(),
            vo_configured: self.initialized
                && self.get_property_flag("vo-configured").unwrap_or(0) != 0,
            hardware_decoder,
            video_output,
            video_output_fallback_used: false,
            playback_transport: self.playback_transport.clone(),
            fsr_status: self.fsr_status.clone(),
            fsr_reason: self.fsr_reason.clone(),
            frame_interpolation,
            logs: Vec::new(),
        }
    }

    fn input_hdr_kind(&self) -> String {
        if !self.initialized || !self.file_loaded {
            return "unknown".to_string();
        }
        let dovi_profile = self
            .get_property_string("video-params/dolby-vision-profile")
            .ok()
            .flatten()
            .or_else(|| {
                self.get_property_string("current-tracks/video/demux-dovi-profile")
                    .ok()
                    .flatten()
            });
        if dovi_profile.as_deref().is_some_and(|value| {
            let value = value.trim();
            !value.is_empty() && value != "unknown"
        }) {
            return "dolby-vision".to_string();
        }
        if self
            .get_property_string("video-params/scene-max-r")
            .ok()
            .flatten()
            .is_some()
        {
            return "hdr10plus".to_string();
        }
        match self
            .get_property_string("video-params/gamma")
            .ok()
            .flatten()
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "pq" => "pq".to_string(),
            "hlg" => "hlg".to_string(),
            _ => "sdr".to_string(),
        }
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
                // Let gpu-next/libplacebo preserve the source HDR intent all the way into the
                // D3D11 swapchain. source-dynamic converts HDR10+/Dolby Vision scene metadata to
                // dynamic HDR10 luminance hints without pretending to synthesize new DV RPUs.
                self.set_option("target-colorspace-hint", "auto")?;
                self.set_option("target-colorspace-hint-mode", "source-dynamic")?;
                self.set_option("hdr-compute-peak", "auto")?;
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
                    self.frame_interpolation_controller
                        .on_media_event(MediaEvent::SurfaceReady);
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
        #[cfg(target_os = "windows")]
        {
            self.windows_frame_interpolation_reason =
                Some("播放已暂停；插帧输出已隐藏，恢复播放后会重新建立新一代帧队列。".to_string());
            self.stop_windows_frame_interpolation_session();
        }
        self.set_property("pause", "true")
    }

    pub fn resume(&mut self) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.set_property("pause", "false")?;
        #[cfg(target_os = "windows")]
        if self.engine_settings.frame_interpolation_mode == "auto" {
            self.windows_frame_interpolation_reason =
                Some("播放已恢复，正在重新建立插帧安全门。".to_string());
        }
        Ok(())
    }

    pub fn stop(&mut self) -> Result<(), String> {
        #[cfg(target_os = "windows")]
        self.stop_windows_frame_interpolation_session();
        self.frame_interpolation_controller
            .on_media_event(MediaEvent::EndFile);
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        if !self.initialized {
            self.playback_state = "idle".to_string();
            self.last_playback_event = "stopped".to_string();
            self.file_loaded = false;
            return Ok(());
        }
        self.stop_requested = true;
        let result = self.command(&["stop"]);
        if result.is_ok() {
            self.playback_state = "idle".to_string();
            self.last_playback_event = "stopped".to_string();
            self.file_loaded = false;
        }
        result
    }

    pub fn seek(&mut self, position: f64) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        #[cfg(target_os = "windows")]
        self.stop_windows_frame_interpolation_session();
        self.frame_interpolation_controller
            .on_media_event(MediaEvent::Seek);
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

    #[cfg(target_os = "windows")]
    fn start_windows_frame_interpolation_session(&mut self, hardware_decode_ready: bool) {
        self.stop_windows_frame_interpolation_session();
        if self.engine_settings.frame_interpolation_mode != "auto" || !hardware_decode_ready {
            return;
        }
        let asset_probe = super::windows_frame_interpolation_assets::probe();
        if !asset_probe.directml_flow_mask_ready {
            self.windows_frame_interpolation_reason = Some(asset_probe.reason.clone());
            return;
        }
        let Some(source_wid) = self
            .render_surface
            .as_ref()
            .map(NativeRenderSurface::mpv_wid)
        else {
            self.windows_frame_interpolation_reason =
                Some("Windows mpv source HWND 不存在，已保持普通播放。".to_string());
            return;
        };
        let target_fps = u32::from(
            self.engine_settings
                .frame_interpolation_target_fps()
                .unwrap_or(60),
        );
        let hdr_input = matches!(
            self.input_hdr_kind().as_str(),
            "pq" | "hlg" | "hdr10plus" | "dolby-vision"
        );
        match super::windows_frame_interpolation_assets::WindowsFrameInterpolationSession::start(
            &source_wid,
            target_fps,
            hdr_input,
            self.frame_interpolation_controller.flow_scale(),
        ) {
            Ok(session) => {
                self.windows_frame_interpolation_session = Some(session);
                self.windows_frame_interpolation_seen_drops = 0;
                self.windows_frame_interpolation_seen_inferences = 0;
                self.windows_frame_interpolation_reason = Some(
                    "Windows 实时 WGC/FP16 隐藏输出正在自检；原 mpv 画面保持可见。".to_string(),
                );
            }
            Err(reason) => {
                self.windows_frame_interpolation_reason = Some(reason);
            }
        }
    }

    #[cfg(target_os = "windows")]
    fn poll_windows_frame_interpolation_session(&mut self) {
        let subtitle_active = self.track_list().ok().is_some_and(|tracks| {
            tracks
                .iter()
                .any(|track| track.kind == "sub" && track.selected)
        });
        self.frame_interpolation_controller
            .set_graphic_subtitle_active(subtitle_active);
        if subtitle_active {
            self.windows_frame_interpolation_reason = Some(
                "当前字幕仍在 mpv 视频层中；为避免字幕被插帧拉丝或被输出层遮挡，Windows 插帧已自动旁路。"
                    .to_string(),
            );
            self.stop_windows_frame_interpolation_session();
            return;
        }
        let paused = self.get_property_flag("pause").unwrap_or(1) != 0;
        if paused {
            if self.windows_frame_interpolation_session.is_some() {
                self.windows_frame_interpolation_reason = Some(
                    "播放已暂停；插帧输出已隐藏，恢复播放后会重新建立新一代帧队列。".to_string(),
                );
                self.stop_windows_frame_interpolation_session();
            }
            return;
        }
        if !self.file_loaded || self.engine_settings.frame_interpolation_mode != "auto" {
            self.stop_windows_frame_interpolation_session();
            return;
        }
        let media_pts = self
            .get_property_double("playback-time")
            .or_else(|_| self.get_property_double("time-pos"))
            .unwrap_or(f64::NAN);
        let container_fps = self
            .get_property_double("container-fps")
            .unwrap_or(f64::NAN);
        let estimated_fps = self
            .get_property_double("estimated-vf-fps")
            .unwrap_or(f64::NAN);
        let timing_reliable =
            has_reliable_windows_cfr_timing(media_pts, container_fps, estimated_fps);
        let hardware_decode_ready = self
            .get_property_string("hwdec-current")
            .ok()
            .flatten()
            .is_some_and(|value| is_windows_frame_interpolation_hwdec(&value));
        if !hardware_decode_ready {
            self.stop_windows_frame_interpolation_session();
            self.frame_interpolation_controller
                .set_gates(false, true, false);
            self.windows_frame_interpolation_reason = Some(
                "当前媒体未使用受支持的硬件解码；插帧已关闭，mpv 原画面继续播放。".to_string(),
            );
            return;
        }
        if !timing_reliable {
            self.stop_windows_frame_interpolation_session();
            self.frame_interpolation_controller
                .set_gates(true, true, false);
            self.windows_frame_interpolation_reason = Some(
                "当前视频帧率未知或检测为 VFR；为避免节奏和音画同步错误，插帧保持关闭。"
                    .to_string(),
            );
            return;
        }
        let target_fps = f64::from(
            self.engine_settings
                .frame_interpolation_target_fps()
                .unwrap_or(60),
        );
        if !source_needs_windows_frame_interpolation(estimated_fps, target_fps) {
            self.stop_windows_frame_interpolation_session();
            self.frame_interpolation_controller
                .set_gates(true, true, false);
            self.windows_frame_interpolation_reason = Some(format!(
                "源帧率 {estimated_fps:.3}fps 已达到目标 {target_fps:.0}fps，无需插帧。"
            ));
            return;
        }
        if self.windows_frame_interpolation_session.is_none() {
            self.start_windows_frame_interpolation_session(true);
        }
        if let Some(session) = self.windows_frame_interpolation_session.as_ref() {
            session.update_timing(media_pts, estimated_fps, timing_reliable, paused);
        }
        let status = match self
            .windows_frame_interpolation_session
            .as_ref()
            .map(|session| session.poll())
        {
            Some(Ok(status)) => Some(status),
            Some(Err(reason)) => {
                self.windows_frame_interpolation_reason = Some(reason.clone());
                self.frame_interpolation_controller.backend_failed(reason);
                self.stop_windows_frame_interpolation_session();
                None
            }
            None => None,
        };
        let Some(status) = status else {
            return;
        };
        let new_drops = status
            .dropped_output_ticks
            .saturating_sub(self.windows_frame_interpolation_seen_drops);
        self.windows_frame_interpolation_seen_drops = status.dropped_output_ticks;
        if new_drops > 0 {
            self.frame_interpolation_controller.record_drops(new_drops);
        }
        if status.inference_sample_count > self.windows_frame_interpolation_seen_inferences
            && status.latest_inference_ms.is_finite()
            && status.latest_inference_ms > 0.0
        {
            let previous_flow_scale = self.frame_interpolation_controller.flow_scale();
            self.frame_interpolation_controller
                .record_model_time(status.latest_inference_ms, target_fps as u16);
            self.windows_frame_interpolation_seen_inferences = status.inference_sample_count;
            if self.frame_interpolation_controller.flow_scale() != previous_flow_scale {
                let flow_scale = self.frame_interpolation_controller.flow_scale();
                self.stop_windows_frame_interpolation_session();
                self.windows_frame_interpolation_reason = Some(format!(
                    "DirectML 推理超过预算，正在以 Flow Scale {flow_scale:.2} 重建低成本推理会话。"
                ));
                return;
            }
        }
        self.windows_frame_interpolation_reason = Some(status.reason.clone());
        if status.hidden_first_present && !status.captured_pair {
            self.frame_interpolation_controller.backend_failed(
                "Windows 隐藏输出在形成有效实时帧对前被 Present，已拒绝切换并保持 mpv 原画面。",
            );
        } else if status.captured_pair && !status.hidden_first_present && status.finished {
            self.frame_interpolation_controller.backend_failed(
                "Windows 实时帧对已捕获，但 FP16 隐藏输出未完成 Present；已保持 mpv 原画面。",
            );
        }
        if status.device_lost {
            self.stop_windows_frame_interpolation_session();
            self.frame_interpolation_controller
                .backend_failed("Windows GPU device lost；已原子回退到 mpv 原画面。");
            self.windows_frame_interpolation_reason =
                Some("Windows GPU device lost；已原子回退到 mpv 原画面。".to_string());
            return;
        }
        if status.finished {
            self.stop_windows_frame_interpolation_session();
            return;
        }
        if status.cadence_stalled {
            self.restore_windows_frame_interpolation_audio_delay();
            let reason = format!(
                "Windows 插帧未能持续输出（已生成 {} 帧、丢弃 {} 个过期输出，最近推理 {:.2}ms）；覆盖画面已隐藏，mpv 原画面继续播放，后端正在等待稳定帧序列后自动重试。",
                status.generated_present_count,
                status.dropped_output_ticks,
                status.latest_inference_ms,
            );
            self.windows_frame_interpolation_reason = Some(reason.clone());
            self.frame_interpolation_controller.backend_stalled(reason);
            return;
        }
        self.frame_interpolation_controller
            .set_gates(true, true, true);
        if status.hidden_first_present && !status.generated_first_present {
            if let Err(reason) = self.apply_windows_frame_interpolation_audio_delay(estimated_fps) {
                self.windows_frame_interpolation_reason = Some(reason.clone());
                self.frame_interpolation_controller.backend_failed(reason);
                self.stop_windows_frame_interpolation_session();
                return;
            }
            let reveal_result = self
                .windows_frame_interpolation_session
                .as_ref()
                .expect("session exists after successful status poll")
                .reveal_after_safe_gates();
            if let Err(reason) = reveal_result {
                self.windows_frame_interpolation_reason = Some(reason.clone());
                self.frame_interpolation_controller.backend_failed(reason);
                self.stop_windows_frame_interpolation_session();
                return;
            }
        }
        if status.generated_first_present
            || self
                .windows_frame_interpolation_session
                .as_ref()
                .and_then(|session| session.poll().ok())
                .is_some_and(|snapshot| snapshot.generated_first_present)
        {
            let generation = self.frame_interpolation_controller.generation();
            if self
                .frame_interpolation_controller
                .backend_first_frame(generation)
            {
                self.windows_frame_interpolation_reason = Some(format!(
                    "Windows DirectML 插帧已启用：{estimated_fps:.3}→{target_fps:.0}fps，实测输出 {:.1}fps，已持续 Present {} 帧（其中生成 {} 帧），FP16 scRGB 输出，音频已补偿一帧前视延迟。",
                    status.measured_output_fps,
                    status.successful_present_count,
                    status.generated_present_count,
                ));
            }
        }
    }

    #[cfg(target_os = "windows")]
    fn stop_windows_frame_interpolation_session(&mut self) {
        // Dropping joins the native worker after it hides and destroys only the generated-output
        // HWND. The source mpv HWND is never destroyed or hidden by this path.
        self.windows_frame_interpolation_session.take();
        self.restore_windows_frame_interpolation_audio_delay();
        self.windows_frame_interpolation_seen_drops = 0;
        self.windows_frame_interpolation_seen_inferences = 0;
    }

    #[cfg(target_os = "windows")]
    fn restore_windows_frame_interpolation_audio_delay(&mut self) {
        let Some(original_delay) = self.windows_frame_interpolation_audio_delay_original else {
            return;
        };
        if !self.initialized || self.ctx.is_null() {
            self.windows_frame_interpolation_audio_delay_original = None;
            return;
        }
        if self
            .set_property("audio-delay", &original_delay.to_string())
            .is_ok()
        {
            self.windows_frame_interpolation_audio_delay_original = None;
        } else {
            self.windows_frame_interpolation_reason = Some(
                "恢复用户原 audio-delay 失败；已保留原值并将在下一次旁路/停止时重试。".to_string(),
            );
        }
    }

    #[cfg(target_os = "windows")]
    fn apply_windows_frame_interpolation_audio_delay(
        &mut self,
        source_fps: f64,
    ) -> Result<(), String> {
        if self
            .windows_frame_interpolation_audio_delay_original
            .is_some()
        {
            return Ok(());
        }
        if !source_fps.is_finite() || source_fps <= 0.0 {
            return Err("无法计算插帧前视延迟；已保持 mpv 原画面。".to_string());
        }
        let original_delay = self
            .get_property_double("audio-delay")
            .map_err(|_| "无法读取用户当前的 audio-delay；已拒绝启用插帧。".to_string())?;
        let compensated_delay = original_delay + 1.0 / source_fps;
        self.set_property("audio-delay", &compensated_delay.to_string())
            .map_err(|_| "无法应用一帧音频前视补偿；已拒绝启用插帧。".to_string())?;
        self.windows_frame_interpolation_audio_delay_original = Some(original_delay);
        Ok(())
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
            if event_id == mpv_event_id_MPV_EVENT_START_FILE {
                #[cfg(target_os = "windows")]
                self.stop_windows_frame_interpolation_session();
                self.frame_interpolation_controller
                    .on_media_event(MediaEvent::StartFile);
                self.playback_state = "loading".to_string();
                self.last_playback_event = "start-file".to_string();
                self.last_playback_error = None;
                self.file_loaded = false;
                self.stop_requested = false;
            } else if event_id == mpv_event_id_MPV_EVENT_FILE_LOADED {
                let hardware_decode_ready = self
                    .get_property_string("hwdec-current")
                    .ok()
                    .flatten()
                    .is_some_and(|value| {
                        let value = value.to_ascii_lowercase();
                        value.starts_with("d3d11va")
                            || value.starts_with("dxva2")
                            || value.starts_with("nvdec")
                            || value.starts_with("vulkan")
                    });
                self.frame_interpolation_controller
                    .set_gates(hardware_decode_ready, true, false);
                self.frame_interpolation_controller
                    .on_media_event(MediaEvent::FileLoaded);
                self.playback_state = "playing".to_string();
                self.last_playback_event = "file-loaded".to_string();
                self.last_playback_error = None;
                self.file_loaded = true;
            } else if event_id == mpv_event_id_MPV_EVENT_VIDEO_RECONFIG {
                #[cfg(target_os = "windows")]
                self.stop_windows_frame_interpolation_session();
                self.frame_interpolation_controller
                    .on_media_event(MediaEvent::VideoReconfig);
                self.last_playback_event = "video-reconfig".to_string();
            } else if event_id == mpv_event_id_MPV_EVENT_END_FILE {
                #[cfg(target_os = "windows")]
                self.stop_windows_frame_interpolation_session();
                self.frame_interpolation_controller
                    .on_media_event(MediaEvent::EndFile);
                if self.stop_requested {
                    self.playback_state = "idle".to_string();
                    self.last_playback_event = "stopped".to_string();
                    self.stop_requested = false;
                } else if self.file_loaded {
                    self.playback_state = "ended".to_string();
                    self.last_playback_event = "end-file".to_string();
                } else {
                    self.playback_state = "error".to_string();
                    self.last_playback_event = "end-file-error".to_string();
                    self.last_playback_error =
                        Some("媒体文件未能完成加载，请打开播放诊断查看原因。".to_string());
                }
                self.file_loaded = false;
            }
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
        #[cfg(target_os = "windows")]
        self.poll_windows_frame_interpolation_session();
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

#[cfg(all(test, target_os = "windows"))]
mod windows_frame_interpolation_gate_tests {
    use super::{has_reliable_windows_cfr_timing, source_needs_windows_frame_interpolation};

    #[test]
    fn rejects_unknown_vfr_and_sources_already_at_target_before_session_start() {
        assert!(has_reliable_windows_cfr_timing(12.0, 24.0, 24.0));
        assert!(!has_reliable_windows_cfr_timing(12.0, 24.0, 23.8));
        assert!(!has_reliable_windows_cfr_timing(f64::NAN, 24.0, 24.0));
        assert!(source_needs_windows_frame_interpolation(24.0, 60.0));
        assert!(!source_needs_windows_frame_interpolation(60.0, 60.0));
        assert!(!source_needs_windows_frame_interpolation(120.0, 60.0));
    }
}

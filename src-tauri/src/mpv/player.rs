use std::{
    cell::{Cell, RefCell},
    ffi::{CStr, CString},
    os::raw::{c_char, c_int, c_void},
    path::PathBuf,
    ptr,
    sync::{Arc, Mutex},
};

use libmpv_sys::{
    mpv_command, mpv_command_ret, mpv_create, mpv_end_file_reason_MPV_END_FILE_REASON_EOF,
    mpv_end_file_reason_MPV_END_FILE_REASON_ERROR, mpv_end_file_reason_MPV_END_FILE_REASON_QUIT,
    mpv_end_file_reason_MPV_END_FILE_REASON_REDIRECT, mpv_end_file_reason_MPV_END_FILE_REASON_STOP,
    mpv_error_string, mpv_event_end_file, mpv_event_hook, mpv_event_id_MPV_EVENT_END_FILE,
    mpv_event_id_MPV_EVENT_FILE_LOADED, mpv_event_id_MPV_EVENT_HOOK,
    mpv_event_id_MPV_EVENT_LOG_MESSAGE, mpv_event_id_MPV_EVENT_NONE,
    mpv_event_id_MPV_EVENT_PLAYBACK_RESTART, mpv_event_id_MPV_EVENT_START_FILE,
    mpv_event_id_MPV_EVENT_VIDEO_RECONFIG, mpv_event_log_message, mpv_event_start_file,
    mpv_format_MPV_FORMAT_DOUBLE, mpv_format_MPV_FORMAT_FLAG, mpv_format_MPV_FORMAT_INT64,
    mpv_format_MPV_FORMAT_NODE, mpv_format_MPV_FORMAT_NODE_ARRAY, mpv_format_MPV_FORMAT_NODE_MAP,
    mpv_format_MPV_FORMAT_STRING, mpv_free, mpv_free_node_contents, mpv_get_property,
    mpv_get_property_string, mpv_handle, mpv_hook_add, mpv_hook_continue, mpv_initialize, mpv_node,
    mpv_node_list, mpv_request_log_messages, mpv_set_option_string, mpv_set_property,
    mpv_set_property_string, mpv_terminate_destroy, mpv_wait_event,
};

use super::{
    color::{
        color_metadata, target_transfer_mode, transfer_mode, MpvColorDiagnostics,
        MpvColorOutputMode,
    },
    diagnostics::NativeLogBuffer,
    playback::MpvEndReason,
    render::{current_render_state, MpvRenderState, RenderStatus},
    surface::{NativeRenderSurface, OwnerWindowEvent, RenderSurfaceBounds, ZOrderStrategy},
};
use crate::commands::player_shared::{
    http_read_profile_diagnostic, playback_file_options, MpvEngineSettings, MpvHttpReadProfile,
};

pub type MpvState = Arc<Mutex<MpvPlayer>>;

const FSR_LOAD_HOOK_ID: u64 = 1;

#[derive(Debug, Default)]
pub struct MpvEventBatch {
    pub file_loaded: bool,
    pub video_ready: bool,
    pub reached_limit: bool,
    pub fsr_fallback: bool,
}

#[derive(Debug, PartialEq, Eq)]
enum EndFileDisposition {
    Stopped,
    Redirecting,
    Ended,
    Failed,
}

fn end_file_disposition(
    reason: i32,
    error_code: i32,
    file_loaded: bool,
    stop_requested: bool,
) -> EndFileDisposition {
    if stop_requested
        || reason == mpv_end_file_reason_MPV_END_FILE_REASON_STOP as i32
        || reason == mpv_end_file_reason_MPV_END_FILE_REASON_QUIT as i32
    {
        EndFileDisposition::Stopped
    } else if reason == mpv_end_file_reason_MPV_END_FILE_REASON_REDIRECT as i32 {
        EndFileDisposition::Redirecting
    } else if reason == mpv_end_file_reason_MPV_END_FILE_REASON_EOF as i32
        && error_code >= 0
        && file_loaded
    {
        EndFileDisposition::Ended
    } else {
        EndFileDisposition::Failed
    }
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
    pub end_reason: Option<MpvEndReason>,
    pub position: f64,
    pub duration: f64,
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
    pub color: Option<MpvColorDiagnostics>,
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
    fsr_applied_configuration: Option<(String, String)>,
    fsr_failed_for_file: bool,
    fsr_load_hook_available: bool,
    color_metadata_ready: bool,
    playback_state: String,
    last_playback_event: String,
    last_playback_error: Option<String>,
    end_reason: Option<MpvEndReason>,
    last_position: Cell<f64>,
    last_duration: Cell<f64>,
    last_video_format: RefCell<Option<String>>,
    last_audio_codec: RefCell<Option<String>>,
    last_hardware_decoder: RefCell<Option<String>>,
    source_color_snapshot: MpvColorDiagnostics,
    file_loaded: bool,
    stop_requested: bool,
    playback_transport: String,
    http_read_profile_diagnostic: Option<String>,
    playback_epoch: Option<u64>,
    expected_entry_id: Option<i64>,
    event_entry_id: Option<i64>,
    logs_frozen: bool,
    initialization_logs: NativeLogBuffer,
    playback_logs: NativeLogBuffer,
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
            fsr_applied_configuration: None,
            fsr_failed_for_file: false,
            fsr_load_hook_available: false,
            color_metadata_ready: false,
            playback_state: "idle".to_string(),
            last_playback_event: "not-started".to_string(),
            last_playback_error: None,
            end_reason: None,
            last_position: Cell::new(0.0),
            last_duration: Cell::new(0.0),
            last_video_format: RefCell::new(None),
            last_audio_codec: RefCell::new(None),
            last_hardware_decoder: RefCell::new(None),
            source_color_snapshot: MpvColorDiagnostics::default(),
            file_loaded: false,
            stop_requested: false,
            playback_transport: "none".to_string(),
            http_read_profile_diagnostic: None,
            playback_epoch: None,
            expected_entry_id: None,
            event_entry_id: None,
            logs_frozen: true,
            initialization_logs: NativeLogBuffer::default(),
            playback_logs: NativeLogBuffer::default(),
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

    pub fn capture_screenshot(&self, path: &std::path::Path) -> Result<(), String> {
        if !self.initialized || !self.file_loaded {
            return Err("视频画面尚未加载，无法截图。".to_string());
        }
        let target = path
            .to_str()
            .ok_or_else(|| "截图路径包含不支持的字符。".to_string())?;
        // The array API passes the filename literally. subtitles excludes the WebView and mpv OSD.
        self.command(&["screenshot-to-file", target, "subtitles"])
    }

    pub fn playback_epoch(&self) -> Option<u64> {
        self.playback_epoch
    }

    pub fn begin_load_attempt(&mut self, epoch: u64) {
        if self.playback_epoch.is_none() {
            // The first load can precede the 250ms event tick. Keep queued initialization
            // evidence in its own phase before opening this media's diagnostic window.
            self.drain_events(64);
        }
        self.playback_epoch = Some(epoch);
        self.http_read_profile_diagnostic = None;
        self.expected_entry_id = None;
        self.logs_frozen = true;
        self.playback_logs = NativeLogBuffer::default();
        self.playback_state = "loading".to_string();
        self.last_playback_event = "preparing-load".to_string();
        self.last_playback_error = None;
        self.end_reason = None;
        self.file_loaded = false;
        self.color_metadata_ready = false;
        self.stop_requested = false;
        self.last_position.set(0.0);
        self.last_duration.set(0.0);
        *self.last_video_format.borrow_mut() = None;
        *self.last_audio_codec.borrow_mut() = None;
        *self.last_hardware_decoder.borrow_mut() = None;
        self.source_color_snapshot = MpvColorDiagnostics::default();
        self.playback_transport = "none".to_string();
    }

    pub fn fail_load_attempt(&mut self, epoch: u64, error: &str) {
        if self.playback_epoch != Some(epoch) {
            return;
        }
        self.playback_state = "error".to_string();
        self.last_playback_event = "load-command-error".to_string();
        self.last_playback_error = Some(error.to_string());
        self.end_reason = Some(MpvEndReason::Error);
        self.logs_frozen = true;
        self.file_loaded = false;
        self.color_metadata_ready = false;
    }

    pub fn load_file_with_headers(
        &mut self,
        path: &str,
        headers: &[(String, String)],
        audio_path: Option<&str>,
        http_read_profile: Option<MpvHttpReadProfile>,
    ) -> Result<(), String> {
        self.ensure_initialized_fallback()?;
        self.apply_http_headers(headers)?;
        // Clear synchronously before loadfile can render the next source. on_load also covers
        // libmpv playlist/redirect transitions that do not issue another Player load command.
        self.prepare_color_for_load()?;
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        let profile_supported = http_read_profile.is_some()
            && self
                .get_property_string("option-info/curl-max-request-size/name")?
                .as_deref()
                == Some("curl-max-request-size")
            && self
                .get_property_string("option-info/curl-enabled/name")?
                .as_deref()
                == Some("curl-enabled");
        self.http_read_profile_diagnostic =
            http_read_profile_diagnostic(http_read_profile, profile_supported);
        let file_options =
            playback_file_options(audio_path, http_read_profile.is_some() && profile_supported);
        self.playback_state = "loading".to_string();
        self.last_playback_event = "load-command".to_string();
        self.last_playback_error = None;
        self.end_reason = None;
        self.last_position.set(0.0);
        self.last_duration.set(0.0);
        *self.last_video_format.borrow_mut() = None;
        *self.last_audio_codec.borrow_mut() = None;
        *self.last_hardware_decoder.borrow_mut() = None;
        self.source_color_snapshot = MpvColorDiagnostics::default();
        self.file_loaded = false;
        self.stop_requested = false;
        self.playback_transport = if path.starts_with("http://127.0.0.1:") {
            "rust-loopback"
        } else {
            "direct"
        }
        .to_string();
        let result = if let Some(option) = file_options.as_deref() {
            self.load_command(&["loadfile", path, "replace", "-1", option])
        } else {
            self.load_command(&["loadfile", path, "replace"])
        };
        match result {
            Ok(entry_id) => {
                self.expected_entry_id = Some(entry_id);
                if let Some(surface) = self.render_surface.as_mut() {
                    surface.set_playback_active(true);
                }
                Ok(())
            }
            Err(error) => {
                if let Some(epoch) = self.playback_epoch {
                    self.fail_load_attempt(epoch, &error);
                }
                Err(error)
            }
        }
    }

    pub fn apply_engine_settings(
        &mut self,
        settings: MpvEngineSettings,
        shader_path: Option<PathBuf>,
    ) -> Result<(), String> {
        let settings = settings.validated()?;
        let renderer_changed = settings.video_output != self.engine_settings.video_output
            || settings.hardware_decoder != self.engine_settings.hardware_decoder;
        let fsr_changed = settings.fsr_mode != self.engine_settings.fsr_mode
            || settings.fsr_sharpness != self.engine_settings.fsr_sharpness
            || settings.fsr_denoise != self.engine_settings.fsr_denoise
            || settings.fsr_target != self.engine_settings.fsr_target
            || shader_path != self.fsr_shader_path;
        if self.initialized {
            if renderer_changed {
                self.clear_fsr_shaders()?;
                self.color_metadata_ready = false;
            }
            #[cfg(target_os = "windows")]
            if renderer_changed {
                self.configure_windows_color_output(&settings.video_output)?;
            }
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
        if fsr_changed || renderer_changed {
            self.fsr_failed_for_file = false;
        }
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
        self.capture_playback_facts();
        let end_reason = if self.playback_state == "error" {
            Some(MpvEndReason::Error)
        } else if self.file_loaded
            && self.playback_state == "playing"
            && !self.stop_requested
            && self.last_playback_error.is_none()
            && self.get_property_flag("vo-configured").unwrap_or(0) != 0
            && self.get_property_flag("eof-reached").unwrap_or(0) != 0
        {
            // keep-open retains the file and emits no END_FILE at natural EOF.
            Some(MpvEndReason::Eof)
        } else {
            self.end_reason
        };
        let video_output = if self.initialized {
            self.get_property_string("current-vo")
                .ok()
                .flatten()
                .unwrap_or_else(|| self.engine_settings.video_output.clone())
        } else {
            self.engine_settings.video_output.clone()
        };
        DesktopPlaybackDiagnostics {
            state: if end_reason == Some(MpvEndReason::Eof) {
                "ended".to_string()
            } else {
                self.playback_state.clone()
            },
            end_reason,
            position: self.time_pos(),
            duration: self.duration(),
            last_event: self.last_playback_event.clone(),
            last_error: self.last_playback_error.clone(),
            file_loaded: self.file_loaded,
            video_format: self.last_video_format.borrow().clone(),
            audio_codec: self.last_audio_codec.borrow().clone(),
            vo_configured: self.initialized
                && self.get_property_flag("vo-configured").unwrap_or(0) != 0,
            hardware_decoder: self.last_hardware_decoder.borrow().clone(),
            video_output,
            video_output_fallback_used: false,
            playback_transport: self.playback_transport.clone(),
            fsr_status: self.fsr_status.clone(),
            fsr_reason: self.fsr_reason.clone(),
            color: Some(self.color_diagnostics()),
            logs: self.native_log_snapshot(),
        }
    }

    fn source_color_metadata(&self) -> MpvColorDiagnostics {
        if !self.initialized || !self.file_loaded || !self.color_metadata_ready {
            return if self.playback_state == "error" || self.playback_state == "ended" {
                self.source_color_snapshot.clone()
            } else {
                MpvColorDiagnostics::default()
            };
        }
        let metadata =
            |property: &str| color_metadata(self.get_property_string(property).ok().flatten());
        MpvColorDiagnostics {
            source_gamma: metadata("video-params/gamma"),
            source_primaries: metadata("video-params/primaries"),
            source_pixel_format: metadata("video-params/pixelformat"),
            source_hardware_pixel_format: metadata("video-params/hw-pixelformat"),
            dolby_vision_profile: self
                .get_property_int64("current-tracks/video/dolby-vision-profile")
                .ok()
                .and_then(|profile| u8::try_from(profile).ok())
                .filter(|profile| *profile > 0),
            ..MpvColorDiagnostics::default()
        }
    }

    fn color_diagnostics(&self) -> MpvColorDiagnostics {
        let mut color = self.source_color_metadata();
        if !self.initialized || !self.file_loaded || !self.color_metadata_ready {
            color.reason = Some(
                if self.playback_state == "error" {
                    "保留故障前确认的片源信息；渲染目标当前不可用。"
                } else {
                    "等待当前片源和渲染目标的色彩信息。"
                }
                .to_string(),
            );
            return color;
        }
        if self.get_property_flag("vo-configured").unwrap_or(0) != 0 {
            let metadata =
                |property: &str| color_metadata(self.get_property_string(property).ok().flatten());
            color.target_gamma = metadata("video-target-params/gamma");
            color.target_primaries = metadata("video-target-params/primaries");
            color.target_pixel_format = metadata("video-target-params/pixelformat");
            color.output_mode = target_transfer_mode(color.target_gamma.as_deref());
            color.gpu_context = metadata("current-gpu-context");
        }
        color.reason = Some(
            if color.dolby_vision_profile.is_some() && self.engine_settings.video_output == "gpu" {
                "当前 gpu 渲染器不能保证杜比视界元数据处理，请使用 gpu-next。"
            } else {
                match color.output_mode {
                    MpvColorOutputMode::Hdr => {
                        "渲染目标已报告 HDR；物理显示模式和输出位深尚未验证。"
                    }
                    MpvColorOutputMode::Sdr
                        if color.dolby_vision_profile.is_some()
                            || transfer_mode(color.source_gamma.as_deref())
                                == MpvColorOutputMode::Hdr =>
                    {
                        "当前渲染目标为 SDR，HDR 片源由渲染器进行色调映射。"
                    }
                    MpvColorOutputMode::Sdr => "当前渲染目标为 SDR。",
                    MpvColorOutputMode::Unknown => "渲染目标的色彩信息暂不可用。",
                }
            }
            .to_string(),
        );
        color
    }

    #[cfg(target_os = "windows")]
    fn configure_windows_color_output(&self, video_output: &str) -> Result<(), String> {
        let context = if video_output == "gpu-next" {
            "d3d11"
        } else {
            "auto"
        };
        for (option, value) in [
            ("gpu-api", context),
            ("gpu-context", context),
            ("target-colorspace-hint", "auto"),
            ("d3d11-output-csp", "auto"),
            ("d3d11-output-format", "auto"),
        ] {
            let result = if self.initialized {
                self.set_property(option, value)
            } else {
                self.set_option(option, value)
            };
            result.map_err(|_| "Windows D3D11 自动色彩输出配置失败。".to_string())?;
        }
        Ok(())
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
                self.configure_windows_color_output(&video_output)?;
                self.set_option("vo", &video_output)?;
                self.set_option("glsl-shaders", "")?;
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
                    // Initialization alone does not establish SDR source metadata. Keep the
                    // shader detached until the current file has reported its color facts.
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
        self.capture_playback_facts();
        self.logs_frozen = true;
        let failed = self.playback_state == "error";
        if !failed {
            self.end_reason = Some(MpvEndReason::Stop);
        }
        if let Some(surface) = self.render_surface.as_mut() {
            surface.set_playback_active(false);
        }
        self.color_metadata_ready = false;
        let clear_result = self.clear_fsr_shaders();
        self.fsr_status = if self.engine_settings.fsr_mode == "off" {
            "disabled"
        } else {
            "bypassed-unknown"
        }
        .to_string();
        self.fsr_reason = None;
        if !self.initialized {
            self.playback_state = "idle".to_string();
            self.last_playback_event = "stopped".to_string();
            self.file_loaded = false;
            return Ok(());
        }
        self.stop_requested = true;
        let result = self.command(&["stop"]);
        if result.is_ok() && !failed {
            self.playback_state = "idle".to_string();
            self.last_playback_event = "stopped".to_string();
            self.file_loaded = false;
        }
        result.and(clear_result)
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
        if self.file_loaded && self.playback_state == "playing" {
            if let Ok(value) = self.get_property_double("time-pos") {
                if value.is_finite() && value >= 0.0 {
                    self.last_position.set(value);
                }
            }
        }
        self.last_position.get()
    }

    pub fn capture_playback_facts(&self) {
        self.time_pos();
        self.duration();
        if !self.initialized || !self.file_loaded || self.playback_state != "playing" {
            return;
        }
        for (name, cache) in [
            ("video-format", &self.last_video_format),
            ("audio-codec-name", &self.last_audio_codec),
            ("hwdec-current", &self.last_hardware_decoder),
        ] {
            if let Some(value) = self
                .get_property_string(name)
                .ok()
                .flatten()
                .filter(|value| !value.trim().is_empty() && value != "unknown" && value != "none")
            {
                *cache.borrow_mut() = Some(value);
            }
        }
    }

    pub fn duration(&self) -> f64 {
        if self.file_loaded && self.playback_state == "playing" {
            if let Ok(value) = self.get_property_double("duration") {
                if value.is_finite() && value > 0.0 {
                    self.last_duration.set(value);
                }
            }
        }
        self.last_duration.get()
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

    fn clear_fsr_shaders(&mut self) -> Result<(), String> {
        if self.initialized {
            self.command(&["change-list", "glsl-shaders", "clr", ""])
                .map_err(|_| "无法停用 FSR 着色器，已阻止不安全的片源切换。".to_string())?;
        }
        self.fsr_applied_configuration = None;
        Ok(())
    }

    fn prepare_color_for_load(&mut self) -> Result<(), String> {
        self.file_loaded = false;
        self.color_metadata_ready = false;
        self.fsr_failed_for_file = false;
        self.clear_fsr_shaders()?;
        self.fsr_status = if self.engine_settings.fsr_mode == "off" {
            "disabled"
        } else {
            "bypassed-unknown"
        }
        .to_string();
        self.fsr_reason = (self.engine_settings.fsr_mode != "off")
            .then(|| "等待确认当前片源为 SDR，暂不启用 FSR。".to_string());
        Ok(())
    }

    fn bypass_fsr(&mut self, status: &str, reason: Option<&str>) {
        if self.fsr_applied_configuration.is_some() && self.clear_fsr_shaders().is_err() {
            self.fail_fsr_detachment();
            return;
        }
        self.fsr_status = status.to_string();
        self.fsr_reason = reason.map(str::to_string);
    }

    fn fail_fsr_detachment(&mut self) {
        // If detaching an active shader failed, stop rather than process HDR with an SDR shader.
        self.fsr_status = "error".to_string();
        self.fsr_reason = Some("FSR 着色器无法安全停用，已停止播放。".to_string());
        self.playback_state = "error".to_string();
        self.last_playback_event = "fsr-detach-error".to_string();
        self.last_playback_error = self.fsr_reason.clone();
        self.end_reason = Some(MpvEndReason::Error);
        self.file_loaded = false;
        self.color_metadata_ready = false;
        self.stop_requested = true;
        let _ = self.command(&["stop"]);
    }

    fn apply_fsr_runtime_safely(&mut self) {
        #[cfg(not(target_os = "windows"))]
        {
            self.bypass_fsr(
                "unsupported-platform",
                Some("FSR 首发仅支持 Windows 与 Android。"),
            );
        }

        #[cfg(target_os = "windows")]
        {
            if self.engine_settings.fsr_mode == "off" {
                self.bypass_fsr("disabled", None);
                return;
            }

            let source = self.source_color_metadata();
            if source.dolby_vision_profile.is_some()
                || transfer_mode(source.source_gamma.as_deref()) == MpvColorOutputMode::Hdr
            {
                self.bypass_fsr(
                    "bypassed-hdr",
                    Some("HDR／杜比视界片源使用原生缩放，保留已保存的 FSR 设置。"),
                );
                return;
            }
            if transfer_mode(source.source_gamma.as_deref()) != MpvColorOutputMode::Sdr {
                self.bypass_fsr(
                    "bypassed-unknown",
                    Some("尚未确认当前片源为 SDR，暂不启用 FSR。"),
                );
                return;
            }
            if !self.fsr_load_hook_available {
                self.bypass_fsr(
                    "unavailable",
                    Some("无法建立片源切换安全屏障，暂不启用 FSR。"),
                );
                return;
            }
            if self.fsr_failed_for_file {
                return;
            }

            if self.render_surface.is_none() && self.engine_settings.fsr_mode != "force" {
                self.bypass_fsr("unavailable", Some("当前没有可用的 Windows GPU 视频表面。"));
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
            let configuration = (shader_path.clone(), options.clone());
            if self.fsr_applied_configuration.as_ref() == Some(&configuration) {
                return;
            }
            let same_shader = self
                .fsr_applied_configuration
                .as_ref()
                .is_some_and(|(path, _)| *path == shader_path);
            let result = (|| -> Result<(), String> {
                if !same_shader {
                    self.clear_fsr_shaders()?;
                }
                self.set_property("glsl-shader-opts", &options)?;
                if !same_shader {
                    self.command(&["change-list", "glsl-shaders", "append", &shader_path])?;
                }
                Ok(())
            })();

            if result.is_err() {
                self.record_fsr_fallback("FSR Shader 加载失败，已恢复普通缩放。")
            } else {
                self.fsr_applied_configuration = Some(configuration);
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
        if self.initialized {
            self.apply_fsr_runtime_safely();
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
        self.fsr_failed_for_file = true;
        if self.clear_fsr_shaders().is_err() {
            self.fail_fsr_detachment();
            return;
        }
        self.fsr_status = "fallback".to_string();
        self.fsr_reason = Some(reason.to_string());
    }

    /// Finalize `mpv_initialize`. Only call once per handle lifetime.
    fn finish_initialize(&mut self) -> Result<(), String> {
        if self.initialized {
            return Ok(());
        }
        // Request before initialization so its first failure is not lost. Keep warn level:
        // debug/trace would compete with playback events in the bounded event pump.
        if let Ok(level) = CString::new("warn") {
            let _ = unsafe { mpv_request_log_messages(self.ctx, level.as_ptr()) };
        }
        self.check_error(unsafe { mpv_initialize(self.ctx) })?;
        self.initialized = true;
        // A synchronous hook also covers libmpv-internal playlist and redirect loads. The
        // existing bounded event pump must continue it after removing the previous shader.
        if let Ok(name) = CString::new("on_load") {
            self.fsr_load_hook_available =
                unsafe { mpv_hook_add(self.ctx, FSR_LOAD_HOOK_ID, name.as_ptr(), 0) } >= 0;
        }
        if !self.fsr_load_hook_available {
            log::warn!("mpv load safety hook unavailable; FSR remains disabled");
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

    fn load_command(&self, args: &[&str]) -> Result<i64, String> {
        let c_args = args
            .iter()
            .map(|arg| CString::new(*arg).map_err(|err| err.to_string()))
            .collect::<Result<Vec<_>, _>>()?;
        let mut raw_args = c_args
            .iter()
            .map(|arg| arg.as_ptr())
            .chain(std::iter::once(ptr::null()))
            .collect::<Vec<*const c_char>>();
        let mut result = unsafe { std::mem::zeroed::<mpv_node>() };
        let status = unsafe { mpv_command_ret(self.ctx, raw_args.as_mut_ptr(), &mut result) };
        // The pinned runtime returns this real ID for loadfile; playlist position is not ID.
        let entry_id = unsafe { map_i64(&result, "playlist_entry_id") };
        unsafe { mpv_free_node_contents(&mut result) };
        self.check_error(status)?;
        entry_id.ok_or_else(|| "播放器未返回本次媒体的原生条目标识。".to_string())
    }

    fn current_native_entry(&self) -> bool {
        self.expected_entry_id.is_some() && self.expected_entry_id == self.event_entry_id
    }

    fn native_log_snapshot(&self) -> Vec<String> {
        let mut logs = self
            .initialization_logs
            .snapshot()
            .into_iter()
            .take(12)
            .map(|line| format!("initialization: {line}"))
            .collect::<Vec<_>>();
        logs.extend(self.http_read_profile_diagnostic.iter().cloned());
        logs.extend(self.playback_logs.snapshot());
        logs
    }

    pub fn drain_events(&mut self, max_events: usize) -> MpvEventBatch {
        let mut batch = MpvEventBatch::default();
        if !self.initialized {
            return batch;
        }
        for index in 0..max_events {
            let event = unsafe { mpv_wait_event(self.ctx, 0.0) };
            if event.is_null() || unsafe { (*event).event_id } == mpv_event_id_MPV_EVENT_NONE {
                break;
            }
            let event_id = unsafe { (*event).event_id };
            if event_id == mpv_event_id_MPV_EVENT_HOOK {
                let hook = unsafe { (*event).data.cast::<mpv_event_hook>().as_ref() };
                if let Some(hook) = hook {
                    // Copy the id before commands; libmpv owns the event data. Every hook
                    // must be continued exactly once, including a failed safety cleanup.
                    let hook_id = hook.id;
                    if unsafe { (*event).reply_userdata } == FSR_LOAD_HOOK_ID
                        && self.prepare_color_for_load().is_err()
                    {
                        self.fail_fsr_detachment();
                    }
                    if self
                        .check_error(unsafe { mpv_hook_continue(self.ctx, hook_id) })
                        .is_err()
                    {
                        self.playback_state = "error".to_string();
                        self.last_playback_event = "load-safety-hook-error".to_string();
                        self.last_playback_error =
                            Some("播放器未能继续安全的片源加载。".to_string());
                        self.end_reason = Some(MpvEndReason::Error);
                        self.color_metadata_ready = false;
                        self.file_loaded = false;
                        let _ = self.command(&["stop"]);
                    }
                }
            } else if event_id == mpv_event_id_MPV_EVENT_START_FILE {
                self.event_entry_id =
                    unsafe { (*event).data.cast::<mpv_event_start_file>().as_ref() }
                        .map(|value| value.playlist_entry_id);
                if !self.current_native_entry() {
                    continue;
                }
                if self.playback_state == "error" || self.stop_requested {
                    continue;
                }
                self.logs_frozen = false;
                self.end_reason = None;
                self.playback_state = "loading".to_string();
                self.last_playback_event = "start-file".to_string();
                self.last_playback_error = None;
                self.file_loaded = false;
                self.stop_requested = false;
                // The load hook remains the before-render barrier. Clearing here also
                // prevents old color facts from leaking while the hook is still queued.
                self.color_metadata_ready = false;
            } else if event_id == mpv_event_id_MPV_EVENT_FILE_LOADED {
                if !self.current_native_entry()
                    || self.playback_state == "error"
                    || self.stop_requested
                {
                    continue;
                }
                self.playback_logs.finish_startup();
                self.playback_state = "playing".to_string();
                self.last_playback_event = "file-loaded".to_string();
                self.last_playback_error = None;
                self.file_loaded = true;
                self.color_metadata_ready = !self.stop_requested;
                self.source_color_snapshot = self.source_color_metadata();
                self.capture_playback_facts();
                self.apply_fsr_runtime_safely();
            } else if event_id == mpv_event_id_MPV_EVENT_VIDEO_RECONFIG {
                if !self.current_native_entry()
                    || self.playback_state == "error"
                    || self.stop_requested
                {
                    continue;
                }
                self.last_playback_event = "video-reconfig".to_string();
                self.color_metadata_ready = self.file_loaded && !self.stop_requested;
                if self.color_metadata_ready {
                    self.source_color_snapshot = self.source_color_metadata();
                }
                self.capture_playback_facts();
                self.apply_fsr_runtime_safely();
            } else if event_id == mpv_event_id_MPV_EVENT_PLAYBACK_RESTART {
                if !self.current_native_entry()
                    || self.playback_state == "error"
                    || self.stop_requested
                {
                    continue;
                }
                self.color_metadata_ready = self.file_loaded && !self.stop_requested;
                self.apply_fsr_runtime_safely();
            } else if event_id == mpv_event_id_MPV_EVENT_END_FILE {
                let end_file = unsafe { (*event).data.cast::<mpv_event_end_file>().as_ref() };
                if end_file.map(|value| value.playlist_entry_id) != self.expected_entry_id
                    || !self.current_native_entry()
                {
                    // A replacement load may be submitted before the previous END_FILE is
                    // drained. That terminal event cannot fail/complete the new media.
                    continue;
                }
                let reason = end_file.map(|value| value.reason).unwrap_or(-1);
                let error_code = end_file.map(|value| value.error).unwrap_or(0);
                let already_failed = self.playback_state == "error";
                let disposition = if already_failed {
                    EndFileDisposition::Failed
                } else {
                    end_file_disposition(reason, error_code, self.file_loaded, self.stop_requested)
                };
                self.end_reason = Some(match disposition {
                    EndFileDisposition::Stopped
                        if reason == mpv_end_file_reason_MPV_END_FILE_REASON_QUIT as i32 =>
                    {
                        MpvEndReason::Quit
                    }
                    EndFileDisposition::Stopped => MpvEndReason::Stop,
                    EndFileDisposition::Redirecting => MpvEndReason::Redirect,
                    EndFileDisposition::Ended => MpvEndReason::Eof,
                    EndFileDisposition::Failed => MpvEndReason::Error,
                });
                match disposition {
                    EndFileDisposition::Stopped => {
                        self.playback_state = "idle".to_string();
                        self.last_playback_event = "stopped".to_string();
                        self.last_playback_error = None;
                    }
                    EndFileDisposition::Redirecting => {
                        // mpv will load the playlist target without another Player command.
                        self.playback_state = "loading".to_string();
                        self.last_playback_event = "playlist-redirect".to_string();
                        self.last_playback_error = None;
                        self.expected_entry_id = end_file
                            .filter(|value| value.playlist_insert_num_entries > 0)
                            .map(|value| value.playlist_insert_id);
                    }
                    EndFileDisposition::Ended => {
                        self.playback_state = "ended".to_string();
                        self.last_playback_event = "end-file".to_string();
                        self.last_playback_error = None;
                    }
                    EndFileDisposition::Failed => {
                        self.end_reason = Some(MpvEndReason::Error);
                        self.playback_state = "error".to_string();
                        self.last_playback_event = "end-file-error".to_string();
                        if !already_failed {
                            self.last_playback_error = Some(
                                if reason == mpv_end_file_reason_MPV_END_FILE_REASON_ERROR as i32
                                    && error_code < 0
                                {
                                    let detail =
                                        unsafe { CStr::from_ptr(mpv_error_string(error_code)) }
                                            .to_string_lossy();
                                    format!("媒体加载失败（mpv {error_code}: {detail}）。")
                                } else {
                                    format!("媒体文件未能完成加载（mpv 结束原因 {reason}）。")
                                },
                            );
                        }
                    }
                }
                self.stop_requested = false;
                self.logs_frozen = true;
                self.event_entry_id = None;
                self.file_loaded = false;
                self.color_metadata_ready = false;
                if self.fsr_applied_configuration.is_some() {
                    self.bypass_fsr("bypassed-unknown", None);
                }
            }
            if event_id == mpv_event_id_MPV_EVENT_LOG_MESSAGE {
                let data = unsafe { (*event).data };
                if !data.is_null() {
                    let message = unsafe { &*(data.cast::<mpv_event_log_message>()) };
                    let prefix = unsafe { optional_c_string(message.prefix) };
                    let text = unsafe { optional_c_string(message.text) };
                    if self.playback_epoch.is_none() {
                        self.initialization_logs
                            .record(&prefix, &text, message.log_level <= 20);
                    } else if !self.logs_frozen && self.current_native_entry() {
                        self.playback_logs
                            .record(&prefix, &text, message.log_level <= 20);
                    }
                    if self.current_native_entry()
                        && !self.logs_frozen
                        && self.handle_fsr_log_message(&prefix, &text)
                    {
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

#[cfg(test)]
mod end_file_tests {
    use super::*;

    #[test]
    fn playlist_redirect_and_explicit_stop_are_not_load_failures() {
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_REDIRECT as i32,
                0,
                false,
                false
            ),
            EndFileDisposition::Redirecting,
        );
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_STOP as i32,
                0,
                false,
                false
            ),
            EndFileDisposition::Stopped,
        );
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_ERROR as i32,
                -1,
                false,
                true
            ),
            EndFileDisposition::Stopped,
        );
    }

    #[test]
    fn real_error_and_unloaded_eof_are_failures() {
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_ERROR as i32,
                -1,
                true,
                false
            ),
            EndFileDisposition::Failed,
        );
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_EOF as i32,
                0,
                false,
                false
            ),
            EndFileDisposition::Failed,
        );
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_EOF as i32,
                0,
                true,
                false
            ),
            EndFileDisposition::Ended,
        );
        assert_eq!(
            end_file_disposition(
                mpv_end_file_reason_MPV_END_FILE_REASON_EOF as i32,
                -1,
                true,
                false
            ),
            EndFileDisposition::Failed,
        );
    }
}

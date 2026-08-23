use std::{fs, path::PathBuf};
use tauri::{AppHandle, State};

use super::player_shared::{
    prepare_external_subtitle, sanitize_http_headers, MpvDisplayBrightnessState, MpvEngineSettings,
    MpvHttpHeader, MpvOrientationState,
};
use crate::mpv::{
    mobile_proxy::AndroidStreamProxyState,
    player::{MpvState, MpvTrackState},
    render::MpvRenderState,
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};
use crate::storage;
const FSR_SHADER_BYTES: &[u8] = include_bytes!("../../resources/shaders/ohmycine-fsr-v1.glsl");

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DesktopPlaybackDiagnostics {
    state: &'static str,
    last_event: &'static str,
    last_error: Option<String>,
    file_loaded: bool,
    video_format: Option<String>,
    audio_codec: Option<String>,
    vo_configured: bool,
    hardware_decoder: Option<String>,
    video_output: &'static str,
    video_output_fallback_used: bool,
    playback_transport: &'static str,
    fsr_status: String,
    fsr_reason: Option<String>,
    logs: Vec<String>,
}

#[tauri::command]
pub fn mpv_playback_diagnostics(state: State<'_, MpvState>) -> DesktopPlaybackDiagnostics {
    let (fsr_status, fsr_reason) = state
        .lock()
        .map(|player| player.fsr_diagnostics())
        .unwrap_or_else(|_| {
            (
                "unavailable".to_string(),
                Some("播放器状态暂不可用。".to_string()),
            )
        });
    DesktopPlaybackDiagnostics {
        state: "desktop",
        last_event: "desktop-backend",
        last_error: None,
        file_loaded: false,
        video_format: None,
        audio_codec: None,
        vo_configured: false,
        hardware_decoder: None,
        video_output: "desktop",
        video_output_fallback_used: false,
        playback_transport: "native",
        fsr_status,
        fsr_reason,
        logs: Vec::new(),
    }
}

#[tauri::command]
pub async fn mpv_load(
    path: String,
    headers: Option<Vec<MpvHttpHeader>>,
    audio_path: Option<String>,
    audio_headers: Option<Vec<MpvHttpHeader>>,
    title: Option<String>,
    state: State<'_, MpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<(), String> {
    let _ = title;
    let headers = sanitize_http_headers(headers.unwrap_or_default())?;
    let audio_headers = sanitize_http_headers(audio_headers.unwrap_or_default())?;
    let (path, headers) = if is_remote_http_stream(&path) && !headers.is_empty() {
        (stream_proxy.prepare(path, headers).await?, Vec::new())
    } else {
        (path, headers)
    };
    let headers = headers
        .into_iter()
        .map(|header| (header.name, header.value))
        .collect::<Vec<_>>();
    let audio_path = match audio_path {
        Some(value) if !value.trim().is_empty() => {
            let value = value.trim().to_string();
            if is_remote_http_stream(&value) && !audio_headers.is_empty() {
                Some(stream_proxy.prepare(value, audio_headers).await?)
            } else {
                Some(value)
            }
        }
        _ => None,
    };
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.load_file_with_headers(&path, &headers, audio_path.as_deref())
}

fn is_remote_http_stream(path: &str) -> bool {
    reqwest::Url::parse(path.trim())
        .map(|url| matches!(url.scheme(), "http" | "https"))
        .unwrap_or(false)
}

#[tauri::command]
pub async fn mpv_apply_engine_settings(
    app: AppHandle,
    settings: MpvEngineSettings,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let shader_path = if settings.fsr_mode == "off" {
        None
    } else {
        materialize_fsr_shader(&app).ok()
    };
    let mut player = state
        .lock()
        .map_err(|_| "播放器设置暂不可用，请稍后重试".to_string())?;
    player.apply_engine_settings(settings, shader_path)
}

fn materialize_fsr_shader(app: &AppHandle) -> Result<PathBuf, String> {
    let layout = storage::initialize(app)?;
    let shader_dir = layout.cache_dir.join("mpv").join("shaders");
    fs::create_dir_all(&shader_dir).map_err(|_| "无法创建播放器 Shader 缓存。".to_string())?;
    let target = shader_dir.join("ohmycine-fsr-v1.glsl");
    if fs::read(&target).ok().as_deref() != Some(FSR_SHADER_BYTES) {
        fs::write(&target, FSR_SHADER_BYTES)
            .map_err(|_| "无法安装播放器内置 FSR Shader。".to_string())?;
    }
    Ok(target)
}

#[tauri::command]
pub async fn mpv_add_subtitle(
    app: AppHandle,
    url: String,
    title: Option<String>,
    language: Option<String>,
    headers: Option<Vec<MpvHttpHeader>>,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let prepared_path =
        prepare_external_subtitle(&app, &url, title.as_deref(), headers.unwrap_or_default())
            .await?;
    let mut player = state
        .lock()
        .map_err(|_| "播放器控制暂不可用，请稍后重试".to_string())?;
    player.add_subtitle(&prepared_path, title.as_deref(), language.as_deref())
}

#[tauri::command]
pub async fn mpv_pause(state: State<'_, MpvState>) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.pause()
}

#[tauri::command]
pub async fn mpv_resume(state: State<'_, MpvState>) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.resume()
}

#[tauri::command]
pub async fn mpv_stop(
    state: State<'_, MpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<(), String> {
    let result = {
        let mut player = state.lock().map_err(|err| err.to_string())?;
        player.stop()
    };
    stream_proxy.clear().await;
    result
}

#[tauri::command]
pub async fn mpv_seek(position: f64, state: State<'_, MpvState>) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.seek(position)
}

#[tauri::command]
pub async fn mpv_get_property(prop: String, state: State<'_, MpvState>) -> Result<String, String> {
    let player = state.lock().map_err(|err| err.to_string())?;
    player.get_property(&prop)
}

#[tauri::command]
pub async fn mpv_set_property(
    prop: String,
    value: String,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let player = state
        .lock()
        .map_err(|_| "播放器控制暂不可用，请稍后重试".to_string())?;
    player.set_property(&prop, &value)
}

#[tauri::command]
pub async fn mpv_track_state(state: State<'_, MpvState>) -> Result<MpvTrackState, String> {
    let mut player = state
        .lock()
        .map_err(|_| "播放器轨道信息暂不可用，请稍后重试".to_string())?;
    player.track_state()
}

#[tauri::command]
pub async fn mpv_orientation_state() -> Result<MpvOrientationState, String> {
    Ok(MpvOrientationState {
        supported: false,
        mode: "auto".to_string(),
    })
}

#[tauri::command]
pub async fn mpv_set_orientation(mode: String) -> Result<MpvOrientationState, String> {
    let _ = mode;
    mpv_orientation_state().await
}

#[tauri::command]
pub async fn mpv_display_brightness_state(
    window: tauri::Window,
) -> Result<MpvDisplayBrightnessState, String> {
    super::display_brightness::state(window).await
}

#[tauri::command]
pub async fn mpv_set_display_brightness(
    window: tauri::Window,
    level: f64,
) -> Result<MpvDisplayBrightnessState, String> {
    super::display_brightness::set(window, level).await
}

#[tauri::command]
pub async fn mpv_init_render_surface(
    window: tauri::Window,
    state: State<'_, MpvState>,
) -> Result<MpvRenderState, String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    Ok(player.init_render_surface(&window))
}

#[tauri::command]
pub async fn mpv_update_render_surface_bounds(
    bounds: RenderSurfaceBounds,
    state: State<'_, MpvState>,
) -> Result<MpvRenderState, String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    Ok(player.update_render_surface_bounds(bounds))
}

#[tauri::command]
pub async fn mpv_render_status(state: State<'_, MpvState>) -> Result<MpvRenderState, String> {
    let player = state.lock().map_err(|err| err.to_string())?;
    Ok(player.render_state())
}

#[tauri::command]
pub async fn mpv_set_render_strategy(
    strategy: ZOrderStrategy,
    state: State<'_, MpvState>,
) -> Result<MpvRenderState, String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    Ok(player.set_render_strategy(strategy))
}

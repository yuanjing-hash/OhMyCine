use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager, State};

use super::player_shared::{
    prepare_external_subtitle, sanitize_http_headers, MpvBufferState, MpvDisplayBrightnessState,
    MpvEngineSettings, MpvHttpHeader, MpvHttpReadProfile, MpvOrientationState, MpvSeekPrecision,
    MpvSeekReceipt,
};
use crate::mpv::{
    mobile::{AndroidMpvSnapshot, AndroidMpvState, AndroidPlaybackDiagnostics, AndroidSurfaceStatus},
    mobile_proxy::AndroidStreamProxyState,
    render::{MpvRenderState, RenderBackendKind, RenderStatus},
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct LoadPayload {
    path: String,
    headers: Vec<MpvHttpHeader>,
    audio_path: Option<String>,
    title: Option<String>,
    http_read_profile: Option<MpvHttpReadProfile>,
    file_options: BTreeMap<String, String>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct SubtitlePayload {
    url: String,
    title: Option<String>,
    language: Option<String>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct SeekPayload {
    position: f64,
    precision: MpvSeekPrecision,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct PropertyPayload {
    prop: String,
    value: Option<String>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct OrientationPayload {
    mode: String,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct BrightnessPayload {
    level: f64,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct ScreenshotPayload {
    title: String,
    season_number: Option<i32>,
    episode_number: Option<i32>,
    directory_uri: Option<String>,
    format: String,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotResult {
    name: String,
    path: String,
}

#[tauri::command]
pub async fn player_screenshot_directory_label(
    app: AppHandle,
    directory: String,
) -> Result<String, String> {
    if directory.trim().is_empty() {
        return Ok("Pictures/OhMyCine".to_string());
    }
    crate::commands::download_android::validate_directory(&app, &directory).await
}

#[tauri::command]
pub async fn mpv_capture_screenshot(
    title: String,
    season_number: Option<i32>,
    episode_number: Option<i32>,
    directory: Option<String>,
    format: String,
    state: State<'_, AndroidMpvState>,
) -> Result<ScreenshotResult, String> {
    if format != "png" && format != "jpg" {
        return Err("不支持的截图格式。".to_string());
    }
    state
        .run(
            "captureScreenshot",
            ScreenshotPayload {
                title: title.chars().take(160).collect(),
                season_number,
                episode_number,
                directory_uri: directory.filter(|value| !value.trim().is_empty()),
                format,
            },
        )
        .await
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvTrack {
    id: i64,
    kind: String,
    language: Option<String>,
    title: Option<String>,
    codec: Option<String>,
    channels: Option<i64>,
    is_default: bool,
    selected: bool,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvTrackState {
    tracks: Vec<MpvTrack>,
    current_subtitle: Option<i64>,
    current_audio: Option<i64>,
}

#[tauri::command]
pub async fn mpv_load(
    path: String,
    headers: Option<Vec<MpvHttpHeader>>,
    audio_path: Option<String>,
    audio_headers: Option<Vec<MpvHttpHeader>>,
    title: Option<String>,
    http_read_profile: Option<MpvHttpReadProfile>,
    app: AppHandle,
) -> Result<(), String> {
    let state = app.state::<AndroidMpvState>();
    let stream_proxy = app.state::<AndroidStreamProxyState>();
    if path.trim().is_empty() {
        return Err("播放地址为空。".to_string());
    }
    let epoch = stream_proxy.begin_load().await;
    let file_options = if is_remote_http_stream(&path) {
        BTreeMap::from([
            ("cache-pause".to_string(), "yes".to_string()),
            ("cache-pause-initial".to_string(), "yes".to_string()),
            ("cache-pause-wait".to_string(), "1".to_string()),
        ])
    } else {
        BTreeMap::new()
    };
    let headers = sanitize_http_headers(headers.unwrap_or_default())?;
    let audio_headers = sanitize_http_headers(audio_headers.unwrap_or_default())?;
    let (path, headers) = if is_remote_http_stream(&path) {
        (
            stream_proxy.prepare_for_epoch(path, headers, epoch).await?,
            Vec::new(),
        )
    } else {
        (path, headers)
    };
    let audio_path = match audio_path {
        Some(value) if !value.trim().is_empty() => {
            let value = value.trim().to_string();
            if is_remote_http_stream(&value) {
                match stream_proxy
                    .prepare_for_epoch(value, audio_headers, epoch)
                    .await
                {
                    Ok(url) => Some(url),
                    Err(error) => {
                        stream_proxy.cancel_preserving_diagnostics(epoch).await;
                        return Err(error);
                    }
                }
            } else {
                Some(value)
            }
        }
        _ => None,
    };
    let payload = LoadPayload {
        path,
        headers,
        audio_path,
        http_read_profile,
        file_options,
        title: title.and_then(|value| {
            let trimmed = value.trim();
            (!trimmed.is_empty()).then(|| trimmed.chars().take(160).collect())
        }),
    };
    let result = async {
        wait_for_android_surface(state.inner()).await?;
        let _submission = stream_proxy.current_load_guard(epoch).await?;
        state.run("load", payload).await
    }
    .await;
    if result.is_err() {
        stream_proxy.cancel_preserving_diagnostics(epoch).await;
    }
    result
}

fn is_remote_http_stream(path: &str) -> bool {
    reqwest::Url::parse(path.trim())
        .map(|url| matches!(url.scheme(), "http" | "https"))
        .unwrap_or(false)
}

#[tauri::command]
pub async fn mpv_apply_engine_settings(
    settings: MpvEngineSettings,
    state: State<'_, AndroidMpvState>,
) -> Result<(), String> {
    let settings = settings.validated()?;
    state.run("applyEngineSettings", settings).await
}

#[tauri::command]
pub async fn mpv_add_subtitle(
    app: AppHandle,
    url: String,
    title: Option<String>,
    language: Option<String>,
    headers: Option<Vec<MpvHttpHeader>>,
    state: State<'_, AndroidMpvState>,
) -> Result<(), String> {
    if url.trim().is_empty() {
        return Err("字幕地址为空。".to_string());
    }
    let prepared_path =
        prepare_external_subtitle(&app, &url, title.as_deref(), headers.unwrap_or_default())
            .await?;
    state
        .run(
            "addSubtitle",
            SubtitlePayload {
                url: prepared_path,
                title,
                language,
            },
        )
        .await
}

#[tauri::command]
pub async fn mpv_pause(state: State<'_, AndroidMpvState>) -> Result<(), String> {
    state.run("pause", ()).await
}

#[tauri::command]
pub async fn mpv_resume(state: State<'_, AndroidMpvState>) -> Result<(), String> {
    state.run("resume", ()).await
}

#[tauri::command]
pub async fn mpv_stop(
    state: State<'_, AndroidMpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<(), String> {
    let epoch = stream_proxy.diagnostic_epoch();
    let Ok(submission) = stream_proxy.current_load_guard(epoch).await else {
        // This stop belonged to a playback already replaced by another load.
        return Ok(());
    };
    let result = state.run("stop", ()).await;
    submission.clear_after_stop().await;
    result
}

#[tauri::command]
pub async fn mpv_seek(
    position: f64,
    precision: MpvSeekPrecision,
    state: State<'_, AndroidMpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<MpvSeekReceipt, String> {
    if !position.is_finite() || position < 0.0 {
        return Err("播放位置无效。".to_string());
    }
    let epoch = stream_proxy.diagnostic_epoch();
    let _submission = stream_proxy.current_load_guard(epoch).await?;
    state.run("seek", SeekPayload { position, precision }).await
}

#[tauri::command]
pub async fn mpv_buffer_state(
    state: State<'_, AndroidMpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<MpvBufferState, String> {
    let epoch = stream_proxy.diagnostic_epoch();
    let _submission = stream_proxy.current_load_guard(epoch).await?;
    let snapshot: AndroidMpvSnapshot = state.run("snapshot", ()).await?;
    Ok(MpvBufferState {
        position: snapshot.time,
        seeking: snapshot.seeking,
        paused_for_cache: snapshot.paused_for_cache,
        cache_buffering_state: snapshot.cache_buffering_state,
        cache_speed: snapshot.cache_speed,
        vo_configured: snapshot.vo_configured,
        file_loaded: snapshot.file_loaded,
        seek_serial: snapshot.seek_serial,
        restarted_seek_serial: snapshot.restarted_seek_serial,
        seek_target: snapshot.seek_target,
    })
}

#[tauri::command]
pub async fn mpv_get_property(
    prop: String,
    state: State<'_, AndroidMpvState>,
) -> Result<String, String> {
    state
        .run("getProperty", PropertyPayload { prop, value: None })
        .await
}

#[tauri::command]
pub async fn mpv_set_property(
    prop: String,
    value: String,
    state: State<'_, AndroidMpvState>,
) -> Result<(), String> {
    state
        .run(
            "setProperty",
            PropertyPayload {
                prop,
                value: Some(value),
            },
        )
        .await
}

#[tauri::command]
pub async fn mpv_track_state(state: State<'_, AndroidMpvState>) -> Result<MpvTrackState, String> {
    state.run("trackState", ()).await
}

#[tauri::command]
pub async fn mpv_playback_diagnostics(
    state: State<'_, AndroidMpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<AndroidPlaybackDiagnostics, String> {
    let epoch = stream_proxy.diagnostic_epoch();
    let mut diagnostics: AndroidPlaybackDiagnostics = state.run("playbackDiagnostics", ()).await?;
    diagnostics
        .logs
        .extend(stream_proxy.diagnostic_logs(epoch).await);
    Ok(diagnostics)
}

#[tauri::command]
pub async fn mpv_orientation_state(
    state: State<'_, AndroidMpvState>,
) -> Result<MpvOrientationState, String> {
    state.run("orientationState", ()).await
}

#[tauri::command]
pub async fn mpv_set_orientation(
    mode: String,
    state: State<'_, AndroidMpvState>,
) -> Result<MpvOrientationState, String> {
    if !matches!(mode.as_str(), "auto" | "landscape" | "portrait") {
        return Err("不支持的屏幕方向模式。".to_string());
    }
    state
        .run("setOrientation", OrientationPayload { mode })
        .await
}

#[tauri::command]
pub async fn mpv_display_brightness_state(
    state: State<'_, AndroidMpvState>,
) -> Result<MpvDisplayBrightnessState, String> {
    state.run("displayBrightnessState", ()).await
}

#[tauri::command]
pub async fn mpv_set_display_brightness(
    level: f64,
    state: State<'_, AndroidMpvState>,
) -> Result<MpvDisplayBrightnessState, String> {
    if !level.is_finite() || !(0.0..=100.0).contains(&level) {
        return Err("屏幕亮度无效。".to_string());
    }
    state
        .run("setDisplayBrightness", BrightnessPayload { level })
        .await
}

#[tauri::command]
pub async fn mpv_init_render_surface(
    state: State<'_, AndroidMpvState>,
) -> Result<MpvRenderState, String> {
    for _ in 0..20 {
        let status = state
            .run::<AndroidSurfaceStatus>("surfaceStatus", ())
            .await?;
        if let Some(error) = status.error {
            return Ok(android_render_error(error));
        }
        if status.ready {
            return Ok(android_render_state(true));
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    Ok(android_render_state(false))
}

#[tauri::command]
pub async fn mpv_update_render_surface_bounds(
    bounds: RenderSurfaceBounds,
    state: State<'_, AndroidMpvState>,
) -> Result<MpvRenderState, String> {
    let _ = bounds.sanitized();
    mpv_render_status(state).await
}

#[tauri::command]
pub async fn mpv_render_status(
    state: State<'_, AndroidMpvState>,
) -> Result<MpvRenderState, String> {
    let status = state
        .run::<AndroidSurfaceStatus>("surfaceStatus", ())
        .await?;
    if let Some(error) = status.error {
        return Ok(android_render_error(error));
    }
    Ok(android_render_state(status.ready))
}

#[tauri::command]
pub async fn mpv_set_render_strategy(
    strategy: ZOrderStrategy,
    state: State<'_, AndroidMpvState>,
) -> Result<MpvRenderState, String> {
    let _ = strategy;
    mpv_render_status(state).await
}

fn android_render_state(ready: bool) -> MpvRenderState {
    MpvRenderState {
        status: if ready {
            RenderStatus::Ready
        } else {
            RenderStatus::Initializing
        },
        backend: RenderBackendKind::AndroidSurface,
        message: Some(if ready {
            "Android SurfaceView 已连接到 libmpv。".to_string()
        } else {
            "Android 播放表面仍在初始化。".to_string()
        }),
        diagnostics: None,
    }
}

fn android_render_error(message: String) -> MpvRenderState {
    MpvRenderState {
        status: RenderStatus::Error,
        backend: RenderBackendKind::AndroidSurface,
        message: Some(message),
        diagnostics: None,
    }
}

async fn wait_for_android_surface(state: &AndroidMpvState) -> Result<(), String> {
    for _ in 0..100 {
        let status = state
            .run::<AndroidSurfaceStatus>("surfaceStatus", ())
            .await?;
        if let Some(error) = status.error {
            return Err(error);
        }
        if status.ready {
            return Ok(());
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    Err("Android 播放器表面准备超时，请重新进入播放页。".to_string())
}

#[cfg(test)]
mod tests {
    use super::is_remote_http_stream;

    #[test]
    fn proxies_http_and_https_streams_before_android_mpv_load() {
        assert!(is_remote_http_stream("http://emby.local/Videos/1/stream"));
        assert!(is_remote_http_stream(" https://cdn.example/media.mkv "));
        assert!(!is_remote_http_stream("/storage/emulated/0/movie.mkv"));
        assert!(!is_remote_http_stream(
            "file:///storage/emulated/0/movie.mkv"
        ));
    }
}

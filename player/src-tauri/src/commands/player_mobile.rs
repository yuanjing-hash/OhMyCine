use serde::{Deserialize, Serialize};
use tauri::State;

use super::player_shared::{
    sanitize_http_headers, MpvDisplayBrightnessState, MpvEngineSettings, MpvHttpHeader,
    MpvOrientationState,
};
use crate::mpv::{
    mobile::{AndroidMpvState, AndroidPlaybackDiagnostics, AndroidSurfaceStatus},
    mobile_proxy::AndroidStreamProxyState,
    render::{MpvRenderState, RenderBackendKind, RenderStatus},
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct LoadPayload {
    path: String,
    headers: Vec<MpvHttpHeader>,
    title: Option<String>,
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
    title: Option<String>,
    state: State<'_, AndroidMpvState>,
    stream_proxy: State<'_, AndroidStreamProxyState>,
) -> Result<(), String> {
    if path.trim().is_empty() {
        return Err("播放地址为空。".to_string());
    }
    let headers = sanitize_http_headers(headers.unwrap_or_default())?;
    let (path, headers) = if is_remote_http_stream(&path) {
        (stream_proxy.prepare(path, headers).await?, Vec::new())
    } else {
        (path, headers)
    };
    let payload = LoadPayload {
        path,
        headers,
        title: title.and_then(|value| {
            let trimmed = value.trim();
            (!trimmed.is_empty()).then(|| trimmed.chars().take(160).collect())
        }),
    };
    wait_for_android_surface(state.inner()).await?;
    state.run("load", payload).await
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
    url: String,
    title: Option<String>,
    language: Option<String>,
    state: State<'_, AndroidMpvState>,
) -> Result<(), String> {
    if url.trim().is_empty() {
        return Err("字幕地址为空。".to_string());
    }
    state
        .run(
            "addSubtitle",
            SubtitlePayload {
                url,
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
    let result = state.run("stop", ()).await;
    stream_proxy.clear().await;
    result
}

#[tauri::command]
pub async fn mpv_seek(position: f64, state: State<'_, AndroidMpvState>) -> Result<(), String> {
    if !position.is_finite() || position < 0.0 {
        return Err("播放位置无效。".to_string());
    }
    state.run("seek", SeekPayload { position }).await
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
) -> Result<AndroidPlaybackDiagnostics, String> {
    state.run("playbackDiagnostics", ()).await
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

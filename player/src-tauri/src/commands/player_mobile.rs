use serde::{Deserialize, Serialize};
use tauri::State;

use super::player_shared::{sanitize_http_headers, MpvHttpHeader};
use crate::mpv::{
    mobile::{AndroidMpvState, AndroidSurfaceStatus},
    render::{MpvRenderState, RenderBackendKind, RenderStatus},
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct LoadPayload {
    path: String,
    headers: Vec<MpvHttpHeader>,
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
    state: State<'_, AndroidMpvState>,
) -> Result<(), String> {
    if path.trim().is_empty() {
        return Err("播放地址为空。".to_string());
    }
    let payload = LoadPayload {
        path,
        headers: sanitize_http_headers(headers.unwrap_or_default())?,
    };
    state.run("load", payload).await
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
pub async fn mpv_stop(state: State<'_, AndroidMpvState>) -> Result<(), String> {
    state.run("stop", ()).await
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
pub async fn mpv_init_render_surface(
    state: State<'_, AndroidMpvState>,
) -> Result<MpvRenderState, String> {
    for _ in 0..20 {
        let status = state
            .run::<AndroidSurfaceStatus>("surfaceStatus", ())
            .await?;
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

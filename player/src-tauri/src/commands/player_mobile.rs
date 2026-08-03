use tauri::AppHandle;

use crate::mpv::{
    render::{current_render_state, MpvRenderState},
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

const MOBILE_PLAYBACK_UNAVAILABLE: &str =
    "Android 原生 libmpv 渲染尚未接入，当前预览版本暂不支持播放。";

#[derive(Clone, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvHttpHeader {
    name: String,
    value: String,
}

#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvTrackState {
    tracks: Vec<serde_json::Value>,
    current_subtitle: Option<i64>,
    current_audio: Option<i64>,
}

#[tauri::command]
pub async fn mpv_load(path: String, headers: Option<Vec<MpvHttpHeader>>) -> Result<(), String> {
    let _ = path;
    if let Some(headers) = headers {
        for header in headers {
            let _ = (header.name, header.value);
        }
    }
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_add_subtitle(
    app: AppHandle,
    url: String,
    title: Option<String>,
    language: Option<String>,
) -> Result<(), String> {
    let _ = (app, url, title, language);
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_pause() -> Result<(), String> {
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_resume() -> Result<(), String> {
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_stop() -> Result<(), String> {
    Ok(())
}

#[tauri::command]
pub async fn mpv_seek(position: f64) -> Result<(), String> {
    let _ = position;
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_get_property(prop: String) -> Result<String, String> {
    let _ = prop;
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_set_property(prop: String, value: String) -> Result<(), String> {
    let _ = (prop, value);
    Err(MOBILE_PLAYBACK_UNAVAILABLE.to_string())
}

#[tauri::command]
pub async fn mpv_track_state() -> Result<MpvTrackState, String> {
    Ok(MpvTrackState {
        tracks: Vec::new(),
        current_subtitle: None,
        current_audio: None,
    })
}

#[tauri::command]
pub async fn mpv_init_render_surface(window: tauri::Window) -> Result<MpvRenderState, String> {
    let _ = window;
    Ok(current_render_state())
}

#[tauri::command]
pub async fn mpv_update_render_surface_bounds(
    bounds: RenderSurfaceBounds,
) -> Result<MpvRenderState, String> {
    let _ = bounds.sanitized();
    Ok(current_render_state())
}

#[tauri::command]
pub async fn mpv_render_status() -> Result<MpvRenderState, String> {
    Ok(current_render_state())
}

#[tauri::command]
pub async fn mpv_set_render_strategy(strategy: ZOrderStrategy) -> Result<MpvRenderState, String> {
    let _ = strategy;
    Ok(current_render_state())
}

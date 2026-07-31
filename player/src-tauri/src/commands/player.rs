use tauri::State;

use crate::mpv::{
    player::{MpvState, MpvTrackState},
    render::MpvRenderState,
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

#[derive(Clone, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvHttpHeader {
    name: String,
    value: String,
}

#[tauri::command]
pub async fn mpv_load(
    path: String,
    headers: Option<Vec<MpvHttpHeader>>,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    let headers = sanitize_http_headers(headers.unwrap_or_default())?;
    player.load_file_with_headers(&path, &headers)
}

#[tauri::command]
pub async fn mpv_add_subtitle(
    url: String,
    title: Option<String>,
    language: Option<String>,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let mut player = state
        .lock()
        .map_err(|_| "播放器控制暂不可用，请稍后重试".to_string())?;
    player.add_subtitle(&url, title.as_deref(), language.as_deref())
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

fn sanitize_http_headers(headers: Vec<MpvHttpHeader>) -> Result<Vec<(String, String)>, String> {
    if headers.len() > 16 {
        return Err("播放请求 header 数量过多。".to_string());
    }

    headers
        .into_iter()
        .map(|header| {
            let name = header.name.trim().to_string();
            let value = header.value.trim().to_string();
            if name.is_empty() || value.is_empty() {
                return Err("播放请求 header 格式无效。".to_string());
            }
            if !is_valid_header_name(&name) || value.chars().any(|ch| ch == '\r' || ch == '\n') {
                return Err("播放请求 header 格式无效。".to_string());
            }
            Ok((name, value))
        })
        .collect()
}

fn is_valid_header_name(value: &str) -> bool {
    value.bytes().all(|byte| {
        byte.is_ascii_alphanumeric()
            || matches!(
                byte,
                b'!' | b'#'
                    | b'$'
                    | b'%'
                    | b'&'
                    | b'\''
                    | b'*'
                    | b'+'
                    | b'-'
                    | b'.'
                    | b'^'
                    | b'_'
                    | b'`'
                    | b'|'
                    | b'~'
            )
    })
}

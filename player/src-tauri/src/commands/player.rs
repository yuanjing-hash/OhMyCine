use tauri::State;

use crate::mpv::{
    player::{MpvState, MpvTrackState},
    render::MpvRenderState,
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};

#[tauri::command]
pub async fn mpv_load(path: String, state: State<'_, MpvState>) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.load_file(&path)
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

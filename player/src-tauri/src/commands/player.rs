use tauri::State;

use crate::mpv::player::MpvState;

#[tauri::command]
pub async fn mpv_load(path: String, state: State<'_, MpvState>) -> Result<(), String> {
    let player = state.lock().map_err(|err| err.to_string())?;
    player.load_file(&path)
}

#[tauri::command]
pub async fn mpv_pause(state: State<'_, MpvState>) -> Result<(), String> {
    let player = state.lock().map_err(|err| err.to_string())?;
    player.pause()
}

#[tauri::command]
pub async fn mpv_resume(state: State<'_, MpvState>) -> Result<(), String> {
    let player = state.lock().map_err(|err| err.to_string())?;
    player.resume()
}

#[tauri::command]
pub async fn mpv_seek(position: f64, state: State<'_, MpvState>) -> Result<(), String> {
    let player = state.lock().map_err(|err| err.to_string())?;
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
    let player = state.lock().map_err(|err| err.to_string())?;
    player.set_property(&prop, &value)
}

// Prevents additional console window on Windows in release
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod commands;
mod mpv;

use commands::player::{
    mpv_get_property, mpv_load, mpv_pause, mpv_resume, mpv_seek, mpv_set_property,
};

fn main() {
    env_logger::init();

    let mpv_state = mpv::player::create_state().expect("failed to initialize libmpv");

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(mpv_state)
        .invoke_handler(tauri::generate_handler![
            mpv_load,
            mpv_pause,
            mpv_resume,
            mpv_seek,
            mpv_get_property,
            mpv_set_property,
        ])
        .setup(|app| {
            mpv::events::start_event_forwarder(app.handle().clone());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

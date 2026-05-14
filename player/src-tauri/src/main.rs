// Prevents additional console window on Windows in release
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod commands;
mod mpv;

use tauri::{utils::config::Color, Manager};

use commands::credential::{credential_delete, credential_get, credential_set};
use commands::player::{
    mpv_add_subtitle, mpv_get_property, mpv_init_render_surface, mpv_load, mpv_pause,
    mpv_render_status, mpv_resume, mpv_seek, mpv_set_property, mpv_set_render_strategy,
    mpv_track_state, mpv_update_render_surface_bounds,
};
use mpv::surface::OwnerWindowEvent;

fn main() {
    env_logger::init();

    let mpv_state = mpv::player::create_state().expect("failed to initialize libmpv");

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(mpv_state)
        .invoke_handler(tauri::generate_handler![
            credential_set,
            credential_get,
            credential_delete,
            mpv_load,
            mpv_add_subtitle,
            mpv_pause,
            mpv_resume,
            mpv_seek,
            mpv_get_property,
            mpv_set_property,
            mpv_track_state,
            mpv_init_render_surface,
            mpv_update_render_surface_bounds,
            mpv_render_status,
            mpv_set_render_strategy,
        ])
        .on_window_event(|window, event| {
            let owner_event = match event {
                tauri::WindowEvent::Moved { .. } => Some(OwnerWindowEvent::Moved),
                tauri::WindowEvent::Resized { .. } => Some(OwnerWindowEvent::Resized),
                tauri::WindowEvent::ScaleFactorChanged { .. } => {
                    Some(OwnerWindowEvent::ScaleFactorChanged)
                }
                tauri::WindowEvent::Focused(focused) => {
                    Some(OwnerWindowEvent::FocusChanged(*focused))
                }
                tauri::WindowEvent::CloseRequested { .. } | tauri::WindowEvent::Destroyed => {
                    Some(OwnerWindowEvent::Destroyed)
                }
                // DragDrop, ThemeChanged, and mobile-only events are not relevant to mpv HWND
                // geometry synchronization. Tauri 2 does not emit Minimized/Restored as
                // WindowEvent variants; the Windows sync_geometry_from_owner reads IsIconic
                // directly from Win32 instead.
                _ => None,
            };

            if let Some(event) = owner_event {
                // Use `try_lock` instead of `lock` to prevent the main thread from blocking
                // on the MpvState mutex. If an async Tauri command (e.g. set_bounds) holds the
                // lock and is waiting for a `run_on_main_thread` callback, a blocking `lock()`
                // here would deadlock: the main thread can't pump messages while blocked on the
                // mutex, so the callback never executes, and the async thread never releases the
                // mutex. `try_lock` lets the main thread skip the event and continue processing
                // its message loop, breaking the cycle. Skipping a geometry event is safe because
                // `sync_geometry_from_owner` re-reads live Win32 state rather than event payloads.
                if let Ok(mut player) = window.state::<mpv::player::MpvState>().try_lock() {
                    player.on_owner_window_event(event);
                }
            }
        })
        .setup(|app| {
            let webview_transparency_applied = app
                .get_webview_window("main")
                .map(|window| window.set_background_color(Some(Color(0, 0, 0, 0))).is_ok())
                .unwrap_or(false);
            mpv::render::set_webview_background_transparency_applied(webview_transparency_applied);
            if !webview_transparency_applied {
                log::warn!("failed to explicitly set transparent native Tauri/WebView background");
            }

            mpv::events::start_event_forwarder(app.handle().clone());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

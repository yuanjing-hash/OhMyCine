#![cfg_attr(target_os = "android", allow(dead_code))]

mod commands;
mod mpv;
mod storage;

#[cfg(not(mobile))]
use tauri::Emitter;
use tauri::{utils::config::Color, Manager};

use commands::credential::{credential_delete, credential_get, credential_set};
use commands::danmaku::{danmaku_comments, danmaku_match, danmaku_search};
use commands::deep_link::{player_take_pending_deep_links, DeepLinkState};
use commands::downloads::{
    player_download_cancel, player_download_default_directory, player_download_enqueue,
    player_download_list, player_download_offline_asset, player_download_offline_detail,
    player_download_offline_list, player_download_pause, player_download_pick_directory,
    player_download_remove, player_download_resolve_local, player_download_resume,
    player_download_retry, player_download_set_default_directory, player_download_settings,
    player_download_sync_attachments, player_download_update_settings, DownloadQueueState,
};
use commands::emby::{emby_post_playback_json, emby_request_json};
use commands::history::{
    player_delete_playback_history_for_source, player_get_playback_completion_batch,
    player_get_playback_progress, player_list_continue_watching, player_list_playback_history,
    player_list_playback_history_sync, player_list_playback_history_tombstones,
    player_merge_playback_history, player_remove_continue_watching, player_set_playback_completed,
    player_upsert_playback_progress,
};
use commands::image_cache::{
    player_cache_image, player_get_cached_image, player_image_cache_stats, player_trim_image_cache,
};
use commands::local_file::{
    local_file_list, local_file_pick_directory, local_file_pick_video, local_file_pick_videos,
    local_file_stream_path,
};
use commands::media_collections::{
    player_add_media_collection_member, player_create_media_collection,
    player_delete_media_collection, player_list_media_collections,
    player_remove_media_collection_member, player_set_local_favorite,
};
use commands::player::{
    mpv_add_subtitle, mpv_apply_engine_settings, mpv_display_brightness_state, mpv_get_property,
    mpv_init_render_surface, mpv_load, mpv_orientation_state, mpv_pause, mpv_playback_diagnostics,
    mpv_render_status, mpv_resume, mpv_seek, mpv_set_display_brightness, mpv_set_orientation,
    mpv_set_property, mpv_set_render_strategy, mpv_stop, mpv_track_state,
    mpv_update_render_surface_bounds,
};
use commands::preference::{
    player_clear_media_cache, player_delete_media_playback_preferences_for_source,
    player_get_media_playback_preference, player_get_playback_speed_preference,
    player_set_playback_speed_preference, player_upsert_media_playback_preference,
};
use commands::server::{
    server_cancel_sse, server_request_blob, server_request_json, server_stream_sse,
    ServerStreamState,
};
use commands::settings::{
    player_get_storage_info, player_settings_delete, player_settings_get_all, player_settings_set,
};
use commands::subtitle::{
    subtitle_download_hash_provider, subtitle_download_opensubtitles, subtitle_import_local,
    subtitle_login_opensubtitles, subtitle_search_hash_provider, subtitle_search_opensubtitles,
    OpenSubtitlesSessionState, SubtitleDownloadState,
};
use commands::updater::{player_check_for_updates, player_install_update, PendingUpdate};
use mpv::mobile_proxy::AndroidStreamProxyState;
#[cfg(not(mobile))]
use mpv::surface::OwnerWindowEvent;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    env_logger::init();

    let builder = tauri::Builder::default();

    #[cfg(not(mobile))]
    let builder = builder.plugin(tauri_plugin_single_instance::init(|app, args, _cwd| {
        let urls = app.state::<DeepLinkState>().push_arguments(args);
        if let Some(window) = app.get_webview_window("main") {
            let _ = window.show();
            let _ = window.unminimize();
            let _ = window.set_focus();
        }
        if !urls.is_empty() {
            let _ = app.emit("ohmycine-deep-link", urls);
        }
    }));

    let builder = builder
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .manage(PendingUpdate::default())
        .manage(OpenSubtitlesSessionState::default())
        .manage(SubtitleDownloadState::default())
        .manage(ServerStreamState::default())
        .manage(AndroidStreamProxyState::default());
    let builder = builder.manage(DeepLinkState::from_current_process());
    let builder = builder.manage(DownloadQueueState::default());

    #[cfg(target_os = "android")]
    let builder = builder
        .plugin(commands::credential_android::init_android())
        .plugin(commands::download_android::init_android())
        .plugin(commands::updater::init_android())
        .plugin(commands::local_file::init_android())
        .plugin(mpv::mobile::init());

    #[cfg(not(mobile))]
    let builder = builder.manage(mpv::player::create_state().expect("failed to initialize libmpv"));

    builder
        .invoke_handler(tauri::generate_handler![
            credential_set,
            credential_get,
            credential_delete,
            danmaku_match,
            danmaku_search,
            danmaku_comments,
            player_take_pending_deep_links,
            player_download_default_directory,
            player_download_set_default_directory,
            player_download_list,
            player_download_offline_list,
            player_download_offline_detail,
            player_download_offline_asset,
            player_download_sync_attachments,
            player_download_pick_directory,
            player_download_enqueue,
            player_download_cancel,
            player_download_pause,
            player_download_resume,
            player_download_retry,
            player_download_resolve_local,
            player_download_remove,
            player_download_settings,
            player_download_update_settings,
            player_get_playback_speed_preference,
            player_set_playback_speed_preference,
            player_get_media_playback_preference,
            player_upsert_media_playback_preference,
            player_delete_media_playback_preferences_for_source,
            player_clear_media_cache,
            player_upsert_playback_progress,
            player_get_playback_progress,
            player_get_playback_completion_batch,
            player_list_continue_watching,
            player_list_playback_history,
            player_list_playback_history_sync,
            player_list_playback_history_tombstones,
            player_set_playback_completed,
            player_remove_continue_watching,
            player_delete_playback_history_for_source,
            player_merge_playback_history,
            player_list_media_collections,
            player_create_media_collection,
            player_delete_media_collection,
            player_set_local_favorite,
            player_add_media_collection_member,
            player_remove_media_collection_member,
            player_get_cached_image,
            player_cache_image,
            player_image_cache_stats,
            player_trim_image_cache,
            player_settings_get_all,
            player_settings_set,
            player_settings_delete,
            player_get_storage_info,
            server_request_json,
            server_request_blob,
            server_stream_sse,
            server_cancel_sse,
            subtitle_search_opensubtitles,
            subtitle_download_opensubtitles,
            subtitle_login_opensubtitles,
            subtitle_search_hash_provider,
            subtitle_download_hash_provider,
            subtitle_import_local,
            player_check_for_updates,
            player_install_update,
            local_file_list,
            local_file_pick_video,
            local_file_pick_videos,
            local_file_pick_directory,
            local_file_stream_path,
            emby_post_playback_json,
            emby_request_json,
            mpv_apply_engine_settings,
            mpv_load,
            mpv_add_subtitle,
            mpv_pause,
            mpv_resume,
            mpv_stop,
            mpv_seek,
            mpv_get_property,
            mpv_set_property,
            mpv_track_state,
            mpv_playback_diagnostics,
            mpv_orientation_state,
            mpv_set_orientation,
            mpv_display_brightness_state,
            mpv_set_display_brightness,
            mpv_init_render_surface,
            mpv_update_render_surface_bounds,
            mpv_render_status,
            mpv_set_render_strategy,
        ])
        .on_window_event(|window, event| {
            #[cfg(mobile)]
            let _ = (window, event);

            #[cfg(not(mobile))]
            {
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
                    _ => None,
                };

                if owner_event.is_some() {
                    // Never block the main thread on the player mutex while a render callback may
                    // itself be waiting for that thread.
                    if let Ok(mut player) = window.state::<mpv::player::MpvState>().try_lock() {
                        player.on_owner_window_event(OwnerWindowEvent::WindowStateChanged {
                            fullscreen: window.is_fullscreen().unwrap_or(false),
                            maximized: window.is_maximized().unwrap_or(false),
                        });
                        if let Some(event) = owner_event {
                            player.on_owner_window_event(event);
                        }
                    }
                }
            }
        })
        .setup(|app| {
            storage::initialize(app.handle()).map_err(std::io::Error::other)?;
            commands::downloads::recover_interrupted_downloads(app.handle())
                .map_err(std::io::Error::other)?;
            commands::preference::clear_raw_scan_cache(app.handle())
                .map_err(std::io::Error::other)?;
            let webview_transparency_applied = app
                .get_webview_window("main")
                .map(|window| window.set_background_color(Some(Color(0, 0, 0, 0))).is_ok())
                .unwrap_or(false);
            mpv::render::set_webview_background_transparency_applied(webview_transparency_applied);
            if !webview_transparency_applied {
                log::warn!("failed to explicitly set transparent native Tauri/WebView background");
            }

            #[cfg(not(mobile))]
            mpv::events::start_event_forwarder(app.handle().clone());
            #[cfg(target_os = "android")]
            mpv::mobile::start_event_forwarder(app.handle().clone());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

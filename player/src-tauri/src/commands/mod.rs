pub mod clouddrive2;
pub mod credential;
#[cfg(target_os = "android")]
pub mod credential_android;
pub mod danmaku;
#[cfg(not(mobile))]
pub(crate) mod display_brightness;
pub mod emby;
pub mod history;
pub mod image_cache;
#[cfg(target_os = "android")]
#[path = "local_file_android.rs"]
pub mod local_file;
#[cfg(not(target_os = "android"))]
pub mod local_file;
pub mod pan123;
#[cfg(not(mobile))]
pub mod player;
#[cfg(mobile)]
#[path = "player_mobile.rs"]
pub mod player;
pub(crate) mod player_shared;
pub mod preference;
pub mod quark;
pub mod raw_scan_cache;
pub mod settings;
pub mod subtitle;
pub mod tmdb;
pub mod updater;

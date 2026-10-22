pub mod color;
#[cfg(not(mobile))]
mod diagnostics;
#[cfg(not(mobile))]
pub mod events;
mod hls_proxy;
#[cfg(target_os = "android")]
pub mod mobile;
pub mod mobile_proxy;
#[cfg(not(mobile))]
pub mod platform;
pub mod playback;
#[cfg(not(mobile))]
pub mod player;
pub mod render;
pub mod surface;

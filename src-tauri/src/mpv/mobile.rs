use std::time::Duration;

use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    AppHandle, Emitter, Manager, Wry,
};

const PLUGIN_IDENTIFIER: &str = "com.ohmycine.player.mpv";
const PLUGIN_CLASS: &str = "MpvPlugin";

#[derive(Clone)]
pub struct AndroidMpvState {
    handle: PluginHandle<Wry>,
}

impl AndroidMpvState {
    pub async fn run<T: DeserializeOwned>(
        &self,
        command: &str,
        payload: impl Serialize,
    ) -> Result<T, String> {
        self.handle
            .run_mobile_plugin_async(command, payload)
            .await
            .map_err(|error| format!("Android 播放器命令执行失败：{error}"))
    }
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AndroidMpvSnapshot {
    pub time: f64,
    pub duration: f64,
    pub paused: bool,
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AndroidSurfaceStatus {
    pub ready: bool,
    pub error: Option<String>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AndroidPlaybackDiagnostics {
    pub state: String,
    pub last_event: String,
    pub last_error: Option<String>,
    pub file_loaded: bool,
    pub video_format: Option<String>,
    pub audio_codec: Option<String>,
    pub vo_configured: bool,
    pub hardware_decoder: Option<String>,
    pub video_output: String,
    pub video_output_fallback_used: bool,
    pub playback_transport: String,
    pub logs: Vec<String>,
}

#[derive(Clone, Serialize)]
struct TimeUpdatePayload {
    time: f64,
}

#[derive(Clone, Serialize)]
struct DurationChangePayload {
    duration: f64,
}

pub fn init() -> TauriPlugin<Wry> {
    Builder::new("mpv-android")
        .setup(|app, api| {
            let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, PLUGIN_CLASS)?;
            app.manage(AndroidMpvState { handle });
            Ok(())
        })
        .build()
}

pub fn start_event_forwarder(app_handle: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let mut last_time = -1.0;
        let mut last_duration = -1.0;
        let mut last_paused = true;

        loop {
            let Some(state) = app_handle.try_state::<AndroidMpvState>() else {
                break;
            };
            let state = state.inner().clone();

            if let Ok(snapshot) = state.run::<AndroidMpvSnapshot>("snapshot", ()).await {
                if (snapshot.time - last_time).abs() >= 0.25 {
                    last_time = snapshot.time;
                    let _ = app_handle.emit(
                        "mpv:time-update",
                        TimeUpdatePayload {
                            time: snapshot.time,
                        },
                    );
                }

                if (snapshot.duration - last_duration).abs() >= 0.25 {
                    last_duration = snapshot.duration;
                    let _ = app_handle.emit(
                        "mpv:duration-change",
                        DurationChangePayload {
                            duration: snapshot.duration,
                        },
                    );
                }

                if snapshot.paused != last_paused {
                    last_paused = snapshot.paused;
                    let event = if snapshot.paused {
                        "mpv:paused"
                    } else {
                        "mpv:resumed"
                    };
                    let _ = app_handle.emit(event, ());
                }
            }

            tokio::time::sleep(Duration::from_millis(250)).await;
        }
    });
}

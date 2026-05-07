use std::time::Duration;

use tauri::{AppHandle, Emitter, Manager};

use super::player::MpvState;

#[derive(Clone, serde::Serialize)]
struct TimeUpdatePayload {
    time: f64,
}

#[derive(Clone, serde::Serialize)]
struct DurationChangePayload {
    duration: f64,
}

pub fn start_event_forwarder(app_handle: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let mut last_time = -1.0;
        let mut last_duration = -1.0;
        let mut last_paused = true;

        loop {
            let Some(state) = app_handle.try_state::<MpvState>() else {
                break;
            };

            let snapshot = state
                .lock()
                .ok()
                .map(|player| (player.time_pos(), player.duration(), player.paused()));

            if let Some((time, duration, paused)) = snapshot {
                if (time - last_time).abs() >= 0.25 {
                    last_time = time;
                    let _ = app_handle.emit("mpv:time-update", TimeUpdatePayload { time });
                }

                if (duration - last_duration).abs() >= 0.25 {
                    last_duration = duration;
                    let _ =
                        app_handle.emit("mpv:duration-change", DurationChangePayload { duration });
                }

                if paused != last_paused {
                    last_paused = paused;
                    let event = if paused { "mpv:paused" } else { "mpv:resumed" };
                    let _ = app_handle.emit(event, ());
                }
            }

            tokio::time::sleep(Duration::from_millis(250)).await;
        }
    });
}

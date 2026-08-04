use reqwest::Url;
use sha2::{Digest, Sha256};
use std::{fs, path::Path, time::Duration};
use tauri::{AppHandle, State};

use super::player_shared::{
    sanitize_http_headers, MpvDisplayBrightnessState, MpvEngineSettings, MpvHttpHeader,
    MpvOrientationState,
};
use crate::mpv::{
    player::{MpvState, MpvTrackState},
    render::MpvRenderState,
    surface::{RenderSurfaceBounds, ZOrderStrategy},
};
use crate::storage;

const MAX_PREPARED_SUBTITLE_BYTES: usize = 12 * 1024 * 1024;

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DesktopPlaybackDiagnostics {
    state: &'static str,
    last_event: &'static str,
    last_error: Option<String>,
    file_loaded: bool,
    video_format: Option<String>,
    audio_codec: Option<String>,
    vo_configured: bool,
    hardware_decoder: Option<String>,
    video_output: &'static str,
    video_output_fallback_used: bool,
    logs: Vec<String>,
}

#[tauri::command]
pub fn mpv_playback_diagnostics() -> DesktopPlaybackDiagnostics {
    DesktopPlaybackDiagnostics {
        state: "desktop",
        last_event: "desktop-backend",
        last_error: None,
        file_loaded: false,
        video_format: None,
        audio_codec: None,
        vo_configured: false,
        hardware_decoder: None,
        video_output: "desktop",
        video_output_fallback_used: false,
        logs: Vec::new(),
    }
}

#[tauri::command]
pub async fn mpv_load(
    path: String,
    headers: Option<Vec<MpvHttpHeader>>,
    title: Option<String>,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let _ = title;
    let mut player = state.lock().map_err(|err| err.to_string())?;
    let headers = sanitize_http_headers(headers.unwrap_or_default())?
        .into_iter()
        .map(|header| (header.name, header.value))
        .collect::<Vec<_>>();
    player.load_file_with_headers(&path, &headers)
}

#[tauri::command]
pub async fn mpv_apply_engine_settings(
    settings: MpvEngineSettings,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let mut player = state
        .lock()
        .map_err(|_| "播放器设置暂不可用，请稍后重试".to_string())?;
    player.apply_engine_settings(settings)
}

#[tauri::command]
pub async fn mpv_add_subtitle(
    app: AppHandle,
    url: String,
    title: Option<String>,
    language: Option<String>,
    state: State<'_, MpvState>,
) -> Result<(), String> {
    let prepared_path = prepare_external_subtitle(&app, &url, title.as_deref()).await?;
    let mut player = state
        .lock()
        .map_err(|_| "播放器控制暂不可用，请稍后重试".to_string())?;
    player.add_subtitle(&prepared_path, title.as_deref(), language.as_deref())
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
pub async fn mpv_stop(state: State<'_, MpvState>) -> Result<(), String> {
    let mut player = state.lock().map_err(|err| err.to_string())?;
    player.stop()
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
pub async fn mpv_orientation_state() -> Result<MpvOrientationState, String> {
    Ok(MpvOrientationState {
        supported: false,
        mode: "auto".to_string(),
    })
}

#[tauri::command]
pub async fn mpv_set_orientation(mode: String) -> Result<MpvOrientationState, String> {
    let _ = mode;
    mpv_orientation_state().await
}

#[tauri::command]
pub async fn mpv_display_brightness_state() -> Result<MpvDisplayBrightnessState, String> {
    Ok(MpvDisplayBrightnessState {
        supported: false,
        level: 50.0,
    })
}

#[tauri::command]
pub async fn mpv_set_display_brightness(level: f64) -> Result<MpvDisplayBrightnessState, String> {
    let _ = level;
    mpv_display_brightness_state().await
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

async fn prepare_external_subtitle(
    app: &AppHandle,
    input: &str,
    title: Option<&str>,
) -> Result<String, String> {
    let extension = subtitle_extension(input, title)?;
    let layout = storage::initialize(app)?;
    let cache_dir = layout.cache_dir.join("mpv-subtitles");
    fs::create_dir_all(&cache_dir).map_err(|_| "无法创建播放器字幕运行缓存。".to_string())?;

    let mut hasher = Sha256::new();
    hasher.update(input.as_bytes());
    let target = cache_dir.join(format!("{:x}.{extension}", hasher.finalize()));
    if prepared_subtitle_is_valid(&target) {
        return Ok(target.to_string_lossy().to_string());
    }
    let _ = fs::remove_file(&target);

    if is_http_url(input) {
        let url = Url::parse(input).map_err(|_| "媒体源字幕地址无效。".to_string())?;
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(20))
            .redirect(reqwest::redirect::Policy::limited(3))
            .build()
            .map_err(|_| "无法初始化媒体源字幕下载。".to_string())?;
        let response = client
            .get(url)
            .send()
            .await
            .map_err(|_| "无法下载媒体源提供的字幕。".to_string())?;
        if !response.status().is_success() {
            return Err(format!(
                "媒体源字幕下载失败：HTTP {}",
                response.status().as_u16()
            ));
        }
        if response
            .content_length()
            .is_some_and(|size| size > MAX_PREPARED_SUBTITLE_BYTES as u64)
        {
            return Err("媒体源字幕文件过大。".to_string());
        }
        let bytes = read_prepared_subtitle_response(response).await?;
        if bytes.is_empty() || bytes.len() > MAX_PREPARED_SUBTITLE_BYTES {
            return Err("媒体源字幕内容为空或过大。".to_string());
        }
        write_prepared_subtitle(&target, &bytes, "媒体源字幕缓存写入失败。")?;
    } else {
        let source = fs::canonicalize(input).map_err(|_| "本地字幕文件不存在。".to_string())?;
        if !source.is_file() {
            return Err("本地字幕路径不是文件。".to_string());
        }
        let size = source
            .metadata()
            .map_err(|_| "无法读取本地字幕文件信息。".to_string())?
            .len();
        if size == 0 || size > MAX_PREPARED_SUBTITLE_BYTES as u64 {
            return Err("本地字幕文件为空或过大。".to_string());
        }
        let bytes = fs::read(source).map_err(|_| "无法读取本地字幕文件。".to_string())?;
        write_prepared_subtitle(&target, &bytes, "本地字幕无法写入播放器运行缓存。")?;
    }

    Ok(target.to_string_lossy().to_string())
}

async fn read_prepared_subtitle_response(
    mut response: reqwest::Response,
) -> Result<Vec<u8>, String> {
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "媒体源字幕内容读取失败。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_PREPARED_SUBTITLE_BYTES {
            return Err("媒体源字幕文件过大。".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

fn prepared_subtitle_is_valid(path: &Path) -> bool {
    path.metadata()
        .map(|metadata| {
            metadata.is_file()
                && metadata.len() > 0
                && metadata.len() <= MAX_PREPARED_SUBTITLE_BYTES as u64
        })
        .unwrap_or(false)
}

fn write_prepared_subtitle(target: &Path, bytes: &[u8], message: &str) -> Result<(), String> {
    let extension = target
        .extension()
        .and_then(|value| value.to_str())
        .unwrap_or("srt");
    let temporary = target.with_extension(format!("{extension}.{}.part", rand::random::<u64>()));
    fs::write(&temporary, bytes).map_err(|_| message.to_string())?;
    match fs::rename(&temporary, target) {
        Ok(()) => Ok(()),
        Err(_) if prepared_subtitle_is_valid(target) => {
            let _ = fs::remove_file(temporary);
            Ok(())
        }
        Err(_) => {
            let _ = fs::remove_file(temporary);
            Err(message.to_string())
        }
    }
}

fn subtitle_extension(input: &str, title: Option<&str>) -> Result<String, String> {
    let input_extension = if is_http_url(input) {
        Url::parse(input).ok().and_then(|url| {
            Path::new(url.path())
                .extension()?
                .to_str()
                .map(str::to_owned)
        })
    } else {
        Path::new(input)
            .extension()
            .and_then(|value| value.to_str())
            .map(str::to_owned)
    };
    let extension = input_extension
        .or_else(|| {
            title.and_then(|value| Path::new(value).extension()?.to_str().map(str::to_owned))
        })
        .unwrap_or_else(|| "srt".to_string())
        .to_ascii_lowercase();
    match extension.as_str() {
        "srt" | "ass" | "ssa" | "vtt" | "sub" => Ok(extension),
        "subrip" => Ok("srt".to_string()),
        _ => Err("当前外部字幕格式不受支持。".to_string()),
    }
}

fn is_http_url(value: &str) -> bool {
    value.starts_with("https://") || value.starts_with("http://")
}

#[cfg(test)]
mod tests {
    use super::subtitle_extension;

    #[test]
    fn resolves_supported_external_subtitle_extensions() {
        assert_eq!(
            subtitle_extension(r"\\?\C:\Media\Movie.srt", None).unwrap(),
            "srt"
        );
        assert_eq!(
            subtitle_extension("https://media.example.test/Stream.ass?token=secret", None).unwrap(),
            "ass"
        );
        assert_eq!(
            subtitle_extension("https://media.example.test/subtitle", Some("Movie.vtt")).unwrap(),
            "vtt"
        );
        assert!(subtitle_extension("C:\\Media\\Movie.exe", None).is_err());
    }
}

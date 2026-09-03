use reqwest::{header::LOCATION, redirect, Url};
use sha2::{Digest, Sha256};
use std::{fs, path::Path, time::Duration};
use tauri::AppHandle;

use crate::storage;

const MAX_PREPARED_SUBTITLE_BYTES: usize = 12 * 1024 * 1024;
const MAX_PREPARED_SUBTITLE_REDIRECTS: usize = 3;

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvHttpHeader {
    pub name: String,
    pub value: String,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvOrientationState {
    pub supported: bool,
    pub mode: String,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvDisplayBrightnessState {
    pub supported: bool,
    pub level: f64,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FrameInterpolationCapability {
    pub supported: bool,
    pub backend: Option<String>,
    pub reason: Option<String>,
    pub api_level: Option<u32>,
    pub gpu_name: Option<String>,
    pub gpu_adapter_id: Option<String>,
    pub fp16: bool,
    pub hdr_kinds: Vec<String>,
    pub max_target_fps: Option<u16>,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FrameInterpolationDiagnostics {
    pub frame_interpolation_requested_mode: String,
    pub frame_interpolation_effective_state: String,
    pub frame_interpolation_reason: Option<String>,
    pub frame_interpolation_backend: Option<String>,
    pub frame_interpolation_input_hdr_kind: String,
    pub frame_interpolation_output_hdr_mode: String,
    pub frame_interpolation_target_fps: Option<u16>,
    pub frame_interpolation_flow_scale: Option<f64>,
    pub frame_interpolation_model_time_p50_ms: Option<f64>,
    pub frame_interpolation_model_time_p95_ms: Option<f64>,
    pub frame_interpolation_dropped_frames: u64,
    pub frame_interpolation_capability: FrameInterpolationCapability,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvEngineSettings {
    pub video_output: String,
    pub hardware_decoder: String,
    pub cache_mode: String,
    pub demuxer_max_bytes_mb: u16,
    pub video_sync: String,
    pub background_playback_enabled: bool,
    pub fsr_mode: String,
    pub fsr_sharpness: f64,
    pub fsr_denoise: bool,
    pub fsr_target: String,
    pub frame_interpolation_mode: String,
    pub frame_interpolation_target: String,
    pub frame_interpolation_quality: String,
}

impl Default for MpvEngineSettings {
    fn default() -> Self {
        Self {
            video_output: "gpu-next".to_string(),
            hardware_decoder: "auto-safe".to_string(),
            cache_mode: "auto".to_string(),
            demuxer_max_bytes_mb: 64,
            video_sync: "audio".to_string(),
            background_playback_enabled: true,
            fsr_mode: "auto".to_string(),
            fsr_sharpness: 35.0,
            fsr_denoise: true,
            fsr_target: "auto".to_string(),
            frame_interpolation_mode: "off".to_string(),
            frame_interpolation_target: "auto".to_string(),
            frame_interpolation_quality: "auto".to_string(),
        }
    }
}

impl MpvEngineSettings {
    pub fn validated(mut self) -> Result<Self, String> {
        if !matches!(self.video_output.as_str(), "gpu-next" | "gpu") {
            return Err("不支持的视频输出设置。".to_string());
        }
        if !matches!(
            self.hardware_decoder.as_str(),
            "auto-safe" | "auto" | "software"
        ) {
            return Err("不支持的硬件解码设置。".to_string());
        }
        if !matches!(self.cache_mode.as_str(), "auto" | "enabled" | "disabled") {
            return Err("不支持的播放缓存设置。".to_string());
        }
        if !matches!(self.demuxer_max_bytes_mb, 64 | 128 | 256 | 512) {
            return Err("不支持的媒体缓存大小。".to_string());
        }
        if !matches!(
            self.video_sync.as_str(),
            "audio" | "display-resample" | "display-vdrop"
        ) {
            return Err("不支持的视频同步设置。".to_string());
        }
        if !matches!(self.fsr_mode.as_str(), "off" | "auto" | "force") {
            self.fsr_mode = "auto".to_string();
        }
        if !self.fsr_sharpness.is_finite() {
            self.fsr_sharpness = 35.0;
        }
        self.fsr_sharpness = self.fsr_sharpness.clamp(0.0, 100.0).round();
        if !matches!(
            self.fsr_target.as_str(),
            "auto" | "1080p" | "1440p" | "2160p"
        ) {
            self.fsr_target = "auto".to_string();
        }
        if !matches!(self.frame_interpolation_mode.as_str(), "off" | "auto") {
            self.frame_interpolation_mode = "off".to_string();
        }
        if !matches!(
            self.frame_interpolation_target.as_str(),
            "auto" | "48" | "60" | "120"
        ) {
            self.frame_interpolation_target = "auto".to_string();
        }
        if !matches!(
            self.frame_interpolation_quality.as_str(),
            "auto" | "quality" | "balanced" | "performance"
        ) {
            self.frame_interpolation_quality = "auto".to_string();
        }
        Ok(self)
    }

    #[cfg(not(mobile))]
    pub fn desktop_hwdec(&self) -> &str {
        match self.hardware_decoder.as_str() {
            "auto" => "auto",
            "software" => "no",
            _ => "auto-safe",
        }
    }

    #[cfg(not(mobile))]
    pub fn cache_value(&self) -> &str {
        match self.cache_mode.as_str() {
            "enabled" => "yes",
            "disabled" => "no",
            _ => "auto",
        }
    }

    #[cfg(not(mobile))]
    pub fn demuxer_max_bytes(&self) -> u64 {
        u64::from(self.demuxer_max_bytes_mb) * 1024 * 1024
    }

    pub fn fsr_sharpness_stops(&self) -> f64 {
        2.0 * (1.0 - self.fsr_sharpness / 100.0)
    }

    pub fn frame_interpolation_target_fps(&self) -> Option<u16> {
        self.frame_interpolation_target.parse().ok()
    }

    pub fn unavailable_frame_interpolation_diagnostics(
        &self,
        hardware_decoder: Option<&str>,
        reason: &str,
        api_level: Option<u32>,
    ) -> FrameInterpolationDiagnostics {
        let has_hwdec = hardware_decoder
            .map(str::trim)
            .is_some_and(|value| !value.is_empty() && value != "no");
        let (effective_state, effective_reason) = if self.frame_interpolation_mode == "off" {
            ("disabled", None)
        } else if !has_hwdec {
            (
                "unavailable-no-hwdec",
                Some("当前媒体未使用硬件解码，视频插帧已自动关闭。".to_string()),
            )
        } else {
            ("backend-unavailable", Some(reason.to_string()))
        };

        FrameInterpolationDiagnostics {
            frame_interpolation_requested_mode: self.frame_interpolation_mode.clone(),
            frame_interpolation_effective_state: effective_state.to_string(),
            frame_interpolation_reason: effective_reason,
            frame_interpolation_backend: None,
            frame_interpolation_input_hdr_kind: "unknown".to_string(),
            frame_interpolation_output_hdr_mode: "unknown".to_string(),
            frame_interpolation_target_fps: self.frame_interpolation_target_fps(),
            frame_interpolation_flow_scale: None,
            frame_interpolation_model_time_p50_ms: None,
            frame_interpolation_model_time_p95_ms: None,
            frame_interpolation_dropped_frames: 0,
            frame_interpolation_capability: FrameInterpolationCapability {
                supported: false,
                backend: None,
                reason: Some(reason.to_string()),
                api_level,
                gpu_name: None,
                gpu_adapter_id: None,
                fp16: false,
                hdr_kinds: Vec::new(),
                max_target_fps: None,
            },
        }
    }

    pub fn fsr_target_short_edge(&self) -> Option<u32> {
        match self.fsr_target.as_str() {
            "1080p" => Some(1080),
            "1440p" => Some(1440),
            "2160p" => Some(2160),
            _ => None,
        }
    }
}

pub fn sanitize_http_headers(headers: Vec<MpvHttpHeader>) -> Result<Vec<MpvHttpHeader>, String> {
    if headers.len() > 16 {
        return Err("播放请求 header 数量过多。".to_string());
    }

    headers
        .into_iter()
        .map(|header| {
            let name = header.name.trim().to_string();
            let value = header.value.trim().to_string();
            if name.is_empty() || value.is_empty() {
                return Err("播放请求 header 格式无效。".to_string());
            }
            if !is_valid_header_name(&name) || value.chars().any(|ch| ch == '\r' || ch == '\n') {
                return Err("播放请求 header 格式无效。".to_string());
            }
            Ok(MpvHttpHeader { name, value })
        })
        .collect()
}

pub(crate) async fn prepare_external_subtitle(
    app: &AppHandle,
    input: &str,
    title: Option<&str>,
    headers: Vec<MpvHttpHeader>,
) -> Result<String, String> {
    let headers = sanitize_http_headers(headers)?;
    let extension = subtitle_extension(input, title)?;
    let layout = storage::initialize(app)?;
    let cache_dir = layout.cache_dir.join("mpv-subtitles");
    fs::create_dir_all(&cache_dir).map_err(|_| "无法创建播放器字幕运行缓存。".to_string())?;

    let mut hasher = Sha256::new();
    hasher.update(input.as_bytes());
    for header in &headers {
        hasher.update([0]);
        hasher.update(header.name.as_bytes());
        hasher.update([0]);
        hasher.update(header.value.as_bytes());
    }
    let target = cache_dir.join(format!("{:x}.{extension}", hasher.finalize()));
    if prepared_subtitle_is_valid(&target) {
        return Ok(target.to_string_lossy().to_string());
    }
    let _ = fs::remove_file(&target);

    if is_http_url(input) {
        let bytes = download_external_subtitle(input, headers).await?;
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

async fn download_external_subtitle(
    input: &str,
    mut headers: Vec<MpvHttpHeader>,
) -> Result<Vec<u8>, String> {
    let mut url = validated_subtitle_url(input)?;
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(20))
        .redirect(redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化媒体源字幕下载。".to_string())?;

    for redirect_count in 0..=MAX_PREPARED_SUBTITLE_REDIRECTS {
        let mut request = client.get(url.clone());
        for header in &headers {
            request = request.header(&header.name, &header.value);
        }
        let response = request
            .send()
            .await
            .map_err(|_| "无法下载媒体源提供的字幕。".to_string())?;

        if response.status().is_redirection() {
            if redirect_count == MAX_PREPARED_SUBTITLE_REDIRECTS {
                return Err("媒体源字幕重定向次数过多。".to_string());
            }
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| "媒体源字幕重定向地址无效。".to_string())?;
            let next = url
                .join(location)
                .map_err(|_| "媒体源字幕重定向地址无效。".to_string())?;
            let next = validated_subtitle_url(next.as_str())?;
            if url.scheme() == "https" && next.scheme() != "https" {
                return Err("媒体源字幕拒绝不安全的 HTTPS 降级。".to_string());
            }
            if !same_origin(&url, &next) {
                headers.clear();
            }
            url = next;
            continue;
        }

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
        return Ok(bytes);
    }

    Err("媒体源字幕重定向次数过多。".to_string())
}

fn validated_subtitle_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "媒体源字幕地址无效。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.host_str().is_none()
    {
        return Err("媒体源字幕地址无效。".to_string());
    }
    Ok(url)
}

fn same_origin(left: &Url, right: &Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
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

fn is_valid_header_name(value: &str) -> bool {
    value.bytes().all(|byte| {
        byte.is_ascii_alphanumeric()
            || matches!(
                byte,
                b'!' | b'#'
                    | b'$'
                    | b'%'
                    | b'&'
                    | b'\''
                    | b'*'
                    | b'+'
                    | b'-'
                    | b'.'
                    | b'^'
                    | b'_'
                    | b'`'
                    | b'|'
                    | b'~'
            )
    })
}

#[cfg(test)]
mod tests {
    use super::{
        download_external_subtitle, same_origin, sanitize_http_headers, subtitle_extension,
        validated_subtitle_url, MpvEngineSettings, MpvHttpHeader,
    };
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::TcpListener,
    };

    #[test]
    fn sanitizes_playback_headers_without_exposing_invalid_lines() {
        let headers = sanitize_http_headers(vec![MpvHttpHeader {
            name: " Authorization ".to_string(),
            value: " Bearer secret ".to_string(),
        }])
        .unwrap();

        assert_eq!(headers[0].name, "Authorization");
        assert_eq!(headers[0].value, "Bearer secret");
        assert!(sanitize_http_headers(vec![MpvHttpHeader {
            name: "Authorization\r\nInjected".to_string(),
            value: "secret".to_string(),
        }])
        .is_err());
    }

    #[test]
    fn validates_engine_settings_as_known_mpv_values() {
        let settings = MpvEngineSettings::default().validated().unwrap();
        assert_eq!(settings.video_output, "gpu-next");
        assert_eq!(settings.desktop_hwdec(), "auto-safe");
        assert_eq!(settings.demuxer_max_bytes(), 64 * 1024 * 1024);
        assert!((settings.fsr_sharpness_stops() - 1.3).abs() < f64::EPSILON);
        assert_eq!(settings.fsr_target_short_edge(), None);
        assert_eq!(settings.frame_interpolation_mode, "off");
        assert_eq!(settings.frame_interpolation_target_fps(), None);

        let invalid = MpvEngineSettings {
            video_output: "custom-vo".to_string(),
            ..MpvEngineSettings::default()
        };
        assert!(invalid.validated().is_err());

        let normalized_fsr = MpvEngineSettings {
            fsr_mode: "unknown".to_string(),
            fsr_sharpness: f64::INFINITY,
            fsr_target: "8k".to_string(),
            ..MpvEngineSettings::default()
        }
        .validated()
        .unwrap();
        assert_eq!(normalized_fsr.fsr_mode, "auto");
        assert_eq!(normalized_fsr.fsr_sharpness, 35.0);
        assert_eq!(normalized_fsr.fsr_target, "auto");

        let normalized_interpolation = MpvEngineSettings {
            frame_interpolation_mode: "force".to_string(),
            frame_interpolation_target: "240".to_string(),
            frame_interpolation_quality: "cinematic".to_string(),
            ..MpvEngineSettings::default()
        }
        .validated()
        .unwrap();
        assert_eq!(normalized_interpolation.frame_interpolation_mode, "off");
        assert_eq!(normalized_interpolation.frame_interpolation_target, "auto");
        assert_eq!(normalized_interpolation.frame_interpolation_quality, "auto");

        let enabled_interpolation = MpvEngineSettings {
            frame_interpolation_mode: "auto".to_string(),
            frame_interpolation_target: "60".to_string(),
            ..MpvEngineSettings::default()
        };
        let no_hwdec = enabled_interpolation.unavailable_frame_interpolation_diagnostics(
            None,
            "backend missing",
            None,
        );
        assert_eq!(
            no_hwdec.frame_interpolation_effective_state,
            "unavailable-no-hwdec"
        );
        let no_backend = enabled_interpolation.unavailable_frame_interpolation_diagnostics(
            Some("d3d11va"),
            "backend missing",
            None,
        );
        assert_eq!(
            no_backend.frame_interpolation_effective_state,
            "backend-unavailable"
        );
        assert_eq!(no_backend.frame_interpolation_target_fps, Some(60));
        assert!(!no_backend.frame_interpolation_capability.supported);
    }

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

    #[test]
    fn validates_subtitle_redirect_security_boundaries() {
        let first = validated_subtitle_url("https://emby.example.test/subtitle").unwrap();
        let same = validated_subtitle_url("https://emby.example.test/next").unwrap();
        let other = validated_subtitle_url("https://cdn.example.test/next").unwrap();
        assert!(same_origin(&first, &same));
        assert!(!same_origin(&first, &other));
        assert!(validated_subtitle_url("https://user:secret@emby.example.test/subtitle").is_err());
        assert!(validated_subtitle_url("file:///tmp/subtitle.srt").is_err());
    }

    #[tokio::test]
    async fn strips_sensitive_headers_on_cross_origin_subtitle_redirects() {
        let redirect_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let redirect_address = redirect_listener.local_addr().unwrap();
        let subtitle_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let subtitle_address = subtitle_listener.local_addr().unwrap();

        let redirect_server = tokio::spawn(async move {
            let (mut stream, _) = redirect_listener.accept().await.unwrap();
            let request = read_test_request(&mut stream).await;
            assert!(request
                .to_ascii_lowercase()
                .contains("authorization: bearer secret"));
            let response = format!(
                "HTTP/1.1 302 Found\r\nLocation: http://{subtitle_address}/subtitle.srt\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            );
            stream.write_all(response.as_bytes()).await.unwrap();
        });
        let subtitle_server = tokio::spawn(async move {
            let (mut stream, _) = subtitle_listener.accept().await.unwrap();
            let request = read_test_request(&mut stream).await;
            assert!(!request.to_ascii_lowercase().contains("authorization:"));
            let body = b"1\n00:00:00,000 --> 00:00:01,000\nOhMyCine\n";
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/x-subrip\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            stream.write_all(response.as_bytes()).await.unwrap();
            stream.write_all(body).await.unwrap();
        });

        let body = download_external_subtitle(
            &format!("http://{redirect_address}/redirect.srt"),
            vec![MpvHttpHeader {
                name: "Authorization".to_string(),
                value: "Bearer secret".to_string(),
            }],
        )
        .await
        .unwrap();

        assert!(String::from_utf8(body).unwrap().contains("OhMyCine"));
        redirect_server.await.unwrap();
        subtitle_server.await.unwrap();
    }

    async fn read_test_request(stream: &mut tokio::net::TcpStream) -> String {
        let mut request = Vec::new();
        let mut buffer = [0_u8; 1024];
        while !request.windows(4).any(|window| window == b"\r\n\r\n") {
            let read = stream.read(&mut buffer).await.unwrap();
            if read == 0 {
                break;
            }
            request.extend_from_slice(&buffer[..read]);
        }
        String::from_utf8(request).unwrap()
    }
}

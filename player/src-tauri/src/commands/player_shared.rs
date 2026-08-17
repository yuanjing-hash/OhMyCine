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
    use super::{sanitize_http_headers, MpvEngineSettings, MpvHttpHeader};

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
    }
}

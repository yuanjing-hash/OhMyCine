use reqwest::header::{HeaderMap, HeaderValue, ACCEPT, CONTENT_TYPE, LOCATION, USER_AGENT};
use reqwest::{StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;
use tauri::AppHandle;

const OPENSUBTITLES_API_BASE: &str = "https://api.opensubtitles.com/api/v1";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const MAX_SEARCH_RESPONSE_BYTES: usize = 2 * 1024 * 1024;
const MAX_DOWNLOAD_RESPONSE_BYTES: usize = 12 * 1024 * 1024;
const MAX_ERROR_BODY_CHARS: usize = 500;
const MAX_REDIRECTS: usize = 3;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesSearchRequest {
    api_key: String,
    language: String,
    query: Option<String>,
    imdb_id: Option<String>,
    tmdb_id: Option<u64>,
    year: Option<u16>,
    season_number: Option<u16>,
    episode_number: Option<u16>,
    media_type: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesDownloadRequest {
    api_key: String,
    file_id: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadedSubtitle {
    path: String,
}

#[tauri::command]
pub async fn subtitle_search_opensubtitles(
    request: OpenSubtitlesSearchRequest,
) -> Result<Value, String> {
    let api_key = validate_api_key(&request.api_key)?;
    let mut url = Url::parse(&format!("{OPENSUBTITLES_API_BASE}/subtitles/"))
        .map_err(|_| "字幕搜索服务地址无效。".to_string())?;
    {
        let mut query = url.query_pairs_mut();
        query.append_pair("languages", &normalize_language(&request.language)?);
        query.append_pair("order_by", "download_count");
        query.append_pair("order_direction", "desc");
        if let Some(value) = normalized_query(request.query.as_deref())? {
            query.append_pair("query", &value);
        }
        if let Some(value) = normalize_imdb_id(request.imdb_id.as_deref())? {
            query.append_pair("imdb_id", &value);
        }
        if let Some(value) = request.tmdb_id {
            query.append_pair("tmdb_id", &value.to_string());
        }
        if let Some(value) = request.year {
            query.append_pair("year", &value.to_string());
        }
        if let Some(value) = request.season_number {
            query.append_pair("season_number", &value.to_string());
        }
        if let Some(value) = request.episode_number {
            query.append_pair("episode_number", &value.to_string());
        }
        if let Some(value) = normalize_media_type(request.media_type.as_deref()) {
            query.append_pair("type", value);
        }
    }

    let response = http_client()?
        .get(url)
        .headers(opensubtitles_headers(&api_key)?)
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    serde_json::from_slice(&bytes).map_err(|_| "字幕搜索服务返回了无法解析的数据。".to_string())
}

#[tauri::command]
pub async fn subtitle_download_opensubtitles(
    app: AppHandle,
    request: OpenSubtitlesDownloadRequest,
) -> Result<DownloadedSubtitle, String> {
    let api_key = validate_api_key(&request.api_key)?;
    if request.file_id == 0 {
        return Err("字幕下载标识无效。".to_string());
    }

    let response = http_client()?
        .post(format!("{OPENSUBTITLES_API_BASE}/download"))
        .headers(opensubtitles_headers(&api_key)?)
        .header(CONTENT_TYPE, "application/json")
        .json(&serde_json::json!({ "file_id": request.file_id }))
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    let payload: Value = serde_json::from_slice(&bytes)
        .map_err(|_| "字幕下载服务返回了无法解析的数据。".to_string())?;
    let link = payload
        .get("link")
        .and_then(Value::as_str)
        .ok_or_else(|| "字幕下载服务没有返回可用链接。".to_string())?;
    let file_name = payload.get("file_name").and_then(Value::as_str);
    let extension = safe_subtitle_extension(file_name).unwrap_or("srt");
    let download_url = validate_download_url(link)?;
    let content = download_with_redirects(download_url).await?;

    let layout = crate::storage::initialize(&app)?;
    let subtitle_dir = layout.cache_dir.join("subtitles");
    fs::create_dir_all(&subtitle_dir).map_err(|_| "无法创建字幕缓存目录。".to_string())?;
    let target = subtitle_cache_path(&subtitle_dir, request.file_id, extension);
    fs::write(&target, content).map_err(|_| "无法写入字幕缓存文件。".to_string())?;

    Ok(DownloadedSubtitle {
        path: target.to_string_lossy().to_string(),
    })
}

fn http_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化字幕网络客户端。".to_string())
}

fn opensubtitles_headers(api_key: &str) -> Result<HeaderMap, String> {
    let mut headers = HeaderMap::new();
    headers.insert(ACCEPT, HeaderValue::from_static("application/json"));
    headers.insert(USER_AGENT, HeaderValue::from_static("OhMyCine Player v0.1"));
    headers.insert(
        "api-key",
        HeaderValue::from_str(api_key)
            .map_err(|_| "OpenSubtitles API Key 格式无效。".to_string())?,
    );
    Ok(headers)
}

async fn download_with_redirects(mut url: Url) -> Result<Vec<u8>, String> {
    let client = http_client()?;
    for _ in 0..=MAX_REDIRECTS {
        let response = client
            .get(url.clone())
            .header(USER_AGENT, "OhMyCine Player v0.1")
            .send()
            .await
            .map_err(network_error)?;
        if response.status().is_redirection() {
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| "字幕下载重定向无效。".to_string())?;
            url = validate_download_url(
                url.join(location)
                    .map_err(|_| "字幕下载重定向无效。".to_string())?
                    .as_str(),
            )?;
            continue;
        }
        return read_limited_response(response, MAX_DOWNLOAD_RESPONSE_BYTES).await;
    }
    Err("字幕下载重定向次数过多。".to_string())
}

async fn read_limited_response(
    response: reqwest::Response,
    max_bytes: usize,
) -> Result<Vec<u8>, String> {
    let status = response.status();
    if response
        .content_length()
        .is_some_and(|length| length > max_bytes as u64)
    {
        return Err("字幕服务响应过大，已拒绝处理。".to_string());
    }
    let bytes = response.bytes().await.map_err(network_error)?;
    if bytes.len() > max_bytes {
        return Err("字幕服务响应过大，已拒绝处理。".to_string());
    }
    if !status.is_success() {
        let message = String::from_utf8_lossy(&bytes)
            .chars()
            .take(MAX_ERROR_BODY_CHARS)
            .collect::<String>();
        return Err(http_error(status, &message));
    }
    Ok(bytes.to_vec())
}

fn validate_download_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "字幕下载链接无效。".to_string())?;
    let host = url.host_str().unwrap_or_default().to_ascii_lowercase();
    if url.scheme() != "https"
        || url.username() != ""
        || url.password().is_some()
        || !(host == "opensubtitles.com" || host.ends_with(".opensubtitles.com"))
    {
        return Err("字幕下载链接不在受信任的 OpenSubtitles 域名内。".to_string());
    }
    Ok(url)
}

fn validate_api_key(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > 256
        || trimmed.chars().any(|character| character.is_control())
    {
        return Err("OpenSubtitles API Key 格式无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_language(value: &str) -> Result<String, String> {
    let normalized = value.trim().to_ascii_lowercase();
    if normalized.is_empty()
        || normalized.len() > 12
        || !normalized
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
    {
        return Err("字幕语言格式无效。".to_string());
    }
    Ok(normalized)
}

fn normalized_query(value: Option<&str>) -> Result<Option<String>, String> {
    let Some(value) = value else {
        return Ok(None);
    };
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    if trimmed.len() > 200 || trimmed.chars().any(|character| character.is_control()) {
        return Err("字幕搜索标题格式无效。".to_string());
    }
    Ok(Some(trimmed.to_string()))
}

fn normalize_imdb_id(value: Option<&str>) -> Result<Option<String>, String> {
    let Some(value) = value else {
        return Ok(None);
    };
    let normalized = value.trim().trim_start_matches("tt");
    if normalized.is_empty() {
        return Ok(None);
    }
    if normalized.len() > 12 || !normalized.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err("IMDb 标识格式无效。".to_string());
    }
    Ok(Some(normalized.to_string()))
}

fn normalize_media_type(value: Option<&str>) -> Option<&'static str> {
    match value {
        Some("movie") => Some("movie"),
        Some("episode") | Some("series") => Some("episode"),
        _ => None,
    }
}

fn safe_subtitle_extension(file_name: Option<&str>) -> Option<&'static str> {
    let extension = Path::new(file_name?)
        .extension()?
        .to_str()?
        .to_ascii_lowercase();
    match extension.as_str() {
        "srt" => Some("srt"),
        "ass" => Some("ass"),
        "ssa" => Some("ssa"),
        "vtt" => Some("vtt"),
        "sub" => Some("sub"),
        _ => None,
    }
}

fn subtitle_cache_path(directory: &Path, file_id: u64, extension: &str) -> PathBuf {
    let mut hasher = Sha256::new();
    hasher.update(b"opensubtitles\0");
    hasher.update(file_id.to_le_bytes());
    directory.join(format!("{:x}.{extension}", hasher.finalize()))
}

fn network_error(error: reqwest::Error) -> String {
    if error.is_timeout() {
        "字幕服务请求超时。".to_string()
    } else if error.is_connect() {
        "无法连接字幕服务。".to_string()
    } else {
        "字幕服务网络请求失败。".to_string()
    }
}

fn http_error(status: StatusCode, body: &str) -> String {
    let detail = if body.trim().is_empty() {
        String::new()
    } else {
        format!("：{}", body.trim())
    };
    match status {
        StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN => {
            "OpenSubtitles API Key 无效或没有权限。".to_string()
        }
        StatusCode::TOO_MANY_REQUESTS => "OpenSubtitles 下载额度或请求频率已达到限制。".to_string(),
        _ => format!("字幕服务返回 HTTP {}{detail}", status.as_u16()),
    }
}

#[cfg(test)]
mod tests {
    use super::{safe_subtitle_extension, validate_download_url};

    #[test]
    fn only_accepts_opensubtitles_https_downloads() {
        assert!(validate_download_url("https://dl.opensubtitles.com/en/download/file/1").is_ok());
        assert!(validate_download_url("http://dl.opensubtitles.com/file/1").is_err());
        assert!(validate_download_url("https://opensubtitles.com.example.test/file/1").is_err());
    }

    #[test]
    fn only_accepts_known_subtitle_extensions() {
        assert_eq!(
            safe_subtitle_extension(Some("movie.zh-CN.srt")),
            Some("srt")
        );
        assert_eq!(safe_subtitle_extension(Some("movie.exe")), None);
    }
}

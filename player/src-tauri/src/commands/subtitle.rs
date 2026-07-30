use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use rand::RngCore;
use reqwest::header::{
    HeaderMap, HeaderValue, ACCEPT, AUTHORIZATION, CONTENT_TYPE, LOCATION, USER_AGENT,
};
use reqwest::{StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha1::Sha1;
use sha2::{Digest, Sha256};
use std::cmp::Reverse;
use std::collections::HashMap;
use std::fs::{self, File};
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tauri::{AppHandle, State};

const OPENSUBTITLES_API_BASE: &str = "https://api.opensubtitles.com/api/v1";
const SHOOTER_API_URL: &str = "https://www.shooter.cn/api/subapi.php";
const XUNLEI_API_PREFIX: &str = "http://sub.xmp.sandai.net:8000/subxl/";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const MAX_SEARCH_RESPONSE_BYTES: usize = 2 * 1024 * 1024;
const MAX_DOWNLOAD_RESPONSE_BYTES: usize = 12 * 1024 * 1024;
const MAX_REDIRECTS: usize = 3;
const DOWNLOAD_REFERENCE_TTL: Duration = Duration::from_secs(30 * 60);
const OPENSUBTITLES_SESSION_TTL: Duration = Duration::from_secs(23 * 60 * 60);
const MAX_PENDING_DOWNLOADS: usize = 256;
const MIN_HASHABLE_FILE_SIZE: u64 = 0xF000;

#[derive(Default)]
pub struct OpenSubtitlesSessionState(tokio::sync::Mutex<Option<OpenSubtitlesSession>>);

struct OpenSubtitlesSession {
    credential_fingerprint: [u8; 32],
    token: String,
    expires_at: Instant,
}

#[derive(Default)]
pub struct SubtitleDownloadState(Mutex<HashMap<String, PendingSubtitleDownload>>);

#[derive(Clone)]
struct PendingSubtitleDownload {
    provider: HashSubtitleProvider,
    url: Url,
    extension: String,
    created_at: Instant,
}

#[derive(Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
enum HashSubtitleProvider {
    Shooter,
    Xunlei,
}

impl HashSubtitleProvider {
    fn id(self) -> &'static str {
        match self {
            Self::Shooter => "shooter",
            Self::Xunlei => "xunlei",
        }
    }

    fn display_name(self) -> &'static str {
        match self {
            Self::Shooter => "射手网",
            Self::Xunlei => "迅雷字幕",
        }
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesLoginRequest {
    api_key: String,
    username: String,
    password: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesSearchRequest {
    api_key: String,
    username: Option<String>,
    password: Option<String>,
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
    username: Option<String>,
    password: Option<String>,
    file_id: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HashSubtitleSearchRequest {
    provider: HashSubtitleProvider,
    file_path: String,
    language: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HashSubtitleDownloadRequest {
    provider: HashSubtitleProvider,
    download_ref: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadedSubtitle {
    path: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HashSubtitleSearchResult {
    id: String,
    origin: &'static str,
    provider_name: &'static str,
    language: String,
    title: String,
    format: String,
    comments: Option<String>,
    rating: Option<f64>,
    download_count: Option<u64>,
    is_hash_match: bool,
    download_ref: String,
}

#[derive(Deserialize)]
struct OpenSubtitlesLoginResponse {
    token: Option<String>,
}

#[derive(Deserialize)]
struct ShooterRecord {
    #[serde(rename = "Desc", default)]
    description: String,
    #[serde(rename = "Delay", default)]
    delay: i64,
    #[serde(rename = "Files", default)]
    files: Vec<ShooterFile>,
}

#[derive(Deserialize)]
struct ShooterFile {
    #[serde(rename = "Ext", default)]
    extension: String,
    #[serde(rename = "Link", default)]
    link: String,
}

#[derive(Deserialize, Default)]
struct XunleiResponse {
    #[serde(default)]
    sublist: Vec<XunleiRecord>,
}

#[derive(Deserialize, Default)]
struct XunleiRecord {
    #[serde(default)]
    scid: String,
    #[serde(default)]
    sname: String,
    #[serde(default)]
    language: String,
    #[serde(default)]
    rate: String,
    #[serde(default)]
    surl: String,
    #[serde(default)]
    svote: u64,
}

#[tauri::command]
pub async fn subtitle_login_opensubtitles(
    sessions: State<'_, OpenSubtitlesSessionState>,
    request: OpenSubtitlesLoginRequest,
) -> Result<(), String> {
    let api_key = validate_api_key(&request.api_key)?;
    let username = validate_account_field(&request.username, "账号")?;
    let password = validate_account_field(&request.password, "密码")?;
    ensure_opensubtitles_token(&sessions, &api_key, Some((&username, &password)))
        .await
        .map(|_| ())
}

#[tauri::command]
pub async fn subtitle_search_opensubtitles(
    sessions: State<'_, OpenSubtitlesSessionState>,
    request: OpenSubtitlesSearchRequest,
) -> Result<Value, String> {
    let api_key = validate_api_key(&request.api_key)?;
    let account =
        validate_optional_account(request.username.as_deref(), request.password.as_deref())?;
    let token = ensure_opensubtitles_token(
        &sessions,
        &api_key,
        account
            .as_ref()
            .map(|(username, password)| (username.as_str(), password.as_str())),
    )
    .await?;
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
        .headers(opensubtitles_headers(&api_key, token.as_deref())?)
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    serde_json::from_slice(&bytes).map_err(|_| "字幕搜索服务返回了无法解析的数据。".to_string())
}

#[tauri::command]
pub async fn subtitle_download_opensubtitles(
    app: AppHandle,
    sessions: State<'_, OpenSubtitlesSessionState>,
    request: OpenSubtitlesDownloadRequest,
) -> Result<DownloadedSubtitle, String> {
    let api_key = validate_api_key(&request.api_key)?;
    let account =
        validate_optional_account(request.username.as_deref(), request.password.as_deref())?;
    let token = ensure_opensubtitles_token(
        &sessions,
        &api_key,
        account
            .as_ref()
            .map(|(username, password)| (username.as_str(), password.as_str())),
    )
    .await?;
    if request.file_id == 0 {
        return Err("字幕下载标识无效。".to_string());
    }

    let response = http_client()?
        .post(format!("{OPENSUBTITLES_API_BASE}/download"))
        .headers(opensubtitles_headers(&api_key, token.as_deref())?)
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
    let download_url = validate_opensubtitles_download_url(link)?;
    let content =
        download_with_redirects(download_url, validate_opensubtitles_download_url).await?;
    write_subtitle_cache(
        &app,
        "opensubtitles",
        &request.file_id.to_string(),
        extension,
        content,
    )
}

#[tauri::command]
pub async fn subtitle_search_hash_provider(
    downloads: State<'_, SubtitleDownloadState>,
    request: HashSubtitleSearchRequest,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    let file_path = validate_hashable_video_path(&request.file_path)?;
    let file_name = safe_local_file_name(&file_path)?;
    let language = normalize_language(&request.language)?;

    match request.provider {
        HashSubtitleProvider::Shooter => {
            search_shooter(&downloads, &file_path, &file_name, &language).await
        }
        HashSubtitleProvider::Xunlei => search_xunlei(&downloads, &file_path, &language).await,
    }
}

#[tauri::command]
pub async fn subtitle_download_hash_provider(
    app: AppHandle,
    downloads: State<'_, SubtitleDownloadState>,
    request: HashSubtitleDownloadRequest,
) -> Result<DownloadedSubtitle, String> {
    let pending = resolve_pending_download(&downloads, request.provider, &request.download_ref)?;
    let validator: fn(&str) -> Result<Url, String> = match request.provider {
        HashSubtitleProvider::Shooter => validate_shooter_download_url,
        HashSubtitleProvider::Xunlei => validate_xunlei_download_url,
    };
    let content = download_with_redirects(pending.url, validator).await?;
    if content.starts_with(b"PK\x03\x04") || content.starts_with(b"Rar!") {
        return Err("该字幕结果是压缩包，当前版本暂不自动解压。".to_string());
    }
    write_subtitle_cache(
        &app,
        request.provider.id(),
        &request.download_ref,
        &pending.extension,
        content,
    )
}

async fn ensure_opensubtitles_token(
    sessions: &OpenSubtitlesSessionState,
    api_key: &str,
    account: Option<(&str, &str)>,
) -> Result<Option<String>, String> {
    let Some((username, password)) = account else {
        return Ok(None);
    };
    let fingerprint = credential_fingerprint(api_key, username, password);
    let mut session = sessions.0.lock().await;
    if let Some(existing) = session.as_ref() {
        if existing.credential_fingerprint == fingerprint && existing.expires_at > Instant::now() {
            return Ok(Some(existing.token.clone()));
        }
    }

    let response = http_client()?
        .post(format!("{OPENSUBTITLES_API_BASE}/login"))
        .headers(opensubtitles_headers(api_key, None)?)
        .header(CONTENT_TYPE, "application/json")
        .json(&serde_json::json!({ "username": username, "password": password }))
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    let payload: OpenSubtitlesLoginResponse = serde_json::from_slice(&bytes)
        .map_err(|_| "OpenSubtitles 登录返回了无法解析的数据。".to_string())?;
    let token = payload
        .token
        .filter(|value| !value.trim().is_empty() && value.len() <= 4096)
        .ok_or_else(|| "OpenSubtitles 登录没有返回有效会话。".to_string())?;
    *session = Some(OpenSubtitlesSession {
        credential_fingerprint: fingerprint,
        token: token.clone(),
        expires_at: Instant::now() + OPENSUBTITLES_SESSION_TTL,
    });
    Ok(Some(token))
}

async fn search_shooter(
    downloads: &SubtitleDownloadState,
    file_path: &Path,
    file_name: &str,
    language: &str,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    let query_language = match language {
        "zh-cn" | "zh-tw" => "Chn",
        "en" => "Eng",
        _ => return Ok(Vec::new()),
    };
    let file_hash = compute_shooter_hash(file_path)?;
    let response = http_client()?
        .post(SHOOTER_API_URL)
        .header(USER_AGENT, "OhMyCine Player v0.1")
        .header(ACCEPT, "application/json")
        .form(&[
            ("filehash", file_hash.as_str()),
            ("pathinfo", file_name),
            ("format", "json"),
            ("lang", query_language),
        ])
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    if bytes == [0xff] {
        return Ok(Vec::new());
    }
    let records: Vec<ShooterRecord> =
        serde_json::from_slice(&bytes).map_err(|_| "射手网返回了无法解析的数据。".to_string())?;
    let mut results = Vec::new();
    for (record_index, record) in records.into_iter().enumerate() {
        for file in record.files {
            let Some(extension) = normalized_subtitle_extension(&file.extension) else {
                continue;
            };
            let url = match validate_shooter_download_url(&file.link) {
                Ok(url) => url,
                Err(_) => continue,
            };
            let download_ref = register_pending_download(
                downloads,
                HashSubtitleProvider::Shooter,
                url,
                extension,
            )?;
            let title = if record.description.trim().is_empty() {
                format!("{file_name} · 射手网匹配 {}", record_index + 1)
            } else {
                record.description.trim().chars().take(180).collect()
            };
            results.push(HashSubtitleSearchResult {
                id: format!("shooter:{download_ref}"),
                origin: "local",
                provider_name: HashSubtitleProvider::Shooter.display_name(),
                language: language.to_string(),
                title,
                format: extension.to_string(),
                comments: (record.delay != 0).then(|| format!("时间偏移 {} ms", record.delay)),
                rating: None,
                download_count: None,
                is_hash_match: true,
                download_ref,
            });
            if results.len() >= 30 {
                return Ok(results);
            }
        }
    }
    Ok(results)
}

async fn search_xunlei(
    downloads: &SubtitleDownloadState,
    file_path: &Path,
    language: &str,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    let cid = compute_xunlei_cid(file_path)?;
    let url = format!("{XUNLEI_API_PREFIX}{cid}.json");
    let response = http_client()?
        .get(url)
        .header(USER_AGENT, "OhMyCine Player v0.1")
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
    let text = String::from_utf8_lossy(&bytes);
    let mut payload: XunleiResponse =
        serde_json::from_str(&text).map_err(|_| "迅雷字幕返回了无法解析的数据。".to_string())?;
    payload.sublist.sort_by_key(|record| Reverse(record.svote));

    let mut results = Vec::new();
    for record in payload.sublist {
        if record.scid.trim().is_empty() || !xunlei_language_matches(&record.language, language) {
            continue;
        }
        let Some(extension) = safe_subtitle_extension(Some(&record.sname)) else {
            continue;
        };
        let url = match normalize_xunlei_download_url(&record.surl) {
            Ok(url) => url,
            Err(_) => continue,
        };
        let download_ref =
            register_pending_download(downloads, HashSubtitleProvider::Xunlei, url, extension)?;
        let title = if record.sname.trim().is_empty() {
            format!(
                "迅雷字幕 {}",
                record.scid.chars().take(8).collect::<String>()
            )
        } else {
            record.sname.trim().chars().take(180).collect()
        };
        results.push(HashSubtitleSearchResult {
            id: format!("xunlei:{download_ref}"),
            origin: "local",
            provider_name: HashSubtitleProvider::Xunlei.display_name(),
            language: normalize_xunlei_language(&record.language, language),
            title,
            format: extension.to_string(),
            comments: Some(format!("按本地视频 CID 精确匹配 · {} 票", record.svote)),
            rating: record
                .rate
                .parse::<f64>()
                .ok()
                .filter(|value| value.is_finite()),
            download_count: None,
            is_hash_match: true,
            download_ref,
        });
        if results.len() >= 30 {
            break;
        }
    }
    Ok(results)
}

fn register_pending_download(
    downloads: &SubtitleDownloadState,
    provider: HashSubtitleProvider,
    url: Url,
    extension: &str,
) -> Result<String, String> {
    let mut pending = downloads
        .0
        .lock()
        .map_err(|_| "字幕下载状态暂不可用。".to_string())?;
    let now = Instant::now();
    pending.retain(|_, value| now.duration_since(value.created_at) < DOWNLOAD_REFERENCE_TTL);
    if pending.len() >= MAX_PENDING_DOWNLOADS {
        if let Some(oldest) = pending
            .iter()
            .min_by_key(|(_, value)| value.created_at)
            .map(|(key, _)| key.clone())
        {
            pending.remove(&oldest);
        }
    }
    let mut random = [0_u8; 18];
    rand::thread_rng().fill_bytes(&mut random);
    let download_ref = URL_SAFE_NO_PAD.encode(random);
    pending.insert(
        download_ref.clone(),
        PendingSubtitleDownload {
            provider,
            url,
            extension: extension.to_string(),
            created_at: now,
        },
    );
    Ok(download_ref)
}

fn resolve_pending_download(
    downloads: &SubtitleDownloadState,
    provider: HashSubtitleProvider,
    download_ref: &str,
) -> Result<PendingSubtitleDownload, String> {
    if download_ref.is_empty()
        || download_ref.len() > 128
        || !download_ref
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_')
    {
        return Err("字幕下载引用无效。".to_string());
    }
    let mut pending = downloads
        .0
        .lock()
        .map_err(|_| "字幕下载状态暂不可用。".to_string())?;
    let Some(value) = pending.get(download_ref).cloned() else {
        return Err("字幕结果已经过期，请重新搜索。".to_string());
    };
    if value.provider != provider || value.created_at.elapsed() >= DOWNLOAD_REFERENCE_TTL {
        pending.remove(download_ref);
        return Err("字幕结果已经过期，请重新搜索。".to_string());
    }
    Ok(value)
}

fn validate_hashable_video_path(value: &str) -> Result<PathBuf, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > 4096
        || trimmed.chars().any(|character| character.is_control())
    {
        return Err("本地视频路径无效。".to_string());
    }
    let path = PathBuf::from(trimmed);
    let metadata = fs::metadata(&path).map_err(|_| "无法读取本地视频文件。".to_string())?;
    if !metadata.is_file() || metadata.len() < MIN_HASHABLE_FILE_SIZE {
        return Err("本地视频文件过小或不是普通文件，无法计算字幕匹配哈希。".to_string());
    }
    let extension = path
        .extension()
        .and_then(|value| value.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if !matches!(
        extension.as_str(),
        "3g2"
            | "3gp"
            | "avi"
            | "flv"
            | "m2ts"
            | "m4v"
            | "mkv"
            | "mov"
            | "mp4"
            | "mpeg"
            | "mpg"
            | "mts"
            | "ogm"
            | "ogv"
            | "rmvb"
            | "ts"
            | "webm"
            | "wmv"
    ) {
        return Err("当前文件类型不支持内容哈希字幕搜索。".to_string());
    }
    Ok(path)
}

fn safe_local_file_name(path: &Path) -> Result<String, String> {
    let name = path
        .file_name()
        .and_then(|value| value.to_str())
        .map(str::trim)
        .filter(|value| !value.is_empty() && value.len() <= 255)
        .ok_or_else(|| "本地视频文件名无效。".to_string())?;
    if name.chars().any(|character| character.is_control()) {
        return Err("本地视频文件名无效。".to_string());
    }
    Ok(name.to_string())
}

fn compute_shooter_hash(path: &Path) -> Result<String, String> {
    let mut file = File::open(path).map_err(|_| "无法读取本地视频文件。".to_string())?;
    let size = file
        .metadata()
        .map_err(|_| "无法读取本地视频大小。".to_string())?
        .len();
    let positions = [4096, size.saturating_mul(2) / 3, size / 3, size - 8192];
    let mut hashes = Vec::with_capacity(4);
    for position in positions {
        let mut sample = [0_u8; 4096];
        file.seek(SeekFrom::Start(position))
            .map_err(|_| "无法读取本地视频哈希片段。".to_string())?;
        file.read_exact(&mut sample)
            .map_err(|_| "无法读取本地视频哈希片段。".to_string())?;
        hashes.push(format!("{:x}", md5::compute(sample)));
    }
    Ok(hashes.join(";"))
}

fn compute_xunlei_cid(path: &Path) -> Result<String, String> {
    let mut file = File::open(path).map_err(|_| "无法读取本地视频文件。".to_string())?;
    let size = file
        .metadata()
        .map_err(|_| "无法读取本地视频大小。".to_string())?
        .len();
    let sample_size = 0x5000_u64;
    let positions = [0, size / 3, size - sample_size];
    let mut hasher = Sha1::new();
    for position in positions {
        let mut sample = vec![0_u8; sample_size as usize];
        file.seek(SeekFrom::Start(position))
            .map_err(|_| "无法读取本地视频 CID 片段。".to_string())?;
        file.read_exact(&mut sample)
            .map_err(|_| "无法读取本地视频 CID 片段。".to_string())?;
        hasher.update(sample);
    }
    Ok(format!("{:X}", hasher.finalize()))
}

fn credential_fingerprint(api_key: &str, username: &str, password: &str) -> [u8; 32] {
    let mut hasher = Sha256::new();
    hasher.update(api_key.as_bytes());
    hasher.update([0]);
    hasher.update(username.as_bytes());
    hasher.update([0]);
    hasher.update(password.as_bytes());
    hasher.finalize().into()
}

fn http_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化字幕网络客户端。".to_string())
}

fn opensubtitles_headers(api_key: &str, token: Option<&str>) -> Result<HeaderMap, String> {
    let mut headers = HeaderMap::new();
    headers.insert(ACCEPT, HeaderValue::from_static("application/json"));
    headers.insert(USER_AGENT, HeaderValue::from_static("OhMyCine Player v0.1"));
    headers.insert(
        "api-key",
        HeaderValue::from_str(api_key)
            .map_err(|_| "OpenSubtitles API Key 格式无效。".to_string())?,
    );
    if let Some(token) = token {
        headers.insert(
            AUTHORIZATION,
            HeaderValue::from_str(&format!("Bearer {token}"))
                .map_err(|_| "OpenSubtitles 登录会话格式无效。".to_string())?,
        );
    }
    Ok(headers)
}

async fn download_with_redirects(
    mut url: Url,
    validator: fn(&str) -> Result<Url, String>,
) -> Result<Vec<u8>, String> {
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
            url = validator(
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
        return Err(http_error(status));
    }
    Ok(bytes.to_vec())
}

fn write_subtitle_cache(
    app: &AppHandle,
    provider: &str,
    identity: &str,
    extension: &str,
    content: Vec<u8>,
) -> Result<DownloadedSubtitle, String> {
    if content.is_empty() || content.len() > MAX_DOWNLOAD_RESPONSE_BYTES {
        return Err("字幕文件为空或大小无效。".to_string());
    }
    let extension = normalized_subtitle_extension(extension)
        .ok_or_else(|| "字幕文件扩展名不受支持。".to_string())?;
    let layout = crate::storage::initialize(app)?;
    let subtitle_dir = layout.cache_dir.join("subtitles");
    fs::create_dir_all(&subtitle_dir).map_err(|_| "无法创建字幕缓存目录。".to_string())?;
    let target = subtitle_cache_path(&subtitle_dir, provider, identity, extension);
    fs::write(&target, content).map_err(|_| "无法写入字幕缓存文件。".to_string())?;
    Ok(DownloadedSubtitle {
        path: target.to_string_lossy().to_string(),
    })
}

fn validate_opensubtitles_download_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "字幕下载链接无效。".to_string())?;
    let host = url.host_str().unwrap_or_default().to_ascii_lowercase();
    if url.scheme() != "https"
        || !url.username().is_empty()
        || url.password().is_some()
        || !(host == "opensubtitles.com" || host.ends_with(".opensubtitles.com"))
    {
        return Err("字幕下载链接不在受信任的 OpenSubtitles 域名内。".to_string());
    }
    Ok(url)
}

fn validate_shooter_download_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "射手网字幕下载链接无效。".to_string())?;
    if url.scheme() != "https"
        || url.host_str() != Some("www.shooter.cn")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/api/subapi.php"
        || url.fragment().is_some()
    {
        return Err("射手网字幕下载链接不受信任。".to_string());
    }
    let mut has_fetch = false;
    let mut has_nonce = false;
    for (key, value) in url.query_pairs() {
        if value.is_empty() {
            return Err("射手网字幕下载参数无效。".to_string());
        }
        match key.as_ref() {
            "fetch" => has_fetch = true,
            "nonce" => has_nonce = true,
            _ => return Err("射手网字幕下载参数不受信任。".to_string()),
        }
    }
    if !has_fetch || !has_nonce {
        return Err("射手网字幕下载参数不完整。".to_string());
    }
    Ok(url)
}

fn normalize_xunlei_download_url(value: &str) -> Result<Url, String> {
    let mut url = Url::parse(value).map_err(|_| "迅雷字幕下载链接无效。".to_string())?;
    if url.scheme() == "http" && url.host_str() == Some("subtitle.v.geilijiasu.com") {
        url.set_scheme("https")
            .map_err(|_| "迅雷字幕下载链接无法升级到 HTTPS。".to_string())?;
    }
    validate_xunlei_download_url(url.as_str())
}

fn validate_xunlei_download_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "迅雷字幕下载链接无效。".to_string())?;
    if url.scheme() != "https"
        || url.host_str() != Some("subtitle.v.geilijiasu.com")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || safe_subtitle_extension(Some(url.path())).is_none()
    {
        return Err("迅雷字幕下载链接不受信任。".to_string());
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

fn validate_optional_account<'a>(
    username: Option<&'a str>,
    password: Option<&'a str>,
) -> Result<Option<(String, String)>, String> {
    match (
        username.filter(|value| !value.trim().is_empty()),
        password.filter(|value| !value.is_empty()),
    ) {
        (None, None) => Ok(None),
        (Some(username), Some(password)) => Ok(Some((
            validate_account_field(username, "账号")?,
            validate_account_field(password, "密码")?,
        ))),
        _ => Err("OpenSubtitles 账号和密码必须同时配置。".to_string()),
    }
}

fn validate_account_field(value: &str, label: &str) -> Result<String, String> {
    let normalized = if label == "账号" {
        value.trim()
    } else {
        value
    };
    if normalized.is_empty()
        || normalized.len() > 256
        || normalized.chars().any(|character| character.is_control())
    {
        return Err(format!("OpenSubtitles {label}格式无效。"));
    }
    Ok(normalized.to_string())
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
    normalized_subtitle_extension(&extension)
}

fn normalized_subtitle_extension(value: &str) -> Option<&'static str> {
    match value
        .trim()
        .trim_start_matches('.')
        .to_ascii_lowercase()
        .as_str()
    {
        "srt" => Some("srt"),
        "ass" => Some("ass"),
        "ssa" => Some("ssa"),
        "vtt" => Some("vtt"),
        "sub" => Some("sub"),
        _ => None,
    }
}

fn subtitle_cache_path(
    directory: &Path,
    provider: &str,
    identity: &str,
    extension: &str,
) -> PathBuf {
    let mut hasher = Sha256::new();
    hasher.update(provider.as_bytes());
    hasher.update([0]);
    hasher.update(identity.as_bytes());
    directory.join(format!("{:x}.{extension}", hasher.finalize()))
}

fn xunlei_language_matches(value: &str, requested: &str) -> bool {
    let normalized = value.trim().to_ascii_lowercase();
    match requested {
        "zh-cn" => {
            normalized.contains("简") || normalized.contains("中") || normalized.contains("未知")
        }
        "zh-tw" => {
            normalized.contains("繁") || normalized.contains("中") || normalized.contains("未知")
        }
        "en" => normalized.contains("英") || normalized.contains("english") || normalized == "en",
        "ja" => normalized.contains("日") || normalized.contains("japanese") || normalized == "ja",
        "ko" => {
            normalized.contains("韩")
                || normalized.contains("朝")
                || normalized.contains("korean")
                || normalized == "ko"
        }
        _ => false,
    }
}

fn normalize_xunlei_language(value: &str, fallback: &str) -> String {
    let normalized = value.trim().to_ascii_lowercase();
    if normalized.contains("简") {
        "zh-CN".to_string()
    } else if normalized.contains("繁") {
        "zh-TW".to_string()
    } else if normalized.contains("英") || normalized.contains("english") {
        "en".to_string()
    } else if normalized.contains("日") || normalized.contains("japanese") {
        "ja".to_string()
    } else if normalized.contains("韩") || normalized.contains("korean") {
        "ko".to_string()
    } else {
        fallback.to_string()
    }
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

fn http_error(status: StatusCode) -> String {
    match status {
        StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN => {
            "字幕服务凭据无效或没有权限。".to_string()
        }
        StatusCode::TOO_MANY_REQUESTS => "字幕下载额度或请求频率已达到限制。".to_string(),
        _ => format!("字幕服务返回 HTTP {}。", status.as_u16()),
    }
}

#[cfg(test)]
mod tests {
    use super::{
        compute_shooter_hash, compute_xunlei_cid, normalize_xunlei_download_url,
        safe_subtitle_extension, validate_opensubtitles_download_url,
        validate_shooter_download_url, validate_xunlei_download_url,
    };
    use std::fs;

    #[test]
    fn only_accepts_opensubtitles_https_downloads() {
        assert!(validate_opensubtitles_download_url(
            "https://dl.opensubtitles.com/en/download/file/1"
        )
        .is_ok());
        assert!(validate_opensubtitles_download_url("http://dl.opensubtitles.com/file/1").is_err());
        assert!(validate_opensubtitles_download_url(
            "https://opensubtitles.com.example.test/file/1"
        )
        .is_err());
    }

    #[test]
    fn only_accepts_fixed_hash_provider_download_hosts() {
        assert!(validate_shooter_download_url(
            "https://www.shooter.cn/api/subapi.php?fetch=test&nonce=test"
        )
        .is_ok());
        assert!(
            validate_shooter_download_url("https://shooter.cn/api/subapi.php?fetch=test").is_err()
        );
        assert!(
            normalize_xunlei_download_url("http://subtitle.v.geilijiasu.com/AA/BB/test.srt")
                .is_ok()
        );
        assert!(
            validate_xunlei_download_url("https://subtitle.v.geilijiasu.com/AA/BB/test.ass")
                .is_ok()
        );
        assert!(validate_xunlei_download_url("https://example.test/AA/BB/test.ass").is_err());
    }

    #[test]
    fn only_accepts_known_subtitle_extensions() {
        assert_eq!(
            safe_subtitle_extension(Some("movie.zh-CN.srt")),
            Some("srt")
        );
        assert_eq!(safe_subtitle_extension(Some("movie.exe")), None);
    }

    #[test]
    fn content_hashes_are_stable() {
        let path =
            std::env::temp_dir().join(format!("ohmycine-subtitle-hash-{}.mp4", std::process::id()));
        let content = (0..0x12002)
            .map(|index| (index % 251) as u8)
            .collect::<Vec<_>>();
        fs::write(&path, content).unwrap();
        let shooter = compute_shooter_hash(&path).unwrap();
        let xunlei = compute_xunlei_cid(&path).unwrap();
        fs::remove_file(path).unwrap();
        assert_eq!(
            shooter,
            "c67faf40372a3d42b00e265e0f6b36a9;1dcd314fb09563fd575fe44a2b8d2795;dde61e0ad768e24c52c3c1dedffe1dcd;e346c820a8e2fcb70c7f4eab58f6b8d8"
        );
        assert_eq!(xunlei, "6069A88CF640B2405499F07040361A1C0CD5FCE8");
    }
}

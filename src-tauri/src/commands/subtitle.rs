use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine;
use flate2::read::GzDecoder;
use rand::RngCore;
use reqwest::header::{
    HeaderMap, HeaderName, HeaderValue, ACCEPT, ACCEPT_ENCODING, CONTENT_RANGE, CONTENT_TYPE,
    LOCATION, RANGE, USER_AGENT,
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
const OPENSUBTITLES_XMLRPC_URL: &str = "https://api.opensubtitles.org/xml-rpc";
const OPENSUBTITLES_XMLRPC_USER_AGENT: &str = "OhMyCine v0.1";
const SHOOTER_API_URL: &str = "https://www.shooter.cn/api/subapi.php";
const XUNLEI_NAME_SEARCH_URL: &str = "https://api-shoulei-ssl.xunlei.com/oracle/subtitle";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const XUNLEI_OPTIONAL_CID_TIMEOUT_SECONDS: u64 = 4;
const MAX_SEARCH_RESPONSE_BYTES: usize = 2 * 1024 * 1024;
const MAX_DOWNLOAD_RESPONSE_BYTES: usize = 12 * 1024 * 1024;
const MAX_REDIRECTS: usize = 3;
const MAX_REMOTE_MEDIA_HEADERS: usize = 32;
const DOWNLOAD_REFERENCE_TTL: Duration = Duration::from_secs(30 * 60);
const OPENSUBTITLES_SESSION_TTL: Duration = Duration::from_secs(23 * 60 * 60);
const MAX_PENDING_DOWNLOADS: usize = 256;
const MIN_HASHABLE_FILE_SIZE: u64 = 0xF000;

#[derive(Default)]
pub struct OpenSubtitlesSessionState(tokio::sync::Mutex<Option<OpenSubtitlesSession>>);

struct OpenSubtitlesSession {
    credential_fingerprint: [u8; 32],
    token: String,
    authenticated: bool,
    expires_at: Instant,
}

#[derive(Clone)]
struct OpenSubtitlesXmlRpcSession {
    token: String,
    authenticated: bool,
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
    auth_mode: OpenSubtitlesAuthMode,
    api_key: Option<String>,
    username: Option<String>,
    password: Option<String>,
}

#[derive(Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
enum OpenSubtitlesAuthMode {
    ApiKey,
    Account,
}

#[derive(Clone)]
struct ValidatedOpenSubtitlesCredential {
    auth_mode: OpenSubtitlesAuthMode,
    api_key: Option<String>,
    username: String,
    password: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesSearchRequest {
    auth_mode: OpenSubtitlesAuthMode,
    api_key: Option<String>,
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
    auth_mode: OpenSubtitlesAuthMode,
    api_key: Option<String>,
    username: Option<String>,
    password: Option<String>,
    file_id: u64,
    format: Option<String>,
    cache_owner: Option<SubtitleCacheOwner>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HashSubtitleSearchRequest {
    provider: HashSubtitleProvider,
    query: Option<String>,
    original_title: Option<String>,
    series_name: Option<String>,
    duration_seconds: Option<f64>,
    keyword_mode: Option<String>,
    year: Option<u16>,
    media_type: Option<String>,
    season_number: Option<u16>,
    episode_number: Option<u16>,
    file_path: Option<String>,
    remote_url: Option<String>,
    #[serde(default)]
    headers: Vec<RemoteMediaHeader>,
    file_name: Option<String>,
    language: String,
}

#[derive(Deserialize)]
struct RemoteMediaHeader {
    name: String,
    value: String,
}

enum HashMediaSource {
    Local {
        path: PathBuf,
        file_name: String,
    },
    Remote {
        client: reqwest::Client,
        url: Url,
        headers: HeaderMap,
        file_name: String,
        size: u64,
    },
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HashSubtitleDownloadRequest {
    provider: HashSubtitleProvider,
    download_ref: String,
    cache_owner: Option<SubtitleCacheOwner>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LocalSubtitleImportRequest {
    path: String,
    cache_owner: Option<SubtitleCacheOwner>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SubtitleCacheOwner {
    source_id: String,
    media_identity: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadedSubtitle {
    path: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OpenSubtitlesLoginStatus {
    authenticated: bool,
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
struct XunleiNameSearchResponse {
    #[serde(default)]
    code: i64,
    #[serde(default)]
    data: Vec<XunleiNameSearchRecord>,
}

#[derive(Deserialize, Default)]
struct XunleiNameSearchRecord {
    #[serde(default)]
    cid: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    url: String,
    #[serde(default)]
    ext: String,
    #[serde(default)]
    languages: Vec<String>,
    #[serde(default)]
    score: i64,
    #[serde(default)]
    duration: u64,
}

struct XunleiMatchAssessment {
    score: i64,
    is_hash_match: bool,
    label: &'static str,
}

#[tauri::command]
pub async fn subtitle_login_opensubtitles(
    sessions: State<'_, OpenSubtitlesSessionState>,
    request: OpenSubtitlesLoginRequest,
) -> Result<OpenSubtitlesLoginStatus, String> {
    let credential = validate_opensubtitles_credential(
        request.auth_mode,
        request.api_key.as_deref(),
        request.username.as_deref(),
        request.password.as_deref(),
    )?;
    if credential.auth_mode == OpenSubtitlesAuthMode::Account {
        let session = ensure_opensubtitles_account_token(
            &sessions,
            &credential.username,
            &credential.password,
        )
        .await?;
        return Ok(OpenSubtitlesLoginStatus {
            authenticated: session.authenticated,
        });
    }
    Ok(OpenSubtitlesLoginStatus {
        authenticated: true,
    })
}

#[tauri::command]
pub async fn subtitle_search_opensubtitles(
    sessions: State<'_, OpenSubtitlesSessionState>,
    request: OpenSubtitlesSearchRequest,
) -> Result<Value, String> {
    let credential = validate_opensubtitles_credential(
        request.auth_mode,
        request.api_key.as_deref(),
        request.username.as_deref(),
        request.password.as_deref(),
    )?;
    match credential.auth_mode {
        OpenSubtitlesAuthMode::ApiKey => {
            search_opensubtitles_rest(&request, credential.api_key.as_deref().unwrap_or_default())
                .await
        }
        OpenSubtitlesAuthMode::Account => {
            search_opensubtitles_xmlrpc(
                &sessions,
                &request,
                &credential.username,
                &credential.password,
            )
            .await
        }
    }
}

async fn search_opensubtitles_rest(
    request: &OpenSubtitlesSearchRequest,
    api_key: &str,
) -> Result<Value, String> {
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
        .headers(opensubtitles_headers(api_key)?)
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
    let credential = validate_opensubtitles_credential(
        request.auth_mode,
        request.api_key.as_deref(),
        request.username.as_deref(),
        request.password.as_deref(),
    )?;
    if request.file_id == 0 {
        return Err("字幕下载标识无效。".to_string());
    }
    match credential.auth_mode {
        OpenSubtitlesAuthMode::ApiKey => {
            download_opensubtitles_rest(
                &app,
                &request,
                credential.api_key.as_deref().unwrap_or_default(),
            )
            .await
        }
        OpenSubtitlesAuthMode::Account => {
            download_opensubtitles_xmlrpc(
                &app,
                &sessions,
                &request,
                &credential.username,
                &credential.password,
            )
            .await
        }
    }
}

async fn download_opensubtitles_rest(
    app: &AppHandle,
    request: &OpenSubtitlesDownloadRequest,
    api_key: &str,
) -> Result<DownloadedSubtitle, String> {
    let response = http_client()?
        .post(format!("{OPENSUBTITLES_API_BASE}/download"))
        .headers(opensubtitles_headers(api_key)?)
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
        app,
        "opensubtitles",
        &request.file_id.to_string(),
        extension,
        content,
        request.cache_owner.as_ref(),
    )
}

#[tauri::command]
pub async fn subtitle_search_hash_provider(
    downloads: State<'_, SubtitleDownloadState>,
    request: HashSubtitleSearchRequest,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    let language = normalize_language(&request.language)?;

    match request.provider {
        HashSubtitleProvider::Shooter => {
            let media = resolve_hash_media_source(&request).await?;
            search_shooter(&downloads, &media, &language).await
        }
        HashSubtitleProvider::Xunlei => search_xunlei(&downloads, &request, &language).await,
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
        request.cache_owner.as_ref(),
    )
}

#[tauri::command]
pub fn subtitle_import_local(
    app: AppHandle,
    request: LocalSubtitleImportRequest,
) -> Result<DownloadedSubtitle, String> {
    let source = fs::canonicalize(request.path.trim())
        .map_err(|_| "选择的本地字幕文件不存在。".to_string())?;
    if !source.is_file() {
        return Err("选择的本地字幕路径不是文件。".to_string());
    }
    let extension = source
        .extension()
        .and_then(|value| value.to_str())
        .and_then(normalized_subtitle_extension)
        .ok_or_else(|| "选择的本地字幕格式不受支持。".to_string())?;
    let size = source
        .metadata()
        .map_err(|_| "无法读取本地字幕文件信息。".to_string())?
        .len();
    if size == 0 || size > MAX_DOWNLOAD_RESPONSE_BYTES as u64 {
        return Err("本地字幕文件为空或超过 12 MiB。".to_string());
    }
    let mut content = Vec::with_capacity(size as usize);
    File::open(&source)
        .map_err(|_| "无法读取本地字幕文件。".to_string())?
        .take(MAX_DOWNLOAD_RESPONSE_BYTES as u64 + 1)
        .read_to_end(&mut content)
        .map_err(|_| "无法读取本地字幕文件。".to_string())?;
    write_subtitle_cache(
        &app,
        "manual-local",
        &source.to_string_lossy(),
        extension,
        content,
        request.cache_owner.as_ref(),
    )
}

async fn ensure_opensubtitles_account_token(
    sessions: &OpenSubtitlesSessionState,
    username: &str,
    password: &str,
) -> Result<OpenSubtitlesXmlRpcSession, String> {
    let fingerprint = credential_fingerprint("xmlrpc", username, password);
    let mut session = sessions.0.lock().await;
    if let Some(existing) = session.as_ref() {
        if existing.credential_fingerprint == fingerprint && existing.expires_at > Instant::now() {
            return Ok(OpenSubtitlesXmlRpcSession {
                token: existing.token.clone(),
                authenticated: existing.authenticated,
            });
        }
    }

    let (status, token) = request_opensubtitles_xmlrpc_login(username, password).await?;
    let (token, authenticated) = if status.starts_with("200") {
        (token, true)
    } else if status.starts_with("401") {
        let (anonymous_status, anonymous_token) =
            request_opensubtitles_xmlrpc_login("", "").await?;
        if !anonymous_status.starts_with("200") {
            return Err(format!(
                "OpenSubtitles 免 Key 兼容登录失败：{anonymous_status}"
            ));
        }
        (anonymous_token, false)
    } else {
        return Err(format!("OpenSubtitles 账号登录失败：{status}"));
    };
    let token = token
        .filter(|value| !value.trim().is_empty() && value.len() <= 4096)
        .ok_or_else(|| "OpenSubtitles 登录没有返回有效会话。".to_string())?;
    *session = Some(OpenSubtitlesSession {
        credential_fingerprint: fingerprint,
        token: token.clone(),
        authenticated,
        expires_at: Instant::now() + OPENSUBTITLES_SESSION_TTL,
    });
    Ok(OpenSubtitlesXmlRpcSession {
        token,
        authenticated,
    })
}

async fn request_opensubtitles_xmlrpc_login(
    username: &str,
    password: &str,
) -> Result<(String, Option<String>), String> {
    let body = format!(
        "<?xml version=\"1.0\" encoding=\"utf-8\"?><methodCall><methodName>LogIn</methodName><params><param><value><string>{}</string></value></param><param><value><string>{}</string></value></param><param><value><string>en</string></value></param><param><value><string>{}</string></value></param></params></methodCall>",
        xml_escape(username),
        xml_escape(password),
        OPENSUBTITLES_XMLRPC_USER_AGENT,
    );
    let payload = post_opensubtitles_xmlrpc(&body, MAX_SEARCH_RESPONSE_BYTES).await?;
    let data = xmlrpc_struct(&payload)?;
    let status = xmlrpc_string_member(data, "status")
        .filter(|value| !value.trim().is_empty() && value.len() <= 128)
        .ok_or_else(|| "OpenSubtitles 登录没有返回状态。".to_string())?;
    let token = xmlrpc_string_member(data, "token").map(str::to_string);
    Ok((status.to_string(), token))
}

async fn search_opensubtitles_xmlrpc(
    sessions: &OpenSubtitlesSessionState,
    request: &OpenSubtitlesSearchRequest,
    username: &str,
    password: &str,
) -> Result<Value, String> {
    let session = ensure_opensubtitles_account_token(sessions, username, password).await?;
    let body = build_opensubtitles_xmlrpc_search_body(&session.token, request)?;
    let payload = post_opensubtitles_xmlrpc(&body, MAX_SEARCH_RESPONSE_BYTES).await?;
    let data = xmlrpc_struct(&payload)?;
    ensure_xmlrpc_success(data)?;
    let records = match data.get("data") {
        Some(XmlRpcValue::Array(records)) => records,
        _ => return Ok(serde_json::json!({ "data": [] })),
    };
    let normalized_language = normalize_language(&request.language)?;
    let results: Vec<Value> = records
        .iter()
        .filter_map(|record| legacy_record_to_rest_shape(record, &normalized_language))
        .collect();
    Ok(serde_json::json!({ "data": results }))
}

fn build_opensubtitles_xmlrpc_search_body(
    token: &str,
    request: &OpenSubtitlesSearchRequest,
) -> Result<String, String> {
    let language = normalize_xmlrpc_language(&request.language)?;
    let query = normalized_query(request.query.as_deref())?
        .ok_or_else(|| "请输入用于搜索字幕的媒体名称或关键词。".to_string())?;
    let mut members = vec![
        xmlrpc_string_member_xml("sublanguageid", language),
        xmlrpc_string_member_xml("query", &query),
    ];
    if let Some(imdb_id) = normalize_imdb_id(request.imdb_id.as_deref())? {
        members.push(xmlrpc_string_member_xml("imdbid", &imdb_id));
    }
    if let Some(season) = request.season_number {
        members.push(xmlrpc_string_member_xml("season", &season.to_string()));
    }
    if let Some(episode) = request.episode_number {
        members.push(xmlrpc_string_member_xml("episode", &episode.to_string()));
    }
    Ok(format!(
        "<?xml version=\"1.0\" encoding=\"utf-8\"?><methodCall><methodName>SearchSubtitles</methodName><params><param><value><string>{}</string></value></param><param><value><array><data><value><struct>{}</struct></value></data></array></value></param></params></methodCall>",
        xml_escape(token),
        members.join(""),
    ))
}

async fn download_opensubtitles_xmlrpc(
    app: &AppHandle,
    sessions: &OpenSubtitlesSessionState,
    request: &OpenSubtitlesDownloadRequest,
    username: &str,
    password: &str,
) -> Result<DownloadedSubtitle, String> {
    let session = ensure_opensubtitles_account_token(sessions, username, password).await?;
    let body = format!(
        "<?xml version=\"1.0\" encoding=\"utf-8\"?><methodCall><methodName>DownloadSubtitles</methodName><params><param><value><string>{}</string></value></param><param><value><array><data><value><string>{}</string></value></data></array></value></param></params></methodCall>",
        xml_escape(&session.token),
        request.file_id,
    );
    let payload = post_opensubtitles_xmlrpc(&body, MAX_DOWNLOAD_RESPONSE_BYTES).await?;
    let data = xmlrpc_struct(&payload)?;
    ensure_xmlrpc_success(data)?;
    let encoded = match data.get("data") {
        Some(XmlRpcValue::Array(records)) => records
            .first()
            .and_then(|record| xmlrpc_struct(record).ok())
            .and_then(|record| xmlrpc_string_member(record, "data")),
        _ => None,
    }
    .ok_or_else(|| "OpenSubtitles 下载没有返回字幕内容。".to_string())?;
    let compressed = STANDARD
        .decode(encoded.as_bytes())
        .map_err(|_| "OpenSubtitles 字幕内容编码无效。".to_string())?;
    let mut decoder = GzDecoder::new(compressed.as_slice());
    let mut content = Vec::new();
    decoder
        .by_ref()
        .take((MAX_DOWNLOAD_RESPONSE_BYTES + 1) as u64)
        .read_to_end(&mut content)
        .map_err(|_| "OpenSubtitles 字幕解压失败。".to_string())?;
    if content.len() > MAX_DOWNLOAD_RESPONSE_BYTES {
        return Err("OpenSubtitles 字幕解压后过大，已拒绝处理。".to_string());
    }
    let extension = request
        .format
        .as_deref()
        .and_then(normalized_subtitle_extension)
        .unwrap_or("srt");
    write_subtitle_cache(
        app,
        "opensubtitles-account",
        &request.file_id.to_string(),
        extension,
        content,
        request.cache_owner.as_ref(),
    )
}

#[derive(Debug)]
enum XmlRpcValue {
    String(String),
    Struct(HashMap<String, XmlRpcValue>),
    Array(Vec<XmlRpcValue>),
}

async fn post_opensubtitles_xmlrpc(body: &str, max_bytes: usize) -> Result<XmlRpcValue, String> {
    let response = http_client()?
        .post(OPENSUBTITLES_XMLRPC_URL)
        .header(CONTENT_TYPE, "text/xml; charset=utf-8")
        .header(ACCEPT, "text/xml")
        .header(USER_AGENT, OPENSUBTITLES_XMLRPC_USER_AGENT)
        .body(body.to_string())
        .send()
        .await
        .map_err(network_error)?;
    let bytes = read_limited_response(response, max_bytes).await?;
    parse_xmlrpc_response(&bytes)
}

fn parse_xmlrpc_response(bytes: &[u8]) -> Result<XmlRpcValue, String> {
    let xml = std::str::from_utf8(bytes)
        .map_err(|_| "OpenSubtitles XML-RPC 返回了无效文本。".to_string())?;
    if xml.contains("<!DOCTYPE") || xml.contains("<!ENTITY") {
        return Err("OpenSubtitles XML-RPC 返回了不受支持的文档声明。".to_string());
    }
    let document = roxmltree::Document::parse(xml)
        .map_err(|_| "OpenSubtitles XML-RPC 返回了无法解析的数据。".to_string())?;
    if let Some(fault) = document
        .descendants()
        .find(|node| node.is_element() && node.tag_name().name() == "fault")
    {
        let message = fault
            .descendants()
            .find(|node| node.is_element() && node.tag_name().name() == "string")
            .and_then(|node| node.text())
            .unwrap_or("OpenSubtitles XML-RPC 请求失败。");
        return Err(message.to_string());
    }
    let value = document
        .descendants()
        .find(|node| {
            node.is_element()
                && node.tag_name().name() == "param"
                && node
                    .ancestors()
                    .any(|ancestor| ancestor.is_element() && ancestor.tag_name().name() == "params")
        })
        .and_then(|param| {
            param
                .children()
                .find(|node| node.is_element() && node.tag_name().name() == "value")
        })
        .ok_or_else(|| "OpenSubtitles XML-RPC 没有返回有效结果。".to_string())?;
    parse_xmlrpc_value(value)
}

fn parse_xmlrpc_value(node: roxmltree::Node<'_, '_>) -> Result<XmlRpcValue, String> {
    let Some(kind) = node.children().find(|child| child.is_element()) else {
        return Ok(XmlRpcValue::String(
            node.text().unwrap_or_default().to_string(),
        ));
    };
    match kind.tag_name().name() {
        "struct" => {
            let mut fields = HashMap::new();
            for member in kind
                .children()
                .filter(|child| child.is_element() && child.tag_name().name() == "member")
            {
                let name = member
                    .children()
                    .find(|child| child.is_element() && child.tag_name().name() == "name")
                    .and_then(|child| child.text())
                    .ok_or_else(|| "OpenSubtitles XML-RPC 字段缺少名称。".to_string())?;
                let value = member
                    .children()
                    .find(|child| child.is_element() && child.tag_name().name() == "value")
                    .ok_or_else(|| "OpenSubtitles XML-RPC 字段缺少值。".to_string())?;
                fields.insert(name.to_string(), parse_xmlrpc_value(value)?);
            }
            Ok(XmlRpcValue::Struct(fields))
        }
        "array" => {
            let values = kind
                .children()
                .find(|child| child.is_element() && child.tag_name().name() == "data")
                .into_iter()
                .flat_map(|data| data.children())
                .filter(|child| child.is_element() && child.tag_name().name() == "value")
                .map(parse_xmlrpc_value)
                .collect::<Result<Vec<_>, _>>()?;
            Ok(XmlRpcValue::Array(values))
        }
        "string" | "int" | "i4" | "i8" | "double" | "boolean" | "base64" | "dateTime.iso8601" => {
            Ok(XmlRpcValue::String(
                kind.text().unwrap_or_default().to_string(),
            ))
        }
        _ => Ok(XmlRpcValue::String(
            kind.text().unwrap_or_default().to_string(),
        )),
    }
}

fn xmlrpc_struct(value: &XmlRpcValue) -> Result<&HashMap<String, XmlRpcValue>, String> {
    match value {
        XmlRpcValue::Struct(fields) => Ok(fields),
        _ => Err("OpenSubtitles XML-RPC 返回结构无效。".to_string()),
    }
}

fn xmlrpc_string_member<'a>(
    fields: &'a HashMap<String, XmlRpcValue>,
    name: &str,
) -> Option<&'a str> {
    match fields.get(name) {
        Some(XmlRpcValue::String(value)) => Some(value),
        _ => None,
    }
}

fn ensure_xmlrpc_success(fields: &HashMap<String, XmlRpcValue>) -> Result<(), String> {
    let status = xmlrpc_string_member(fields, "status").unwrap_or_default();
    if status.starts_with("200") {
        Ok(())
    } else {
        Err(if status.is_empty() {
            "OpenSubtitles XML-RPC 请求失败。".to_string()
        } else {
            format!("OpenSubtitles XML-RPC 请求失败：{status}")
        })
    }
}

fn legacy_record_to_rest_shape(record: &XmlRpcValue, language: &str) -> Option<Value> {
    let fields = xmlrpc_struct(record).ok()?;
    let file_id = xmlrpc_string_member(fields, "IDSubtitleFile")?
        .parse::<u64>()
        .ok()?;
    if file_id == 0 {
        return None;
    }
    let file_name = xmlrpc_string_member(fields, "SubFileName").unwrap_or_default();
    let release = xmlrpc_string_member(fields, "MovieReleaseName")
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(file_name);
    let rating =
        xmlrpc_string_member(fields, "SubRating").and_then(|value| value.parse::<f64>().ok());
    let download_count =
        xmlrpc_string_member(fields, "SubDownloadsCnt").and_then(|value| value.parse::<u64>().ok());
    let hearing_impaired = xmlrpc_string_member(fields, "SubHearingImpaired") == Some("1");
    Some(serde_json::json!({
        "id": format!("xmlrpc-{file_id}"),
        "attributes": {
            "language": language,
            "download_count": download_count,
            "hearing_impaired": hearing_impaired,
            "ratings": rating,
            "release": release,
            "comments": xmlrpc_string_member(fields, "SubAuthorComment"),
            "uploader": { "name": xmlrpc_string_member(fields, "UserNickName") },
            "files": [{ "file_id": file_id, "file_name": file_name }],
        }
    }))
}

fn normalize_xmlrpc_language(value: &str) -> Result<&'static str, String> {
    match normalize_language(value)?.as_str() {
        "zh-cn" => Ok("chi"),
        "zh-tw" => Ok("zht"),
        "en" => Ok("eng"),
        "ja" => Ok("jpn"),
        "ko" => Ok("kor"),
        _ => Err("OpenSubtitles 账号模式暂不支持该字幕语言。".to_string()),
    }
}

fn xmlrpc_string_member_xml(name: &str, value: &str) -> String {
    format!(
        "<member><name>{}</name><value><string>{}</string></value></member>",
        xml_escape(name),
        xml_escape(value),
    )
}

fn xml_escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

async fn search_shooter(
    downloads: &SubtitleDownloadState,
    media: &HashMediaSource,
    language: &str,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    let query_language = match language {
        "zh-cn" | "zh-tw" => "Chn",
        "en" => "Eng",
        _ => return Ok(Vec::new()),
    };
    let file_hash = compute_shooter_media_hash(media).await?;
    let file_name = hash_media_file_name(media);
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
    request: &HashSubtitleSearchRequest,
    language: &str,
) -> Result<Vec<HashSubtitleSearchResult>, String> {
    if !matches!(language, "zh-cn" | "zh-tw") {
        return Ok(Vec::new());
    }
    let query = normalized_query(request.query.as_deref())?
        .or_else(|| {
            request
                .file_name
                .as_deref()
                .and_then(validate_remote_media_file_name)
        })
        .ok_or_else(|| "请输入用于迅雷字幕搜索的媒体名称或关键词。".to_string())?;
    let url = build_xunlei_name_search_url(&query)?;
    let search = async {
        let response = http_client()?
            .get(url)
            .header(USER_AGENT, "OhMyCine Player v0.1")
            .header(ACCEPT, "application/json")
            .send()
            .await
            .map_err(network_error)?;
        let bytes = read_limited_response(response, MAX_SEARCH_RESPONSE_BYTES).await?;
        let payload: XunleiNameSearchResponse = serde_json::from_slice(&bytes)
            .map_err(|_| "迅雷字幕返回了无法解析的数据。".to_string())?;
        if payload.code != 0 {
            return Err("迅雷字幕搜索服务返回了失败状态。".to_string());
        }
        Ok::<_, String>(payload.data)
    };
    let (records, cid) = tokio::join!(search, optional_xunlei_media_cid(request));
    let records = records?;
    let normalized_cid = cid
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty());
    let mut ranked_records = records
        .into_iter()
        .filter_map(|record| {
            assess_xunlei_match(&record, request, normalized_cid)
                .map(|assessment| (record, assessment))
        })
        .collect::<Vec<_>>();
    ranked_records.sort_by_key(|(_, assessment)| Reverse(assessment.score));
    let mut results = Vec::new();
    for (record, assessment) in ranked_records {
        let title = record.name.trim();
        if title.is_empty() {
            continue;
        }
        let Some(extension) = normalized_subtitle_extension(&record.ext)
            .or_else(|| safe_subtitle_extension(Some(title)))
        else {
            continue;
        };
        let url = match normalize_xunlei_download_url(&record.url) {
            Ok(url) => url,
            Err(_) => continue,
        };
        let download_ref =
            register_pending_download(downloads, HashSubtitleProvider::Xunlei, url, extension)?;
        results.push(HashSubtitleSearchResult {
            id: format!("xunlei:{download_ref}"),
            origin: "local",
            provider_name: HashSubtitleProvider::Xunlei.display_name(),
            language: normalize_xunlei_languages(&record.languages, language),
            title: title.chars().take(180).collect(),
            format: extension.to_string(),
            comments: Some(format!("迅雷名称搜索 · {}", assessment.label)),
            rating: (record.score > 0).then_some(record.score as f64),
            download_count: None,
            is_hash_match: assessment.is_hash_match,
            download_ref,
        });
        if results.len() >= 50 {
            break;
        }
    }
    Ok(results)
}

fn assess_xunlei_match(
    record: &XunleiNameSearchRecord,
    request: &HashSubtitleSearchRequest,
    cid: Option<&str>,
) -> Option<XunleiMatchAssessment> {
    let candidate = record.name.trim();
    if candidate.is_empty() {
        return None;
    }
    let is_hash_match = cid.is_some_and(|expected| {
        !record.cid.trim().is_empty() && record.cid.trim().eq_ignore_ascii_case(expected)
    });
    if is_hash_match {
        return Some(XunleiMatchAssessment {
            score: 20_000 + record.score.max(0),
            is_hash_match: true,
            label: "CID 精确匹配",
        });
    }
    if request.keyword_mode.as_deref() == Some("custom") {
        return Some(XunleiMatchAssessment {
            score: record.score.max(0),
            is_hash_match: false,
            label: "自定义关键词匹配",
        });
    }

    let media_type = normalize_media_type(request.media_type.as_deref());
    let episode_marker = parse_episode_marker(candidate);
    if media_type == Some("movie") && looks_like_tv_episode(candidate, episode_marker) {
        return None;
    }
    if media_type == Some("episode") {
        if let (Some(expected_season), Some(expected_episode), Some((season, episode))) = (
            request.season_number,
            request.episode_number,
            episode_marker,
        ) {
            if season != expected_season || episode != expected_episode {
                return None;
            }
        }
    }

    let mut score = record.score.max(0);
    let candidate_years = extract_years(candidate);
    let year_matches = request
        .year
        .is_some_and(|year| candidate_years.contains(&year));
    if request.year.is_some() {
        if year_matches {
            score += 1_200;
        } else if !candidate_years.is_empty() {
            return None;
        }
    }

    let title_score = xunlei_title_match_score(candidate, request);
    score += title_score;
    let duration_score = xunlei_duration_match_score(record.duration, request.duration_seconds);
    if duration_score < 0 {
        return None;
    }
    score += duration_score;

    let episode_matches = media_type == Some("episode")
        && request.season_number.is_some()
        && request.episode_number.is_some()
        && episode_marker == request.season_number.zip(request.episode_number);
    if episode_matches {
        score += 1_400;
    }
    if media_type == Some("movie") {
        score += 200;
        if request.year.is_some() && !year_matches && duration_score < 350 {
            return None;
        }
    } else if media_type == Some("episode")
        && request.season_number.is_some()
        && request.episode_number.is_some()
        && !episode_matches
        && duration_score < 350
    {
        return None;
    }

    let label = if score >= 1_400 {
        "高置信度匹配"
    } else if score >= 700 {
        "较高置信度匹配"
    } else {
        "模糊匹配"
    };
    Some(XunleiMatchAssessment {
        score,
        is_hash_match: false,
        label,
    })
}

fn xunlei_title_match_score(candidate: &str, request: &HashSubtitleSearchRequest) -> i64 {
    let candidate_key = compact_match_key(candidate);
    let aliases = [
        request.query.as_deref(),
        request.original_title.as_deref(),
        request.series_name.as_deref(),
    ];
    let alias_match = aliases
        .into_iter()
        .flatten()
        .map(compact_match_key)
        .filter(|value| value.chars().count() >= 3)
        .map(|value| {
            if candidate_key == value {
                700
            } else if candidate_key.contains(&value) || value.contains(&candidate_key) {
                450
            } else {
                0
            }
        })
        .max()
        .unwrap_or_default();
    let file_token_score = request
        .file_name
        .as_deref()
        .map(|file_name| shared_match_token_count(candidate, file_name).min(6) as i64 * 55)
        .unwrap_or_default();
    alias_match + file_token_score
}

fn xunlei_duration_match_score(record_duration_ms: u64, expected_seconds: Option<f64>) -> i64 {
    let Some(expected) = expected_seconds.filter(|value| value.is_finite() && *value > 0.0) else {
        return 0;
    };
    if record_duration_ms == 0 {
        return 0;
    }
    let actual = record_duration_ms as f64 / 1000.0;
    let difference = (actual - expected).abs() / expected;
    if difference <= 0.01 {
        900
    } else if difference <= 0.03 {
        700
    } else if difference <= 0.08 {
        400
    } else if difference <= 0.15 {
        150
    } else if difference > 0.30 {
        -1
    } else {
        0
    }
}

fn compact_match_key(value: &str) -> String {
    value
        .chars()
        .flat_map(char::to_lowercase)
        .filter(|character| character.is_alphanumeric())
        .collect()
}

fn shared_match_token_count(left: &str, right: &str) -> usize {
    let left_tokens = match_tokens(left);
    let right_tokens = match_tokens(right);
    left_tokens
        .iter()
        .filter(|token| right_tokens.contains(token))
        .count()
}

fn match_tokens(value: &str) -> Vec<String> {
    value
        .split(|character: char| !character.is_alphanumeric())
        .map(str::trim)
        .filter(|token| token.chars().count() >= 2)
        .map(str::to_lowercase)
        .filter(|token| {
            !matches!(
                token.as_str(),
                "srt" | "ass" | "ssa" | "vtt" | "mkv" | "mp4"
            )
        })
        .collect()
}

fn extract_years(value: &str) -> Vec<u16> {
    value
        .split(|character: char| !character.is_ascii_digit())
        .filter(|token| token.len() == 4)
        .filter_map(|token| token.parse::<u16>().ok())
        .filter(|year| (1900..=2099).contains(year))
        .collect()
}

fn parse_episode_marker(value: &str) -> Option<(u16, u16)> {
    let normalized = value.to_ascii_lowercase();
    parse_episode_pattern(normalized.as_bytes(), b's', b'e')
        .or_else(|| parse_episode_pattern(normalized.as_bytes(), 0, b'x'))
}

fn looks_like_tv_episode(value: &str, marker: Option<(u16, u16)>) -> bool {
    if marker.is_some() {
        return true;
    }
    let normalized = value.to_ascii_lowercase();
    if normalized.contains("season") || normalized.contains("episode") {
        return true;
    }
    let Some(start) = value.find('第') else {
        return false;
    };
    let Some(end_offset) = value[start..].find('集') else {
        return false;
    };
    value[start..start + end_offset]
        .chars()
        .any(|character| character.is_ascii_digit())
}

fn parse_episode_pattern(
    bytes: &[u8],
    season_prefix: u8,
    episode_prefix: u8,
) -> Option<(u16, u16)> {
    for index in 0..bytes.len() {
        let season_start = if season_prefix == 0 {
            index
        } else if bytes[index] == season_prefix {
            index + 1
        } else {
            continue;
        };
        let Some((season, separator_index)) = parse_ascii_number(bytes, season_start, 2) else {
            continue;
        };
        if bytes.get(separator_index).copied() != Some(episode_prefix) {
            continue;
        }
        let Some((episode, _)) = parse_ascii_number(bytes, separator_index + 1, 3) else {
            continue;
        };
        return Some((season, episode));
    }
    None
}

fn parse_ascii_number(bytes: &[u8], start: usize, max_digits: usize) -> Option<(u16, usize)> {
    let mut end = start;
    while end < bytes.len() && end - start < max_digits && bytes[end].is_ascii_digit() {
        end += 1;
    }
    if end == start {
        return None;
    }
    std::str::from_utf8(&bytes[start..end])
        .ok()?
        .parse::<u16>()
        .ok()
        .map(|value| (value, end))
}

fn build_xunlei_name_search_url(query: &str) -> Result<Url, String> {
    let mut url =
        Url::parse(XUNLEI_NAME_SEARCH_URL).map_err(|_| "迅雷字幕搜索服务地址无效。".to_string())?;
    url.query_pairs_mut().append_pair("name", query);
    Ok(url)
}

async fn optional_xunlei_media_cid(request: &HashSubtitleSearchRequest) -> Option<String> {
    tokio::time::timeout(
        Duration::from_secs(XUNLEI_OPTIONAL_CID_TIMEOUT_SECONDS),
        async {
            let media = resolve_hash_media_source(request).await.ok()?;
            compute_xunlei_media_cid(&media).await.ok()
        },
    )
    .await
    .ok()
    .flatten()
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

async fn resolve_hash_media_source(
    request: &HashSubtitleSearchRequest,
) -> Result<HashMediaSource, String> {
    if let Some(path) = request
        .file_path
        .as_deref()
        .filter(|value| !value.trim().is_empty())
    {
        let path = validate_hashable_video_path(path)?;
        let file_name = safe_local_file_name(&path)?;
        return Ok(HashMediaSource::Local { path, file_name });
    }

    let url = validate_remote_media_url(request.remote_url.as_deref().unwrap_or_default())?;
    let headers = validate_remote_media_headers(&request.headers)?;
    let file_name = request
        .file_name
        .as_deref()
        .and_then(validate_remote_media_file_name)
        .or_else(|| remote_media_file_name(&url))
        .unwrap_or_else(|| "remote-media".to_string());
    let client = http_client()?;
    let (url, headers, size) = probe_remote_media(&client, url, headers).await?;
    if size < MIN_HASHABLE_FILE_SIZE {
        return Err("远程视频过小，无法计算字幕匹配哈希。".to_string());
    }
    Ok(HashMediaSource::Remote {
        client,
        url,
        headers,
        file_name,
        size,
    })
}

fn validate_remote_media_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value.trim()).map_err(|_| "远程视频播放地址无效。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.host_str().is_none()
        || url.fragment().is_some()
    {
        return Err("远程视频播放地址不受支持。".to_string());
    }
    Ok(url)
}

fn validate_remote_media_headers(values: &[RemoteMediaHeader]) -> Result<HeaderMap, String> {
    if values.len() > MAX_REMOTE_MEDIA_HEADERS {
        return Err("远程视频播放 Header 数量过多。".to_string());
    }
    let mut headers = HeaderMap::new();
    let mut total_size = 0_usize;
    for header in values {
        total_size = total_size
            .saturating_add(header.name.len())
            .saturating_add(header.value.len());
        if total_size > 32 * 1024 {
            return Err("远程视频播放 Header 过大。".to_string());
        }
        let name = HeaderName::from_bytes(header.name.trim().as_bytes())
            .map_err(|_| "远程视频播放 Header 名称无效。".to_string())?;
        if matches!(
            name.as_str(),
            "range"
                | "host"
                | "content-length"
                | "connection"
                | "transfer-encoding"
                | "accept-encoding"
        ) {
            return Err("远程视频播放 Header 包含受限字段。".to_string());
        }
        let value = HeaderValue::from_str(&header.value)
            .map_err(|_| "远程视频播放 Header 值无效。".to_string())?;
        headers.insert(name, value);
    }
    Ok(headers)
}

fn validate_remote_media_file_name(value: &str) -> Option<String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > 255
        || value.contains(['/', '\\'])
        || value.chars().any(|character| character.is_control())
    {
        return None;
    }
    Some(value.to_string())
}

fn remote_media_file_name(url: &Url) -> Option<String> {
    url.path_segments()?
        .rfind(|segment| !segment.is_empty())
        .and_then(validate_remote_media_file_name)
}

async fn probe_remote_media(
    client: &reqwest::Client,
    url: Url,
    headers: HeaderMap,
) -> Result<(Url, HeaderMap, u64), String> {
    let (response, final_url, final_headers) =
        request_remote_media_range(client, url, headers, 0, 0).await?;
    if !response.status().is_success() {
        return Err(http_error(response.status()));
    }
    if response.status() != StatusCode::PARTIAL_CONTENT {
        return Err("当前远程媒体源不支持 Range 读取，无法计算字幕匹配哈希。".to_string());
    }
    let content_range = response
        .headers()
        .get(CONTENT_RANGE)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| "远程媒体源没有返回文件大小。".to_string())?;
    let (start, end, total) = parse_content_range(content_range)?;
    if start != 0 || end != 0 {
        return Err("远程媒体源返回了错误的 Range 范围。".to_string());
    }
    Ok((final_url, final_headers, total))
}

async fn read_remote_media_range(
    client: &reqwest::Client,
    url: &Url,
    headers: &HeaderMap,
    start: u64,
    length: usize,
    expected_total: u64,
) -> Result<Vec<u8>, String> {
    let end = start
        .checked_add(length.saturating_sub(1) as u64)
        .ok_or_else(|| "远程媒体 Range 范围无效。".to_string())?;
    let (response, _, _) =
        request_remote_media_range(client, url.clone(), headers.clone(), start, end).await?;
    if !response.status().is_success() {
        return Err(http_error(response.status()));
    }
    if response.status() != StatusCode::PARTIAL_CONTENT {
        return Err("当前远程媒体源未按 Range 返回视频片段。".to_string());
    }
    let content_range = response
        .headers()
        .get(CONTENT_RANGE)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| "远程媒体片段缺少 Content-Range。".to_string())?;
    let (actual_start, actual_end, total) = parse_content_range(content_range)?;
    if actual_start != start || actual_end != end || total != expected_total {
        return Err("远程媒体源返回了不一致的视频片段。".to_string());
    }
    let bytes = read_limited_response(response, length).await?;
    if bytes.len() != length {
        return Err("远程媒体源返回的视频片段长度不足。".to_string());
    }
    Ok(bytes)
}

async fn request_remote_media_range(
    client: &reqwest::Client,
    mut url: Url,
    mut headers: HeaderMap,
    start: u64,
    end: u64,
) -> Result<(reqwest::Response, Url, HeaderMap), String> {
    for _ in 0..=MAX_REDIRECTS {
        let mut request_headers = headers.clone();
        request_headers.insert(
            RANGE,
            HeaderValue::from_str(&format!("bytes={start}-{end}"))
                .map_err(|_| "远程媒体 Range 格式无效。".to_string())?,
        );
        request_headers.insert(ACCEPT_ENCODING, HeaderValue::from_static("identity"));
        let response = client
            .get(url.clone())
            .headers(request_headers)
            .send()
            .await
            .map_err(network_error)?;
        if response.status().is_redirection() {
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| "远程媒体重定向缺少目标地址。".to_string())?;
            let next = validate_remote_media_url(
                url.join(location)
                    .map_err(|_| "远程媒体重定向地址无效。".to_string())?
                    .as_str(),
            )?;
            if url.scheme() == "https" && next.scheme() != "https" {
                return Err("远程媒体拒绝从 HTTPS 降级到 HTTP。".to_string());
            }
            if !same_url_origin(&url, &next) {
                headers.clear();
            }
            url = next;
            continue;
        }
        return Ok((response, url, headers));
    }
    Err("远程媒体重定向次数过多。".to_string())
}

fn same_url_origin(left: &Url, right: &Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
}

fn parse_content_range(value: &str) -> Result<(u64, u64, u64), String> {
    let value = value
        .trim()
        .strip_prefix("bytes ")
        .ok_or_else(|| "远程媒体 Content-Range 格式无效。".to_string())?;
    let (range, total) = value
        .split_once('/')
        .ok_or_else(|| "远程媒体 Content-Range 格式无效。".to_string())?;
    let (start, end) = range
        .split_once('-')
        .ok_or_else(|| "远程媒体 Content-Range 格式无效。".to_string())?;
    let start = start
        .parse::<u64>()
        .map_err(|_| "远程媒体 Content-Range 起点无效。".to_string())?;
    let end = end
        .parse::<u64>()
        .map_err(|_| "远程媒体 Content-Range 终点无效。".to_string())?;
    let total = total
        .parse::<u64>()
        .map_err(|_| "远程媒体 Content-Range 总大小无效。".to_string())?;
    if start > end || end >= total {
        return Err("远程媒体 Content-Range 数值无效。".to_string());
    }
    Ok((start, end, total))
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

fn hash_media_file_name(media: &HashMediaSource) -> &str {
    match media {
        HashMediaSource::Local { file_name, .. } | HashMediaSource::Remote { file_name, .. } => {
            file_name
        }
    }
}

async fn compute_shooter_media_hash(media: &HashMediaSource) -> Result<String, String> {
    match media {
        HashMediaSource::Local { path, .. } => compute_shooter_hash(path),
        HashMediaSource::Remote {
            client,
            url,
            headers,
            size,
            ..
        } => {
            let positions = [4096, size.saturating_mul(2) / 3, size / 3, size - 8192];
            let mut hashes = Vec::with_capacity(4);
            for position in positions {
                let sample =
                    read_remote_media_range(client, url, headers, position, 4096, *size).await?;
                hashes.push(format!("{:x}", md5::compute(sample)));
            }
            Ok(hashes.join(";"))
        }
    }
}

async fn compute_xunlei_media_cid(media: &HashMediaSource) -> Result<String, String> {
    match media {
        HashMediaSource::Local { path, .. } => compute_xunlei_cid(path),
        HashMediaSource::Remote {
            client,
            url,
            headers,
            size,
            ..
        } => {
            let sample_size = 0x5000_usize;
            let positions = [0, size / 3, size - sample_size as u64];
            let mut hasher = Sha1::new();
            for position in positions {
                let sample =
                    read_remote_media_range(client, url, headers, position, sample_size, *size)
                        .await?;
                hasher.update(sample);
            }
            Ok(format!("{:X}", hasher.finalize()))
        }
    }
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
    mut response: reqwest::Response,
    max_bytes: usize,
) -> Result<Vec<u8>, String> {
    let status = response.status();
    if response
        .content_length()
        .is_some_and(|length| length > max_bytes as u64)
    {
        return Err("字幕服务响应过大，已拒绝处理。".to_string());
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(network_error)? {
        if bytes.len().saturating_add(chunk.len()) > max_bytes {
            return Err("字幕服务响应过大，已拒绝处理。".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    if !status.is_success() {
        return Err(http_error(status));
    }
    Ok(bytes)
}

fn write_subtitle_cache(
    app: &AppHandle,
    provider: &str,
    identity: &str,
    extension: &str,
    content: Vec<u8>,
    cache_owner: Option<&SubtitleCacheOwner>,
) -> Result<DownloadedSubtitle, String> {
    if content.is_empty() || content.len() > MAX_DOWNLOAD_RESPONSE_BYTES {
        return Err("字幕文件为空或大小无效。".to_string());
    }
    let extension = normalized_subtitle_extension(extension)
        .ok_or_else(|| "字幕文件扩展名不受支持。".to_string())?;
    let layout = crate::storage::initialize(app)?;
    let subtitle_dir = subtitle_cache_owner_directory(&layout.cache_dir, cache_owner)?;
    fs::create_dir_all(&subtitle_dir).map_err(|_| "无法创建字幕缓存目录。".to_string())?;
    let target = subtitle_cache_path(&subtitle_dir, provider, identity, extension);
    fs::write(&target, content).map_err(|_| "无法写入字幕缓存文件。".to_string())?;
    Ok(DownloadedSubtitle {
        path: target.to_string_lossy().to_string(),
    })
}

fn subtitle_cache_owner_directory(
    cache_dir: &Path,
    cache_owner: Option<&SubtitleCacheOwner>,
) -> Result<PathBuf, String> {
    let mut subtitle_dir = cache_dir.join("subtitles");
    if let Some(owner) = cache_owner {
        validate_subtitle_cache_identity(&owner.source_id, 512)?;
        validate_subtitle_cache_identity(&owner.media_identity, 2048)?;
        subtitle_dir = subtitle_dir
            .join(crate::storage::scoped_cache_key(
                "subtitle-source",
                owner.source_id.trim(),
            ))
            .join(crate::storage::scoped_cache_key(
                "subtitle-media",
                owner.media_identity.trim(),
            ));
    }
    Ok(subtitle_dir)
}

fn validate_subtitle_cache_identity(value: &str, max_length: usize) -> Result<(), String> {
    let value = value.trim();
    if value.is_empty() || value.len() > max_length || value.chars().any(char::is_control) {
        return Err("字幕缓存归属信息无效。".to_string());
    }
    Ok(())
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

fn validate_opensubtitles_credential(
    auth_mode: OpenSubtitlesAuthMode,
    api_key: Option<&str>,
    username: Option<&str>,
    password: Option<&str>,
) -> Result<ValidatedOpenSubtitlesCredential, String> {
    match auth_mode {
        OpenSubtitlesAuthMode::ApiKey => Ok(ValidatedOpenSubtitlesCredential {
            auth_mode,
            api_key: Some(validate_api_key(api_key.unwrap_or_default())?),
            username: String::new(),
            password: String::new(),
        }),
        OpenSubtitlesAuthMode::Account => Ok(ValidatedOpenSubtitlesCredential {
            auth_mode,
            api_key: None,
            username: validate_account_field(username.unwrap_or_default(), "账号")?,
            password: validate_account_field(password.unwrap_or_default(), "密码")?,
        }),
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

fn normalize_xunlei_languages(values: &[String], fallback: &str) -> String {
    let normalized = values.join(" ").trim().to_ascii_lowercase();
    if normalized.contains("繁") && fallback == "zh-tw" {
        "zh-TW".to_string()
    } else if normalized.contains("简") {
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
        assess_xunlei_match, build_opensubtitles_xmlrpc_search_body, build_xunlei_name_search_url,
        compute_shooter_hash, compute_shooter_media_hash, compute_xunlei_cid,
        compute_xunlei_media_cid, normalize_xunlei_download_url, normalize_xunlei_languages,
        parse_episode_marker, parse_xmlrpc_response, resolve_hash_media_source,
        safe_subtitle_extension, subtitle_cache_owner_directory,
        validate_opensubtitles_download_url, validate_shooter_download_url,
        validate_xunlei_download_url, xmlrpc_string_member, xmlrpc_struct, HashSubtitleProvider,
        HashSubtitleSearchRequest, OpenSubtitlesAuthMode, OpenSubtitlesSearchRequest,
        RemoteMediaHeader, SubtitleCacheOwner, XunleiNameSearchRecord,
    };
    use std::fs;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    #[test]
    fn subtitle_cache_owner_uses_hashed_source_and_media_directories() {
        let root = std::path::PathBuf::from("cache-root");
        let owner = SubtitleCacheOwner {
            source_id: "emby-private".to_string(),
            media_identity: "episode-secret".to_string(),
        };
        let directory = subtitle_cache_owner_directory(&root, Some(&owner))
            .expect("build scoped subtitle cache directory");
        let display = directory.to_string_lossy();

        assert!(directory.starts_with(root.join("subtitles")));
        assert!(!display.contains("emby-private"));
        assert!(!display.contains("episode-secret"));
    }

    fn xunlei_request(media_type: Option<&str>) -> HashSubtitleSearchRequest {
        HashSubtitleSearchRequest {
            provider: HashSubtitleProvider::Xunlei,
            query: Some("超级少女".to_string()),
            original_title: Some("Supergirl".to_string()),
            series_name: None,
            duration_seconds: Some(6462.0),
            keyword_mode: Some("mediaTitle".to_string()),
            year: Some(2026),
            media_type: media_type.map(str::to_string),
            season_number: None,
            episode_number: None,
            file_path: None,
            remote_url: None,
            headers: Vec::new(),
            file_name: Some("Supergirl.2026.1080p.WEB-DL.mkv".to_string()),
            language: "zh-CN".to_string(),
        }
    }

    fn xunlei_record(name: &str, duration: u64) -> XunleiNameSearchRecord {
        XunleiNameSearchRecord {
            cid: String::new(),
            name: name.to_string(),
            url: "https://subtitle.v.geilijiasu.com/AA/BB/test.srt".to_string(),
            ext: "srt".to_string(),
            languages: vec!["简体".to_string()],
            score: 0,
            duration,
        }
    }

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
    fn builds_xunlei_name_search_without_media_hash() {
        let url = build_xunlei_name_search_url("超级少女 S01E01 & test").unwrap();
        assert_eq!(url.scheme(), "https");
        assert_eq!(url.host_str(), Some("api-shoulei-ssl.xunlei.com"));
        assert_eq!(url.path(), "/oracle/subtitle");
        assert_eq!(
            url.query_pairs().collect::<Vec<_>>(),
            vec![("name".into(), "超级少女 S01E01 & test".into())]
        );
    }

    #[test]
    fn normalizes_xunlei_name_result_languages() {
        assert_eq!(
            normalize_xunlei_languages(&["简体&英语".to_string()], "zh-cn"),
            "zh-CN"
        );
        assert_eq!(
            normalize_xunlei_languages(&["繁体".to_string()], "zh-tw"),
            "zh-TW"
        );
        assert_eq!(normalize_xunlei_languages(&[], "zh-cn"), "zh-cn");
    }

    #[test]
    fn parses_xunlei_name_search_response() {
        let payload: super::XunleiNameSearchResponse = serde_json::from_str(
            r#"{
              "code": 0,
              "result": "ok",
              "data": [{
                "cid": "ABC123",
                "url": "https://subtitle.v.geilijiasu.com/AA/BB/test.srt",
                "ext": "srt",
                "name": "超级少女.S01E01.简体.srt",
                "languages": ["简体&英语"],
                "score": 8
              }]
            }"#,
        )
        .unwrap();
        assert_eq!(payload.code, 0);
        assert_eq!(payload.data.len(), 1);
        assert_eq!(payload.data[0].cid, "ABC123");
        assert_eq!(payload.data[0].score, 8);
    }

    #[test]
    fn ranks_supergirl_2026_movie_and_rejects_tv_episodes() {
        let request = xunlei_request(Some("movie"));
        let movie = xunlei_record("Supergirl.2026.WEB-DL.Chs.ass", 6_462_000);
        let series = xunlei_record("Supergirl.S01E01.1080p.WEB-DL.Chs.srt", 2_540_000);
        let chinese_episode = xunlei_record("超级少女.第01集.简体.srt", 2_540_000);
        let wrong_year = xunlei_record("Supergirl.1984.BluRay.Chs.srt", 6_300_000);
        let assessment = assess_xunlei_match(&movie, &request, None).unwrap();
        assert!(assessment.score >= 1_400);
        assert_eq!(assessment.label, "高置信度匹配");
        assert!(assess_xunlei_match(&series, &request, None).is_none());
        assert!(assess_xunlei_match(&chinese_episode, &request, None).is_none());
        assert!(assess_xunlei_match(&wrong_year, &request, None).is_none());
    }

    #[test]
    fn ranks_exact_episode_and_rejects_other_episode_numbers() {
        let mut request = xunlei_request(Some("episode"));
        request.query = Some("超级少女".to_string());
        request.series_name = Some("Supergirl".to_string());
        request.year = None;
        request.duration_seconds = Some(2540.0);
        request.season_number = Some(1);
        request.episode_number = Some(1);
        let exact = xunlei_record("Supergirl.S01E01.1080p.WEB-DL.Chs.srt", 2_540_000);
        let wrong = xunlei_record("Supergirl.S01E02.1080p.WEB-DL.Chs.srt", 2_540_000);
        assert!(assess_xunlei_match(&exact, &request, None).is_some());
        assert!(assess_xunlei_match(&wrong, &request, None).is_none());
        assert_eq!(parse_episode_marker("Supergirl.1x01.srt"), Some((1, 1)));
    }

    #[test]
    fn custom_xunlei_search_keeps_broad_results() {
        let mut request = xunlei_request(None);
        request.keyword_mode = Some("custom".to_string());
        request.year = None;
        request.duration_seconds = None;
        request.original_title = None;
        let result = xunlei_record("Supergirl.S01E01.1080p.WEB-DL.Chs.srt", 2_540_000);
        let assessment = assess_xunlei_match(&result, &request, None).unwrap();
        assert_eq!(assessment.label, "自定义关键词匹配");
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

    #[test]
    fn remote_range_hashes_match_local_file_hashes() {
        let content = (0..0x12002)
            .map(|index| (index % 251) as u8)
            .collect::<Vec<_>>();
        let path =
            std::env::temp_dir().join(format!("ohmycine-remote-hash-{}.mp4", std::process::id()));
        fs::write(&path, &content).unwrap();
        let expected_shooter = compute_shooter_hash(&path).unwrap();
        let expected_xunlei = compute_xunlei_cid(&path).unwrap();

        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        let server_content = content.clone();
        let server = thread::spawn(move || {
            for _ in 0..8 {
                let (mut stream, _) = listener.accept().unwrap();
                let mut request = Vec::new();
                let mut buffer = [0_u8; 1024];
                while !request.windows(4).any(|window| window == b"\r\n\r\n") {
                    let read = stream.read(&mut buffer).unwrap();
                    if read == 0 {
                        break;
                    }
                    request.extend_from_slice(&buffer[..read]);
                }
                let request = String::from_utf8(request).unwrap();
                assert!(request
                    .to_ascii_lowercase()
                    .contains("authorization: bearer range-test"));
                let range = request
                    .lines()
                    .find(|line| line.to_ascii_lowercase().starts_with("range:"))
                    .and_then(|line| line.split_once(':'))
                    .map(|(_, value)| value.trim())
                    .and_then(|value| value.strip_prefix("bytes="))
                    .and_then(|value| value.split_once('-'))
                    .map(|(start, end)| {
                        (
                            start.parse::<usize>().unwrap(),
                            end.parse::<usize>().unwrap(),
                        )
                    })
                    .unwrap();
                let body = &server_content[range.0..=range.1];
                write!(
                    stream,
                    "HTTP/1.1 206 Partial Content\r\nContent-Range: bytes {}-{}/{}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    range.0,
                    range.1,
                    server_content.len(),
                    body.len(),
                )
                .unwrap();
                stream.write_all(body).unwrap();
            }
        });

        let request = HashSubtitleSearchRequest {
            provider: HashSubtitleProvider::Shooter,
            query: Some("movie".to_string()),
            original_title: None,
            series_name: None,
            duration_seconds: None,
            keyword_mode: Some("mediaTitle".to_string()),
            year: None,
            media_type: Some("movie".to_string()),
            season_number: None,
            episode_number: None,
            file_path: None,
            remote_url: Some(format!("http://{address}/movie.mp4")),
            headers: vec![RemoteMediaHeader {
                name: "Authorization".to_string(),
                value: "Bearer range-test".to_string(),
            }],
            file_name: Some("movie.mp4".to_string()),
            language: "zh-CN".to_string(),
        };
        let (actual_shooter, actual_xunlei) = tauri::async_runtime::block_on(async {
            let media = resolve_hash_media_source(&request).await.unwrap();
            (
                compute_shooter_media_hash(&media).await.unwrap(),
                compute_xunlei_media_cid(&media).await.unwrap(),
            )
        });
        server.join().unwrap();
        fs::remove_file(path).unwrap();
        assert_eq!(actual_shooter, expected_shooter);
        assert_eq!(actual_xunlei, expected_xunlei);
    }

    #[test]
    fn parses_opensubtitles_xmlrpc_structs_and_arrays() {
        let xml = br#"<?xml version="1.0"?><methodResponse><params><param><value><struct>
          <member><name>status</name><value><string>200 OK</string></value></member>
          <member><name>data</name><value><array><data><value><struct>
            <member><name>IDSubtitleFile</name><value><string>123</string></value></member>
          </struct></value></data></array></value></member>
        </struct></value></param></params></methodResponse>"#;
        let payload = parse_xmlrpc_response(xml).unwrap();
        let fields = xmlrpc_struct(&payload).unwrap();
        assert_eq!(xmlrpc_string_member(fields, "status"), Some("200 OK"));
        assert!(
            matches!(fields.get("data"), Some(super::XmlRpcValue::Array(values)) if values.len() == 1)
        );
    }

    #[test]
    fn rejects_xmlrpc_document_declarations() {
        let xml = br#"<?xml version="1.0"?><!DOCTYPE foo><methodResponse/>"#;
        assert!(parse_xmlrpc_response(xml).is_err());
    }

    #[test]
    fn custom_keyword_is_written_to_opensubtitles_search_request() {
        let request = OpenSubtitlesSearchRequest {
            auth_mode: OpenSubtitlesAuthMode::Account,
            api_key: None,
            username: Some("account".to_string()),
            password: Some("password".to_string()),
            language: "zh-CN".to_string(),
            query: Some("复仇者联盟 & 2012".to_string()),
            imdb_id: None,
            tmdb_id: None,
            year: None,
            season_number: None,
            episode_number: None,
            media_type: Some("movie".to_string()),
        };
        let body = build_opensubtitles_xmlrpc_search_body("session-token", &request).unwrap();
        assert!(body.contains("复仇者联盟 &amp; 2012"));
        assert!(body.contains("<name>sublanguageid</name><value><string>chi</string>"));
        assert!(!body.contains("account"));
        assert!(!body.contains("password"));
    }
}

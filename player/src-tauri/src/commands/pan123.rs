use base64::{engine::general_purpose, Engine as _};
use chrono::{DateTime, FixedOffset, NaiveDateTime, TimeZone, Utc};
use rand::Rng;
use reqwest::header::{
    HeaderValue, ACCEPT, AUTHORIZATION, CONTENT_TYPE, LOCATION, RANGE, REFERER, USER_AGENT,
};
use reqwest::{redirect::Policy, Client, Method, StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::{HashMap, VecDeque};
use std::time::Duration;

const LOGIN_API: &str = "https://login.123pan.com/api/user/sign_in";
const API_BASE: &str = "https://yun.123pan.com/b/api";
const PROVIDER_ORIGIN: &str = "https://yun.123pan.com";
const PROVIDER_REFERER: &str = "https://yun.123pan.com/";
const PLATFORM: &str = "web";
const APP_VERSION: &str = "3";
const USER_AGENT_VALUE: &str = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) OhMyCine-Player/123Pan";
const PAGE_SIZE: usize = 100;
const MAX_PAGES: usize = 200;
const MAX_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const MAX_PATH_LENGTH: usize = 4096;
const MAX_KEYWORD_LENGTH: usize = 512;
const MAX_TOKEN_LENGTH: usize = 32 * 1024;
const MAX_SEARCH_ENTRIES: usize = 10_000;
const MAX_SEARCH_RESULTS: usize = 100;

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123LoginRequest {
    username: String,
    password: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123PathRequest {
    access_token: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    path: String,
    root_path: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123SearchRequest {
    access_token: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    keyword: String,
    root_path: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123StreamRequest {
    access_token: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    root_path: String,
    path: String,
    file_id: String,
    file_name: String,
    etag: String,
    s3_key_flag: String,
    size: u64,
}

#[derive(Clone, Debug)]
struct Pan123Credential {
    access_token: String,
    username: String,
    password: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123FileEntry {
    file_id: String,
    name: String,
    path: String,
    is_dir: bool,
    size: Option<u64>,
    modified_ms: Option<i64>,
    etag: String,
    s3_key_flag: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123LoginResponse {
    access_token: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123ListResponse {
    entries: Vec<Pan123FileEntry>,
    updated_access_token: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Pan123StreamResponse {
    url: String,
    headers: HashMap<String, String>,
    updated_access_token: Option<String>,
}

#[derive(Debug)]
pub(crate) struct Pan123ResolvedStream {
    pub(crate) url: String,
    pub(crate) headers: HashMap<String, String>,
}

#[derive(Clone, Debug)]
struct ProviderFile {
    file_id: String,
    name: String,
    is_dir: bool,
    size: u64,
    modified_ms: Option<i64>,
    etag: String,
    s3_key_flag: String,
}

#[tauri::command]
pub async fn pan123_login(request: Pan123LoginRequest) -> Result<Pan123LoginResponse, String> {
    let client = create_api_client()?;
    let access_token = login_with_account(&client, &request.username, &request.password).await?;
    Ok(Pan123LoginResponse { access_token })
}

#[tauri::command]
pub async fn pan123_list(request: Pan123PathRequest) -> Result<Pan123ListResponse, String> {
    let root_path = normalize_provider_path(&request.root_path)?;
    let path = normalize_provider_path(&request.path)?;
    if !path_within_root(&path, &root_path) {
        return Err("123 云盘路径不在已选择的根目录内。".to_string());
    }

    let client = create_api_client()?;
    let mut credential =
        credential_from_parts(request.access_token, request.username, request.password)?;
    let original_token = credential.access_token.clone();
    let parent_id = resolve_directory_id(&client, &mut credential, &path).await?;
    let files = list_directory(&client, &mut credential, &parent_id).await?;
    let entries = files
        .into_iter()
        .filter_map(|file| map_file_entry(file, &path).ok())
        .collect();
    Ok(Pan123ListResponse {
        entries,
        updated_access_token: changed_token(&original_token, &credential.access_token),
    })
}

#[tauri::command]
pub async fn pan123_search(request: Pan123SearchRequest) -> Result<Pan123ListResponse, String> {
    let root_path = normalize_provider_path(&request.root_path)?;
    let keyword = normalize_keyword(&request.keyword)?.to_lowercase();
    let client = create_api_client()?;
    let mut credential =
        credential_from_parts(request.access_token, request.username, request.password)?;
    let original_token = credential.access_token.clone();
    let root_id = resolve_directory_id(&client, &mut credential, &root_path).await?;
    let mut queue = VecDeque::from([(root_id, root_path.clone())]);
    let mut entries = Vec::new();
    let mut visited = 0_usize;

    while let Some((directory_id, directory_path)) = queue.pop_front() {
        let files = list_directory(&client, &mut credential, &directory_id).await?;
        for file in files {
            visited += 1;
            if visited > MAX_SEARCH_ENTRIES || entries.len() >= MAX_SEARCH_RESULTS {
                queue.clear();
                break;
            }
            let entry = map_file_entry(file, &directory_path)?;
            if entry.is_dir {
                queue.push_back((entry.file_id.clone(), entry.path.clone()));
            }
            if entry.name.to_lowercase().contains(&keyword) {
                entries.push(entry);
            }
        }
    }

    Ok(Pan123ListResponse {
        entries,
        updated_access_token: changed_token(&original_token, &credential.access_token),
    })
}

#[tauri::command]
pub async fn pan123_get_stream(
    request: Pan123StreamRequest,
) -> Result<Pan123StreamResponse, String> {
    let root_path = normalize_provider_path(&request.root_path)?;
    let path = normalize_provider_path(&request.path)?;
    if !path_within_root(&path, &root_path) {
        return Err("123 云盘文件不在已选择的根目录内。".to_string());
    }
    validate_file_name(&request.file_name)?;
    let file_id = normalize_file_id(&request.file_id)?;
    let file_id_number = file_id
        .parse::<u64>()
        .map_err(|_| "123 云盘文件标识无效。".to_string())?;
    let etag = normalize_short_value(&request.etag, "123 云盘文件校验值无效。")?;
    let s3_key_flag = normalize_short_value(&request.s3_key_flag, "123 云盘文件存储标识无效。")?;

    let client = create_api_client()?;
    let mut credential =
        credential_from_parts(request.access_token, request.username, request.password)?;
    let original_token = credential.access_token.clone();
    let body = json!({
        "driveId": 0,
        "etag": etag,
        "fileId": file_id_number,
        "fileName": request.file_name,
        "s3keyFlag": s3_key_flag,
        "size": request.size,
        "type": 0,
    });
    let response = send_api_request(
        &client,
        &mut credential,
        Method::POST,
        "/file/download_info",
        &[],
        Some(body),
    )
    .await?;
    let download_url = response
        .get("data")
        .and_then(|data| data.get("DownloadUrl"))
        .and_then(Value::as_str)
        .ok_or_else(|| "123 云盘没有返回可用的播放地址。".to_string())?;
    let upstream_url = decode_download_url(download_url)?;
    let referer = origin_for_url(&upstream_url).unwrap_or_else(|| PROVIDER_REFERER.to_string());
    let url = resolve_download_redirect(&upstream_url).await?;
    let mut headers = HashMap::new();
    headers.insert("Referer".to_string(), referer);

    Ok(Pan123StreamResponse {
        url,
        headers,
        updated_access_token: changed_token(&original_token, &credential.access_token),
    })
}

pub(crate) async fn resolve_download_stream(
    access_token: String,
    username: String,
    password: String,
    root_path: String,
    path: String,
) -> Result<Pan123ResolvedStream, String> {
    let root_path = normalize_provider_path(&root_path)?;
    let path = normalize_provider_path(&path)?;
    if !path_within_root(&path, &root_path) || path == "/" {
        return Err("123 Pan media path is outside the configured root.".to_string());
    }
    let (parent_path, file_name) = split_parent_name(&path)?;
    let client = create_api_client()?;
    let mut credential = credential_from_parts(access_token, username, password)?;
    let parent_id = resolve_directory_id(&client, &mut credential, &parent_path).await?;
    let file = list_directory(&client, &mut credential, &parent_id)
        .await?
        .into_iter()
        .find(|entry| !entry.is_dir && entry.name == file_name)
        .ok_or_else(|| {
            "123 Pan media item is unavailable or the account cannot access it.".to_string()
        })?;
    let file_id = file
        .file_id
        .parse::<u64>()
        .map_err(|_| "123 Pan returned an invalid media identity.".to_string())?;
    let response = send_api_request(
        &client,
        &mut credential,
        Method::POST,
        "/file/download_info",
        &[],
        Some(json!({
            "driveId": 0,
            "etag": file.etag,
            "fileId": file_id,
            "fileName": file.name,
            "s3keyFlag": file.s3_key_flag,
            "size": file.size,
            "type": 0,
        })),
    )
    .await?;
    let download_url = response
        .get("data")
        .and_then(|data| data.get("DownloadUrl"))
        .and_then(Value::as_str)
        .ok_or_else(|| "123 Pan did not return a usable media download address.".to_string())?;
    let upstream_url = decode_download_url(download_url)?;
    let referer = origin_for_url(&upstream_url).unwrap_or_else(|| PROVIDER_REFERER.to_string());
    let url = resolve_download_redirect(&upstream_url).await?;
    Ok(Pan123ResolvedStream {
        url,
        headers: HashMap::from([("Referer".to_string(), referer)]),
    })
}

pub(crate) async fn delete_source_path(
    access_token: String,
    username: String,
    password: String,
    root_path: String,
    path: String,
) -> Result<(), String> {
    let root_path = normalize_provider_path(&root_path)?;
    let path = normalize_provider_path(&path)?;
    if !path_within_root(&path, &root_path) || path == root_path || path == "/" {
        return Err("123 Pan refuses to delete the configured root.".to_string());
    }
    let (parent_path, file_name) = split_parent_name(&path)?;
    let client = create_api_client()?;
    let mut credential = credential_from_parts(access_token, username, password)?;
    let parent_id = resolve_directory_id(&client, &mut credential, &parent_path).await?;
    let file = list_directory(&client, &mut credential, &parent_id)
        .await?
        .into_iter()
        .find(|entry| entry.name == file_name)
        .ok_or_else(|| {
            "123 Pan source item is unavailable or the account cannot delete it.".to_string()
        })?;
    send_api_request(
        &client,
        &mut credential,
        Method::POST,
        "/file/trash",
        &[],
        Some(json!({
            "driveId": 0,
            "operation": true,
            "fileTrashInfoList": [{
                "FileId": file.file_id.parse::<u64>()
                    .map_err(|_| "123 Pan returned an invalid source identity.".to_string())?,
                "FileName": file.name,
                "Type": if file.is_dir { 1 } else { 0 },
                "Size": file.size,
                "Etag": file.etag,
                "S3KeyFlag": file.s3_key_flag,
            }],
        })),
    )
    .await
    .map_err(|error| {
        format!("123 Pan source delete failed or its private web API changed: {error}")
    })?;
    Ok(())
}

fn split_parent_name(path: &str) -> Result<(String, String), String> {
    let (parent, name) = path
        .rsplit_once('/')
        .ok_or_else(|| "123 Pan media path is invalid.".to_string())?;
    validate_file_name(name)?;
    Ok((
        if parent.is_empty() {
            "/".to_string()
        } else {
            parent.to_string()
        },
        name.to_string(),
    ))
}

fn create_api_client() -> Result<Client, String> {
    Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(30))
        .redirect(Policy::limited(5))
        .build()
        .map_err(|_| "123 云盘网络客户端初始化失败。".to_string())
}

async fn login_with_account(
    client: &Client,
    username: &str,
    password: &str,
) -> Result<String, String> {
    let username = normalize_username(username)?;
    let password = normalize_password(password)?;
    let body = if username.contains('@') {
        json!({ "mail": username, "password": password, "type": 2 })
    } else {
        json!({ "passport": username, "password": password, "remember": true })
    };
    let response = client
        .post(LOGIN_API)
        .header(ACCEPT, "application/json, text/plain, */*")
        .header(CONTENT_TYPE, "application/json")
        .header("platform", PLATFORM)
        .header("app-version", APP_VERSION)
        .header("origin", PROVIDER_ORIGIN)
        .header(REFERER, PROVIDER_REFERER)
        .header(USER_AGENT, USER_AGENT_VALUE)
        .json(&body)
        .send()
        .await
        .map_err(|_| "123 云盘账号登录请求失败。".to_string())?;
    let status = response.status();
    let value = read_json_response(response, "123 云盘登录响应读取失败。").await?;
    let code = provider_code(&value).unwrap_or(status.as_u16() as i64);
    if !status.is_success() || !matches!(code, 0 | 200) {
        return Err("123 云盘账号或密码无效，或当前登录需要额外安全验证。".to_string());
    }
    let token = value
        .get("data")
        .and_then(|data| data.get("token"))
        .and_then(Value::as_str)
        .unwrap_or_default();
    normalize_access_token(token)
}

async fn resolve_directory_id(
    client: &Client,
    credential: &mut Pan123Credential,
    path: &str,
) -> Result<String, String> {
    if path == "/" {
        return Ok("0".to_string());
    }
    let mut parent_id = "0".to_string();
    for segment in path.split('/').filter(|segment| !segment.is_empty()) {
        let files = list_directory(client, credential, &parent_id).await?;
        let directory = files
            .into_iter()
            .find(|file| file.is_dir && file.name == segment)
            .ok_or_else(|| "123 云盘目录不存在或当前账号无权访问。".to_string())?;
        parent_id = directory.file_id;
    }
    Ok(parent_id)
}

async fn list_directory(
    client: &Client,
    credential: &mut Pan123Credential,
    parent_id: &str,
) -> Result<Vec<ProviderFile>, String> {
    let parent_id = normalize_file_id(parent_id)?;
    let mut files = Vec::new();
    for page in 1..=MAX_PAGES {
        let page_string = page.to_string();
        let response = send_api_request(
            client,
            credential,
            Method::GET,
            "/file/list/new",
            &[
                ("driveId", "0"),
                ("limit", "100"),
                ("next", "0"),
                ("orderBy", "file_id"),
                ("orderDirection", "desc"),
                ("parentFileId", parent_id.as_str()),
                ("trashed", "false"),
                ("SearchData", ""),
                ("Page", page_string.as_str()),
                ("OnlyLookAbnormalFile", "0"),
                ("event", "homeListFile"),
                ("operateType", "4"),
                ("inDirectSpace", "false"),
            ],
            None,
        )
        .await?;
        let page_files = parse_file_page(&response);
        let count = page_files.len();
        files.extend(page_files);
        let next = response
            .get("data")
            .and_then(|data| data.get("Next"))
            .and_then(value_as_string)
            .unwrap_or_default();
        if count < PAGE_SIZE || next == "-1" {
            break;
        }
    }
    Ok(files)
}

async fn send_api_request(
    client: &Client,
    credential: &mut Pan123Credential,
    method: Method,
    endpoint: &str,
    query: &[(&str, &str)],
    body: Option<Value>,
) -> Result<Value, String> {
    let mut refreshed = false;
    loop {
        let url = build_signed_api_url(endpoint, query)?;
        let mut request = client
            .request(method.clone(), url)
            .header(ACCEPT, "application/json, text/plain, */*")
            .header(AUTHORIZATION, format!("Bearer {}", credential.access_token))
            .header("platform", PLATFORM)
            .header("app-version", APP_VERSION)
            .header("origin", PROVIDER_ORIGIN)
            .header(REFERER, PROVIDER_REFERER)
            .header(USER_AGENT, USER_AGENT_VALUE);
        if let Some(value) = body.clone() {
            request = request
                .header(CONTENT_TYPE, "application/json")
                .json(&value);
        }
        let response = request
            .send()
            .await
            .map_err(|_| "123 云盘网络请求失败。".to_string())?;
        let status = response.status();
        let value = read_json_response(response, "123 云盘响应读取失败。").await?;
        if is_auth_failure(status, &value) && !refreshed && can_refresh(credential) {
            credential.access_token =
                login_with_account(client, &credential.username, &credential.password).await?;
            refreshed = true;
            continue;
        }
        validate_api_response(status, &value)?;
        return Ok(value);
    }
}

async fn read_json_response(response: reqwest::Response, error: &str) -> Result<Value, String> {
    let bytes = response.bytes().await.map_err(|_| error.to_string())?;
    if bytes.len() > MAX_RESPONSE_BYTES {
        return Err("123 云盘响应过大，已停止处理。".to_string());
    }
    serde_json::from_slice(&bytes).map_err(|_| "123 云盘返回了无效的数据。".to_string())
}

fn validate_api_response(status: StatusCode, value: &Value) -> Result<(), String> {
    let code = provider_code(value).unwrap_or(status.as_u16() as i64);
    if status.is_success() && code == 0 {
        return Ok(());
    }
    if is_auth_failure(status, value) {
        return Err("123 云盘登录已失效，请在数据源设置中重新登录。".to_string());
    }
    Err(format!("123 云盘请求失败（代码 {code}）。"))
}

fn is_auth_failure(status: StatusCode, value: &Value) -> bool {
    matches!(status.as_u16(), 401 | 403) || provider_code(value) == Some(401)
}

fn provider_code(value: &Value) -> Option<i64> {
    let value = value.get("code")?;
    value
        .as_i64()
        .or_else(|| value.as_str().and_then(|raw| raw.parse().ok()))
}

fn build_signed_api_url(endpoint: &str, query: &[(&str, &str)]) -> Result<Url, String> {
    if !endpoint.starts_with('/') || endpoint.contains("..") {
        return Err("123 云盘请求地址无效。".to_string());
    }
    let mut url = Url::parse(&format!("{API_BASE}{endpoint}"))
        .map_err(|_| "123 云盘请求地址无效。".to_string())?;
    let (sign_key, sign_value) = sign_path(url.path());
    {
        let mut pairs = url.query_pairs_mut();
        pairs.append_pair(&sign_key, &sign_value);
        for (key, value) in query {
            pairs.append_pair(key, value);
        }
    }
    Ok(url)
}

fn sign_path(path: &str) -> (String, String) {
    let now = Utc::now();
    let timestamp = now.timestamp();
    let offset = FixedOffset::east_opt(8 * 60 * 60).expect("valid CST offset");
    let minute = now.with_timezone(&offset).format("%Y%m%d%H%M").to_string();
    let random = rand::thread_rng().gen_range(0..=10_000_000_u32);
    sign_path_at(path, timestamp, random, &minute)
}

fn sign_path_at(path: &str, timestamp: i64, random: u32, minute: &str) -> (String, String) {
    const TABLE: [char; 26] = [
        'a', 'd', 'e', 'f', 'g', 'h', 'l', 'm', 'y', 'i', 'j', 'n', 'o', 'p', 'k', 'q', 'r', 's',
        't', 'u', 'b', 'c', 'v', 'w', 's', 'z',
    ];
    let encoded_minute: String = minute
        .bytes()
        .filter(|value| value.is_ascii_digit())
        .map(|value| TABLE[(value - b'0') as usize])
        .collect();
    let time_sign = crc32fast::hash(encoded_minute.as_bytes()).to_string();
    let payload = format!("{timestamp}|{random}|{path}|{PLATFORM}|{APP_VERSION}|{time_sign}");
    let data_sign = crc32fast::hash(payload.as_bytes());
    (time_sign, format!("{timestamp}-{random}-{data_sign}"))
}

fn parse_file_page(value: &Value) -> Vec<ProviderFile> {
    value
        .get("data")
        .and_then(|data| data.get("InfoList"))
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(parse_provider_file)
        .collect()
}

fn parse_provider_file(value: &Value) -> Option<ProviderFile> {
    let file_id = value.get("FileId").and_then(value_as_string)?;
    let name = value.get("FileName")?.as_str()?.trim().to_string();
    if normalize_file_id(&file_id).is_err() || validate_file_name(&name).is_err() {
        return None;
    }
    let is_dir = value.get("Type").and_then(Value::as_i64) == Some(1);
    Some(ProviderFile {
        file_id,
        name,
        is_dir,
        size: value.get("Size").and_then(value_as_u64).unwrap_or(0),
        modified_ms: value.get("UpdateAt").and_then(parse_modified_ms),
        etag: value
            .get("Etag")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_string(),
        s3_key_flag: value
            .get("S3KeyFlag")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_string(),
    })
}

fn map_file_entry(file: ProviderFile, parent_path: &str) -> Result<Pan123FileEntry, String> {
    Ok(Pan123FileEntry {
        path: join_provider_path(parent_path, &file.name)?,
        file_id: file.file_id,
        name: file.name,
        is_dir: file.is_dir,
        size: (!file.is_dir).then_some(file.size),
        modified_ms: file.modified_ms,
        etag: file.etag,
        s3_key_flag: file.s3_key_flag,
    })
}

fn parse_modified_ms(value: &Value) -> Option<i64> {
    if let Some(raw) = value.as_i64() {
        return if raw.abs() < 10_000_000_000 {
            raw.checked_mul(1000)
        } else {
            Some(raw)
        };
    }
    let raw = value.as_str()?.trim();
    DateTime::parse_from_rfc3339(raw)
        .ok()
        .map(|value| value.timestamp_millis())
        .or_else(|| {
            NaiveDateTime::parse_from_str(raw, "%Y-%m-%d %H:%M:%S")
                .ok()
                .and_then(|value| {
                    FixedOffset::east_opt(8 * 60 * 60)?
                        .from_local_datetime(&value)
                        .single()
                        .map(|value| value.timestamp_millis())
                })
        })
}

fn value_as_string(value: &Value) -> Option<String> {
    value
        .as_str()
        .map(str::to_string)
        .or_else(|| value.as_i64().map(|value| value.to_string()))
        .or_else(|| value.as_u64().map(|value| value.to_string()))
}

fn value_as_u64(value: &Value) -> Option<u64> {
    value
        .as_u64()
        .or_else(|| value.as_str().and_then(|raw| raw.parse().ok()))
}

fn decode_download_url(value: &str) -> Result<String, String> {
    let url = validate_stream_url(value)?;
    let parsed = Url::parse(&url).map_err(|_| "123 云盘返回了无效的播放地址。".to_string())?;
    let Some(params) = parsed
        .query_pairs()
        .find_map(|(key, value)| (key == "params").then(|| value.into_owned()))
    else {
        return Ok(url);
    };
    for engine in [
        &general_purpose::STANDARD,
        &general_purpose::URL_SAFE,
        &general_purpose::STANDARD_NO_PAD,
        &general_purpose::URL_SAFE_NO_PAD,
    ] {
        if let Ok(decoded) = engine.decode(params.as_bytes()) {
            if let Ok(decoded) = String::from_utf8(decoded) {
                if let Ok(url) = validate_stream_url(&decoded) {
                    return Ok(url);
                }
            }
        }
    }
    Err("123 云盘返回了无法解析的播放地址。".to_string())
}

async fn resolve_download_redirect(value: &str) -> Result<String, String> {
    let source_url = Url::parse(&validate_stream_url(value)?)
        .map_err(|_| "123 云盘返回了无效的播放地址。".to_string())?;
    let client = Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(20))
        .redirect(Policy::none())
        .build()
        .map_err(|_| "123 云盘播放地址解析器初始化失败。".to_string())?;
    let response = client
        .get(source_url.clone())
        .header(REFERER, PROVIDER_REFERER)
        .header(USER_AGENT, USER_AGENT_VALUE)
        .header(RANGE, "bytes=0-0")
        .send()
        .await
        .map_err(|_| "123 云盘播放地址解析失败。".to_string())?;

    if response.status().is_redirection() {
        let location = response
            .headers()
            .get(LOCATION)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(|| "123 云盘播放重定向缺少目标地址。".to_string())?;
        let target = source_url
            .join(location)
            .map_err(|_| "123 云盘播放重定向地址无效。".to_string())?;
        return validate_stream_url(target.as_str());
    }

    if response.status().is_success() {
        let content_type = response
            .headers()
            .get(CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default()
            .to_ascii_lowercase();
        if content_type.contains("json") {
            let value = read_json_response(response, "123 云盘播放地址响应读取失败。").await?;
            if let Some(redirect_url) = value
                .get("data")
                .and_then(|data| data.get("redirect_url"))
                .and_then(Value::as_str)
            {
                return validate_stream_url(redirect_url);
            }
        }
        return Ok(source_url.to_string());
    }

    Err(format!(
        "123 云盘播放地址解析失败（HTTP {}）。",
        response.status().as_u16()
    ))
}

fn credential_from_parts(
    access_token: String,
    username: String,
    password: String,
) -> Result<Pan123Credential, String> {
    let access_token = normalize_access_token(&access_token)?;
    let username = username.trim().to_string();
    if !username.is_empty() {
        normalize_username(&username)?;
    }
    if !password.is_empty() {
        normalize_password(&password)?;
    }
    if username.is_empty() != password.is_empty() {
        return Err("123 云盘账号凭据不完整，请重新登录。".to_string());
    }
    Ok(Pan123Credential {
        access_token,
        username,
        password,
    })
}

fn can_refresh(credential: &Pan123Credential) -> bool {
    !credential.username.is_empty() && !credential.password.is_empty()
}

fn changed_token(original: &str, current: &str) -> Option<String> {
    (original != current).then(|| current.to_string())
}

fn normalize_access_token(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > MAX_TOKEN_LENGTH
        || contains_control_character(value)
        || HeaderValue::from_str(value).is_err()
    {
        return Err("123 云盘访问令牌无效。".to_string());
    }
    Ok(value.to_string())
}

fn normalize_username(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > 320 || contains_control_character(value) {
        return Err("请输入有效的 123 云盘手机号或邮箱。".to_string());
    }
    Ok(value.to_string())
}

fn normalize_password(value: &str) -> Result<String, String> {
    if value.is_empty() || value.len() > 1024 || contains_control_character(value) {
        return Err("请输入有效的 123 云盘密码。".to_string());
    }
    Ok(value.to_string())
}

fn normalize_file_id(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > 32 || !value.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err("123 云盘文件标识无效。".to_string());
    }
    Ok(value.to_string())
}

fn normalize_short_value(value: &str, error: &str) -> Result<String, String> {
    let value = value.trim();
    if value.len() > 1024 || contains_control_character(value) {
        return Err(error.to_string());
    }
    Ok(value.to_string())
}

fn validate_file_name(value: &str) -> Result<(), String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > 1024
        || contains_control_character(value)
        || value.contains('/')
        || value.contains('\\')
        || matches!(value, "." | "..")
    {
        return Err("123 云盘文件名无效。".to_string());
    }
    Ok(())
}

fn normalize_keyword(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > MAX_KEYWORD_LENGTH || contains_control_character(value) {
        return Err("123 云盘搜索关键词无效。".to_string());
    }
    Ok(value.to_string())
}

fn normalize_provider_path(value: &str) -> Result<String, String> {
    let replaced = value.trim().replace('\\', "/");
    if replaced.len() > MAX_PATH_LENGTH
        || contains_control_character(&replaced)
        || replaced.contains('?')
        || replaced.contains('#')
        || replaced.contains("://")
    {
        return Err("123 云盘路径无效。".to_string());
    }
    let mut segments = Vec::new();
    for segment in replaced.split('/').filter(|segment| !segment.is_empty()) {
        if matches!(segment, "." | "..") || is_encoded_dot_segment(segment) {
            return Err("123 云盘路径无效。".to_string());
        }
        validate_file_name(segment)?;
        segments.push(segment);
    }
    Ok(if segments.is_empty() {
        "/".to_string()
    } else {
        format!("/{}", segments.join("/"))
    })
}

fn join_provider_path(parent: &str, name: &str) -> Result<String, String> {
    validate_file_name(name)?;
    let path = if parent == "/" {
        format!("/{name}")
    } else {
        format!("{parent}/{name}")
    };
    normalize_provider_path(&path)
}

fn path_within_root(path: &str, root: &str) -> bool {
    root == "/" || path == root || path.starts_with(&format!("{root}/"))
}

fn validate_stream_url(value: &str) -> Result<String, String> {
    let value = value.trim();
    let url = Url::parse(value).map_err(|_| "123 云盘返回了无效的播放地址。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || contains_control_character(value)
    {
        return Err("123 云盘返回了无效的播放地址。".to_string());
    }
    Ok(url.to_string())
}

fn origin_for_url(value: &str) -> Option<String> {
    let url = Url::parse(value).ok()?;
    let host = url.host_str()?;
    let port = url
        .port()
        .map(|port| format!(":{port}"))
        .unwrap_or_default();
    Some(format!("{}://{}{port}/", url.scheme(), host))
}

fn contains_control_character(value: &str) -> bool {
    value.chars().any(char::is_control)
}

fn is_encoded_dot_segment(value: &str) -> bool {
    matches!(
        value.to_ascii_lowercase().as_str(),
        "%2e" | ".%2e" | "%2e." | "%2e%2e" | "%252e" | ".%252e" | "%252e." | "%252e%252e"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn signs_current_web_api_shape() {
        let (key, value) = sign_path_at(
            "/b/api/file/list/new",
            1_700_000_000,
            1_234_567,
            "202311150613",
        );
        assert!(key.bytes().all(|byte| byte.is_ascii_digit()));
        assert_eq!(value.matches('-').count(), 2);
        assert!(value.starts_with("1700000000-1234567-"));
    }

    #[test]
    fn rejects_paths_outside_the_selected_root() {
        assert!(path_within_root("/媒体/电影/影片.mkv", "/媒体"));
        assert!(!path_within_root("/媒体库/影片.mkv", "/媒体"));
        assert!(normalize_provider_path("/媒体/../secret").is_err());
        assert!(normalize_provider_path("/媒体/%2e%2e/secret").is_err());
    }

    #[test]
    fn parses_provider_file_without_exposing_download_url() {
        let value = json!({
            "FileId": 1234567890123_u64,
            "FileName": "电影.mkv",
            "Type": 0,
            "Size": 42,
            "Etag": "etag-value",
            "S3KeyFlag": "storage-value",
            "DownloadUrl": "https://download.example.test/secret"
        });
        let file = parse_provider_file(&value).unwrap();
        assert_eq!(file.file_id, "1234567890123");
        assert_eq!(file.name, "电影.mkv");
        assert_eq!(file.size, 42);
    }

    #[test]
    fn maps_provider_errors_to_safe_messages() {
        let auth = json!({ "code": 401, "message": "token secret" });
        assert_eq!(
            validate_api_response(StatusCode::UNAUTHORIZED, &auth).unwrap_err(),
            "123 云盘登录已失效，请在数据源设置中重新登录。"
        );
        let failure = json!({ "code": 50001, "message": "upstream secret" });
        let message = validate_api_response(StatusCode::OK, &failure).unwrap_err();
        assert!(!message.contains("upstream secret"));
    }

    #[test]
    fn decodes_base64_wrapped_download_urls() {
        let target = "https://cdn.example.test/video.mkv?token=secret";
        let encoded = general_purpose::STANDARD.encode(target);
        let wrapped = format!("https://download.example.test/file?params={encoded}");
        assert_eq!(decode_download_url(&wrapped).unwrap(), target);
    }
}

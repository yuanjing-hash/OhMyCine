use reqwest::header::{
    HeaderMap, HeaderValue, ACCEPT, CONTENT_TYPE, COOKIE, REFERER, SET_COOKIE, USER_AGENT,
};
use reqwest::{Client, Method, Url};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tauri::{AppHandle, Manager, State, WebviewUrl, WebviewWindowBuilder};
use tokio::sync::Mutex;

const API_BASE: &str = "https://drive.quark.cn/1/clouddrive";
const REFERER_VALUE: &str = "https://pan.quark.cn";
const USER_AGENT_VALUE: &str = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch";
const MAX_COOKIE_LENGTH: usize = 32 * 1024;
const MAX_PATH_LENGTH: usize = 4096;
const MAX_KEYWORD_LENGTH: usize = 512;
const MAX_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const PAGE_SIZE: usize = 100;
const MAX_PAGES: usize = 200;
const QR_SESSION_TTL: Duration = Duration::from_secs(10 * 60);
const ACCOUNT_LOGIN_URL: &str = "https://uop.quark.cn/cas/custom/login";

#[derive(Default)]
pub struct QuarkAuthState {
    qr_sessions: Mutex<HashMap<String, QuarkQrSession>>,
}

struct QuarkQrSession {
    client: Client,
    jar: Arc<Jar>,
    token: String,
    created_at: Instant,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkPathRequest {
    cookie: String,
    path: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkSearchRequest {
    cookie: String,
    keyword: String,
    root_path: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkStreamRequest {
    cookie: String,
    fid: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkFileEntry {
    fid: String,
    name: String,
    path: String,
    is_dir: bool,
    size: Option<u64>,
    modified_ms: Option<i64>,
    thumbnail: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkListResponse {
    entries: Vec<QuarkFileEntry>,
    updated_cookie: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkStreamResponse {
    url: String,
    headers: HashMap<String, String>,
    updated_cookie: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkQrStartResponse {
    session_id: String,
    qr_image_url: String,
    expires_at_ms: u64,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkAuthPollResponse {
    status: String,
    cookie: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct QuarkAccountLoginResponse {
    session_id: String,
}

struct ApiResponse {
    body: Value,
}

#[tauri::command]
pub async fn quark_auth_start_qr(
    state: State<'_, QuarkAuthState>,
) -> Result<QuarkQrStartResponse, String> {
    let jar = Arc::new(Jar::default());
    let client = create_auth_client(jar.clone())?;
    let response = client
        .get("https://uop.quark.cn/cas/ajax/getTokenForQrcodeLogin")
        .header(ACCEPT, "application/json, text/plain, */*")
        .header(REFERER, REFERER_VALUE)
        .header(USER_AGENT, USER_AGENT_VALUE)
        .send()
        .await
        .map_err(|_| "夸克扫码登录初始化失败。".to_string())?;
    let value: Value = response
        .json()
        .await
        .map_err(|_| "夸克扫码登录返回了无效的数据。".to_string())?;
    if value.get("status").and_then(Value::as_i64) != Some(2_000_000) {
        return Err("夸克扫码登录初始化失败。".to_string());
    }
    let token = value
        .get("data")
        .and_then(|data| data.get("members"))
        .and_then(|members| members.get("token"))
        .and_then(Value::as_str)
        .filter(|token| !token.is_empty() && token.len() <= 512)
        .ok_or_else(|| "夸克扫码登录未返回有效令牌。".to_string())?
        .to_string();
    let qr_url = build_quark_qr_url(&token)?;
    let qr_image_url = render_qr_data_url(&qr_url)?;
    let session_id = create_auth_session_id("qr");
    state.qr_sessions.lock().await.insert(
        session_id.clone(),
        QuarkQrSession {
            client,
            jar,
            token,
            created_at: Instant::now(),
        },
    );
    Ok(QuarkQrStartResponse {
        session_id,
        qr_image_url,
        expires_at_ms: unix_time_ms().saturating_add(QR_SESSION_TTL.as_millis() as u64),
    })
}

#[tauri::command]
pub async fn quark_auth_poll_qr(
    state: State<'_, QuarkAuthState>,
    session_id: String,
) -> Result<QuarkAuthPollResponse, String> {
    let session_id = normalize_session_id(&session_id)?;
    let session = state
        .qr_sessions
        .lock()
        .await
        .remove(&session_id)
        .ok_or_else(|| "夸克扫码登录会话不存在或已过期。".to_string())?;
    if session.created_at.elapsed() > QR_SESSION_TTL {
        return Ok(auth_poll_response("expired", None));
    }
    let response = session
        .client
        .get("https://uop.quark.cn/cas/ajax/getServiceTicketByQrcodeToken")
        .query(&[("token", session.token.as_str())])
        .header(ACCEPT, "application/json, text/plain, */*")
        .header(REFERER, REFERER_VALUE)
        .header(USER_AGENT, USER_AGENT_VALUE)
        .send()
        .await
        .map_err(|_| "夸克扫码登录状态查询失败。".to_string())?;
    let value: Value = response
        .json()
        .await
        .map_err(|_| "夸克扫码登录状态无效。".to_string())?;
    let status = value
        .get("status")
        .and_then(Value::as_i64)
        .unwrap_or_default();
    if status == 2_000_000 {
        let service_ticket = value
            .get("data")
            .and_then(|data| data.get("members"))
            .and_then(|members| members.get("service_ticket"))
            .and_then(Value::as_str)
            .filter(|ticket| !ticket.is_empty() && ticket.len() <= 2048)
            .ok_or_else(|| "夸克扫码登录未返回有效授权票据。".to_string())?;
        let cookie = exchange_service_ticket(&session.client, &session.jar, service_ticket).await?;
        return Ok(auth_poll_response("success", Some(cookie)));
    }
    if status == 5_000_4002 {
        return Ok(auth_poll_response("expired", None));
    }
    state.qr_sessions.lock().await.insert(session_id, session);
    Ok(auth_poll_response("pending", None))
}

#[tauri::command]
pub async fn quark_auth_start_account(app: AppHandle) -> Result<QuarkAccountLoginResponse, String> {
    let session_id = create_auth_session_id("account");
    let label = account_window_label(&session_id);
    let url = build_account_login_url()?;
    let builder = WebviewWindowBuilder::new(&app, label, WebviewUrl::External(url))
        .title("夸克网盘账号登录")
        .inner_size(520.0, 720.0)
        .min_inner_size(420.0, 620.0)
        .on_navigation(is_allowed_account_login_navigation);
    #[cfg(not(mobile))]
    let builder = builder.center();
    builder
        .build()
        .map_err(|_| "无法打开夸克官方账号登录页面。".to_string())?;
    Ok(QuarkAccountLoginResponse { session_id })
}

#[tauri::command]
pub async fn quark_auth_poll_account(
    app: AppHandle,
    session_id: String,
) -> Result<QuarkAuthPollResponse, String> {
    let session_id = normalize_session_id(&session_id)?;
    let label = account_window_label(&session_id);
    let Some(window) = app.get_webview_window(&label) else {
        return Ok(auth_poll_response("cancelled", None));
    };
    let url = Url::parse("https://drive.quark.cn/")
        .map_err(|_| "夸克账号登录状态地址无效。".to_string())?;
    let cookies = window
        .cookies_for_url(url)
        .map_err(|_| "无法读取夸克官方登录状态。".to_string())?;
    let cookie = cookies
        .into_iter()
        .filter(|cookie| !cookie.name().is_empty() && !cookie.value().is_empty())
        .map(|cookie| format!("{}={}", cookie.name(), cookie.value()))
        .collect::<Vec<_>>()
        .join("; ");
    if cookie.is_empty() || validate_login_cookie(&cookie).await.is_err() {
        return Ok(auth_poll_response("pending", None));
    }
    let _ = window.close();
    Ok(auth_poll_response("success", Some(cookie)))
}

#[tauri::command]
pub async fn quark_auth_cancel(
    app: AppHandle,
    state: State<'_, QuarkAuthState>,
    session_id: String,
) -> Result<(), String> {
    let session_id = normalize_session_id(&session_id)?;
    state.qr_sessions.lock().await.remove(&session_id);
    if let Some(window) = app.get_webview_window(&account_window_label(&session_id)) {
        let _ = window.close();
    }
    Ok(())
}

#[tauri::command]
pub async fn quark_list(request: QuarkPathRequest) -> Result<QuarkListResponse, String> {
    let mut cookie = normalize_cookie(&request.cookie)?;
    let path = normalize_provider_path(&request.path)?;
    let original_cookie = cookie.clone();
    let client = create_client()?;
    let fid = resolve_path_fid(&client, &mut cookie, &path).await?;
    let entries = list_directory(&client, &mut cookie, &fid, &path).await?;
    Ok(QuarkListResponse {
        entries,
        updated_cookie: changed_cookie(&original_cookie, &cookie),
    })
}

#[tauri::command]
pub async fn quark_search(request: QuarkSearchRequest) -> Result<QuarkListResponse, String> {
    let mut cookie = normalize_cookie(&request.cookie)?;
    let keyword = normalize_keyword(&request.keyword)?;
    let root_path = normalize_provider_path(&request.root_path)?;
    let original_cookie = cookie.clone();
    let client = create_client()?;
    let mut entries = Vec::new();

    for page in 1..=MAX_PAGES {
        let response = send_api_request(
            &client,
            &mut cookie,
            Method::GET,
            "/file/search",
            &[
                ("q", keyword.as_str()),
                ("_page", &page.to_string()),
                ("_size", &PAGE_SIZE.to_string()),
                ("_fetch_total", "1"),
                ("_fetch_full_path", "1"),
                ("_sort", "file_type:asc,updated_at:desc"),
            ],
            None,
        )
        .await?;
        let page_entries = parse_file_list(&response.body, &root_path, true);
        let count = page_entries.len();
        entries.extend(
            page_entries
                .into_iter()
                .filter(|entry| path_within_root(&entry.path, &root_path)),
        );
        let total = response_total(&response.body);
        if count < PAGE_SIZE || total.is_some_and(|total| entries.len() >= total) {
            break;
        }
    }

    Ok(QuarkListResponse {
        entries,
        updated_cookie: changed_cookie(&original_cookie, &cookie),
    })
}

#[tauri::command]
pub async fn quark_get_stream(request: QuarkStreamRequest) -> Result<QuarkStreamResponse, String> {
    let mut cookie = normalize_cookie(&request.cookie)?;
    let fid = normalize_fid(&request.fid)?;
    let original_cookie = cookie.clone();
    let client = create_client()?;
    let response = send_api_request(
        &client,
        &mut cookie,
        Method::POST,
        "/file/download",
        &[],
        Some(json!({ "fids": [fid] })),
    )
    .await?;
    let download_url = response
        .body
        .get("data")
        .and_then(Value::as_array)
        .and_then(|items| items.first())
        .and_then(|item| item.get("download_url"))
        .and_then(Value::as_str)
        .ok_or_else(|| "夸克网盘未返回可用的播放地址。".to_string())?;
    let url = validate_stream_url(download_url)?;
    let parsed = Url::parse(&url).map_err(|_| "夸克网盘返回了无效的播放地址。".to_string())?;
    let mut headers = HashMap::from([
        ("Cookie".to_string(), cookie.clone()),
        ("Referer".to_string(), REFERER_VALUE.to_string()),
        ("User-Agent".to_string(), USER_AGENT_VALUE.to_string()),
    ]);
    if !parsed.path().is_empty() {
        headers.insert("x-urlp".to_string(), parsed.path().to_string());
    }
    Ok(QuarkStreamResponse {
        url,
        headers,
        updated_cookie: changed_cookie(&original_cookie, &cookie),
    })
}

fn create_client() -> Result<Client, String> {
    Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(30))
        .redirect(reqwest::redirect::Policy::limited(3))
        .build()
        .map_err(|_| "夸克网盘网络客户端初始化失败。".to_string())
}

fn create_auth_client(jar: Arc<Jar>) -> Result<Client, String> {
    Client::builder()
        .cookie_provider(jar)
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(30))
        .redirect(reqwest::redirect::Policy::limited(5))
        .build()
        .map_err(|_| "夸克登录客户端初始化失败。".to_string())
}

fn build_quark_qr_url(token: &str) -> Result<String, String> {
    let mut url = Url::parse("https://su.quark.cn/4_eMHBJ")
        .map_err(|_| "夸克扫码登录地址无效。".to_string())?;
    url.query_pairs_mut()
        .append_pair("token", token)
        .append_pair("client_id", "532")
        .append_pair("ssb", "weblogin")
        .append_pair("uc_param_str", "")
        .append_pair(
            "uc_biz_str",
            "S:custom|OPT:SAREA@0|OPT:IMMERSIVE@1|OPT:BACK_BTN_STYLE@0",
        );
    Ok(url.to_string())
}

fn render_qr_data_url(value: &str) -> Result<String, String> {
    let code =
        QrCode::new(value.as_bytes()).map_err(|_| "夸克扫码登录二维码生成失败。".to_string())?;
    let image = code
        .render::<svg::Color>()
        .min_dimensions(260, 260)
        .dark_color(svg::Color("#111111"))
        .light_color(svg::Color("#ffffff"))
        .build();
    Ok(format!(
        "data:image/svg+xml;base64,{}",
        base64::engine::general_purpose::STANDARD.encode(image.as_bytes())
    ))
}

fn build_account_login_url() -> Result<Url, String> {
    let mut url =
        Url::parse(ACCOUNT_LOGIN_URL).map_err(|_| "夸克账号登录地址无效。".to_string())?;
    url.query_pairs_mut()
        .append_pair("custom_login_type", "common")
        .append_pair("client_id", "532")
        .append_pair("display", "pc")
        .append_pair("redirect_uri", "https://pan.quark.cn/");
    Ok(url)
}

async fn exchange_service_ticket(
    client: &Client,
    jar: &Arc<Jar>,
    service_ticket: &str,
) -> Result<String, String> {
    let response = client
        .get("https://pan.quark.cn/account/info")
        .query(&[("st", service_ticket)])
        .header(ACCEPT, "application/json, text/plain, */*")
        .header(REFERER, REFERER_VALUE)
        .header(USER_AGENT, USER_AGENT_VALUE)
        .send()
        .await
        .map_err(|_| "夸克扫码授权失败。".to_string())?;
    let value: Value = response
        .json()
        .await
        .map_err(|_| "夸克扫码授权返回了无效的数据。".to_string())?;
    if value.get("success").and_then(Value::as_bool) != Some(true) {
        return Err("夸克扫码授权未能完成，请刷新二维码后重试。".to_string());
    }
    let url =
        Url::parse("https://drive.quark.cn/").map_err(|_| "夸克登录状态地址无效。".to_string())?;
    let header = jar
        .cookies(&url)
        .and_then(|value| value.to_str().ok().map(ToOwned::to_owned))
        .ok_or_else(|| "夸克扫码授权成功，但未获得登录 Cookie。".to_string())?;
    validate_login_cookie(&header).await
}

async fn validate_login_cookie(cookie: &str) -> Result<String, String> {
    let client = create_client()?;
    let mut cookie = normalize_cookie(cookie)?;
    send_api_request(&client, &mut cookie, Method::GET, "/config", &[], None).await?;
    Ok(cookie)
}

fn create_auth_session_id(prefix: &str) -> String {
    format!("{prefix}-{:x}-{:x}", unix_time_ms(), rand::random::<u64>())
}

fn normalize_session_id(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > 160
        || !trimmed
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || character == '-')
    {
        return Err("夸克登录会话标识无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn account_window_label(session_id: &str) -> String {
    format!("quark-login-{session_id}")
}

fn is_allowed_account_login_navigation(url: &Url) -> bool {
    url.scheme() == "https"
        && url.host_str().is_some_and(|host| {
            host == "quark.cn"
                || host.ends_with(".quark.cn")
                || host == "uc.cn"
                || host.ends_with(".uc.cn")
        })
}

fn auth_poll_response(status: &str, cookie: Option<String>) -> QuarkAuthPollResponse {
    QuarkAuthPollResponse {
        status: status.to_string(),
        cookie,
    }
}

fn unix_time_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

async fn resolve_path_fid(
    client: &Client,
    cookie: &mut String,
    path: &str,
) -> Result<String, String> {
    if path == "/" {
        return Ok("0".to_string());
    }
    let response = send_api_request(
        client,
        cookie,
        Method::POST,
        "/file/info/path_list",
        &[],
        Some(json!({ "file_path": [path], "namespace": "0" })),
    )
    .await?;
    response
        .body
        .get("data")
        .and_then(Value::as_array)
        .and_then(|items| {
            items.iter().find(|item| {
                item.get("file_path")
                    .and_then(Value::as_str)
                    .and_then(|value| normalize_provider_path(value).ok())
                    .is_some_and(|value| value == path)
            })
        })
        .and_then(|item| item.get("fid"))
        .and_then(Value::as_str)
        .map(ToOwned::to_owned)
        .ok_or_else(|| "夸克网盘目录不存在或当前 Cookie 无权访问。".to_string())
}

async fn list_directory(
    client: &Client,
    cookie: &mut String,
    fid: &str,
    parent_path: &str,
) -> Result<Vec<QuarkFileEntry>, String> {
    let mut entries = Vec::new();
    for page in 1..=MAX_PAGES {
        let response = send_api_request(
            client,
            cookie,
            Method::GET,
            "/file/sort",
            &[
                ("pdir_fid", fid),
                ("_page", &page.to_string()),
                ("_size", &PAGE_SIZE.to_string()),
                ("_fetch_total", "1"),
                ("fetch_all_file", "1"),
                ("fetch_risk_file_name", "1"),
                ("_fetch_full_path", "1"),
                ("_sort", "file_type:asc,file_name:asc"),
            ],
            None,
        )
        .await?;
        let page_entries = parse_file_list(&response.body, parent_path, false);
        let count = page_entries.len();
        entries.extend(page_entries);
        let total = response_total(&response.body);
        if count < PAGE_SIZE || total.is_some_and(|total| entries.len() >= total) {
            break;
        }
    }
    Ok(entries)
}

async fn send_api_request(
    client: &Client,
    cookie: &mut String,
    method: Method,
    endpoint: &str,
    query: &[(&str, &str)],
    body: Option<Value>,
) -> Result<ApiResponse, String> {
    let mut url = Url::parse(&format!("{API_BASE}{endpoint}"))
        .map_err(|_| "夸克网盘请求地址无效。".to_string())?;
    {
        let mut pairs = url.query_pairs_mut();
        pairs.append_pair("pr", "ucpro").append_pair("fr", "pc");
        for (key, value) in query {
            pairs.append_pair(key, value);
        }
    }
    let mut request = client
        .request(method, url)
        .header(
            COOKIE,
            HeaderValue::from_str(cookie).map_err(|_| "夸克网盘 Cookie 格式无效。".to_string())?,
        )
        .header(ACCEPT, "application/json, text/plain, */*")
        .header(REFERER, REFERER_VALUE)
        .header(USER_AGENT, USER_AGENT_VALUE);
    if let Some(body) = body {
        request = request.header(CONTENT_TYPE, "application/json").json(&body);
    }
    let response = request
        .send()
        .await
        .map_err(|_| "夸克网盘网络请求失败。".to_string())?;
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = response
        .bytes()
        .await
        .map_err(|_| "夸克网盘响应读取失败。".to_string())?;
    if bytes.len() > MAX_RESPONSE_BYTES {
        return Err("夸克网盘响应过大，已停止处理。".to_string());
    }
    let value: Value =
        serde_json::from_slice(&bytes).map_err(|_| "夸克网盘返回了无效的数据。".to_string())?;
    merge_rotated_cookies(cookie, &headers)?;
    validate_api_response(status.as_u16(), &value)?;
    Ok(ApiResponse { body: value })
}

fn validate_api_response(http_status: u16, value: &Value) -> Result<(), String> {
    let status = value
        .get("status")
        .and_then(Value::as_i64)
        .unwrap_or(http_status as i64);
    let code = value.get("code").and_then(Value::as_i64).unwrap_or(0);
    if http_status < 400 && status < 400 && code == 0 {
        return Ok(());
    }
    if matches!(code, 31001 | 31002 | 31003 | 31023) || matches!(http_status, 401 | 403) {
        return Err("夸克网盘 Cookie 无效或已过期，请重新登录。".to_string());
    }
    Err(format!("夸克网盘请求失败（代码 {code}）。"))
}

fn parse_file_list(value: &Value, parent_path: &str, search: bool) -> Vec<QuarkFileEntry> {
    value
        .get("data")
        .and_then(|data| data.get("list"))
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|item| parse_file_entry(item, parent_path, search))
        .collect()
}

fn parse_file_entry(value: &Value, parent_path: &str, search: bool) -> Option<QuarkFileEntry> {
    let fid = value.get("fid")?.as_str()?.trim();
    let name = value.get("file_name")?.as_str()?.trim();
    if fid.is_empty() || name.is_empty() || contains_control_character(name) {
        return None;
    }
    let is_dir = value
        .get("file")
        .and_then(Value::as_bool)
        .is_some_and(|file| !file)
        || value.get("file_type").and_then(Value::as_i64) == Some(0);
    let candidate_path = value
        .get("file_path")
        .and_then(Value::as_str)
        .or_else(|| value.get("full_path").and_then(Value::as_str))
        .and_then(|path| normalize_search_result_path(path, name));
    let fallback_parent = if search { parent_path } else { parent_path };
    let path = candidate_path.unwrap_or_else(|| join_provider_path(fallback_parent, name));
    Some(QuarkFileEntry {
        fid: fid.to_string(),
        name: name.to_string(),
        path,
        is_dir,
        size: if is_dir {
            None
        } else {
            value.get("size").and_then(Value::as_u64)
        },
        modified_ms: timestamp_ms(value),
        thumbnail: value
            .get("thumbnail")
            .and_then(Value::as_str)
            .and_then(validate_optional_https_url),
    })
}

fn normalize_search_result_path(value: &str, name: &str) -> Option<String> {
    let path = normalize_provider_path(value).ok()?;
    if path == "/" {
        return Some(join_provider_path(&path, name));
    }
    let basename = path.rsplit('/').next().unwrap_or_default();
    if basename == name {
        Some(path)
    } else {
        Some(join_provider_path(&path, name))
    }
}

fn response_total(value: &Value) -> Option<usize> {
    value
        .get("metadata")?
        .get("_total")?
        .as_u64()
        .map(|value| value as usize)
}

fn timestamp_ms(value: &Value) -> Option<i64> {
    let raw = value
        .get("updated_at")
        .or_else(|| value.get("l_updated_at"))
        .or_else(|| value.get("created_at"))?
        .as_i64()?;
    if raw.abs() < 10_000_000_000 {
        raw.checked_mul(1000)
    } else {
        Some(raw)
    }
}

fn normalize_cookie(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_COOKIE_LENGTH
        || contains_control_character(trimmed)
        || !trimmed
            .split(';')
            .any(|part| part.trim().split_once('=').is_some())
        || HeaderValue::from_str(trimmed).is_err()
    {
        return Err("夸克网盘 Cookie 格式无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_fid(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || trimmed.len() > 256 || contains_control_character(trimmed) {
        return Err("夸克网盘文件标识无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_keyword(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_KEYWORD_LENGTH
        || contains_control_character(trimmed)
    {
        return Err("夸克网盘搜索关键词无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_provider_path(value: &str) -> Result<String, String> {
    let replaced = value.trim().replace('\\', "/");
    if replaced.len() > MAX_PATH_LENGTH
        || contains_control_character(&replaced)
        || replaced.contains('?')
        || replaced.contains('#')
        || replaced.contains("://")
    {
        return Err("夸克网盘路径无效。".to_string());
    }
    let mut segments = Vec::new();
    for segment in replaced.split('/').filter(|segment| !segment.is_empty()) {
        if matches!(segment, "." | "..") || is_encoded_dot_segment(segment) {
            return Err("夸克网盘路径无效。".to_string());
        }
        segments.push(segment);
    }
    Ok(if segments.is_empty() {
        "/".to_string()
    } else {
        format!("/{}", segments.join("/"))
    })
}

fn join_provider_path(parent: &str, name: &str) -> String {
    if parent == "/" {
        format!("/{name}")
    } else {
        format!("{parent}/{name}")
    }
}

fn path_within_root(path: &str, root: &str) -> bool {
    root == "/" || path == root || path.starts_with(&format!("{root}/"))
}

fn merge_rotated_cookies(cookie: &mut String, headers: &HeaderMap) -> Result<(), String> {
    for value in headers.get_all(SET_COOKIE).iter() {
        let raw = value
            .to_str()
            .map_err(|_| "夸克网盘返回了无效的 Cookie。".to_string())?;
        let Some((name, value)) = raw.split(';').next().and_then(|pair| pair.split_once('='))
        else {
            continue;
        };
        let name = name.trim();
        if matches!(name, "__puus" | "__pus") {
            set_cookie_value(cookie, name, value.trim());
        }
    }
    normalize_cookie(cookie).map(|normalized| *cookie = normalized)
}

fn set_cookie_value(cookie: &mut String, name: &str, value: &str) {
    let mut parts: Vec<(String, String)> = cookie
        .split(';')
        .filter_map(|part| part.trim().split_once('='))
        .map(|(key, value)| (key.trim().to_string(), value.trim().to_string()))
        .filter(|(key, _)| key != name)
        .collect();
    parts.push((name.to_string(), value.to_string()));
    *cookie = parts
        .into_iter()
        .map(|(key, value)| format!("{key}={value}"))
        .collect::<Vec<_>>()
        .join("; ");
}

fn changed_cookie(original: &str, current: &str) -> Option<String> {
    (original != current).then(|| current.to_string())
}

fn validate_stream_url(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    let url = Url::parse(trimmed).map_err(|_| "夸克网盘返回了无效的播放地址。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || contains_control_character(trimmed)
    {
        return Err("夸克网盘返回了无效的播放地址。".to_string());
    }
    Ok(url.to_string())
}

fn validate_optional_https_url(value: &str) -> Option<String> {
    let url = Url::parse(value.trim()).ok()?;
    (url.scheme() == "https" && url.host_str().is_some()).then(|| url.to_string())
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
    fn validates_cookie_without_exposing_it() {
        assert!(normalize_cookie("__uid=123; __puus=secret").is_ok());
        assert!(normalize_cookie("missing-pair").is_err());
        assert!(normalize_cookie("a=b\r\nInjected: yes").is_err());
    }

    #[test]
    fn merges_only_rotating_quark_cookies() {
        let mut cookie = "__uid=1; __puus=old".to_string();
        let mut headers = HeaderMap::new();
        headers.append(
            SET_COOKIE,
            HeaderValue::from_static("__puus=new; Path=/; HttpOnly"),
        );
        headers.append(
            SET_COOKIE,
            HeaderValue::from_static("ignored=value; Path=/"),
        );
        merge_rotated_cookies(&mut cookie, &headers).unwrap();
        assert_eq!(cookie, "__uid=1; __puus=new");
    }

    #[test]
    fn rejects_unsafe_paths_and_keywords() {
        assert_eq!(normalize_provider_path("/电影/科幻").unwrap(), "/电影/科幻");
        assert!(normalize_provider_path("/电影/../secret").is_err());
        assert!(normalize_provider_path("/电影/%2e%2e/secret").is_err());
        assert!(normalize_keyword("\n").is_err());
    }

    #[test]
    fn maps_provider_failures_to_safe_messages() {
        let auth = json!({ "status": 401, "code": 31001, "message": "secret cookie" });
        assert_eq!(
            validate_api_response(401, &auth).unwrap_err(),
            "夸克网盘 Cookie 无效或已过期，请重新登录。"
        );
        let failure = json!({ "status": 500, "code": 50001, "message": "sensitive upstream" });
        let message = validate_api_response(500, &failure).unwrap_err();
        assert!(!message.contains("sensitive upstream"));
    }

    #[test]
    fn parses_list_entries_into_rooted_paths() {
        let value = json!({
            "data": { "list": [{
                "fid": "file-1",
                "file_name": "电影.mkv",
                "file": true,
                "size": 42,
                "updated_at": 1_700_000_000_000_i64
            }] }
        });
        let entries = parse_file_list(&value, "/媒体", false);
        assert_eq!(entries[0].path, "/媒体/电影.mkv");
        assert_eq!(entries[0].size, Some(42));
    }

    #[test]
    fn builds_official_login_urls_and_local_qr_image() {
        let qr_url = build_quark_qr_url("token-value").unwrap();
        assert!(qr_url.starts_with("https://su.quark.cn/4_eMHBJ?"));
        assert!(qr_url.contains("token=token-value"));
        let image = render_qr_data_url(&qr_url).unwrap();
        assert!(image.starts_with("data:image/svg+xml;base64,"));

        let account_url = build_account_login_url().unwrap();
        assert_eq!(account_url.host_str(), Some("uop.quark.cn"));
        assert!(account_url.as_str().contains("custom_login_type=common"));
        assert!(account_url
            .as_str()
            .contains("redirect_uri=https%3A%2F%2Fpan.quark.cn%2F"));
    }

    #[test]
    fn validates_auth_session_identifiers() {
        assert!(normalize_session_id("qr-1234-abcd").is_ok());
        assert!(normalize_session_id("../window").is_err());
        assert!(normalize_session_id("bad label").is_err());
        assert!(is_allowed_account_login_navigation(
            &Url::parse("https://uop.quark.cn/cas/login").unwrap()
        ));
        assert!(!is_allowed_account_login_navigation(
            &Url::parse("https://example.test/login").unwrap()
        ));
    }
}
use base64::Engine;
use qrcode::render::svg;
use qrcode::QrCode;
use reqwest::cookie::{CookieStore, Jar};

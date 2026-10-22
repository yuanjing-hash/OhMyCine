use base64::{engine::general_purpose::STANDARD, Engine as _};
use reqwest::header::{ACCEPT, AUTHORIZATION, CONTENT_TYPE};
use reqwest::{Method, Url};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;
use tauri::{ipc::Channel, State};
use tokio::sync::watch;

const MAX_BASE_URL_LENGTH: usize = 2048;
const MAX_PATH_LENGTH: usize = 2048;
const MAX_TOKEN_LENGTH: usize = 256;
const MAX_REQUEST_BODY_BYTES: usize = 64 * 1024;
const MAX_RESPONSE_BODY_BYTES: usize = 4 * 1024 * 1024;
const MAX_SSE_EVENT_BYTES: usize = 512 * 1024;
const MAX_SSE_TOTAL_BYTES: usize = 16 * 1024 * 1024;
const SSE_IDLE_TIMEOUT: Duration = Duration::from_secs(75);
const SSE_TOTAL_TIMEOUT: Duration = Duration::from_secs(390);
const MAX_EARLY_STREAM_CANCELLATIONS: usize = 64;

#[derive(Clone, Copy)]
struct SseTiming {
    idle: Duration,
    total: Duration,
}

impl Default for SseTiming {
    fn default() -> Self {
        Self {
            idle: SSE_IDLE_TIMEOUT,
            total: SSE_TOTAL_TIMEOUT,
        }
    }
}

#[derive(Default)]
pub struct ServerStreamState {
    streams: Mutex<ServerStreams>,
}

#[derive(Default)]
struct ServerStreams {
    active: HashMap<String, watch::Sender<bool>>,
    cancelled_before_start: HashMap<String, std::time::Instant>,
}

impl ServerStreams {
    fn register(&mut self, request_id: &str, sender: watch::Sender<bool>) -> Result<bool, String> {
        self.prune();
        if self.cancelled_before_start.remove(request_id).is_some() {
            return Ok(false);
        }
        if self.active.contains_key(request_id) {
            return Err("Server 搜索请求标识已存在。".to_string());
        }
        self.active.insert(request_id.to_string(), sender);
        Ok(true)
    }

    fn cancel(&mut self, request_id: &str) {
        if let Some(sender) = self.active.get(request_id) {
            let _ = sender.send(true);
            return;
        }
        // Synchronous cancel IPC can overtake the first poll of an async stream IPC.
        self.prune();
        self.cancelled_before_start
            .insert(request_id.to_string(), std::time::Instant::now());
        while self.cancelled_before_start.len() > MAX_EARLY_STREAM_CANCELLATIONS {
            let oldest = self
                .cancelled_before_start
                .iter()
                .min_by_key(|(_, at)| **at)
                .map(|(id, _)| id.clone());
            if let Some(id) = oldest {
                self.cancelled_before_start.remove(&id);
            }
        }
    }

    fn prune(&mut self) {
        self.cancelled_before_start
            .retain(|_, at| at.elapsed() <= SSE_TOTAL_TIMEOUT);
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerJsonRequest {
    base_url: String,
    method: String,
    path: String,
    access_token: Option<String>,
    body: Option<Value>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerJsonResponse {
    status: u16,
    body: Value,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerBlobResponse {
    mime_type: String,
    data_base64: String,
}

#[derive(Serialize, Clone)]
pub struct ServerSseEvent {
    event: String,
    data: Value,
}

#[tauri::command]
pub async fn server_request_json(request: ServerJsonRequest) -> Result<ServerJsonResponse, String> {
    let url = server_url(&request.base_url, &request.path)?;
    let method = server_method(&request.method)?;
    let token = request.access_token.as_deref().unwrap_or_default().trim();
    if token.len() > MAX_TOKEN_LENGTH || token.chars().any(char::is_control) {
        return Err("Server access token is invalid.".to_string());
    }
    if request.path != "/api/v1/player/auth/login" && !token.starts_with("omc_player_") {
        return Err("Server access token is required.".to_string());
    }

    let timeout = server_json_timeout(&method, &request.path);
    let client = reqwest::Client::builder()
        .timeout(timeout)
        .read_timeout(timeout)
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "Failed to initialize Server HTTP client.".to_string())?;
    let mut builder = client
        .request(method.clone(), url)
        .header(ACCEPT, "application/json");
    if !token.is_empty() {
        builder = builder.header(AUTHORIZATION, format!("Bearer {token}"));
    }
    if let Some(body) = request.body.as_ref() {
        let encoded =
            serde_json::to_vec(body).map_err(|_| "Server request body is invalid.".to_string())?;
        if encoded.len() > server_json_body_limit(&method, &request.path) {
            return Err("Server request body is too large.".to_string());
        }
        builder = builder
            .header(CONTENT_TYPE, "application/json")
            .body(encoded);
    } else if method == Method::POST || method == Method::DELETE {
        builder = builder
            .header(CONTENT_TYPE, "application/json")
            .body(Vec::new());
    }

    let response = builder.send().await.map_err(|error| {
        if error.is_timeout() {
            "连接 OhMyCine Server 超时。".to_string()
        } else if error.is_connect() {
            "无法连接 OhMyCine Server。".to_string()
        } else {
            "OhMyCine Server 网络请求失败。".to_string()
        }
    })?;
    let status = response.status();
    if status.is_redirection() {
        return Err("OhMyCine Server JSON 接口返回了不受信任的跳转。".to_string());
    }
    if response
        .content_length()
        .is_some_and(|size| size > MAX_RESPONSE_BODY_BYTES as u64)
    {
        return Err("OhMyCine Server 响应过大。".to_string());
    }
    let mut response = response;
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "读取 OhMyCine Server 响应失败。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_RESPONSE_BODY_BYTES {
            return Err("OhMyCine Server 响应过大。".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    let body = if bytes.is_empty() {
        Value::Null
    } else {
        serde_json::from_slice(&bytes)
            .map_err(|_| "OhMyCine Server 返回了无效 JSON。".to_string())?
    };
    Ok(ServerJsonResponse {
        status: status.as_u16(),
        body,
    })
}

fn json_request_timeout(path: &str) -> Duration {
    if path == "/api/v1/player/online-libraries"
        || path.starts_with("/api/v1/player/online-libraries/")
    {
        Duration::from_secs(60)
    } else {
        Duration::from_secs(20)
    }
}

#[tauri::command]
pub async fn server_request_blob(request: ServerJsonRequest) -> Result<ServerBlobResponse, String> {
    if !request.method.trim().eq_ignore_ascii_case("GET")
        || !request.path.starts_with("/api/v1/player/discovery/images/")
    {
        return Err("Server binary request path is not allowed.".to_string());
    }
    let url = server_url(&request.base_url, &request.path)?;
    let token = request.access_token.as_deref().unwrap_or_default().trim();
    if !token.starts_with("omc_player_")
        || token.len() > MAX_TOKEN_LENGTH
        || token.chars().any(char::is_control)
    {
        return Err("Server access token is required.".to_string());
    }
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(20))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "Failed to initialize Server HTTP client.".to_string())?;
    let response = client
        .get(url)
        .header(ACCEPT, "image/avif,image/webp,image/jpeg,image/png")
        .header(AUTHORIZATION, format!("Bearer {token}"))
        .send()
        .await
        .map_err(|_| "无法读取 Server 海报。".to_string())?;
    if !response.status().is_success() {
        return Err(format!(
            "Server 海报请求失败（HTTP {}）。",
            response.status().as_u16()
        ));
    }
    let mime_type = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .unwrap_or("")
        .trim()
        .to_ascii_lowercase();
    if !matches!(
        mime_type.as_str(),
        "image/jpeg" | "image/png" | "image/webp" | "image/avif"
    ) {
        return Err("Server 海报格式无效。".to_string());
    }
    let bytes = response
        .bytes()
        .await
        .map_err(|_| "读取 Server 海报失败。".to_string())?;
    if bytes.is_empty() || bytes.len() > 8 * 1024 * 1024 {
        return Err("Server 海报响应无效。".to_string());
    }
    Ok(ServerBlobResponse {
        mime_type,
        data_base64: STANDARD.encode(bytes),
    })
}

#[tauri::command]
pub async fn server_stream_sse(
    request: ServerJsonRequest,
    request_id: String,
    on_event: Channel<ServerSseEvent>,
    state: State<'_, ServerStreamState>,
) -> Result<(), String> {
    validate_stream_request(&request, &request_id)?;
    let (cancel_tx, cancel_rx) = watch::channel(false);
    {
        let mut streams = state
            .streams
            .lock()
            .map_err(|_| "Server 搜索取消状态暂不可用。".to_string())?;
        if !streams.register(&request_id, cancel_tx)? {
            return Ok(());
        }
    }

    let result = run_sse_stream(request, cancel_rx, &on_event).await;
    if let Ok(mut streams) = state.streams.lock() {
        streams.active.remove(&request_id);
    }
    result
}

#[tauri::command]
pub fn server_cancel_sse(
    request_id: String,
    state: State<'_, ServerStreamState>,
) -> Result<(), String> {
    if !valid_request_id(&request_id) {
        return Err("Server 搜索请求标识无效。".to_string());
    }
    let mut streams = state
        .streams
        .lock()
        .map_err(|_| "Server 搜索取消状态暂不可用。".to_string())?;
    streams.cancel(&request_id);
    Ok(())
}

async fn run_sse_stream(
    request: ServerJsonRequest,
    cancel_rx: watch::Receiver<bool>,
    on_event: &Channel<ServerSseEvent>,
) -> Result<(), String> {
    receive_sse_stream(
        request,
        cancel_rx,
        |event| {
            on_event
                .send(event)
                .map_err(|_| "无法向 Player 传递 Server 搜索进度。".to_string())
        },
        SseTiming::default(),
    )
    .await
}

async fn receive_sse_stream<F>(
    request: ServerJsonRequest,
    mut cancel_rx: watch::Receiver<bool>,
    mut on_event: F,
    timing: SseTiming,
) -> Result<(), String>
where
    F: FnMut(ServerSseEvent) -> Result<(), String>,
{
    let deadline = tokio::time::Instant::now() + timing.total;
    let url = server_url(&request.base_url, &request.path)?;
    let token = request.access_token.as_deref().unwrap_or_default().trim();
    let client = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "Failed to initialize Server HTTP client.".to_string())?;
    let send = client
        .get(url)
        .header(ACCEPT, "text/event-stream")
        .header(AUTHORIZATION, format!("Bearer {token}"))
        .send();
    let mut response = tokio::select! {
        biased;
        () = stream_cancelled(&mut cancel_rx) => return Ok(()),
        result = tokio::time::timeout_at(deadline, send) => {
            result.map_err(|_| "OhMyCine Server 搜索等待时间已用尽。".to_string())?
                .map_err(|error| {
                    if error.is_timeout() {
                        "连接 OhMyCine Server 搜索流超时。".to_string()
                    } else if error.is_connect() {
                        "无法连接 OhMyCine Server。".to_string()
                    } else {
                        "OhMyCine Server 搜索流请求失败。".to_string()
                    }
                })?
        }
    };
    if response.status().is_redirection() {
        return Err("OhMyCine Server 搜索流返回了不受信任的跳转。".to_string());
    }
    if !response.status().is_success() {
        return Err(format!(
            "OhMyCine Server 搜索失败（HTTP {}）。",
            response.status().as_u16()
        ));
    }
    let content_type = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if content_type.split(';').next().unwrap_or_default().trim() != "text/event-stream" {
        return Err("OhMyCine Server 返回了无效搜索流。".to_string());
    }

    let mut decoder = SseDecoder::default();
    loop {
        let chunk = tokio::select! {
            biased;
            () = stream_cancelled(&mut cancel_rx) => return Ok(()),
            () = tokio::time::sleep_until(deadline) => return Err("OhMyCine Server 搜索等待时间已用尽。".to_string()),
            result = tokio::time::timeout(timing.idle, response.chunk()) => {
                match result {
                    Ok(Ok(value)) => value,
                    Ok(Err(_)) => return Err("读取 OhMyCine Server 搜索流失败。".to_string()),
                    Err(_) => return Err("OhMyCine Server 搜索流长时间没有响应。".to_string()),
                }
            }
        };
        let Some(chunk) = chunk else {
            return decoder.finish(&mut on_event);
        };
        decoder.push(&chunk, &mut on_event)?;
        if decoder.finished {
            return Ok(());
        }
    }
}

async fn stream_cancelled(cancel_rx: &mut watch::Receiver<bool>) {
    loop {
        if *cancel_rx.borrow() || cancel_rx.changed().await.is_err() {
            return;
        }
    }
}

#[derive(Default)]
struct SseDecoder {
    buffer: Vec<u8>,
    total: usize,
    finished: bool,
}

impl SseDecoder {
    fn push<F>(&mut self, chunk: &[u8], on_event: &mut F) -> Result<(), String>
    where
        F: FnMut(ServerSseEvent) -> Result<(), String>,
    {
        self.total = self.total.saturating_add(chunk.len());
        if self.total > MAX_SSE_TOTAL_BYTES {
            return Err("OhMyCine Server 搜索流响应过大。".to_string());
        }
        self.buffer.extend_from_slice(chunk);
        let mut consumed = 0;
        while let Some((position, delimiter_length)) = find_sse_boundary(&self.buffer[consumed..]) {
            if let Some(event) = parse_sse_block(&self.buffer[consumed..consumed + position])? {
                self.finished = matches!(event.event.as_str(), "done" | "error");
                on_event(event)?;
            }
            consumed += position + delimiter_length;
            if self.finished {
                break;
            }
        }
        self.buffer.drain(..consumed);
        if self.buffer.len() > MAX_SSE_EVENT_BYTES {
            return Err("OhMyCine Server 搜索流事件过大。".to_string());
        }
        Ok(())
    }

    fn finish<F>(&self, on_event: &mut F) -> Result<(), String>
    where
        F: FnMut(ServerSseEvent) -> Result<(), String>,
    {
        if let Some(event) = parse_sse_block(&self.buffer)? {
            on_event(event)?;
        }
        Ok(())
    }
}

fn parse_sse_block(block: &[u8]) -> Result<Option<ServerSseEvent>, String> {
    if block.len() > MAX_SSE_EVENT_BYTES {
        return Err("OhMyCine Server 搜索流事件过大。".to_string());
    }
    let text =
        std::str::from_utf8(block).map_err(|_| "OhMyCine Server 搜索流编码无效。".to_string())?;
    let mut event = "message".to_string();
    let mut data = Vec::new();
    for line in text.lines() {
        if let Some(value) = line.strip_prefix("event:") {
            event = value.trim().to_string();
        } else if let Some(value) = line.strip_prefix("data:") {
            data.push(value.trim_start());
        }
    }
    if !matches!(
        event.as_str(),
        "media" | "progress" | "site" | "done" | "error"
    ) {
        return Ok(None);
    }
    let payload = serde_json::from_str::<Value>(&data.join("\n"))
        .map_err(|_| "OhMyCine Server 搜索流 JSON 无效。".to_string())?;
    Ok(Some(ServerSseEvent {
        event,
        data: payload,
    }))
}

fn find_sse_boundary(buffer: &[u8]) -> Option<(usize, usize)> {
    buffer
        .windows(2)
        .enumerate()
        .find_map(|(position, window)| {
            if window == b"\n\n" {
                Some((position, 2))
            } else if window == b"\r\n" && buffer[position..].starts_with(b"\r\n\r\n") {
                Some((position, 4))
            } else {
                None
            }
        })
}

fn validate_stream_request(request: &ServerJsonRequest, request_id: &str) -> Result<(), String> {
    if !request.method.trim().eq_ignore_ascii_case("GET")
        || request.body.is_some()
        || !valid_request_id(request_id)
        || !allowed_discovery_stream_path(&request.path)
    {
        return Err("Server 搜索流请求无效。".to_string());
    }
    let token = request.access_token.as_deref().unwrap_or_default().trim();
    if !token.starts_with("omc_player_")
        || token.len() > MAX_TOKEN_LENGTH
        || token.chars().any(char::is_control)
    {
        return Err("Server access token is required.".to_string());
    }
    server_url(&request.base_url, &request.path).map(|_| ())
}

fn valid_request_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || character == '-')
}

fn allowed_discovery_stream_path(path: &str) -> bool {
    let route = path.split_once('?').map_or(path, |(value, _)| value);
    route == "/api/v1/player/discovery/torrent-search/stream"
        || route
            .strip_prefix("/api/v1/player/discovery/media/")
            .and_then(|tail| tail.strip_suffix("/torrent-search/stream"))
            .and_then(|identity| identity.split_once('/'))
            .is_some_and(|(media_type, id)| {
                matches!(media_type, "movie" | "tv")
                    && id.parse::<u64>().is_ok_and(|value| value > 0)
                    && id.bytes().all(|value| value.is_ascii_digit())
            })
}

fn server_url(base_url: &str, path: &str) -> Result<Url, String> {
    let base = base_url.trim().trim_end_matches('/');
    if base.is_empty() || base.len() > MAX_BASE_URL_LENGTH || base.chars().any(char::is_control) {
        return Err("OhMyCine Server 地址无效。".to_string());
    }
    let parsed = Url::parse(base).map_err(|_| "OhMyCine Server 地址无效。".to_string())?;
    if !matches!(parsed.scheme(), "http" | "https")
        || parsed.host_str().is_none()
        || !parsed.username().is_empty()
        || parsed.password().is_some()
        || parsed.query().is_some()
        || parsed.fragment().is_some()
        || (parsed.path() != "" && parsed.path() != "/")
    {
        return Err("OhMyCine Server 地址无效。".to_string());
    }
    let path = path.trim();
    let route_path = path.split_once('?').map_or(path, |(route, _)| route);
    let normalized_route_path = route_path.to_ascii_lowercase();
    if path.is_empty()
        || path.len() > MAX_PATH_LENGTH
        || !path.starts_with("/api/v1/player/")
        || path.contains("..")
        || path.contains('\\')
        || path.contains('#')
        || ["%2e", "%2f", "%5c"]
            .iter()
            .any(|encoded| normalized_route_path.contains(encoded))
        || path.chars().any(char::is_control)
    {
        return Err("OhMyCine Server API 路径无效。".to_string());
    }
    Url::parse(&format!("{base}{path}")).map_err(|_| "OhMyCine Server API 地址无效。".to_string())
}

fn server_method(method: &str) -> Result<Method, String> {
    match method.trim().to_ascii_uppercase().as_str() {
        "GET" => Ok(Method::GET),
        "POST" => Ok(Method::POST),
        "PUT" => Ok(Method::PUT),
        "DELETE" => Ok(Method::DELETE),
        _ => Err("OhMyCine Server 请求方法无效。".to_string()),
    }
}

fn server_json_timeout(method: &Method, path: &str) -> Duration {
    if *method == Method::POST && path == "/api/v1/player/discovery/torrent-results/recognize" {
        Duration::from_secs(60)
    } else if *method == Method::POST && path == "/api/v1/player/discovery/share-preview" {
        Duration::from_secs(50)
    } else {
        json_request_timeout(path.split('?').next().unwrap_or(path))
    }
}

fn server_json_body_limit(method: &Method, path: &str) -> usize {
    if *method == Method::POST && path == "/api/v1/player/discovery/downloads" {
        256 * 1024
    } else {
        MAX_REQUEST_BODY_BYTES
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn share_preview_limits_are_scoped_to_exact_player_methods_and_paths() {
        assert_eq!(
            server_json_timeout(
                &Method::POST,
                "/api/v1/player/discovery/torrent-results/recognize"
            ),
            Duration::from_secs(60)
        );
        for (method, path) in [
            (
                Method::GET,
                "/api/v1/player/discovery/torrent-results/recognize",
            ),
            (
                Method::POST,
                "/api/v1/player/discovery/torrent-results/recognize?extra=1",
            ),
        ] {
            assert_eq!(server_json_timeout(&method, path), Duration::from_secs(20));
        }
        assert_eq!(
            server_json_timeout(&Method::GET, "/api/v1/player/online-libraries?limit=10"),
            Duration::from_secs(60)
        );
        assert_eq!(
            server_json_timeout(
                &Method::POST,
                "/api/v1/player/online-libraries/id/items/work/playback"
            ),
            Duration::from_secs(60)
        );
        assert_eq!(
            server_json_timeout(&Method::POST, "/api/v1/player/discovery/share-preview"),
            Duration::from_secs(50)
        );
        assert_eq!(
            server_json_timeout(&Method::GET, "/api/v1/player/discovery/share-preview"),
            Duration::from_secs(20)
        );
        assert_eq!(
            server_json_timeout(
                &Method::POST,
                "/api/v1/player/discovery/share-preview/recognize"
            ),
            Duration::from_secs(20)
        );
        assert_eq!(
            server_json_timeout(
                &Method::POST,
                "/api/v1/player/discovery/share-preview?extra=1"
            ),
            Duration::from_secs(20)
        );
        assert_eq!(
            server_json_body_limit(&Method::POST, "/api/v1/player/discovery/downloads"),
            256 * 1024
        );
        assert_eq!(
            server_json_body_limit(&Method::PUT, "/api/v1/player/discovery/downloads"),
            MAX_REQUEST_BODY_BYTES
        );
        assert_eq!(
            server_json_body_limit(&Method::POST, "/api/v1/player/discovery/follows"),
            MAX_REQUEST_BODY_BYTES
        );
    }

    #[test]
    fn only_online_library_json_uses_the_plugin_resolution_timeout() {
        assert_eq!(
            json_request_timeout("/api/v1/player/online-libraries"),
            Duration::from_secs(60)
        );
        assert_eq!(
            json_request_timeout("/api/v1/player/online-libraries/id/items/work/playback"),
            Duration::from_secs(60)
        );
        for path in [
            "/api/v1/player/bootstrap",
            "/api/v1/player/online-libraries-other",
            "/api/v1/player/media-entries/1",
        ] {
            assert_eq!(json_request_timeout(path), Duration::from_secs(20));
        }
    }

    #[test]
    fn server_url_accepts_only_player_api_paths() {
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/bootstrap").is_ok());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/users").is_err());
        assert!(server_url("http://127.0.0.1:3000/base", "/api/v1/player/bootstrap").is_err());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/%2e%2e/users").is_err());
        assert!(server_url(
            "http://127.0.0.1:3000",
            "/api/v1/player/catalog%2F..%2Fusers"
        )
        .is_err());
        assert!(server_url(
            "http://127.0.0.1:3000",
            "/api/v1/player/catalog%5c..%5cusers"
        )
        .is_err());
        assert!(server_url(
            "http://127.0.0.1:3000",
            "/api/v1/player/search?query=%E4%B8%83%E6%AD%A6%E5%A3%AB"
        )
        .is_ok());
        assert!(server_url(
            "http://user:password@127.0.0.1:3000",
            "/api/v1/player/bootstrap"
        )
        .is_err());
    }

    #[test]
    fn discovery_stream_allowlist_is_narrow() {
        assert!(allowed_discovery_stream_path(
            "/api/v1/player/discovery/torrent-search/stream?keyword=test"
        ));
        assert!(allowed_discovery_stream_path(
            "/api/v1/player/discovery/media/movie/346/torrent-search/stream?site_ids=1"
        ));
        assert!(!allowed_discovery_stream_path(
            "/api/v1/player/online-assets/private/stream"
        ));
        assert!(!allowed_discovery_stream_path(
            "/api/v1/users/torrent-search/stream"
        ));
        for path in [
            "/api/v1/player/discovery/media/movie/0/torrent-search/stream",
            "/api/v1/player/discovery/media/movie/id/torrent-search/stream",
            "/api/v1/player/discovery/media/person/346/torrent-search/stream",
            "/api/v1/player/discovery/media/movie/346/extra/torrent-search/stream",
        ] {
            assert!(!allowed_discovery_stream_path(path));
        }
    }

    #[test]
    fn sse_boundary_supports_lf_and_crlf() {
        assert_eq!(
            find_sse_boundary(b"event: done\ndata: {}\n\nrest"),
            Some((20, 2))
        );
        assert_eq!(
            find_sse_boundary(b"event: done\r\ndata: {}\r\n\r\nrest"),
            Some((21, 4))
        );
        assert_eq!(
            find_sse_boundary(b": heartbeat\r\n\r\nevent: done\ndata: {}\n\n"),
            Some((11, 4))
        );
    }

    fn stream_request(base_url: String) -> ServerJsonRequest {
        ServerJsonRequest {
            base_url,
            method: "GET".to_string(),
            path: "/api/v1/player/discovery/torrent-search/stream?progress_version=2&site_ids=1"
                .to_string(),
            access_token: Some(format!("omc_player_{}", "a".repeat(43))),
            body: None,
        }
    }

    #[test]
    fn stream_request_rejects_unsafe_method_body_and_credentials() {
        let mut request = stream_request("http://127.0.0.1:3000".to_string());
        assert!(validate_stream_request(&request, "request-1").is_ok());
        request.method = "POST".to_string();
        assert!(validate_stream_request(&request, "request-1").is_err());
        request.method = "GET".to_string();
        request.body = Some(serde_json::json!({}));
        assert!(validate_stream_request(&request, "request-1").is_err());
        request.body = None;
        request.access_token = Some("invalid".to_string());
        assert!(validate_stream_request(&request, "request-1").is_err());
        assert_eq!(SSE_TOTAL_TIMEOUT, Duration::from_secs(390));
        assert_eq!(
            json_request_timeout("/api/v1/player/discovery/torrent-search"),
            Duration::from_secs(20)
        );
    }

    #[test]
    fn native_cancellation_can_overtake_async_stream_registration_without_leaking_ids() {
        let mut streams = ServerStreams::default();
        streams.cancel("early");
        let (sender, _) = watch::channel(false);
        assert!(!streams.register("early", sender).unwrap());
        assert!(streams.active.is_empty());
        let (sender, receiver) = watch::channel(false);
        assert!(streams.register("running", sender).unwrap());
        streams.cancel("running");
        assert!(*receiver.borrow());
        for index in 0..MAX_EARLY_STREAM_CANCELLATIONS + 5 {
            streams.cancel(&format!("cancelled-{index}"));
        }
        assert!(streams.cancelled_before_start.len() <= MAX_EARLY_STREAM_CANCELLATIONS);
        streams.cancelled_before_start.insert(
            "expired".to_string(),
            std::time::Instant::now() - SSE_TOTAL_TIMEOUT - Duration::from_secs(1),
        );
        let (sender, _) = watch::channel(false);
        assert!(streams.register("expired", sender).unwrap());
    }

    #[test]
    fn sse_decoder_ignores_heartbeats_and_unknown_events_but_rejects_invalid_json() {
        let mut decoder = SseDecoder::default();
        let mut events = Vec::new();
        let mut emit = |event| {
            events.push(event);
            Ok(())
        };
        decoder
            .push(
                b": heartbeat\r\n\r\nevent: site\ndata: {\"items\":[]}",
                &mut emit,
            )
            .unwrap();
        decoder
            .push(
                b"\n\nevent: private\ndata: not-json\n\nevent: done\r\ndata: {}\r\n\r\n",
                &mut emit,
            )
            .unwrap();
        assert_eq!(events.len(), 2);
        assert_eq!(events[0].event, "site");
        assert_eq!(events[1].event, "done");
        assert!(SseDecoder::default()
            .push(b"event: site\ndata: {bad}\n\n", &mut |_| Ok(()))
            .unwrap_err()
            .contains("JSON"));
        assert!(parse_sse_block(&[0xff]).is_err());
    }

    #[test]
    fn sse_decoder_keeps_event_and_whole_stream_limits() {
        let mut decoder = SseDecoder::default();
        assert!(decoder
            .push(&vec![b'x'; MAX_SSE_EVENT_BYTES + 1], &mut |_| Ok(()))
            .unwrap_err()
            .contains("事件过大"));
        let mut decoder = SseDecoder::default();
        let chunk = b": heartbeat\n\n".repeat(4096);
        let mut accepted = 0;
        while accepted + chunk.len() <= MAX_SSE_TOTAL_BYTES {
            decoder.push(&chunk, &mut |_| Ok(())).unwrap();
            accepted += chunk.len();
        }
        assert!(decoder
            .push(&chunk, &mut |_| Ok(()))
            .unwrap_err()
            .contains("响应过大"));
    }

    async fn local_stream(
        headers: Option<&'static str>,
        chunks: Vec<(Duration, &'static str)>,
    ) -> (
        String,
        tokio::sync::oneshot::Receiver<()>,
        tokio::task::JoinHandle<()>,
    ) {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (accepted_tx, accepted_rx) = tokio::sync::oneshot::channel();
        let task = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = Vec::new();
            let mut buffer = [0; 1024];
            while !request.windows(4).any(|part| part == b"\r\n\r\n") {
                let count = stream.read(&mut buffer).await.unwrap();
                if count == 0 {
                    return;
                }
                request.extend_from_slice(&buffer[..count]);
                assert!(request.len() < 8192);
            }
            assert!(String::from_utf8_lossy(&request).contains("progress_version=2"));
            let _ = accepted_tx.send(());
            if let Some(headers) = headers {
                if stream.write_all(headers.as_bytes()).await.is_err() {
                    return;
                }
                for (delay, chunk) in chunks {
                    tokio::time::sleep(delay).await;
                    if stream.write_all(chunk.as_bytes()).await.is_err() {
                        return;
                    }
                }
            }
            // Leave the socket open so cancellation/idle/total limits can be observed.
            std::future::pending::<()>().await;
        });
        (format!("http://{address}"), accepted_rx, task)
    }

    const SSE_HEADERS: &str =
        "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n";

    #[tokio::test]
    async fn cancellation_interrupts_header_wait_and_body_read() {
        for headers in [None, Some(SSE_HEADERS)] {
            let (url, accepted, server) = local_stream(headers, vec![]).await;
            let (cancel_tx, cancel_rx) = watch::channel(false);
            let request = stream_request(url);
            let reader = tokio::spawn(receive_sse_stream(
                request,
                cancel_rx,
                |_| Ok(()),
                SseTiming::default(),
            ));
            tokio::time::timeout(Duration::from_secs(2), accepted)
                .await
                .unwrap()
                .unwrap();
            cancel_tx.send(true).unwrap();
            assert!(tokio::time::timeout(Duration::from_secs(1), reader)
                .await
                .unwrap()
                .unwrap()
                .is_ok());
            server.abort();
        }
    }

    #[tokio::test]
    async fn body_idle_failure_preserves_delivered_snapshot() {
        let (url, _, server) = local_stream(
            Some(SSE_HEADERS),
            vec![(
                Duration::ZERO,
                "event: site\ndata: {\"items\":[{\"token\":\"a\"}]}\n\n",
            )],
        )
        .await;
        let (_cancel_tx, cancel_rx) = watch::channel(false);
        let mut events = Vec::new();
        let result = receive_sse_stream(
            stream_request(url),
            cancel_rx,
            |event| {
                events.push(event);
                Ok(())
            },
            SseTiming {
                idle: Duration::from_millis(80),
                total: Duration::from_secs(2),
            },
        )
        .await;
        assert!(result.unwrap_err().contains("长时间没有响应"));
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].data["items"][0]["token"], "a");
        server.abort();
    }

    #[tokio::test]
    async fn cumulative_batches_arrive_before_done_and_body_can_be_cancelled() {
        let chunks = vec![
            (
                Duration::ZERO,
                ": heartbeat\r\n\r\nevent: site\ndata: {\"items\":[{\"token\":\"a\"}]}\n\n",
            ),
            (
                Duration::from_millis(10),
                "event: site\ndata: {\"items\":[{\"token\":\"a\"},{\"token\":\"b\"}]}\n\n",
            ),
            (
                Duration::from_millis(10),
                "event: done\ndata: {\"completed\":1}\n\n",
            ),
        ];
        let (url, _, server) = local_stream(Some(SSE_HEADERS), chunks).await;
        let (_cancel_tx, cancel_rx) = watch::channel(false);
        let mut events = Vec::new();
        receive_sse_stream(
            stream_request(url),
            cancel_rx,
            |event| {
                events.push(event);
                Ok(())
            },
            SseTiming {
                idle: Duration::from_secs(1),
                total: Duration::from_secs(2),
            },
        )
        .await
        .unwrap();
        assert_eq!(
            events
                .iter()
                .map(|event| event.event.as_str())
                .collect::<Vec<_>>(),
            vec!["site", "site", "done"]
        );
        assert_eq!(events[0].data["items"].as_array().unwrap().len(), 1);
        assert_eq!(events[1].data["items"].as_array().unwrap().len(), 2);
        server.abort();

        let (url, _, server) = local_stream(
            Some(SSE_HEADERS),
            vec![(Duration::ZERO, "event: site\ndata: {\"items\":[]}\n\n")],
        )
        .await;
        let (cancel_tx, cancel_rx) = watch::channel(false);
        let mut delivered = false;
        receive_sse_stream(
            stream_request(url),
            cancel_rx,
            |_| {
                delivered = true;
                cancel_tx.send(true).unwrap();
                Ok(())
            },
            SseTiming::default(),
        )
        .await
        .unwrap();
        assert!(
            delivered,
            "Cancel was issued only after the body delivered an event"
        );
        server.abort();
    }

    #[tokio::test]
    async fn total_deadline_applies_to_headers_and_heartbeat_streams() {
        for headers in [None, Some(SSE_HEADERS)] {
            let chunks = (0..30)
                .map(|_| (Duration::from_millis(10), ": heartbeat\n\n"))
                .collect();
            let (url, _, server) = local_stream(headers, chunks).await;
            let (_cancel_tx, cancel_rx) = watch::channel(false);
            let result = receive_sse_stream(
                stream_request(url),
                cancel_rx,
                |_| Ok(()),
                SseTiming {
                    idle: Duration::from_secs(1),
                    total: Duration::from_millis(90),
                },
            )
            .await;
            assert!(result.unwrap_err().contains("等待时间已用尽"));
            server.abort();
        }
    }

    #[tokio::test]
    async fn stream_rejects_redirect_and_non_sse_response() {
        for headers in [
            "HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/private\r\nContent-Length: 0\r\n\r\n",
            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}",
        ] {
            let (url, _, server) = local_stream(Some(headers), vec![]).await;
            let (_cancel_tx, cancel_rx) = watch::channel(false);
            let result = receive_sse_stream(stream_request(url), cancel_rx, |_| Ok(()), SseTiming::default()).await;
            assert!(result.is_err());
            server.abort();
        }
    }
}

use reqwest::header::{HeaderMap, HeaderValue, ACCEPT, AUTHORIZATION, CONTENT_TYPE, USER_AGENT};
use reqwest::{StatusCode, Url};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use std::time::Duration;

const HTTP_TIMEOUT_SECONDS: u64 = 15;
const MAX_BASE_URL_LENGTH: usize = 2048;
const MAX_PATH_LENGTH: usize = 2048;
const MAX_HEADER_VALUE_LENGTH: usize = 4096;
const MAX_ERROR_BODY_CHARS: usize = 700;
const MAX_QUERY_PARAMETERS: usize = 96;
const MAX_REQUEST_BODY_BYTES: usize = 512 * 1024;
const MAX_RESPONSE_BODY_BYTES: usize = 4 * 1024 * 1024;
const EMBY_CLIENT_VERSION: &str = env!("CARGO_PKG_VERSION");

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EmbyJsonRequest {
    method: String,
    base_url: String,
    path: String,
    query: Option<Map<String, Value>>,
    body: Option<Value>,
    token: Option<String>,
    user_id: Option<String>,
    device_id: String,
    auth_mode: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EmbyPlaybackJsonRequest {
    base_url: String,
    path: String,
    query: Option<Map<String, Value>>,
    body: Option<Value>,
    token: String,
    user_id: String,
    device_id: String,
    auth_mode: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EmbyPlaybackJsonResponse {
    status: u16,
    body: Value,
}

#[tauri::command]
pub async fn emby_request_json(
    request: EmbyJsonRequest,
) -> Result<EmbyPlaybackJsonResponse, String> {
    execute_json_request(request).await
}

#[tauri::command]
pub async fn emby_post_playback_json(
    request: EmbyPlaybackJsonRequest,
) -> Result<EmbyPlaybackJsonResponse, String> {
    execute_json_request(EmbyJsonRequest {
        method: "POST".to_string(),
        base_url: request.base_url,
        path: request.path,
        query: request.query,
        body: request.body,
        token: Some(request.token),
        user_id: Some(request.user_id),
        device_id: request.device_id,
        auth_mode: request.auth_mode,
    })
    .await
}

async fn execute_json_request(
    request: EmbyJsonRequest,
) -> Result<EmbyPlaybackJsonResponse, String> {
    let url = build_url(&request.base_url, &request.path, request.query.as_ref())?;
    let headers = build_headers(
        request.token.as_deref(),
        request.user_id.as_deref(),
        &request.device_id,
        request.auth_mode.as_deref(),
    )?;
    let method = normalize_method(&request.method)?;
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "Failed to initialize Emby native HTTP client.".to_string())?;

    let mut builder = client.request(method.clone(), url).headers(headers);
    builder = if let Some(body) = request.body.as_ref() {
        let encoded =
            serde_json::to_vec(body).map_err(|_| "Invalid Emby request body.".to_string())?;
        if encoded.len() > MAX_REQUEST_BODY_BYTES {
            return Err("Emby request body is too large.".to_string());
        }
        builder.json(body)
    } else if method == reqwest::Method::POST {
        builder
            .header(CONTENT_TYPE, "application/json")
            .body(Vec::new())
    } else {
        builder
    };

    let response = builder.send().await.map_err(|error| {
        if error.is_timeout() {
            "Network error while contacting Emby endpoint (timeout).".to_string()
        } else if error.is_connect() {
            "Network error while contacting Emby endpoint (connect failed).".to_string()
        } else {
            "Network error while contacting Emby endpoint.".to_string()
        }
    })?;

    let status = response.status();
    if response
        .content_length()
        .is_some_and(|length| length > MAX_RESPONSE_BODY_BYTES as u64)
    {
        if !status.is_success() {
            return Err(format!(
                "HTTP {} from Emby endpoint. Body: [response too large]",
                status.as_u16()
            ));
        }
        return Err("Emby response was too large.".to_string());
    }

    let text = read_limited_response_body(response).await?;

    if !status.is_success() {
        return Err(http_error_message(
            status,
            &text,
            &request.base_url,
            request.token.as_deref().unwrap_or_default(),
            request.user_id.as_deref().unwrap_or_default(),
        ));
    }

    let body = if text.trim().is_empty() {
        Value::Null
    } else {
        serde_json::from_str::<Value>(&text).unwrap_or(Value::String(text))
    };

    Ok(EmbyPlaybackJsonResponse {
        status: status.as_u16(),
        body,
    })
}

async fn read_limited_response_body(mut response: reqwest::Response) -> Result<String, String> {
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "Failed to read Emby response.".to_string())?
    {
        if body.len().saturating_add(chunk.len()) > MAX_RESPONSE_BODY_BYTES {
            return Err("Emby response was too large.".to_string());
        }
        body.extend_from_slice(&chunk);
    }
    String::from_utf8(body).map_err(|_| "Emby response was not valid UTF-8.".to_string())
}

fn normalize_method(method: &str) -> Result<reqwest::Method, String> {
    match method.trim().to_ascii_uppercase().as_str() {
        "GET" => Ok(reqwest::Method::GET),
        "POST" => Ok(reqwest::Method::POST),
        "DELETE" => Ok(reqwest::Method::DELETE),
        _ => Err("Invalid Emby request method.".to_string()),
    }
}

fn build_url(
    base_url: &str,
    path: &str,
    query: Option<&Map<String, Value>>,
) -> Result<Url, String> {
    let normalized_base = normalize_base_url(base_url)?;
    let normalized_path = normalize_path(path)?;
    let mut url = Url::parse(&format!("{normalized_base}{normalized_path}"))
        .map_err(|_| "Invalid Emby endpoint.".to_string())?;

    if let Some(query) = query {
        if query.len() > MAX_QUERY_PARAMETERS {
            return Err("Invalid Emby query.".to_string());
        }
        let mut pairs = url.query_pairs_mut();
        for (key, value) in query {
            if key.trim().is_empty()
                || key.len() > MAX_HEADER_VALUE_LENGTH
                || contains_control_character(key)
            {
                return Err("Invalid Emby query.".to_string());
            }
            if let Some(value) = query_value_to_string(value) {
                if value.len() > MAX_HEADER_VALUE_LENGTH || contains_control_character(&value) {
                    return Err("Invalid Emby query.".to_string());
                }
                pairs.append_pair(key, &value);
            }
        }
    }

    Ok(url)
}

fn normalize_base_url(base_url: &str) -> Result<String, String> {
    let trimmed = base_url.trim().trim_end_matches('/');
    if trimmed.is_empty()
        || trimmed.len() > MAX_BASE_URL_LENGTH
        || contains_control_character(trimmed)
    {
        return Err("Invalid Emby server URL.".to_string());
    }

    let parsed = Url::parse(trimmed).map_err(|_| "Invalid Emby server URL.".to_string())?;
    if parsed.scheme() != "http" && parsed.scheme() != "https" {
        return Err("Invalid Emby server URL.".to_string());
    }
    if parsed.username() != ""
        || parsed.password().is_some()
        || parsed.host_str().is_none()
        || parsed.query().is_some()
        || parsed.fragment().is_some()
    {
        return Err("Invalid Emby server URL.".to_string());
    }

    Ok(trimmed.to_string())
}

fn normalize_path(path: &str) -> Result<String, String> {
    let trimmed = path.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_PATH_LENGTH
        || contains_control_character(trimmed)
        || !trimmed.starts_with('/')
        || trimmed.starts_with("//")
        || trimmed.contains("://")
        || trimmed.contains('?')
        || trimmed.contains('#')
        || trimmed
            .split('/')
            .any(|segment| matches!(segment, "." | ".."))
    {
        return Err("Invalid Emby path.".to_string());
    }

    Ok(trimmed.to_string())
}

fn build_headers(
    token: Option<&str>,
    user_id: Option<&str>,
    device_id: &str,
    auth_mode: Option<&str>,
) -> Result<HeaderMap, String> {
    let token = normalize_optional_header_value(token, "Invalid Emby token.")?;
    let user_id = normalize_optional_header_value(user_id, "Invalid Emby user.")?;
    let device_id = normalize_header_value(device_id, "Invalid Emby device.")?;
    let auth_mode = auth_mode.unwrap_or("default");
    let default_auth = authorization_header(&device_id, token.as_deref(), None);
    let official_auth = authorization_header(&device_id, token.as_deref(), user_id.as_deref());

    let mut headers = HeaderMap::new();
    headers.insert(ACCEPT, HeaderValue::from_static("application/json"));
    headers.insert(
        USER_AGENT,
        HeaderValue::from_static(concat!("OhMyCine Player/", env!("CARGO_PKG_VERSION"))),
    );
    if let Some(token) = token.as_deref() {
        headers.insert(
            "x-emby-token",
            HeaderValue::from_str(token).map_err(|_| "Invalid Emby token.".to_string())?,
        );
    }

    match auth_mode {
        "default" => {
            headers.insert(
                "x-emby-authorization",
                HeaderValue::from_str(&default_auth)
                    .map_err(|_| "Invalid Emby authorization.".to_string())?,
            );
        }
        "official-compatible" => {
            let token = token
                .as_deref()
                .ok_or_else(|| "Invalid Emby token.".to_string())?;
            if user_id.is_none() {
                return Err("Invalid Emby user.".to_string());
            }
            headers.insert(
                AUTHORIZATION,
                HeaderValue::from_str(&official_auth)
                    .map_err(|_| "Invalid Emby authorization.".to_string())?,
            );
            headers.insert(
                "x-mediabrowser-token",
                HeaderValue::from_str(token).map_err(|_| "Invalid Emby token.".to_string())?,
            );
            headers.insert(
                "x-emby-authorization",
                HeaderValue::from_str(&official_auth)
                    .map_err(|_| "Invalid Emby authorization.".to_string())?,
            );
        }
        _ => return Err("Invalid Emby auth mode.".to_string()),
    }

    Ok(headers)
}

fn normalize_optional_header_value(
    value: Option<&str>,
    message: &str,
) -> Result<Option<String>, String> {
    match value.map(str::trim).filter(|value| !value.is_empty()) {
        Some(value) => normalize_header_value(value, message).map(Some),
        None => Ok(None),
    }
}

fn normalize_header_value(value: &str, message: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_HEADER_VALUE_LENGTH
        || contains_control_character(trimmed)
    {
        Err(message.to_string())
    } else {
        Ok(trimmed.to_string())
    }
}

fn authorization_header(device_id: &str, token: Option<&str>, user_id: Option<&str>) -> String {
    let user_segment = user_id
        .map(|value| format!(", UserId=\"{value}\""))
        .unwrap_or_default();
    let token_segment = token
        .map(|value| format!(", Token=\"{value}\""))
        .unwrap_or_default();
    format!(
        "MediaBrowser Client=\"OhMyCine Player\", Device=\"Desktop\", DeviceId=\"{device_id}\", Version=\"{EMBY_CLIENT_VERSION}\"{user_segment}{token_segment}"
    )
}

fn query_value_to_string(value: &Value) -> Option<String> {
    match value {
        Value::Null => None,
        Value::Bool(value) => Some(value.to_string()),
        Value::Number(value) => Some(value.to_string()),
        Value::String(value) => {
            if value.is_empty() {
                None
            } else {
                Some(value.clone())
            }
        }
        Value::Array(_) | Value::Object(_) => None,
    }
}

fn http_error_message(
    status: StatusCode,
    body: &str,
    base_url: &str,
    token: &str,
    user_id: &str,
) -> String {
    let mut message = format!("HTTP {} from Emby endpoint.", status.as_u16());
    let snippet = safe_body_snippet(body, base_url, token, user_id);
    if !snippet.is_empty() {
        message.push_str(" Body: ");
        message.push_str(&snippet);
    }
    message
}

fn safe_body_snippet(body: &str, base_url: &str, token: &str, user_id: &str) -> String {
    let collapsed = body
        .chars()
        .map(|ch| if ch.is_control() { ' ' } else { ch })
        .collect::<String>();
    let clipped = collapsed
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
        .chars()
        .take(MAX_ERROR_BODY_CHARS)
        .collect::<String>();
    redact_sensitive_text(&clipped, base_url, token, user_id)
}

fn redact_sensitive_text(value: &str, base_url: &str, token: &str, user_id: &str) -> String {
    let mut redacted = value.to_string();
    if !token.is_empty() {
        redacted = redacted.replace(token, "[redacted]");
    }
    if !user_id.is_empty() {
        redacted = redacted.replace(user_id, "[redacted-user]");
    }
    if let Ok(url) = Url::parse(base_url) {
        if let Some(host) = url.host_str() {
            redacted = redacted.replace(host, "[redacted-host]");
        }
    }

    redacted = redact_url_hosts(&redacted);
    for key in [
        "api_key",
        "apikey",
        "access_token",
        "token",
        "Authorization",
        "X-Emby-Authorization",
        "X-Emby-Token",
        "X-MediaBrowser-Token",
        "UserId",
        "userId",
        "password",
        "signature",
        "sign",
        "sig",
    ] {
        redacted = redact_known_key_values(&redacted, key);
    }
    redacted
}

fn redact_url_hosts(value: &str) -> String {
    let mut output = String::with_capacity(value.len());
    let mut index = 0;
    while index < value.len() {
        let rest = &value[index..];
        let next_http = rest.find("http://");
        let next_https = rest.find("https://");
        let Some(relative_start) = min_option(next_http, next_https) else {
            output.push_str(rest);
            break;
        };
        let start = index + relative_start;
        output.push_str(&value[index..start]);
        let scheme_len = if value[start..].starts_with("https://") {
            8
        } else {
            7
        };
        output.push_str(&value[start..start + scheme_len]);
        output.push_str("[redacted-host]");
        let host_start = start + scheme_len;
        let host_end = value[host_start..]
            .find(['/', '?', '#', ' ', '"', '\'', ')', ','])
            .map(|relative| host_start + relative)
            .unwrap_or(value.len());
        index = host_end;
    }
    output
}

fn min_option(left: Option<usize>, right: Option<usize>) -> Option<usize> {
    match (left, right) {
        (Some(left), Some(right)) => Some(left.min(right)),
        (Some(left), None) => Some(left),
        (None, Some(right)) => Some(right),
        (None, None) => None,
    }
}

fn redact_known_key_values(value: &str, key: &str) -> String {
    let mut redacted = value.to_string();
    for separator in ["=", ":", "=\"", ":\"", "='", ":'"] {
        redacted = redact_after_pattern(&redacted, &format!("{key}{separator}"));
    }
    redacted
}

fn redact_after_pattern(value: &str, pattern: &str) -> String {
    let lower = value.to_ascii_lowercase();
    let needle = pattern.to_ascii_lowercase();
    let mut output = String::with_capacity(value.len());
    let mut index = 0;

    while let Some(relative_start) = lower[index..].find(&needle) {
        let start = index + relative_start;
        let value_start = start + pattern.len();
        let value_end = value[value_start..]
            .find(['&', ',', ';', ' ', '"', '\'', '}', ']'])
            .map(|relative| value_start + relative)
            .unwrap_or(value.len());
        output.push_str(&value[index..value_start]);
        output.push_str("[redacted]");
        index = value_end;
    }

    output.push_str(&value[index..]);
    output
}

fn contains_control_character(value: &str) -> bool {
    value.chars().any(|ch| ch.is_control())
}

#[cfg(test)]
mod tests {
    use super::{execute_json_request, EmbyJsonRequest, MAX_RESPONSE_BODY_BYTES};
    use axum::{
        body::Body,
        extract::Query,
        http::{HeaderMap, StatusCode},
        response::{IntoResponse, Redirect, Response},
        routing::get,
        Json, Router,
    };
    use serde_json::{json, Map, Value};
    use std::collections::HashMap;
    use tokio::net::TcpListener;

    #[tokio::test]
    async fn generic_get_uses_bounded_native_client_and_auth_headers() {
        let router = Router::new().route(
            "/System/Info",
            get(
                |headers: HeaderMap, Query(query): Query<HashMap<String, String>>| async move {
                    assert_eq!(
                        headers
                            .get("x-emby-token")
                            .and_then(|value| value.to_str().ok()),
                        Some("test-token")
                    );
                    assert_eq!(query.get("Fields").map(String::as_str), Some("Overview"));
                    Json(json!({ "ServerName": "Test" }))
                },
            ),
        );
        let base_url = serve(router).await;
        let response = execute_json_request(request(
            "GET",
            &base_url,
            "/System/Info",
            Some(Map::from_iter([(
                "Fields".to_string(),
                Value::String("Overview".to_string()),
            )])),
        ))
        .await
        .expect("native Emby GET should succeed");

        assert_eq!(response.status, 200);
        assert_eq!(response.body["ServerName"], "Test");
    }

    #[tokio::test]
    async fn redirects_are_rejected_instead_of_followed() {
        let router = Router::new()
            .route(
                "/redirect",
                get(|| async { Redirect::temporary("/target") }),
            )
            .route(
                "/target",
                get(|| async { Json(json!({ "followed": true })) }),
            );
        let base_url = serve(router).await;

        let error = execute_json_request(request("GET", &base_url, "/redirect", None))
            .await
            .expect_err("redirect must not be followed");
        assert!(error.contains("HTTP 307"));
        assert!(!error.contains("followed"));
    }

    #[tokio::test]
    async fn oversized_json_responses_are_rejected() {
        let router = Router::new().route(
            "/large",
            get(|| async {
                Response::builder()
                    .status(StatusCode::OK)
                    .body(Body::from(vec![b'a'; MAX_RESPONSE_BODY_BYTES + 1]))
                    .expect("large response")
                    .into_response()
            }),
        );
        let base_url = serve(router).await;

        let error = execute_json_request(request("GET", &base_url, "/large", None))
            .await
            .expect_err("large response must fail");
        assert!(error.contains("too large"));
    }

    #[tokio::test]
    async fn unsafe_paths_and_methods_are_rejected_before_network_access() {
        let invalid_path = execute_json_request(request(
            "GET",
            "http://127.0.0.1:1",
            "/Items/../secret",
            None,
        ))
        .await
        .expect_err("path traversal must fail");
        assert_eq!(invalid_path, "Invalid Emby path.");

        let invalid_method =
            execute_json_request(request("PATCH", "http://127.0.0.1:1", "/Items", None))
                .await
                .expect_err("unsupported method must fail");
        assert_eq!(invalid_method, "Invalid Emby request method.");
    }

    fn request(
        method: &str,
        base_url: &str,
        path: &str,
        query: Option<Map<String, Value>>,
    ) -> EmbyJsonRequest {
        EmbyJsonRequest {
            method: method.to_string(),
            base_url: base_url.to_string(),
            path: path.to_string(),
            query,
            body: None,
            token: Some("test-token".to_string()),
            user_id: Some("test-user".to_string()),
            device_id: "test-device".to_string(),
            auth_mode: Some("default".to_string()),
        }
    }

    async fn serve(router: Router) -> String {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind test server");
        let address = listener.local_addr().expect("test server address");
        tokio::spawn(async move {
            axum::serve(listener, router)
                .await
                .expect("serve Emby test endpoint");
        });
        format!("http://{address}")
    }
}

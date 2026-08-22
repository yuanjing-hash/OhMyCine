use reqwest::header::{ACCEPT, AUTHORIZATION, CONTENT_TYPE};
use reqwest::{Method, Url};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;

const MAX_BASE_URL_LENGTH: usize = 2048;
const MAX_PATH_LENGTH: usize = 2048;
const MAX_TOKEN_LENGTH: usize = 256;
const MAX_REQUEST_BODY_BYTES: usize = 64 * 1024;
const MAX_RESPONSE_BODY_BYTES: usize = 4 * 1024 * 1024;

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

    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(20))
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
        if encoded.len() > MAX_REQUEST_BODY_BYTES {
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
        || ["%2e", "%2f", "%5c"].iter().any(|encoded| normalized_route_path.contains(encoded))
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
        "DELETE" => Ok(Method::DELETE),
        _ => Err("OhMyCine Server 请求方法无效。".to_string()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn server_url_accepts_only_player_api_paths() {
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/bootstrap").is_ok());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/users").is_err());
        assert!(server_url("http://127.0.0.1:3000/base", "/api/v1/player/bootstrap").is_err());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/%2e%2e/users").is_err());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/catalog%2F..%2Fusers").is_err());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/catalog%5c..%5cusers").is_err());
        assert!(server_url("http://127.0.0.1:3000", "/api/v1/player/search?query=%E4%B8%83%E6%AD%A6%E5%A3%AB").is_ok());
        assert!(server_url(
            "http://user:password@127.0.0.1:3000",
            "/api/v1/player/bootstrap"
        )
        .is_err());
    }
}

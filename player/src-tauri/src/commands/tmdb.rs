use reqwest::{header, redirect::Policy, Url};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;

use super::image_cache::detect_image_mime;

const MAX_JSON_BYTES: usize = 2 * 1024 * 1024;
const MAX_IMAGE_BYTES: usize = 8 * 1024 * 1024;
const MAX_CREDENTIAL_BYTES: usize = 4096;
const MAX_PARAMETER_COUNT: usize = 32;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TmdbRequest {
    base_url: String,
    path: String,
    #[serde(default)]
    params: Vec<TmdbRequestParameter>,
    auth_type: String,
    credential: String,
    #[serde(default = "default_timeout_ms")]
    timeout_ms: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct TmdbRequestParameter {
    key: String,
    value: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TmdbRequestResult {
    ok: bool,
    network_error: bool,
    status: Option<u16>,
    data: Option<Value>,
    response_text: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TmdbImageTestRequest {
    url: String,
    #[serde(default = "default_timeout_ms")]
    timeout_ms: u64,
}

#[tauri::command]
pub async fn tmdb_request_json(request: TmdbRequest) -> Result<TmdbRequestResult, String> {
    let mut url = parse_base_url(&request.base_url)?;
    append_request_path(&mut url, &request.path)?;
    append_parameters(&mut url, &request.params)?;
    let credential = validate_credential(&request.credential)?;
    let client = http_client(request.timeout_ms)?;
    let mut builder = client.get(url).header(header::ACCEPT, "application/json");

    match request.auth_type.as_str() {
        "apiKey" => {
            builder = builder.query(&[("api_key", credential)]);
        }
        "readAccessToken" => {
            let value = header::HeaderValue::from_str(&format!("Bearer {credential}"))
                .map_err(|_| "TMDB 凭据无效。".to_string())?;
            builder = builder.header(header::AUTHORIZATION, value);
        }
        _ => return Err("TMDB 凭据类型无效。".to_string()),
    }

    let mut response = match builder.send().await {
        Ok(response) => response,
        Err(_) => {
            return Ok(TmdbRequestResult {
                ok: false,
                network_error: true,
                status: None,
                data: None,
                response_text: None,
            });
        }
    };
    let status = response.status();
    let bytes = read_bounded_body(&mut response, MAX_JSON_BYTES, "TMDB 响应过大。").await?;

    if !status.is_success() {
        return Ok(TmdbRequestResult {
            ok: false,
            network_error: false,
            status: Some(status.as_u16()),
            data: None,
            response_text: Some(String::from_utf8_lossy(&bytes).chars().take(500).collect()),
        });
    }

    let data = serde_json::from_slice::<Value>(&bytes)
        .map_err(|_| "TMDB 返回了无效的 JSON 数据。".to_string())?;
    Ok(TmdbRequestResult {
        ok: true,
        network_error: false,
        status: Some(status.as_u16()),
        data: Some(data),
        response_text: None,
    })
}

#[tauri::command]
pub async fn tmdb_test_image(request: TmdbImageTestRequest) -> Result<(), String> {
    let url = parse_https_url(&request.url, "TMDB 图片测试地址无效。")?;
    let client = http_client(request.timeout_ms)?;
    let mut response = client
        .get(url)
        .header(header::ACCEPT, "image/avif,image/webp,image/png,image/jpeg")
        .send()
        .await
        .map_err(|_| "TMDB 图片地址连接失败。".to_string())?;
    if !response.status().is_success() {
        return Err(format!(
            "TMDB 图片地址测试失败（HTTP {}）。",
            response.status().as_u16()
        ));
    }

    let bytes = read_bounded_body(&mut response, MAX_IMAGE_BYTES, "TMDB 测试图片过大。").await?;
    detect_image_mime(&bytes).ok_or_else(|| "TMDB 图片地址返回的不是受支持图片。".to_string())?;
    Ok(())
}

fn http_client(timeout_ms: u64) -> Result<reqwest::Client, String> {
    let timeout = Duration::from_millis(timeout_ms.clamp(1_000, 30_000));
    reqwest::Client::builder()
        .connect_timeout(timeout.min(Duration::from_secs(10)))
        .timeout(timeout)
        .user_agent("OhMyCine-Player/0.1")
        .redirect(Policy::none())
        .build()
        .map_err(|_| "无法初始化 TMDB 网络请求。".to_string())
}

fn parse_base_url(value: &str) -> Result<Url, String> {
    let mut url = parse_https_url(value, "TMDB API 地址无效。")?;
    if url.query().is_some() || url.fragment().is_some() {
        return Err("TMDB API 地址不能包含查询参数或片段。".to_string());
    }
    if !url.username().is_empty() || url.password().is_some() {
        return Err("TMDB API 地址不能包含账号或密码。".to_string());
    }
    let normalized_path = url.path().trim_end_matches('/').to_string();
    url.set_path(if normalized_path.is_empty() {
        "/"
    } else {
        &normalized_path
    });
    Ok(url)
}

fn parse_https_url(value: &str, message: &str) -> Result<Url, String> {
    let url = Url::parse(value.trim()).map_err(|_| message.to_string())?;
    if url.scheme() != "https" || url.host_str().is_none() {
        return Err(message.to_string());
    }
    if !url.username().is_empty() || url.password().is_some() || url.fragment().is_some() {
        return Err(message.to_string());
    }
    Ok(url)
}

fn append_request_path(url: &mut Url, path: &str) -> Result<(), String> {
    let path = path.trim();
    if !path.starts_with('/') || path.contains('?') || path.contains('#') || path.contains("..") {
        return Err("TMDB 请求路径无效。".to_string());
    }
    let base_path = url.path().trim_end_matches('/');
    url.set_path(&format!("{base_path}{path}"));
    Ok(())
}

fn append_parameters(url: &mut Url, params: &[TmdbRequestParameter]) -> Result<(), String> {
    if params.len() > MAX_PARAMETER_COUNT {
        return Err("TMDB 请求参数过多。".to_string());
    }
    let mut query = url.query_pairs_mut();
    for parameter in params {
        let key = parameter.key.trim();
        if key.is_empty()
            || key.len() > 64
            || parameter.value.len() > 2048
            || key.eq_ignore_ascii_case("api_key")
        {
            return Err("TMDB 请求参数无效。".to_string());
        }
        query.append_pair(key, &parameter.value);
    }
    Ok(())
}

fn validate_credential(value: &str) -> Result<&str, String> {
    let credential = value.trim();
    if credential.is_empty()
        || credential.len() > MAX_CREDENTIAL_BYTES
        || credential.chars().any(char::is_whitespace)
    {
        return Err("TMDB 凭据无效。".to_string());
    }
    Ok(credential)
}

async fn read_bounded_body(
    response: &mut reqwest::Response,
    limit: usize,
    limit_message: &str,
) -> Result<Vec<u8>, String> {
    if response.content_length().unwrap_or(0) > limit as u64 {
        return Err(limit_message.to_string());
    }
    let mut bytes = Vec::with_capacity(response.content_length().unwrap_or(0) as usize);
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "TMDB 响应读取失败。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > limit {
            return Err(limit_message.to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

fn default_timeout_ms() -> u64 {
    10_000
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_unsafe_base_urls() {
        assert!(parse_base_url("http://api.example.test/3").is_err());
        assert!(parse_base_url("https://user:pass@example.test/3").is_err());
        assert!(parse_base_url("https://example.test/3?token=secret").is_err());
        assert!(parse_base_url("https://example.test/3#fragment").is_err());
    }

    #[test]
    fn preserves_proxy_prefix_when_appending_path() {
        let mut url = parse_base_url("https://proxy.example.test/tmdb/3/").unwrap();
        append_request_path(&mut url, "/movie/550").unwrap();
        assert_eq!(url.as_str(), "https://proxy.example.test/tmdb/3/movie/550");
    }
}

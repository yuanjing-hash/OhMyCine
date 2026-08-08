use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use reqwest::{header, redirect, Url};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const OFFICIAL_BASE_URL: &str = "https://api.dandanplay.net";
const MAX_RESPONSE_BYTES: usize = 16 * 1024 * 1024;
const MAX_FILE_NAME_BYTES: usize = 512;
const MAX_COMMENTS: usize = 50_000;

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DanmakuMatchRequest {
    base_url: String,
    file_name: String,
    video_duration: Option<u32>,
    #[serde(default)]
    official: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DanmakuCommentsRequest {
    base_url: String,
    episode_id: i64,
    #[serde(default)]
    official: bool,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DanmakuApiResult {
    status: u16,
    data: Value,
}

#[tauri::command]
pub async fn danmaku_match(request: DanmakuMatchRequest) -> Result<DanmakuApiResult, String> {
    let base_url = validate_base_url(&request.base_url, request.official)?;
    let file_name = validate_file_name(&request.file_name)?;
    let url = endpoint_url(&base_url, "api/v2/match")?;
    let client = http_client()?;
    let builder = client
        .post(url.clone())
        .header(header::ACCEPT, "application/json")
        .json(&json!({
            "fileName": file_name,
            "fileHash": "",
            "fileSize": 0,
            "videoDuration": request.video_duration.unwrap_or(0).min(86_400),
            "matchMode": "fileNameOnly"
        }));
    send_json(with_official_auth(builder, request.official, url.path())?).await
}

#[tauri::command]
pub async fn danmaku_comments(request: DanmakuCommentsRequest) -> Result<DanmakuApiResult, String> {
    if request.episode_id <= 0 {
        return Err("弹幕剧集编号无效。".to_string());
    }
    let base_url = validate_base_url(&request.base_url, request.official)?;
    let url = endpoint_url(&base_url, &format!("api/v2/comment/{}", request.episode_id))?;
    let client = http_client()?;
    let builder = client
        .get(url.clone())
        .query(&[("withRelated", "true"), ("chConvert", "1")])
        .header(header::ACCEPT, "application/json");
    let result = send_json_with_redirects(
        with_official_auth(builder, request.official, url.path())?,
        &client,
    )
    .await?;
    if result
        .data
        .get("comments")
        .and_then(Value::as_array)
        .is_some_and(|comments| comments.len() > MAX_COMMENTS)
    {
        return Err("弹幕数量超过安全上限。".to_string());
    }
    Ok(result)
}

fn with_official_auth(
    builder: reqwest::RequestBuilder,
    official: bool,
    path: &str,
) -> Result<reqwest::RequestBuilder, String> {
    if !official {
        return Ok(builder);
    }
    let app_id = option_env!("OHMYCINE_DANDANPLAY_APP_ID")
        .unwrap_or("")
        .trim();
    let app_secret = option_env!("OHMYCINE_DANDANPLAY_APP_SECRET")
        .unwrap_or("")
        .trim();
    if app_id.is_empty() || app_secret.is_empty() {
        return Err("官方弹幕服务暂不可用，请在设置中使用兼容 API。".to_string());
    }
    let app_id_header =
        header::HeaderValue::from_str(app_id).map_err(|_| "官方弹幕服务配置无效。".to_string())?;
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "设备时间无效，无法连接官方弹幕服务。".to_string())?
        .as_secs()
        .to_string();
    let signature = BASE64.encode(Sha256::digest(
        format!("{app_id}{timestamp}{path}{app_secret}").as_bytes(),
    ));
    Ok(builder
        .header("X-AppId", app_id_header)
        .header("X-Timestamp", timestamp)
        .header("X-Signature", signature))
}

fn http_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(8))
        .timeout(Duration::from_secs(15))
        .user_agent(concat!("OhMyCine-Player/", env!("CARGO_PKG_VERSION")))
        .redirect(redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化弹幕网络请求。".to_string())
}

async fn send_json_with_redirects(
    builder: reqwest::RequestBuilder,
    client: &reqwest::Client,
) -> Result<DanmakuApiResult, String> {
    let mut response = builder
        .send()
        .await
        .map_err(|_| "弹幕服务连接失败。".to_string())?;
    for _ in 0..3 {
        if !response.status().is_redirection() {
            return response_json(response).await;
        }
        let location = response
            .headers()
            .get(header::LOCATION)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(|| "弹幕服务返回了无效跳转。".to_string())?;
        let next = response
            .url()
            .join(location)
            .map_err(|_| "弹幕服务返回了无效跳转。".to_string())?;
        if response.url().scheme() == "https" && next.scheme() != "https" {
            return Err("弹幕服务拒绝了不安全跳转。".to_string());
        }
        response = client
            .get(next)
            .header(header::ACCEPT, "application/json")
            .send()
            .await
            .map_err(|_| "弹幕服务跳转连接失败。".to_string())?;
    }
    Err("弹幕服务跳转次数过多。".to_string())
}

async fn send_json(builder: reqwest::RequestBuilder) -> Result<DanmakuApiResult, String> {
    let response = builder
        .send()
        .await
        .map_err(|_| "弹幕服务连接失败。".to_string())?;
    response_json(response).await
}

async fn response_json(mut response: reqwest::Response) -> Result<DanmakuApiResult, String> {
    let status = response.status();
    let bytes = read_bounded_body(&mut response).await?;
    if !status.is_success() {
        return Err(match status.as_u16() {
            401 | 403 => "弹幕服务拒绝了请求，请检查服务配置。".to_string(),
            404 => "没有找到匹配的弹幕。".to_string(),
            code => format!("弹幕服务请求失败（HTTP {code}）。"),
        });
    }
    let data =
        serde_json::from_slice(&bytes).map_err(|_| "弹幕服务返回了无效数据。".to_string())?;
    Ok(DanmakuApiResult {
        status: status.as_u16(),
        data,
    })
}

async fn read_bounded_body(response: &mut reqwest::Response) -> Result<Vec<u8>, String> {
    if response.content_length().unwrap_or(0) > MAX_RESPONSE_BYTES as u64 {
        return Err("弹幕响应过大。".to_string());
    }
    let mut bytes = Vec::with_capacity(
        response
            .content_length()
            .unwrap_or(0)
            .min(MAX_RESPONSE_BYTES as u64) as usize,
    );
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "弹幕响应读取失败。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_RESPONSE_BYTES {
            return Err("弹幕响应过大。".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

fn validate_base_url(value: &str, official: bool) -> Result<Url, String> {
    let value = value.trim();
    if official && value != OFFICIAL_BASE_URL {
        return Err("官方弹幕服务地址无效。".to_string());
    }
    let mut url = Url::parse(value).map_err(|_| "弹幕 API 地址无效。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("弹幕 API 地址无效。".to_string());
    }
    let path = url.path().trim_end_matches('/').to_string();
    url.set_path(if path.is_empty() { "/" } else { &path });
    Ok(url)
}

fn endpoint_url(base: &Url, path: &str) -> Result<Url, String> {
    let mut url = base.clone();
    let base_path = base.path().trim_end_matches('/');
    url.set_path(&format!("{base_path}/{path}"));
    Ok(url)
}

fn validate_file_name(value: &str) -> Result<&str, String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > MAX_FILE_NAME_BYTES
        || value.contains('/')
        || value.contains('\\')
        || value.contains("://")
        || value.chars().any(char::is_control)
    {
        return Err("弹幕匹配名称无效。".to_string());
    }
    Ok(value)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    #[test]
    fn rejects_paths_urls_and_credentials_in_match_name() {
        for value in [
            "/media/show.mkv",
            "C:\\video\\show.mkv",
            "https://host/video",
        ] {
            assert!(validate_file_name(value).is_err());
        }
        assert_eq!(
            validate_file_name("Show.S01E01.mkv").unwrap(),
            "Show.S01E01.mkv"
        );
    }

    #[test]
    fn validates_custom_and_official_base_urls() {
        assert!(validate_base_url(OFFICIAL_BASE_URL, true).is_ok());
        assert!(validate_base_url("http://192.168.1.2:9000/dandan", false).is_ok());
        assert!(validate_base_url("https://u:p@example.test", false).is_err());
        assert!(validate_base_url("https://example.test?token=secret", false).is_err());
        assert!(validate_base_url("http://example.test", true).is_err());
    }

    #[tokio::test]
    async fn follows_bounded_redirect_without_forwarding_signature_headers() {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind mock danmaku server");
        let address = listener.local_addr().expect("mock address");
        let server = std::thread::spawn(move || {
            let (mut first, _) = listener.accept().expect("first request");
            let first_request = read_request(&mut first);
            assert!(first_request
                .to_ascii_lowercase()
                .contains("x-signature: test-signature"));
            write!(
                first,
                "HTTP/1.1 302 Found\r\nLocation: http://{address}/accelerated\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            )
            .expect("redirect response");

            let (mut second, _) = listener.accept().expect("redirected request");
            let second_request = read_request(&mut second);
            assert!(second_request.starts_with("GET /accelerated "));
            assert!(!second_request.to_ascii_lowercase().contains("x-signature"));
            let body = r#"{"count":1,"comments":[{"cid":1,"p":"1,1,16777215,u","m":"ok"}]}"#;
            write!(
                second,
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body.len(),
                body
            )
            .expect("json response");
        });

        let client = http_client().expect("http client");
        let result = send_json_with_redirects(
            client
                .get(format!("http://{address}/comments"))
                .header("X-Signature", "test-signature"),
            &client,
        )
        .await
        .expect("redirect result");
        assert_eq!(result.data["count"], 1);
        server.join().expect("mock server completed");
    }

    #[tokio::test]
    async fn rejects_oversized_responses_from_content_length() {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind mock danmaku server");
        let address = listener.local_addr().expect("mock address");
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().expect("request");
            let _ = read_request(&mut stream);
            write!(
                stream,
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                MAX_RESPONSE_BYTES + 1
            )
            .expect("oversized response");
        });
        let client = http_client().expect("http client");
        let error = send_json(client.get(format!("http://{address}/oversized")))
            .await
            .expect_err("oversized response must fail");
        assert_eq!(error, "弹幕响应过大。");
        server.join().expect("mock server completed");
    }

    fn read_request(stream: &mut std::net::TcpStream) -> String {
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .expect("set timeout");
        let mut buffer = [0_u8; 4096];
        let size = stream.read(&mut buffer).expect("read request");
        String::from_utf8_lossy(&buffer[..size]).to_string()
    }
}

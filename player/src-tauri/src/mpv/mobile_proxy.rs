use std::{sync::Arc, time::Duration};

use axum::{
    body::Body,
    extract::{Path, State},
    http::{
        header::{
            ACCEPT, ACCEPT_RANGES, CACHE_CONTROL, CONTENT_DISPOSITION, CONTENT_LENGTH,
            CONTENT_RANGE, CONTENT_TYPE, ETAG, IF_MODIFIED_SINCE, IF_NONE_MATCH, IF_RANGE,
            LAST_MODIFIED, RANGE,
        },
        HeaderMap, Method, Request, StatusCode,
    },
    response::Response,
    routing::any,
    Router,
};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use rand::{rngs::OsRng, RngCore};
use tokio::{net::TcpListener, sync::RwLock};

use crate::commands::player_shared::MpvHttpHeader;

const LOOPBACK_PATH: &str = "/media/{token}";

#[derive(Default)]
pub struct AndroidStreamProxyState {
    runtime: tokio::sync::Mutex<Option<ProxyRuntime>>,
}

struct ProxyRuntime {
    port: u16,
    target: Arc<RwLock<Option<ProxyTarget>>>,
}

#[derive(Clone)]
struct ProxyTarget {
    token: String,
    url: String,
    headers: Vec<MpvHttpHeader>,
}

#[derive(Clone)]
struct ProxyAppState {
    client: reqwest::Client,
    target: Arc<RwLock<Option<ProxyTarget>>>,
}

impl AndroidStreamProxyState {
    pub async fn prepare(
        &self,
        url: String,
        headers: Vec<MpvHttpHeader>,
    ) -> Result<String, String> {
        let mut runtime = self.runtime.lock().await;
        if runtime.is_none() {
            *runtime = Some(start_proxy().await?);
        }
        let runtime = runtime.as_ref().expect("proxy runtime initialized");
        let token = random_token();
        *runtime.target.write().await = Some(ProxyTarget {
            token: token.clone(),
            url,
            headers,
        });
        Ok(format!("http://127.0.0.1:{}/media/{token}", runtime.port))
    }

    pub async fn clear(&self) {
        if let Some(runtime) = self.runtime.lock().await.as_ref() {
            *runtime.target.write().await = None;
        }
    }
}

async fn start_proxy() -> Result<ProxyRuntime, String> {
    let listener = TcpListener::bind(("127.0.0.1", 0))
        .await
        .map_err(|_| "Android 本地流桥接启动失败。".to_string())?;
    let port = listener
        .local_addr()
        .map_err(|_| "Android 本地流桥接端口不可用。".to_string())?
        .port();
    let target = Arc::new(RwLock::new(None));
    let client = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::limited(10))
        .build()
        .map_err(|_| "Android 安全媒体客户端初始化失败。".to_string())?;
    let app = Router::new()
        .route(LOOPBACK_PATH, any(proxy_media))
        .with_state(ProxyAppState {
            client,
            target: target.clone(),
        });

    tauri::async_runtime::spawn(async move {
        if let Err(error) = axum::serve(listener, app).await {
            log::warn!("Android loopback media proxy stopped: {error}");
        }
    });

    Ok(ProxyRuntime { port, target })
}

async fn proxy_media(
    Path(token): Path<String>,
    State(state): State<ProxyAppState>,
    request: Request<Body>,
) -> Response<Body> {
    if request.method() != Method::GET && request.method() != Method::HEAD {
        return plain_response(StatusCode::METHOD_NOT_ALLOWED, "method not allowed");
    }

    let Some(target) = state.target.read().await.clone() else {
        return plain_response(StatusCode::GONE, "media session expired");
    };
    if !constant_time_eq(token.as_bytes(), target.token.as_bytes()) {
        return plain_response(StatusCode::NOT_FOUND, "not found");
    }

    let mut upstream = state.client.request(request.method().clone(), &target.url);
    for header in &target.headers {
        upstream = upstream.header(&header.name, &header.value);
    }
    upstream = forward_request_headers(upstream, request.headers());

    let response = match upstream.send().await {
        Ok(response) => response,
        Err(error) => {
            log::warn!(
                "Android secure media bridge upstream request failed: {}",
                safe_error(&error)
            );
            return plain_response(StatusCode::BAD_GATEWAY, "upstream media connection failed");
        }
    };

    let status = response.status();
    let headers = response.headers().clone();
    let mut builder = Response::builder().status(status);
    for name in [
        CONTENT_TYPE,
        CONTENT_LENGTH,
        CONTENT_RANGE,
        ACCEPT_RANGES,
        ETAG,
        LAST_MODIFIED,
        CACHE_CONTROL,
        CONTENT_DISPOSITION,
    ] {
        if let Some(value) = headers.get(&name) {
            builder = builder.header(name, value);
        }
    }

    let body = if request.method() == Method::HEAD {
        Body::empty()
    } else {
        Body::from_stream(response.bytes_stream())
    };
    builder
        .body(body)
        .unwrap_or_else(|_| plain_response(StatusCode::BAD_GATEWAY, "invalid upstream response"))
}

fn forward_request_headers(
    mut request: reqwest::RequestBuilder,
    headers: &HeaderMap,
) -> reqwest::RequestBuilder {
    for name in [RANGE, IF_RANGE, IF_NONE_MATCH, IF_MODIFIED_SINCE, ACCEPT] {
        if let Some(value) = headers.get(&name) {
            request = request.header(name, value);
        }
    }
    request
}

fn plain_response(status: StatusCode, message: &'static str) -> Response<Body> {
    Response::builder()
        .status(status)
        .header(CONTENT_TYPE, "text/plain; charset=utf-8")
        .body(Body::from(message))
        .unwrap_or_else(|_| Response::new(Body::empty()))
}

fn random_token() -> String {
    let mut bytes = [0_u8; 24];
    OsRng.fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}

fn constant_time_eq(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    left.iter()
        .zip(right)
        .fold(0_u8, |difference, (left, right)| {
            difference | (left ^ right)
        })
        == 0
}

fn safe_error(error: &reqwest::Error) -> &'static str {
    if error.is_connect() {
        "connect"
    } else if error.is_timeout() {
        "timeout"
    } else if error.is_redirect() {
        "redirect"
    } else {
        "request"
    }
}

#[cfg(test)]
mod tests {
    use super::{constant_time_eq, random_token};

    #[test]
    fn loopback_tokens_are_random_and_url_safe() {
        let first = random_token();
        let second = random_token();
        assert_ne!(first, second);
        assert!(first.len() >= 32);
        assert!(first
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_')));
    }

    #[test]
    fn token_comparison_requires_exact_bytes() {
        assert!(constant_time_eq(b"same-token", b"same-token"));
        assert!(!constant_time_eq(b"same-token", b"other-token"));
        assert!(!constant_time_eq(b"short", b"longer"));
    }
}

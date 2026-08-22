#![cfg_attr(test, allow(dead_code))]

use std::{sync::Arc, time::Duration};

use axum::{
    body::Body,
    extract::{Path, State},
    http::{
        header::{
            ACCEPT, ACCEPT_ENCODING, ACCEPT_RANGES, CACHE_CONTROL, CONTENT_DISPOSITION,
            CONTENT_LENGTH, CONTENT_RANGE, CONTENT_TYPE, ETAG, IF_MODIFIED_SINCE, IF_NONE_MATCH,
            IF_RANGE, LAST_MODIFIED, LOCATION, RANGE,
        },
        HeaderMap, Method, Request, StatusCode,
    },
    response::Response,
    routing::any,
    Router,
};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use rand::{rngs::OsRng, RngCore};
use reqwest::Url;
use tokio::{net::TcpListener, sync::RwLock};

use crate::commands::player_shared::MpvHttpHeader;

const LOOPBACK_PATH: &str = "/media/:token";
const MAX_UPSTREAM_REDIRECTS: usize = 10;

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
        .redirect(reqwest::redirect::Policy::none())
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

    let response = match send_upstream(
        &state.client,
        request.method(),
        &target.url,
        &target.headers,
        request.headers(),
    )
    .await
    {
        Ok(response) => response,
        Err(reason) => {
            log::warn!("Android secure media bridge upstream request failed: {reason}");
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

async fn send_upstream(
    client: &reqwest::Client,
    method: &Method,
    url: &str,
    target_headers: &[MpvHttpHeader],
    playback_headers: &HeaderMap,
) -> Result<reqwest::Response, &'static str> {
    let mut current_url = Url::parse(url).map_err(|_| "invalid-url")?;
    if !matches!(current_url.scheme(), "http" | "https") {
        return Err("invalid-scheme");
    }
    let mut forward_target_headers = true;

    for redirect_count in 0..=MAX_UPSTREAM_REDIRECTS {
        let mut upstream = client.request(method.clone(), current_url.clone());
        if forward_target_headers {
            for header in target_headers {
                upstream = upstream.header(&header.name, &header.value);
            }
        }
        upstream =
            forward_request_headers(upstream, playback_headers).header(ACCEPT_ENCODING, "identity");

        let response = upstream.send().await.map_err(|error| safe_error(&error))?;
        if !response.status().is_redirection() {
            return Ok(response);
        }
        if redirect_count == MAX_UPSTREAM_REDIRECTS {
            return Err("redirect-limit");
        }

        let location = response
            .headers()
            .get(LOCATION)
            .and_then(|value| value.to_str().ok())
            .ok_or("redirect-location")?;
        let next_url = current_url
            .join(location)
            .map_err(|_| "redirect-location")?;
        if !matches!(next_url.scheme(), "http" | "https") {
            return Err("redirect-scheme");
        }
        if !same_origin(&current_url, &next_url) {
            forward_target_headers = false;
        }
        current_url = next_url;
    }

    Err("redirect-limit")
}

fn same_origin(left: &Url, right: &Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
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
    use std::sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    };

    use axum::{
        http::{
            header::{AUTHORIZATION, COOKIE, RANGE},
            HeaderMap, StatusCode,
        },
        response::Response,
        routing::get,
        Router,
    };
    use reqwest::Url;
    use tokio::net::TcpListener;

    use crate::commands::player_shared::MpvHttpHeader;

    use super::{constant_time_eq, random_token, same_origin, AndroidStreamProxyState};

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

    #[test]
    fn redirect_origin_check_drops_provider_headers_before_cdn_hop() {
        let emby_http = Url::parse("http://emby.local/Videos/1/stream").unwrap();
        let emby_https = Url::parse("https://emby.local/Videos/1/stream").unwrap();
        let emby_same_origin = Url::parse("http://emby.local/Videos/2/stream").unwrap();
        let cdn = Url::parse("https://cdn.example/media.mkv?token=signed").unwrap();

        assert!(same_origin(&emby_http, &emby_same_origin));
        assert!(!same_origin(&emby_http, &emby_https));
        assert!(!same_origin(&emby_http, &cdn));
    }

    #[test]
    fn loopback_bridge_follows_http_redirect_and_preserves_range_without_private_headers() {
        tauri::async_runtime::block_on(async {
            let cdn_listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
            let cdn_address = cdn_listener.local_addr().unwrap();
            let cdn = Router::new().route(
                "/media",
                get(|headers: HeaderMap| async move {
                    let range_matches = headers.get(RANGE).and_then(|value| value.to_str().ok())
                        == Some("bytes=0-3");
                    let private_header_removed = !headers.contains_key("x-emby-token")
                        && !headers.contains_key(AUTHORIZATION)
                        && !headers.contains_key(COOKIE);
                    let status = if range_matches && private_header_removed {
                        StatusCode::PARTIAL_CONTENT
                    } else {
                        StatusCode::BAD_REQUEST
                    };
                    Response::builder()
                        .status(status)
                        .header("content-range", "bytes 0-3/4")
                        .body(axum::body::Body::from("test"))
                        .unwrap()
                }),
            );
            tauri::async_runtime::spawn(async move {
                axum::serve(cdn_listener, cdn).await.unwrap();
            });

            let emby_listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
            let emby_address = emby_listener.local_addr().unwrap();
            let redirect_location = format!("http://{cdn_address}/media");
            let first_origin_received_private_headers = Arc::new(AtomicBool::new(false));
            let first_origin_probe = first_origin_received_private_headers.clone();
            let emby = Router::new().route(
                "/stream",
                get(move |headers: HeaderMap| {
                    let location = redirect_location.clone();
                    let first_origin_probe = first_origin_probe.clone();
                    async move {
                        first_origin_probe.store(
                            headers.get(AUTHORIZATION).and_then(|value| value.to_str().ok())
                                == Some("Bearer private-device-token")
                                && headers.get(COOKIE).and_then(|value| value.to_str().ok())
                                    == Some("private=session")
                                && headers.get("x-emby-token").is_some(),
                            Ordering::SeqCst,
                        );
                        Response::builder()
                            .status(StatusCode::FOUND)
                            .header("location", location)
                            .body(axum::body::Body::empty())
                            .unwrap()
                    }
                }),
            );
            tauri::async_runtime::spawn(async move {
                axum::serve(emby_listener, emby).await.unwrap();
            });

            let proxy = AndroidStreamProxyState::default();
            let loopback_url = proxy
                .prepare(
                    format!("http://{emby_address}/stream"),
                    vec![MpvHttpHeader {
                        name: "X-Emby-Token".to_string(),
                        value: "private-token".to_string(),
                    }, MpvHttpHeader {
                        name: "Authorization".to_string(),
                        value: "Bearer private-device-token".to_string(),
                    }, MpvHttpHeader {
                        name: "Cookie".to_string(),
                        value: "private=session".to_string(),
                    }],
                )
                .await
                .unwrap();
            let response = reqwest::Client::new()
                .get(loopback_url)
                .header(RANGE, "bytes=0-3")
                .send()
                .await
                .unwrap();

            assert_eq!(response.status(), StatusCode::PARTIAL_CONTENT);
            assert_eq!(response.bytes().await.unwrap().as_ref(), b"test");
            assert!(first_origin_received_private_headers.load(Ordering::SeqCst));
        });
    }
}

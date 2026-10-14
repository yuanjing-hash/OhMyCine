#![cfg_attr(test, allow(dead_code))]

use std::{
    collections::{HashMap, VecDeque},
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};

use axum::{
    body::Body,
    extract::{Path, State},
    http::{
        header::{
            ACCEPT, ACCEPT_ENCODING, ACCEPT_RANGES, CACHE_CONTROL, CONTENT_DISPOSITION,
            CONTENT_LENGTH, CONTENT_RANGE, CONTENT_TYPE, ETAG, HOST, IF_MODIFIED_SINCE,
            IF_NONE_MATCH, IF_RANGE, LAST_MODIFIED, LOCATION, ORIGIN, RANGE,
        },
        HeaderMap, Method, Request, StatusCode,
    },
    response::Response,
    routing::any,
    Router,
};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use futures_util::StreamExt;
use rand::{rngs::OsRng, RngCore};
use reqwest::Url;
use tokio::{
    net::TcpListener,
    sync::{watch, RwLock, Semaphore},
};

use super::hls_proxy;
use crate::commands::player_shared::MpvHttpHeader;
use crate::commands::public_dns::{public_ip, resolve_cdn};

const LOOPBACK_PATH: &str = "/media/:token";
const MAX_UPSTREAM_REDIRECTS: usize = 10;
const MAX_SESSION_TARGETS: usize = 8;
const MAX_DERIVED_TARGETS: usize = 16_384;
const MAX_HLS_DEPTH: u8 = 8;
const SESSION_LIFETIME: Duration = Duration::from_secs(12 * 60 * 60);
const MAX_TRANSPORT_FAILURES: usize = 12;

#[derive(Clone, Copy, PartialEq, Eq)]
enum TransportFailureStage {
    Request,
    Response,
    PrefixRead,
    BodyRead,
    HlsRefetch,
    HlsRead,
    HlsRewrite,
    HlsControl,
}

impl TransportFailureStage {
    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    fn label(self) -> &'static str {
        match self {
            Self::Request => "request",
            Self::Response => "response",
            Self::PrefixRead => "prefix-read",
            Self::BodyRead => "body-read",
            Self::HlsRefetch => "hls-refetch",
            Self::HlsRead => "hls-read",
            Self::HlsRewrite => "hls-rewrite",
            Self::HlsControl => "hls-control",
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum TransportFailureOrigin {
    Bridge,
    Upstream,
}

#[derive(Clone, PartialEq, Eq)]
struct TransportFailure {
    origin: TransportFailureOrigin,
    stage: TransportFailureStage,
    reason: &'static str,
    head: bool,
    status: u16,
    upstream_status: Option<u16>,
}

impl TransportFailure {
    fn bridge(
        stage: TransportFailureStage,
        reason: &'static str,
        method: &Method,
        status: StatusCode,
        upstream_status: Option<StatusCode>,
    ) -> Self {
        Self {
            origin: TransportFailureOrigin::Bridge,
            stage,
            reason: diagnostic_reason(reason),
            head: method == Method::HEAD,
            status: status.as_u16(),
            upstream_status: upstream_status.map(|value| value.as_u16()),
        }
    }

    fn upstream(
        stage: TransportFailureStage,
        reason: &'static str,
        method: &Method,
        status: StatusCode,
    ) -> Self {
        Self {
            origin: TransportFailureOrigin::Upstream,
            stage,
            reason: diagnostic_reason(reason),
            head: method == Method::HEAD,
            status: status.as_u16(),
            upstream_status: None,
        }
    }

    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    fn line(&self) -> String {
        let origin = match self.origin {
            TransportFailureOrigin::Bridge => "bridge-generated",
            TransportFailureOrigin::Upstream => "upstream",
        };
        let upstream = self
            .upstream_status
            .map(|value| format!(" upstream-status={value}"))
            .unwrap_or_default();
        format!(
            "transport: origin={origin} stage={} reason={} method={} status={}{upstream}",
            self.stage.label(),
            self.reason,
            if self.head { "HEAD" } else { "GET" },
            self.status
        )
    }
}

#[derive(Default)]
struct TransportDiagnostics {
    epoch: u64,
    failures: VecDeque<TransportFailure>,
}

struct ProxySession {
    origin: Url,
    cancelled: watch::Sender<bool>,
    created: Instant,
    epoch: u64,
    diagnostics: Arc<Mutex<TransportDiagnostics>>,
}

impl ProxySession {
    fn active(&self) -> bool {
        !*self.cancelled.borrow() && self.created.elapsed() < SESSION_LIFETIME
    }
}

#[derive(Default)]
pub struct AndroidStreamProxyState {
    runtime: tokio::sync::Mutex<Option<ProxyRuntime>>,
    submission: tokio::sync::Mutex<()>,
    epoch: AtomicU64,
}

// Desktop reuses the HTTP bridge, while only Android submits native IPC here.
#[cfg_attr(not(target_os = "android"), allow(dead_code))]
pub struct CurrentLoadSubmission<'a> {
    proxy: &'a AndroidStreamProxyState,
    epoch: u64,
    _guard: tokio::sync::MutexGuard<'a, ()>,
}

impl CurrentLoadSubmission<'_> {
    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    pub async fn clear_after_stop(self) {
        // Keep the submission lock through native stop and cleanup. A new load
        // cannot register its targets in the gap, and this does not relock it.
        self.proxy.cancel_locked(Some(self.epoch), true).await;
    }
}

struct ProxyRuntime {
    port: u16,
    targets: Arc<RwLock<HashMap<String, ProxyTarget>>>,
    diagnostics: Arc<Mutex<TransportDiagnostics>>,
}

#[derive(Clone)]
struct ProxyTarget {
    url: String,
    headers: Arc<Vec<MpvHttpHeader>>,
    session: Arc<ProxySession>,
    depth: u8,
    ancestors: Arc<Vec<String>>,
}

#[derive(Clone)]
struct ProxyAppState {
    client: reqwest::Client,
    targets: Arc<RwLock<HashMap<String, ProxyTarget>>>,
    port: u16,
    slots: Arc<Semaphore>,
}

impl AndroidStreamProxyState {
    pub async fn prepare(
        &self,
        url: String,
        headers: Vec<MpvHttpHeader>,
    ) -> Result<String, String> {
        self.prepare_for_epoch(url, headers, self.diagnostic_epoch())
            .await
    }

    pub async fn prepare_for_epoch(
        &self,
        url: String,
        headers: Vec<MpvHttpHeader>,
        epoch: u64,
    ) -> Result<String, String> {
        let origin = validated_url(&url).map_err(|_| "播放地址无效。".to_string())?;
        let mut runtime = self.runtime.lock().await;
        if self.diagnostic_epoch() != epoch {
            return Err("播放会话已被替换。".to_string());
        }
        if runtime.is_none() {
            let value = start_proxy().await?;
            if let Ok(mut diagnostics) = value.diagnostics.lock() {
                diagnostics.epoch = epoch;
            }
            *runtime = Some(value);
        }
        let runtime = runtime.as_ref().expect("proxy runtime initialized");
        let token = random_token();
        let mut targets = runtime.targets.write().await;
        targets.retain(|_, target| target.session.active());
        if targets.values().filter(|target| target.depth == 0).count() >= MAX_SESSION_TARGETS {
            return Err("当前媒体包含过多独立流，无法安全建立播放会话。".to_string());
        }
        let (cancelled, _) = watch::channel(false);
        targets.insert(
            token.clone(),
            ProxyTarget {
                url,
                headers: Arc::new(headers),
                depth: 0,
                ancestors: Arc::new(Vec::new()),
                session: Arc::new(ProxySession {
                    origin,
                    cancelled,
                    created: Instant::now(),
                    epoch,
                    diagnostics: runtime.diagnostics.clone(),
                }),
            },
        );
        Ok(format!("http://127.0.0.1:{}/media/{token}", runtime.port))
    }

    pub async fn clear(&self) {
        self.begin_load().await;
    }

    pub async fn begin_load(&self) -> u64 {
        self.cancel(None, true).await
    }

    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    pub async fn cancel_preserving_diagnostics(&self, epoch: u64) {
        self.cancel(Some(epoch), false).await;
    }

    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    pub async fn current_load_guard(
        &self,
        epoch: u64,
    ) -> Result<CurrentLoadSubmission<'_>, String> {
        let guard = self.submission.lock().await;
        if self.diagnostic_epoch() != epoch {
            return Err("播放会话已被替换。".to_string());
        }
        // Native load submission and begin/clear share this guard. A replacement
        // cannot happen between the epoch check and sending the prepared URL.
        Ok(CurrentLoadSubmission {
            proxy: self,
            epoch,
            _guard: guard,
        })
    }

    async fn cancel(&self, expected_epoch: Option<u64>, reset_diagnostics: bool) -> u64 {
        let _submission = self.submission.lock().await;
        self.cancel_locked(expected_epoch, reset_diagnostics).await
    }

    async fn cancel_locked(&self, expected_epoch: Option<u64>, reset_diagnostics: bool) -> u64 {
        let runtime = self.runtime.lock().await;
        let current_epoch = self.diagnostic_epoch();
        if expected_epoch.is_some_and(|expected| expected != current_epoch) {
            return current_epoch;
        }
        let epoch = if reset_diagnostics {
            self.epoch.fetch_add(1, Ordering::AcqRel).wrapping_add(1)
        } else {
            current_epoch
        };
        if let Some(runtime) = runtime.as_ref() {
            let mut targets = runtime.targets.write().await;
            for target in targets.values() {
                target.session.cancelled.send_replace(true);
            }
            targets.clear();
            if reset_diagnostics {
                if let Ok(mut diagnostics) = runtime.diagnostics.lock() {
                    diagnostics.epoch = epoch;
                    diagnostics.failures.clear();
                }
            }
        }
        epoch
    }

    pub fn diagnostic_epoch(&self) -> u64 {
        self.epoch.load(Ordering::Acquire)
    }

    #[cfg_attr(not(target_os = "android"), allow(dead_code))]
    pub async fn diagnostic_logs(&self, epoch: u64) -> Vec<String> {
        if self.diagnostic_epoch() != epoch {
            return Vec::new();
        }
        self.runtime
            .lock()
            .await
            .as_ref()
            .and_then(|runtime| {
                runtime.diagnostics.lock().ok().map(|value| {
                    if value.epoch == epoch {
                        value.failures.iter().map(TransportFailure::line).collect()
                    } else {
                        Vec::new()
                    }
                })
            })
            .unwrap_or_default()
    }
}

fn record_failure(target: &ProxyTarget, failure: TransportFailure) {
    if let Ok(mut diagnostics) = target.session.diagnostics.lock() {
        // clear/new-load advances this epoch under the same lock. A request that
        // finishes after cancellation cannot populate a later playback's log.
        if target.session.active()
            && target.session.epoch == diagnostics.epoch
            && !diagnostics.failures.contains(&failure)
        {
            // Keep the first failure as well as the most recent distinct failures.
            if diagnostics.failures.len() == MAX_TRANSPORT_FAILURES {
                diagnostics.failures.remove(1);
            }
            diagnostics.failures.push_back(failure);
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
    let targets = Arc::new(RwLock::new(HashMap::new()));
    let diagnostics = Arc::new(Mutex::new(TransportDiagnostics::default()));
    let client = reqwest::Client::builder()
        .no_proxy()
        .connect_timeout(Duration::from_secs(15))
        .read_timeout(Duration::from_secs(30))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "Android 安全媒体客户端初始化失败。".to_string())?;
    let app = Router::new()
        .route(LOOPBACK_PATH, any(proxy_media))
        .with_state(ProxyAppState {
            client,
            targets: targets.clone(),
            port,
            slots: Arc::new(Semaphore::new(32)),
        });

    tauri::async_runtime::spawn(async move {
        if let Err(error) = axum::serve(listener, app).await {
            log::warn!("Android loopback media proxy stopped: {error}");
        }
    });

    Ok(ProxyRuntime {
        port,
        targets,
        diagnostics,
    })
}

async fn proxy_media(
    Path(token): Path<String>,
    State(state): State<ProxyAppState>,
    request: Request<Body>,
) -> Response<Body> {
    // Native libmpv has no browser Origin/Fetch metadata. Refuse web requests and
    // DNS-rebinding Host values even if a session capability was discovered.
    if request.headers().contains_key(ORIGIN)
        || request.headers().contains_key("sec-fetch-site")
        || request
            .headers()
            .get(HOST)
            .and_then(|value| value.to_str().ok())
            != Some(format!("127.0.0.1:{}", state.port).as_str())
    {
        return plain_response(StatusCode::FORBIDDEN, "native media only");
    }
    if request.method() != Method::GET && request.method() != Method::HEAD {
        return plain_response(StatusCode::METHOD_NOT_ALLOWED, "method not allowed");
    }

    let targets = state.targets.read().await;
    if targets.is_empty() {
        return plain_response(StatusCode::GONE, "media session expired");
    }
    let Some(target) = targets.iter().find_map(|(candidate, target)| {
        constant_time_eq(token.as_bytes(), candidate.as_bytes()).then(|| target.clone())
    }) else {
        return plain_response(StatusCode::NOT_FOUND, "not found");
    };
    drop(targets);
    if !target.session.active() {
        return plain_response(StatusCode::GONE, "media session expired");
    }
    let Ok(slot) = state.slots.clone().try_acquire_owned() else {
        record_failure(
            &target,
            TransportFailure::bridge(
                TransportFailureStage::Request,
                "bridge-busy",
                request.method(),
                StatusCode::SERVICE_UNAVAILABLE,
                None,
            ),
        );
        return plain_response(StatusCode::SERVICE_UNAVAILABLE, "media bridge busy");
    };
    let mut cancelled = target.session.cancelled.subscribe();
    let expires_in = SESSION_LIFETIME.saturating_sub(target.session.created.elapsed());
    let expires_at = tokio::time::Instant::now() + expires_in;
    let response = tokio::select! {
        biased;
        _ = wait_for_cancellation(&mut cancelled) => return plain_response(StatusCode::GONE, "media session expired"),
        _ = tokio::time::sleep_until(expires_at) => return plain_response(StatusCode::GONE, "media session expired"),
        result = forward_media(&state, &target, request) => result,
    };
    // Retain concurrency accounting until the actual streaming body is dropped.
    let (parts, body) = response.into_parts();
    let stream = body.into_data_stream();
    let stream = stream.take_until(async move {
        tokio::select! { _ = wait_for_cancellation(&mut cancelled) => {}, _ = tokio::time::sleep_until(expires_at) => {} }
    });
    let stream = stream.map(move |chunk| {
        let _ = &slot;
        chunk
    });
    Response::from_parts(parts, Body::from_stream(stream))
}

async fn wait_for_cancellation(cancelled: &mut watch::Receiver<bool>) {
    // clear can finish before the request subscribes. A new receiver already
    // knows that value, so changed() alone would wait for a second notification.
    if !*cancelled.borrow() {
        let _ = cancelled.changed().await;
    }
}

async fn forward_media(
    state: &ProxyAppState,
    target: &ProxyTarget,
    request: Request<Body>,
) -> Response<Body> {
    let (mut response, effective_headers) = match send_upstream(
        &state.client,
        request.method(),
        &target.url,
        &target.headers,
        request.headers(),
        &target.session.origin,
    )
    .await
    {
        Ok(response) => response,
        Err(reason) => {
            log::warn!("Android secure media bridge upstream request failed: {reason}");
            record_failure(
                target,
                TransportFailure::bridge(
                    TransportFailureStage::Request,
                    reason,
                    request.method(),
                    StatusCode::BAD_GATEWAY,
                    None,
                ),
            );
            return plain_response(StatusCode::BAD_GATEWAY, "upstream media connection failed");
        }
    };

    if response.status().is_client_error() || response.status().is_server_error() {
        record_failure(
            target,
            TransportFailure::upstream(
                TransportFailureStage::Response,
                "http-status",
                request.method(),
                response.status(),
            ),
        );
    }

    let mime_hls = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(hls_proxy::content_type)
        || response
            .url()
            .path()
            .to_ascii_lowercase()
            .ends_with(".m3u8");
    let mut prefix = Vec::new();
    if response.status().is_success() && request.method() == Method::GET && !mime_hls {
        while prefix.len() < 10 {
            match response.chunk().await {
                Ok(Some(chunk)) => prefix.extend_from_slice(&chunk),
                Ok(None) => break,
                Err(error) => {
                    record_failure(
                        target,
                        TransportFailure::bridge(
                            TransportFailureStage::PrefixRead,
                            safe_error(&error),
                            request.method(),
                            StatusCode::BAD_GATEWAY,
                            Some(response.status()),
                        ),
                    );
                    return plain_response(StatusCode::BAD_GATEWAY, "upstream media read failed");
                }
            }
        }
    }
    if response.status().is_success() && (mime_hls || hls_proxy::signature(&prefix)) {
        return hls_response(
            state,
            target,
            request.method(),
            request.headers(),
            response,
            effective_headers,
            prefix,
        )
        .await;
    }

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
        let diagnostic_target = target.clone();
        let method = request.method().clone();
        let stream = response.bytes_stream().map(move |chunk| {
            if let Err(error) = &chunk {
                record_failure(
                    &diagnostic_target,
                    TransportFailure::upstream(
                        TransportFailureStage::BodyRead,
                        safe_error(error),
                        &method,
                        status,
                    ),
                );
            }
            chunk
        });
        let prefix = futures_util::stream::once(async move {
            Ok::<_, reqwest::Error>(axum::body::Bytes::from(prefix))
        });
        Body::from_stream(prefix.chain(stream))
    };
    builder.body(body).unwrap_or_else(|_| {
        record_failure(
            target,
            TransportFailure::bridge(
                TransportFailureStage::Response,
                "response-build",
                request.method(),
                StatusCode::BAD_GATEWAY,
                Some(status),
            ),
        );
        plain_response(StatusCode::BAD_GATEWAY, "invalid upstream response")
    })
}

async fn send_upstream(
    client: &reqwest::Client,
    method: &Method,
    url: &str,
    target_headers: &[MpvHttpHeader],
    playback_headers: &HeaderMap,
    owning_origin: &Url,
) -> Result<(reqwest::Response, Vec<MpvHttpHeader>), &'static str> {
    let mut current_url = validated_url(url)?;
    let mut forward_target_headers = true;

    for redirect_count in 0..=MAX_UPSTREAM_REDIRECTS {
        if owning_origin
            .path()
            .starts_with("/api/v1/player/online-assets/")
            && (!same_origin(&current_url, owning_origin) || !server_asset_path(&current_url))
        {
            return Err("server-asset-authority-denied");
        }
        let public_client;
        let client = if same_origin(&current_url, owning_origin) {
            client
        } else {
            public_client = public_client_for(&current_url).await?;
            &public_client
        };
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
            return Ok((
                response,
                if forward_target_headers {
                    target_headers.to_vec()
                } else {
                    Vec::new()
                },
            ));
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
        let next_url = validated_url(next_url.as_str())?;
        if current_url.scheme() == "https" && next_url.scheme() != "https" {
            return Err("redirect-downgrade");
        }
        if !same_origin(&current_url, &next_url) {
            forward_target_headers = false;
        }
        current_url = next_url;
    }

    Err("redirect-limit")
}

fn validated_url(value: &str) -> Result<Url, &'static str> {
    let url = Url::parse(value).map_err(|_| "invalid-url")?;
    if value.len() > hls_proxy::MAX_URI_BYTES
        || !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err("invalid-url");
    }
    Ok(url)
}

async fn public_client_for(url: &Url) -> Result<reqwest::Client, &'static str> {
    let host = url.host_str().ok_or("invalid-url")?;
    let port = url.port_or_known_default().ok_or("invalid-url")?;
    #[cfg(not(test))]
    if url.scheme() != "https" {
        return Err("public-media-requires-https");
    }
    let addresses = {
        #[cfg(test)]
        if matches!(host, "127.0.0.1" | "localhost") {
            tokio::net::lookup_host((host, port))
                .await
                .map_err(|_| "public-dns")?
                .collect::<Vec<_>>()
        } else {
            resolve_cdn(host, port).await.map_err(|_| "public-dns")?
        }
        #[cfg(not(test))]
        {
            resolve_cdn(host, port).await.map_err(|_| "public-dns")?
        }
    };
    if addresses.is_empty() || addresses.iter().any(|address| !public_ip(address.ip())) {
        #[cfg(not(test))]
        return Err("public-address-denied");
        #[cfg(test)]
        if addresses.is_empty() || addresses.iter().any(|address| !address.ip().is_loopback()) {
            return Err("public-address-denied");
        }
    }
    reqwest::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(15))
        .read_timeout(Duration::from_secs(30))
        .resolve_to_addrs(host, &addresses)
        .build()
        .map_err(|_| "public-client")
}

async fn hls_response(
    state: &ProxyAppState,
    target: &ProxyTarget,
    method: &Method,
    request_headers: &HeaderMap,
    response: reqwest::Response,
    effective_headers: Vec<MpvHttpHeader>,
    bytes: Vec<u8>,
) -> Response<Body> {
    // Include full-control refetch and publication, not only body reads, in the
    // deadline once this response has been identified as an HLS document.
    tokio::time::timeout(
        Duration::from_secs(20),
        hls_response_inner(
            state,
            target,
            method,
            request_headers,
            response,
            effective_headers,
            bytes,
        ),
    )
    .await
    .unwrap_or_else(|_| {
        record_failure(
            target,
            TransportFailure::bridge(
                TransportFailureStage::HlsControl,
                "timeout",
                method,
                StatusCode::BAD_GATEWAY,
                None,
            ),
        );
        plain_response(StatusCode::BAD_GATEWAY, "HLS control timeout")
    })
}

async fn hls_response_inner(
    state: &ProxyAppState,
    target: &ProxyTarget,
    method: &Method,
    request_headers: &HeaderMap,
    mut response: reqwest::Response,
    mut effective_headers: Vec<MpvHttpHeader>,
    mut bytes: Vec<u8>,
) -> Response<Body> {
    // A native Range may cover only the start/middle of the original manifest.
    // Rewriting requires the complete control entity, then ranges are applied to
    // the transformed bytes (whose tokens and length differ from the upstream).
    let refetch = response.status() == StatusCode::PARTIAL_CONTENT || method == Method::HEAD;
    if refetch {
        match send_upstream(
            &state.client,
            &Method::GET,
            response.url().as_str(),
            &effective_headers,
            &HeaderMap::new(),
            &target.session.origin,
        )
        .await
        {
            Ok((full, headers)) if full.status() == StatusCode::OK => {
                response = full;
                effective_headers = headers;
                bytes.clear();
            }
            Ok((full, _)) => {
                record_failure(
                    target,
                    TransportFailure::bridge(
                        TransportFailureStage::HlsRefetch,
                        "http-status",
                        &Method::GET,
                        StatusCode::BAD_GATEWAY,
                        Some(full.status()),
                    ),
                );
                return plain_response(StatusCode::BAD_GATEWAY, "complete HLS control unavailable");
            }
            Err(reason) => {
                record_failure(
                    target,
                    TransportFailure::bridge(
                        TransportFailureStage::HlsRefetch,
                        reason,
                        &Method::GET,
                        StatusCode::BAD_GATEWAY,
                        None,
                    ),
                );
                return plain_response(StatusCode::BAD_GATEWAY, "complete HLS control unavailable");
            }
        }
    }
    let base = response.url().clone();
    let upstream_status = response.status();
    let read_method = if refetch { &Method::GET } else { method };
    let read = async {
        if response
            .content_length()
            .is_some_and(|size| size > hls_proxy::MAX_MANIFEST_BYTES as u64)
            || bytes.len() > hls_proxy::MAX_MANIFEST_BYTES
        {
            return Err("hls-body-too-large");
        }
        while let Some(chunk) = response.chunk().await.map_err(|error| safe_error(&error))? {
            if bytes.len() + chunk.len() > hls_proxy::MAX_MANIFEST_BYTES {
                return Err("hls-body-too-large");
            }
            bytes.extend_from_slice(&chunk);
        }
        Ok(())
    }
    .await;
    if let Err(reason) = read {
        record_failure(
            target,
            TransportFailure::bridge(
                TransportFailureStage::HlsRead,
                reason,
                read_method,
                StatusCode::BAD_GATEWAY,
                Some(upstream_status),
            ),
        );
        return plain_response(StatusCode::BAD_GATEWAY, "invalid HLS control");
    }
    let rewritten = match register_hls(state, target, &base, &effective_headers, &bytes).await {
        Ok(value) => value,
        Err(reason) => {
            log::warn!("Native secure media bridge HLS rejected: {reason}");
            let status = if reason == "session-expired" {
                StatusCode::GONE
            } else {
                StatusCode::BAD_GATEWAY
            };
            record_failure(
                target,
                TransportFailure::bridge(
                    TransportFailureStage::HlsRewrite,
                    reason,
                    read_method,
                    status,
                    Some(upstream_status),
                ),
            );
            return plain_response(status, "HLS control unavailable");
        }
    };
    control_response(method, request_headers, rewritten)
}

async fn register_hls(
    state: &ProxyAppState,
    target: &ProxyTarget,
    base: &Url,
    effective_headers: &[MpvHttpHeader],
    bytes: &[u8],
) -> Result<Vec<u8>, &'static str> {
    if target.depth >= MAX_HLS_DEPTH {
        return Err("hls-depth-limit");
    }
    let mut targets = state.targets.write().await;
    // clear/new-load takes this same lock and cancels the owning root. Never
    // publish children from a manifest whose request outlived its playback.
    if !target.session.active()
        || !targets
            .values()
            .any(|value| Arc::ptr_eq(&value.session, &target.session))
    {
        return Err("session-expired");
    }
    let mut ancestors = target.ancestors.as_ref().clone();
    ancestors.push(target.url.clone());
    if base.as_str() != target.url {
        ancestors.push(base.as_str().to_string());
    }
    let ancestors = Arc::new(ancestors);
    let inherited_headers = Arc::new(effective_headers.to_vec());
    let public_headers = Arc::new(Vec::new());
    let session_count = targets
        .values()
        .filter(|value| Arc::ptr_eq(&value.session, &target.session))
        .count();
    let derived_count = targets.values().filter(|value| value.depth > 0).count();
    let mut pending: HashMap<String, ProxyTarget> = HashMap::new();
    let rewritten = hls_proxy::rewrite(bytes, |uri| {
        let url = base.join(uri).map_err(|_| "hls-uri-invalid")?;
        let url = validated_url(url.as_str())?;
        // Server gateway HLS has an exact opaque media-only namespace. Never
        // turn its Bearer into an authenticated arbitrary same-origin fetch.
        if target
            .session
            .origin
            .path()
            .starts_with("/api/v1/player/online-assets/")
            && (!same_origin(&target.session.origin, &url) || !server_asset_path(&url))
        {
            return Err("hls-server-asset-denied");
        }
        if ancestors.iter().any(|ancestor| ancestor == url.as_str()) {
            return Err("hls-cycle");
        }
        if base.scheme() == "https" && url.scheme() != "https" {
            return Err("hls-downgrade");
        }
        let headers = if same_origin(base, &url) {
            inherited_headers.clone()
        } else {
            public_headers.clone()
        };
        let existing = targets.iter().chain(pending.iter()).find(|(_, value)| {
            Arc::ptr_eq(&value.session, &target.session)
                && value.url == url.as_str()
                && value.ancestors == ancestors
                && headers_equal(&value.headers, &headers)
        });
        let token = if let Some((token, _)) = existing {
            token.clone()
        } else {
            if session_count + pending.len() > hls_proxy::MAX_HLS_REFERENCES
                || derived_count + pending.len() >= MAX_DERIVED_TARGETS
            {
                return Err("hls-target-limit");
            }
            let token = random_token();
            pending.insert(
                token.clone(),
                ProxyTarget {
                    url: url.to_string(),
                    headers,
                    session: target.session.clone(),
                    depth: target.depth + 1,
                    ancestors: ancestors.clone(),
                },
            );
            token
        };
        Ok(format!("/media/{token}"))
    })?;
    targets.extend(pending);
    Ok(rewritten)
}

fn headers_equal(left: &[MpvHttpHeader], right: &[MpvHttpHeader]) -> bool {
    left.len() == right.len()
        && left
            .iter()
            .zip(right)
            .all(|(left, right)| left.name == right.name && left.value == right.value)
}

fn server_asset_path(url: &Url) -> bool {
    let Some(opaque) = url.path().strip_prefix("/api/v1/player/online-assets/") else {
        return false;
    };
    url.query().is_none()
        && opaque.len() == 36
        && opaque.bytes().enumerate().all(|(index, value)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                value == b'-'
            } else {
                value.is_ascii_hexdigit()
            }
        })
}

fn control_response(method: &Method, headers: &HeaderMap, bytes: Vec<u8>) -> Response<Body> {
    let total = bytes.len();
    let range = headers.get(RANGE).map(|value| {
        value
            .to_str()
            .ok()
            .and_then(|value| control_range(value, total))
    });
    let (status, start, end) = match range {
        Some(Some((start, end))) => (StatusCode::PARTIAL_CONTENT, start, end),
        Some(None) => {
            return Response::builder()
                .status(StatusCode::RANGE_NOT_SATISFIABLE)
                .header(CONTENT_RANGE, format!("bytes */{total}"))
                .header(CACHE_CONTROL, "no-store")
                .body(Body::empty())
                .unwrap()
        }
        None => (StatusCode::OK, 0, total - 1),
    };
    let mut builder = Response::builder()
        .status(status)
        .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
        .header(CACHE_CONTROL, "no-store")
        .header(ACCEPT_RANGES, "bytes")
        .header(CONTENT_LENGTH, end - start + 1);
    if status == StatusCode::PARTIAL_CONTENT {
        builder = builder.header(CONTENT_RANGE, format!("bytes {start}-{end}/{total}"));
    }
    let body = if method == Method::HEAD {
        Body::empty()
    } else {
        Body::from(bytes[start..=end].to_vec())
    };
    builder
        .body(body)
        .unwrap_or_else(|_| plain_response(StatusCode::BAD_GATEWAY, "invalid HLS control response"))
}

fn control_range(value: &str, total: usize) -> Option<(usize, usize)> {
    let value = value.strip_prefix("bytes=")?;
    let (start, end) = value.split_once('-')?;
    if start.is_empty() {
        let length = end.parse::<usize>().ok()?;
        if length == 0 {
            return None;
        }
        Some((total.saturating_sub(length), total - 1))
    } else {
        let start = start.parse::<usize>().ok()?;
        let end = if end.is_empty() {
            total - 1
        } else {
            end.parse::<usize>().ok()?.min(total - 1)
        };
        (start <= end && start < total).then_some((start, end))
    }
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

fn diagnostic_reason(reason: &'static str) -> &'static str {
    match reason {
        "connect"
        | "timeout"
        | "redirect"
        | "request"
        | "http-status"
        | "response-build"
        | "bridge-busy"
        | "invalid-url"
        | "server-asset-authority-denied"
        | "redirect-limit"
        | "redirect-location"
        | "redirect-downgrade"
        | "public-media-requires-https"
        | "public-dns"
        | "public-address-denied"
        | "public-client"
        | "hls-body-too-large"
        | "session-expired"
        | "hls-depth-limit"
        | "hls-uri-invalid"
        | "hls-server-asset-denied"
        | "hls-cycle"
        | "hls-downgrade"
        | "hls-target-limit"
        | "hls-manifest-invalid"
        | "hls-manifest-encoding"
        | "hls-manifest-limit" => reason,
        _ => "unclassified",
    }
}

#[cfg(test)]
#[path = "hls_transport_tests.rs"]
mod hls_transport_tests;

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
                            headers
                                .get(AUTHORIZATION)
                                .and_then(|value| value.to_str().ok())
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
                    vec![
                        MpvHttpHeader {
                            name: "X-Emby-Token".to_string(),
                            value: "private-token".to_string(),
                        },
                        MpvHttpHeader {
                            name: "Authorization".to_string(),
                            value: "Bearer private-device-token".to_string(),
                        },
                        MpvHttpHeader {
                            name: "Cookie".to_string(),
                            value: "private=session".to_string(),
                        },
                    ],
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

    #[test]
    fn loopback_bridge_keeps_dash_video_and_audio_targets_in_one_session() {
        tauri::async_runtime::block_on(async {
            let upstream_listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
            let upstream_address = upstream_listener.local_addr().unwrap();
            let upstream = Router::new()
                .route(
                    "/video",
                    get(|headers: HeaderMap| async move {
                        let status = if headers.get(RANGE).and_then(|value| value.to_str().ok())
                            == Some("bytes=0-10")
                        {
                            StatusCode::PARTIAL_CONTENT
                        } else {
                            StatusCode::BAD_REQUEST
                        };
                        Response::builder()
                            .status(status)
                            .header("content-range", "bytes 0-10/11")
                            .body(axum::body::Body::from("video-track"))
                            .unwrap()
                    }),
                )
                .route(
                    "/audio",
                    get(|headers: HeaderMap| async move {
                        let status = if headers.get(RANGE).and_then(|value| value.to_str().ok())
                            == Some("bytes=0-10")
                        {
                            StatusCode::PARTIAL_CONTENT
                        } else {
                            StatusCode::BAD_REQUEST
                        };
                        Response::builder()
                            .status(status)
                            .header("content-range", "bytes 0-10/11")
                            .body(axum::body::Body::from("audio-track"))
                            .unwrap()
                    }),
                );
            tauri::async_runtime::spawn(async move {
                axum::serve(upstream_listener, upstream).await.unwrap();
            });

            let proxy = AndroidStreamProxyState::default();
            let video_url = proxy
                .prepare(
                    format!("http://{upstream_address}/video"),
                    vec![MpvHttpHeader {
                        name: "Referer".to_string(),
                        value: "https://www.bilibili.com/".to_string(),
                    }],
                )
                .await
                .unwrap();
            let audio_url = proxy
                .prepare(
                    format!("http://{upstream_address}/audio"),
                    vec![MpvHttpHeader {
                        name: "Referer".to_string(),
                        value: "https://www.bilibili.com/".to_string(),
                    }],
                )
                .await
                .unwrap();

            let client = reqwest::Client::new();
            let (video, audio) = tokio::join!(
                client.get(&video_url).header(RANGE, "bytes=0-10").send(),
                client.get(&audio_url).header(RANGE, "bytes=0-10").send(),
            );
            let video = video.unwrap();
            let audio = audio.unwrap();
            assert_eq!(video.status(), StatusCode::PARTIAL_CONTENT);
            assert_eq!(audio.status(), StatusCode::PARTIAL_CONTENT);
            assert_eq!(video.text().await.unwrap(), "video-track");
            assert_eq!(audio.text().await.unwrap(), "audio-track");

            let unknown = client
                .get(video_url.replace("/media/", "/media/unknown-"))
                .send()
                .await
                .unwrap();
            assert_eq!(unknown.status(), StatusCode::NOT_FOUND);

            proxy.clear().await;
            let expired = client.get(audio_url).send().await.unwrap();
            assert_eq!(expired.status(), StatusCode::GONE);
        });
    }

    #[test]
    fn loopback_bridge_rewrites_server_hls_children_with_owning_bearer() {
        tauri::async_runtime::block_on(async {
            let listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
            let address = listener.local_addr().unwrap();
            let upstream = Router::new().route(
                "/api/v1/player/online-assets/:asset",
                get(|axum::extract::Path(asset): axum::extract::Path<String>, headers: HeaderMap| async move {
                    if headers.get(AUTHORIZATION).and_then(|value| value.to_str().ok()) != Some("Bearer fixture-device") {
                        return Response::builder().status(StatusCode::UNAUTHORIZED).body(axum::body::Body::empty()).unwrap();
                    }
                    let (content_type, body) = match asset.as_str() {
                        "00000000-0000-4000-8000-000000000001" => ("application/vnd.apple.mpegurl", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n/api/v1/player/online-assets/00000000-0000-4000-8000-000000000002\n"),
                        "00000000-0000-4000-8000-000000000002" => ("application/vnd.apple.mpegurl", "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-KEY:METHOD=AES-128,URI=\"/api/v1/player/online-assets/00000000-0000-4000-8000-000000000003\"\n#EXT-X-MAP:URI=\"/api/v1/player/online-assets/00000000-0000-4000-8000-000000000004\"\n#EXTINF:6,\n/api/v1/player/online-assets/00000000-0000-4000-8000-000000000005\n#EXT-X-ENDLIST\n"),
                        "00000000-0000-4000-8000-000000000003" => ("application/octet-stream", "0123456789abcdef"),
                        "00000000-0000-4000-8000-000000000004" => ("video/mp4", "fixture-init"),
                        "00000000-0000-4000-8000-000000000005" => ("video/mp2t", "fixture-segment"),
                        _ => return Response::builder().status(StatusCode::NOT_FOUND).body(axum::body::Body::empty()).unwrap(),
                    };
                    Response::builder().header("content-type", content_type).body(axum::body::Body::from(body)).unwrap()
                }),
            );
            let server = tauri::async_runtime::spawn(async move {
                axum::serve(listener, upstream).await.unwrap()
            });
            let proxy = AndroidStreamProxyState::default();
            let root = proxy
                .prepare(
                    format!("http://{address}/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001"),
                    vec![MpvHttpHeader {
                        name: "Authorization".into(),
                        value: "Bearer fixture-device".into(),
                    }],
                )
                .await
                .unwrap();
            let client = reqwest::Client::new();
            let master = client
                .get(&root)
                .send()
                .await
                .unwrap()
                .text()
                .await
                .unwrap();
            let child = master
                .lines()
                .find(|line| !line.starts_with('#') && !line.is_empty())
                .unwrap();
            let nested = client
                .get(Url::parse(&root).unwrap().join(child).unwrap())
                .send()
                .await
                .unwrap();
            assert_eq!(
                nested.status(),
                StatusCode::OK,
                "Server-shaped HLS child must remain reachable through actual native proxy"
            );
            let manifest = nested.text().await.unwrap();
            for (line, expected) in [
                ("#EXT-X-KEY:", "0123456789abcdef"),
                ("#EXT-X-MAP:", "fixture-init"),
            ] {
                let directive = manifest
                    .lines()
                    .find(|value| value.starts_with(line))
                    .unwrap();
                let uri = directive
                    .split("URI=\"")
                    .nth(1)
                    .unwrap()
                    .split('"')
                    .next()
                    .unwrap();
                let response = client
                    .get(Url::parse(&root).unwrap().join(uri).unwrap())
                    .send()
                    .await
                    .unwrap();
                assert_eq!(response.status(), StatusCode::OK);
                assert_eq!(response.text().await.unwrap(), expected);
            }
            let segment = manifest
                .lines()
                .find(|line| !line.starts_with('#') && !line.is_empty())
                .unwrap();
            let response = client
                .get(Url::parse(&root).unwrap().join(segment).unwrap())
                .send()
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            assert_eq!(response.text().await.unwrap(), "fixture-segment");
            proxy.clear().await;
            server.abort();
        });
    }
}

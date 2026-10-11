use super::*;
use axum::{
    http::header::{AUTHORIZATION, COOKIE},
    routing::get,
};
use std::sync::{
    atomic::{AtomicUsize, Ordering},
    Mutex,
};

async fn serve(app: Router) -> (String, tauri::async_runtime::JoinHandle<()>) {
    let listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
    let address = listener.local_addr().unwrap();
    let task =
        tauri::async_runtime::spawn(async move { axum::serve(listener, app).await.unwrap() });
    (format!("http://{address}"), task)
}

fn secret_headers() -> Vec<MpvHttpHeader> {
    vec![
        MpvHttpHeader {
            name: "Authorization".into(),
            value: "Bearer fixture".into(),
        },
        MpvHttpHeader {
            name: "Cookie".into(),
            value: "fixture=private".into(),
        },
    ]
}

fn uri(text: &str) -> &str {
    text.lines()
        .find(|line| !line.starts_with('#') && !line.is_empty())
        .unwrap()
}

fn join(base: &str, path: &str) -> Url {
    Url::parse(base).unwrap().join(path).unwrap()
}

#[test]
fn actual_server_asset_redirect_cannot_escape_media_authority() {
    tauri::async_runtime::block_on(async {
        let escaped = Arc::new(AtomicUsize::new(0));
        let count = escaped.clone();
        let app = Router::new()
            .route(
                "/api/v1/player/online-assets/:asset",
                get(|Path(asset): Path<String>, headers: HeaderMap| async move {
                    assert_eq!(headers.get(AUTHORIZATION).unwrap(), "Bearer fixture");
                    match asset.as_str() {
                        "00000000-0000-4000-8000-000000000001" => Response::builder()
                            .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                            .body(Body::from("#EXTM3U\n#EXTINF:6,\n/api/v1/player/online-assets/00000000-0000-4000-8000-000000000002\n#EXT-X-ENDLIST\n"))
                            .unwrap(),
                        "00000000-0000-4000-8000-000000000004" => Response::builder()
                            .status(StatusCode::FOUND)
                            .header(LOCATION, "/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001?unexpected=1")
                            .body(Body::empty()).unwrap(),
                        "00000000-0000-4000-8000-000000000005" => Response::builder()
                            .status(StatusCode::FOUND)
                            .header(LOCATION, "/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001#fragment")
                            .body(Body::empty()).unwrap(),
                        _ => Response::builder().status(StatusCode::FOUND)
                            .header(LOCATION, "/api/v1/player/bootstrap")
                            .body(Body::empty()).unwrap(),
                    }
                }),
            )
            .route("/api/v1/player/bootstrap", get(move || {
                let count = count.clone();
                async move {
                    count.fetch_add(1, Ordering::SeqCst);
                    Body::from("non-media-route")
                }
            }));
        let (base, server) = serve(app).await;
        let proxy = AndroidStreamProxyState::default();
        let client = reqwest::Client::new();
        for suffix in [3, 4, 5] {
            let root = proxy
                .prepare(
                    format!(
                        "{base}/api/v1/player/online-assets/00000000-0000-4000-8000-{suffix:012}"
                    ),
                    secret_headers(),
                )
                .await
                .unwrap();
            assert_eq!(
                client.get(root).send().await.unwrap().status(),
                StatusCode::BAD_GATEWAY,
                "Server asset redirect must remain inside its exact media authority"
            );
        }
        let root = proxy
            .prepare(
                format!("{base}/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001"),
                secret_headers(),
            )
            .await
            .unwrap();
        let master = client
            .get(&root)
            .send()
            .await
            .unwrap()
            .text()
            .await
            .unwrap();
        assert_eq!(
            client
                .get(join(&root, uri(&master)))
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::BAD_GATEWAY
        );
        assert_eq!(
            escaped.load(Ordering::SeqCst),
            0,
            "no non-media request may be issued"
        );
        proxy.clear().await;
        server.abort();
    });
}

#[test]
fn actual_hls_ranges_head_sniff_and_browser_rejection() {
    tauri::async_runtime::block_on(async {
        let controls = Arc::new(AtomicUsize::new(0));
        let count = controls.clone();
        let app = Router::new().route("/root", get(move |headers: HeaderMap| {
            let count = count.clone();
            async move {
                count.fetch_add(1, Ordering::SeqCst);
                let body = "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nsegment\n#EXT-X-ENDLIST\n";
                if headers.contains_key(RANGE) {
                    Response::builder().status(StatusCode::PARTIAL_CONTENT).header(CONTENT_TYPE,"application/vnd.apple.mpegurl")
                        .header(CONTENT_RANGE,format!("bytes 0-8/{}",body.len())).header(ETAG,"upstream-only").body(Body::from(&body[..9])).unwrap()
                } else { Response::builder().header(CONTENT_TYPE,"application/vnd.apple.mpegurl").body(Body::from(body)).unwrap() }
            }
        })).route("/sniff", get(|| async { Response::builder().header(CONTENT_TYPE,"application/octet-stream").body(Body::from("#EXTM3U\n#EXTINF:6,\nsegment\n#EXT-X-ENDLIST\n")).unwrap() }))
        .route("/segment", get(|headers: HeaderMap| async move {
            assert!(!headers.contains_key(AUTHORIZATION));
            assert_eq!(headers.get(RANGE).unwrap(), "bytes=2-5");
            Response::builder().status(StatusCode::PARTIAL_CONTENT).header(CONTENT_TYPE,"video/mp2t").header(CONTENT_RANGE,"bytes 2-5/8").body(Body::from("cdef")).unwrap()
        }));
        let (base, server) = serve(app).await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy.prepare(format!("{base}/root"), vec![]).await.unwrap();
        let client = reqwest::Client::new();
        let first = client
            .get(&root)
            .header(RANGE, "bytes=0-11")
            .send()
            .await
            .unwrap();
        assert_eq!(first.status(), StatusCode::PARTIAL_CONTENT);
        assert_eq!(first.headers().get(CONTENT_LENGTH).unwrap(), "12");
        assert!(!first.headers().contains_key(ETAG));
        let length = first
            .headers()
            .get(CONTENT_RANGE)
            .unwrap()
            .to_str()
            .unwrap()
            .split('/')
            .nth(1)
            .unwrap()
            .parse::<usize>()
            .unwrap();
        assert_eq!(first.text().await.unwrap(), "#EXTM3U\n#EXT");
        assert_eq!(
            controls.load(Ordering::SeqCst),
            2,
            "partial upstream must be refetched completely"
        );
        let full = client
            .get(&root)
            .send()
            .await
            .unwrap()
            .text()
            .await
            .unwrap();
        assert_eq!(full.len(), length);
        assert!(!full.contains(&base));
        let head = client.head(&root).send().await.unwrap();
        assert_eq!(head.status(), StatusCode::OK);
        assert_eq!(
            head.headers()
                .get(CONTENT_LENGTH)
                .unwrap()
                .to_str()
                .unwrap()
                .parse::<usize>()
                .unwrap(),
            length
        );
        assert!(head.bytes().await.unwrap().is_empty());
        for range in ["bytes=999999-", "bytes=0-1,4-5", "bytes=-0"] {
            assert_eq!(
                client
                    .get(&root)
                    .header(RANGE, range)
                    .send()
                    .await
                    .unwrap()
                    .status(),
                StatusCode::RANGE_NOT_SATISFIABLE
            );
        }
        let suffix = client
            .get(&root)
            .header(RANGE, "bytes=-10")
            .send()
            .await
            .unwrap();
        assert_eq!(suffix.status(), StatusCode::PARTIAL_CONTENT);
        assert_eq!(suffix.text().await.unwrap(), full[length - 10..]);
        let segment = client
            .get(join(&root, uri(&full)))
            .header(RANGE, "bytes=2-5")
            .header(AUTHORIZATION, "Bearer browser-injected")
            .send()
            .await
            .unwrap();
        assert_eq!(segment.status(), StatusCode::PARTIAL_CONTENT);
        assert_eq!(segment.text().await.unwrap(), "cdef");
        assert_eq!(
            client
                .get(&root)
                .header(ORIGIN, "https://untrusted.example")
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::FORBIDDEN
        );
        assert_eq!(
            client
                .get(&root)
                .header("sec-fetch-site", "cross-site")
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::FORBIDDEN
        );
        assert_eq!(
            client
                .get(&root)
                .header(HOST, "evil.example")
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::FORBIDDEN
        );
        let sniff = proxy
            .prepare(format!("{base}/sniff"), vec![])
            .await
            .unwrap();
        let manifest = client
            .get(&sniff)
            .send()
            .await
            .unwrap()
            .text()
            .await
            .unwrap();
        assert!(uri(&manifest).starts_with("/media/"));
        proxy.clear().await;
        assert_eq!(
            client
                .get(join(&root, uri(&full)))
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::GONE
        );
        server.abort();
    });
}

#[test]
fn actual_redirect_base_and_children_never_restore_private_headers() {
    tauri::async_runtime::block_on(async {
        let destination = Arc::new(Mutex::new(String::new()));
        let seen = Arc::new(AtomicUsize::new(0));
        let redirect = destination.clone();
        let count = seen.clone();
        let app = Router::new()
            .route(
                "/root",
                get(move |headers: HeaderMap| {
                    let redirect = redirect.clone();
                    let count = count.clone();
                    async move {
                        assert_eq!(headers.get(AUTHORIZATION).unwrap(), "Bearer fixture");
                        assert_eq!(headers.get(COOKIE).unwrap(), "fixture=private");
                        count.fetch_add(1, Ordering::SeqCst);
                        Response::builder()
                            .status(StatusCode::FOUND)
                            .header(LOCATION, redirect.lock().unwrap().clone())
                            .body(Body::empty())
                            .unwrap()
                    }
                }),
            )
            .route(
                "/return",
                get(|headers: HeaderMap| async move {
                    assert!(!headers.contains_key(AUTHORIZATION));
                    assert!(!headers.contains_key(COOKIE));
                    Response::builder()
                        .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                        .body(Body::from(
                            "#EXTM3U\n#EXTINF:6,\nfinal-segment\n#EXT-X-ENDLIST\n",
                        ))
                        .unwrap()
                }),
            )
            .route(
                "/final-segment",
                get(|headers: HeaderMap| async move {
                    assert!(!headers.contains_key(AUTHORIZATION));
                    assert!(!headers.contains_key(COOKIE));
                    Body::from("public-segment")
                }),
            );
        let (first, first_server) = serve(app).await;
        let return_url = format!("{first}/return");
        let app = Router::new()
            .route(
                "/dir/master",
                get(|headers: HeaderMap| async move {
                    assert!(!headers.contains_key(AUTHORIZATION));
                    assert!(!headers.contains_key(COOKIE));
                    Response::builder()
                        .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                        .body(Body::from(
                            "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nnested\n",
                        ))
                        .unwrap()
                }),
            )
            .route(
                "/dir/nested",
                get(move |headers: HeaderMap| {
                    let return_url = return_url.clone();
                    async move {
                        assert!(!headers.contains_key(AUTHORIZATION));
                        assert!(!headers.contains_key(COOKIE));
                        Response::builder()
                            .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                            .body(Body::from(format!(
                                "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n{return_url}\n"
                            )))
                            .unwrap()
                    }
                }),
            );
        let (second, second_server) = serve(app).await;
        *destination.lock().unwrap() = format!("{second}/dir/master");
        let proxy = AndroidStreamProxyState::default();
        let root = proxy
            .prepare(format!("{first}/root"), secret_headers())
            .await
            .unwrap();
        let client = reqwest::Client::new();
        let mut current = Url::parse(&root).unwrap();
        for _ in 0..3 {
            let response = client.get(current.clone()).send().await.unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            let body = response.text().await.unwrap();
            assert!(!body.contains(&first));
            assert!(!body.contains(&second));
            current = join(&root, uri(&body));
        }
        assert_eq!(
            client
                .get(current)
                .send()
                .await
                .unwrap()
                .text()
                .await
                .unwrap(),
            "public-segment"
        );
        assert_eq!(seen.load(Ordering::SeqCst), 1);
        proxy.clear().await;
        first_server.abort();
        second_server.abort();
    });
}

#[test]
fn hls_publication_is_atomic_bounded_and_fenced_by_root_session() {
    tauri::async_runtime::block_on(async {
        let proxy = AndroidStreamProxyState::default();
        let root = proxy
            .prepare("http://127.0.0.1:9876/root".into(), vec![])
            .await
            .unwrap();
        let (state, target) = {
            let runtime = proxy.runtime.lock().await;
            let runtime = runtime.as_ref().unwrap();
            let target = runtime
                .targets
                .read()
                .await
                .values()
                .next()
                .unwrap()
                .clone();
            (
                ProxyAppState {
                    client: reqwest::Client::new(),
                    targets: runtime.targets.clone(),
                    port: runtime.port,
                    slots: Arc::new(Semaphore::new(32)),
                },
                target,
            )
        };
        let base = Url::parse(&target.url).unwrap();
        let manifest = format!(
            "#EXTM3U\n{}#EXT-X-ENDLIST\n",
            (0..1000)
                .map(|index| format!("#EXTINF:6,\nsegment{index}\n"))
                .collect::<String>()
        );
        let full = register_hls(&state, &target, &base, &[], manifest.as_bytes())
            .await
            .unwrap();
        assert_eq!(state.targets.read().await.len(), 1001);
        assert_eq!(
            register_hls(&state, &target, &base, &[], manifest.as_bytes())
                .await
                .unwrap(),
            full,
            "same entity deduplicates capabilities"
        );
        for index in 1..MAX_SESSION_TARGETS {
            proxy
                .prepare(format!("http://127.0.0.1:9876/root{index}"), vec![])
                .await
                .unwrap();
        }
        assert!(
            proxy
                .prepare("http://127.0.0.1:9876/overflow".into(), vec![])
                .await
                .is_err(),
            "roots remain independently bounded"
        );
        let before = state.targets.read().await.len();
        assert!(register_hls(
            &state,
            &target,
            &base,
            &[],
            b"#EXTM3U\nnew-valid\nfile:///etc/passwd\n"
        )
        .await
        .is_err());
        assert_eq!(
            state.targets.read().await.len(),
            before,
            "failed control exposes no partial target registrations"
        );
        let mut deep = target.clone();
        deep.depth = MAX_HLS_DEPTH;
        assert!(register_hls(&state, &deep, &base, &[], manifest.as_bytes())
            .await
            .is_err());
        assert!(
            register_hls(&state, &target, &base, &[], b"#EXTM3U\nroot\n")
                .await
                .is_err()
        );
        proxy.clear().await;
        assert_eq!(
            register_hls(&state, &target, &base, &[], manifest.as_bytes())
                .await
                .unwrap_err(),
            "session-expired"
        );
        assert!(state.targets.read().await.is_empty());
        assert!(
            reqwest::Client::new()
                .get(root)
                .send()
                .await
                .unwrap()
                .status()
                == StatusCode::GONE
        );
    });
}

#[test]
fn hls_owning_server_asset_namespace_and_unsafe_url_matrix() {
    tauri::async_runtime::block_on(async {
        let proxy = AndroidStreamProxyState::default();
        proxy.prepare("http://127.0.0.1:9876/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001".into(),secret_headers()).await.unwrap();
        let (state, target) = {
            let runtime = proxy.runtime.lock().await;
            let runtime = runtime.as_ref().unwrap();
            let target = runtime
                .targets
                .read()
                .await
                .values()
                .next()
                .unwrap()
                .clone();
            (
                ProxyAppState {
                    client: reqwest::Client::new(),
                    targets: runtime.targets.clone(),
                    port: runtime.port,
                    slots: Arc::new(Semaphore::new(32)),
                },
                target,
            )
        };
        let base = Url::parse(&target.url).unwrap();
        for url in ["/api/v1/admin/credentials", "https://foreign.example/api/v1/player/online-assets/00000000-0000-4000-8000-000000000002", "/api/v1/player/online-assets/00000000-0000-4000-8000-000000000002?token=bad", "file:///secret", "http://user:secret@127.0.0.1:9876/private", "data:text/plain,secret"] {
            assert!(register_hls(&state,&target,&base,&target.headers,format!("#EXTM3U\n{url}\n").as_bytes()).await.is_err());
            assert_eq!(state.targets.read().await.len(),1);
        }
        proxy.clear().await;
    });
}

#[test]
fn actual_inflight_manifest_is_cancelled_before_child_publication() {
    tauri::async_runtime::block_on(async {
        let started = Arc::new(tokio::sync::Notify::new());
        let release = Arc::new(tokio::sync::Notify::new());
        let start = started.clone();
        let unblock = release.clone();
        let (base, server) = serve(Router::new().route(
            "/root",
            get(move || {
                let start = start.clone();
                let unblock = unblock.clone();
                async move {
                    start.notify_one();
                    unblock.notified().await;
                    Response::builder()
                        .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                        .body(Body::from("#EXTM3U\n#EXTINF:6,\nsegment\n#EXT-X-ENDLIST\n"))
                        .unwrap()
                }
            }),
        ))
        .await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy.prepare(format!("{base}/root"), vec![]).await.unwrap();
        let pending = tauri::async_runtime::spawn(async move {
            reqwest::Client::new()
                .get(root)
                .send()
                .await
                .unwrap()
                .status()
        });
        started.notified().await;
        proxy.clear().await;
        assert_eq!(
            tokio::time::timeout(Duration::from_secs(2), pending)
                .await
                .unwrap()
                .unwrap(),
            StatusCode::GONE
        );
        release.notify_one();
        let runtime = proxy.runtime.lock().await;
        assert!(runtime.as_ref().unwrap().targets.read().await.is_empty());
        server.abort();
    });
}

#[test]
fn exact_root_and_global_derived_capacities_reject_only_new_children() {
    tauri::async_runtime::block_on(async {
        let proxy = AndroidStreamProxyState::default();
        let mut roots = Vec::new();
        for index in 0..3 {
            let url = format!("http://127.0.0.1:9876/root{index}");
            proxy.prepare(url.clone(), vec![]).await.unwrap();
            roots.push(url);
        }
        let (state, root_targets) = {
            let runtime = proxy.runtime.lock().await;
            let runtime = runtime.as_ref().unwrap();
            let targets = runtime.targets.read().await;
            let roots = roots
                .iter()
                .map(|url| {
                    targets
                        .values()
                        .find(|value| &value.url == url)
                        .unwrap()
                        .clone()
                })
                .collect::<Vec<_>>();
            (
                ProxyAppState {
                    client: reqwest::Client::new(),
                    targets: runtime.targets.clone(),
                    port: runtime.port,
                    slots: Arc::new(Semaphore::new(32)),
                },
                roots,
            )
        };
        {
            let mut targets = state.targets.write().await;
            for index in 0..hls_proxy::MAX_HLS_REFERENCES - 1 {
                targets.insert(
                    format!("existing-a-{index}"),
                    ProxyTarget {
                        url: format!("http://127.0.0.1:9876/a{index}"),
                        depth: 1,
                        ..root_targets[0].clone()
                    },
                );
            }
        }
        let first = &root_targets[0];
        let base = Url::parse(&first.url).unwrap();
        let last = register_hls(&state, first, &base, &[], b"#EXTM3U\nlast\n")
            .await
            .unwrap();
        let before = state.targets.read().await.len();
        assert_eq!(
            register_hls(&state, first, &base, &[], b"#EXTM3U\noverflow\n")
                .await
                .unwrap_err(),
            "hls-target-limit"
        );
        assert_eq!(state.targets.read().await.len(), before);
        {
            let mut targets = state.targets.write().await;
            for index in 0..hls_proxy::MAX_HLS_REFERENCES {
                targets.insert(
                    format!("existing-b-{index}"),
                    ProxyTarget {
                        url: format!("http://127.0.0.1:9876/b{index}"),
                        depth: 1,
                        ..root_targets[1].clone()
                    },
                );
            }
            assert_eq!(
                targets.values().filter(|value| value.depth > 0).count(),
                MAX_DERIVED_TARGETS
            );
        }
        let third = &root_targets[2];
        assert_eq!(
            register_hls(
                &state,
                third,
                &Url::parse(&third.url).unwrap(),
                &[],
                b"#EXTM3U\nnew\n"
            )
            .await
            .unwrap_err(),
            "hls-target-limit"
        );
        assert_eq!(
            register_hls(&state, first, &base, &[], b"#EXTM3U\nlast\n")
                .await
                .unwrap(),
            last,
            "known child capabilities remain usable at capacity"
        );
        proxy.clear().await;
    });
}

#[test]
fn actual_control_deadline_includes_blocked_full_refetch() {
    tauri::async_runtime::block_on(async {
        let started = Arc::new(tokio::sync::Notify::new());
        let release = Arc::new(tokio::sync::Notify::new());
        let start = started.clone();
        let unblock = release.clone();
        let (base, server) = serve(Router::new().route(
            "/root",
            get(move |headers: HeaderMap| {
                let start = start.clone();
                let unblock = unblock.clone();
                async move {
                    if headers.contains_key(RANGE) {
                        Response::builder()
                            .status(StatusCode::PARTIAL_CONTENT)
                            .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                            .header(CONTENT_RANGE, "bytes 0-8/30")
                            .body(Body::from("#EXTM3U\n#"))
                            .unwrap()
                    } else {
                        start.notify_one();
                        unblock.notified().await;
                        Response::builder()
                            .header(CONTENT_TYPE, "application/vnd.apple.mpegurl")
                            .body(Body::from("#EXTM3U\nsegment\n#EXT-X-ENDLIST\n"))
                            .unwrap()
                    }
                }
            }),
        ))
        .await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy.prepare(format!("{base}/root"), vec![]).await.unwrap();
        let pending = tauri::async_runtime::spawn(async move {
            reqwest::Client::new()
                .get(root)
                .header(RANGE, "bytes=0-8")
                .send()
                .await
                .unwrap()
                .status()
        });
        started.notified().await;
        assert_eq!(
            tokio::time::timeout(Duration::from_secs(22), pending)
                .await
                .expect("identified HLS refetch must respect the single 20s deadline")
                .unwrap(),
            StatusCode::BAD_GATEWAY
        );
        release.notify_one();
        proxy.clear().await;
        server.abort();
    });
}

#[test]
fn actual_headers_do_not_restart_the_root_body_expiry() {
    tauri::async_runtime::block_on(async {
        let expires_at = Instant::now() + Duration::from_secs(2);
        let headers_at = tokio::time::Instant::from_std(expires_at - Duration::from_secs(1));
        let (base, server) = serve(Router::new().route(
            "/stream",
            get(move || async move {
                // An explicit fixture deadline holds headers for half the root's
                // remaining lifetime. The body then waits until the real root ends.
                tokio::time::sleep_until(headers_at).await;
                let prefix = futures_util::stream::once(async {
                    Ok::<_, std::io::Error>(axum::body::Bytes::from_static(b"0123456789"))
                });
                Body::from_stream(prefix.chain(futures_util::stream::pending::<
                    Result<axum::body::Bytes, std::io::Error>,
                >()))
            }),
        ))
        .await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy
            .prepare(format!("{base}/stream"), vec![])
            .await
            .unwrap();
        {
            let runtime = proxy.runtime.lock().await;
            let mut targets = runtime.as_ref().unwrap().targets.write().await;
            let target = targets.values_mut().next().unwrap();
            Arc::get_mut(&mut target.session).unwrap().created = expires_at - SESSION_LIFETIME;
        }
        let response = reqwest::Client::new().get(root).send().await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let bytes = tokio::time::timeout_at(
            tokio::time::Instant::from_std(expires_at + Duration::from_millis(500)),
            response.bytes(),
        )
        .await
        .expect("receiving headers cannot extend the root deadline")
        .unwrap();
        assert_eq!(bytes.as_ref(), b"0123456789");
        proxy.clear().await;
        server.abort();
    });
}

#[test]
fn late_cancel_subscription_does_not_wait_for_another_clear() {
    tauri::async_runtime::block_on(async {
        let (cancelled, _) = watch::channel(false);
        cancelled.send_replace(true);
        let mut receiver = cancelled.subscribe();
        tokio::time::timeout(
            Duration::from_millis(50),
            wait_for_cancellation(&mut receiver),
        )
        .await
        .expect("a receiver created after clear must already be cancelled");
    });
}

#[test]
fn actual_stream_body_holds_capacity_until_clear_and_stops_old_reads() {
    tauri::async_runtime::block_on(async {
        let (base, server) = serve(Router::new().route(
            "/stream",
            get(|| async {
                let prefix = futures_util::stream::once(async {
                    Ok::<_, std::io::Error>(axum::body::Bytes::from_static(b"0123456789"))
                });
                let rest =
                    futures_util::stream::pending::<Result<axum::body::Bytes, std::io::Error>>();
                Body::from_stream(prefix.chain(rest))
            }),
        ))
        .await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy
            .prepare(format!("{base}/stream"), vec![])
            .await
            .unwrap();
        let client = reqwest::Client::new();
        let mut open = Vec::new();
        for _ in 0..32 {
            let response = client.get(&root).send().await.unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            open.push(response);
        }
        assert_eq!(
            client.get(&root).send().await.unwrap().status(),
            StatusCode::SERVICE_UNAVAILABLE,
            "streaming bodies must keep their concurrency permits"
        );
        proxy.clear().await;
        for response in open {
            let bytes = tokio::time::timeout(Duration::from_secs(2), response.bytes())
                .await
                .unwrap()
                .unwrap();
            assert_eq!(
                bytes.as_ref(),
                b"0123456789",
                "old streams end immediately after clear"
            );
        }
        assert_eq!(
            client.get(root).send().await.unwrap().status(),
            StatusCode::GONE
        );
        let replacement = proxy
            .prepare(format!("{base}/stream"), vec![])
            .await
            .unwrap();
        let response = client.get(replacement).send().await.unwrap();
        assert_eq!(
            response.status(),
            StatusCode::OK,
            "replacement has released capacity"
        );
        proxy.clear().await;
        assert_eq!(
            tokio::time::timeout(Duration::from_secs(2), response.bytes())
                .await
                .unwrap()
                .unwrap()
                .as_ref(),
            b"0123456789"
        );
        server.abort();
    });
}

#[test]
#[ignore = "requires an explicitly verified native FFmpeg fixture runtime"]
fn synthetic_hls_decodes_through_actual_authenticated_proxy() {
    use std::{fs, process::Command};
    fn command(executable: &str) -> Command {
        let mut command = Command::new(executable);
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000);
        }
        command.args(["-hide_banner", "-nostdin", "-loglevel", "error"]);
        command
    }
    let executable =
        std::env::var("OHMYCINE_HLS_TEST_FFMPEG").expect("explicit verified fixture tool required");
    let directory = std::env::temp_dir().join(format!("ohmycine-hls-fixture-{}", random_token()));
    fs::create_dir(&directory).unwrap();
    let generated = command(&executable)
        .current_dir(&directory)
        .args([
            "-f",
            "lavfi",
            "-i",
            "testsrc2=size=96x64:rate=4",
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:sample_rate=32000",
            "-t",
            "2",
            "-c:v",
            "libopenh264",
            "-g",
            "4",
            "-b:v",
            "100k",
            "-c:a",
            "aac",
            "-f",
            "hls",
            "-hls_time",
            "1",
            "-hls_segment_filename",
            "segment%d.ts",
            "media.m3u8",
        ])
        .output()
        .unwrap();
    assert!(
        generated.status.success(),
        "synthetic fixture encoder exit {:?}",
        generated.status.code()
    );
    let media_id = "00000000-0000-4000-8000-000000000002";
    let mut manifest = fs::read_to_string(directory.join("media.m3u8")).unwrap();
    let mut data: HashMap<String, (String, Vec<u8>)> = HashMap::new();
    let mut count = 0;
    for entry in fs::read_dir(&directory).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_some_and(|value| value == "ts") {
            count += 1;
            let id = format!("00000000-0000-4000-8000-{count:012}");
            // Keep identities distinct from master and media control references.
            let id = id.replace("8000-", "8001-");
            manifest = manifest.replace(
                path.file_name().unwrap().to_str().unwrap(),
                &format!("/api/v1/player/online-assets/{id}"),
            );
            data.insert(id, ("video/mp2t".into(), fs::read(path).unwrap()));
        }
    }
    assert!(count >= 2);
    data.insert(
        media_id.into(),
        (
            "application/vnd.apple.mpegurl".into(),
            manifest.into_bytes(),
        ),
    );
    data.insert("00000000-0000-4000-8000-000000000001".into(),("application/vnd.apple.mpegurl".into(),format!("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=150000\n/api/v1/player/online-assets/{media_id}\n").into_bytes()));
    tauri::async_runtime::block_on(async {
        let reads = Arc::new(AtomicUsize::new(0));
        let probe = reads.clone();
        let data = Arc::new(data);
        let app = Router::new().route(
            "/api/v1/player/online-assets/:asset",
            get(move |Path(asset): Path<String>, headers: HeaderMap| {
                let data = data.clone();
                let probe = probe.clone();
                async move {
                    if headers
                        .get(AUTHORIZATION)
                        .and_then(|value| value.to_str().ok())
                        != Some("Bearer fixture")
                    {
                        return plain_response(StatusCode::UNAUTHORIZED, "fixture auth required");
                    }
                    let (mime, bytes) = data.get(&asset).unwrap();
                    if hls_proxy::content_type(mime) {
                        return control_response(&Method::GET, &headers, bytes.clone());
                    }
                    probe.fetch_add(1, Ordering::SeqCst);
                    let (status, start, end) =
                        match headers.get(RANGE).and_then(|value| value.to_str().ok()) {
                            Some(value) => {
                                let (start, end) = control_range(value, bytes.len()).unwrap();
                                (StatusCode::PARTIAL_CONTENT, start, end)
                            }
                            None => (StatusCode::OK, 0, bytes.len() - 1),
                        };
                    let mut builder = Response::builder()
                        .status(status)
                        .header(CONTENT_TYPE, mime)
                        .header(CONTENT_LENGTH, end - start + 1);
                    if status == StatusCode::PARTIAL_CONTENT {
                        builder = builder.header(
                            CONTENT_RANGE,
                            format!("bytes {start}-{end}/{}", bytes.len()),
                        );
                    }
                    builder
                        .body(Body::from(bytes[start..=end].to_vec()))
                        .unwrap()
                }
            }),
        );
        let (base, server) = serve(app).await;
        let proxy = AndroidStreamProxyState::default();
        let root = proxy
            .prepare(
                format!("{base}/api/v1/player/online-assets/00000000-0000-4000-8000-000000000001"),
                secret_headers(),
            )
            .await
            .unwrap();
        #[cfg(not(mobile))]
        {
            let mpv_root = root.clone();
            let (loaded, video, audio, progress, eof, error, frame) =
                tauri::async_runtime::spawn_blocking(move || embedded_mpv_decode(&mpv_root))
                    .await
                    .unwrap();
            assert!(loaded && video && audio && progress>1.0 && eof && error==0 && frame,
                "embedded product mpv fixture loaded={loaded} h264={video} aac={audio} progress={progress} eof={eof} error={error} decoded-frame={frame}");
            assert!(
                reads.load(Ordering::SeqCst) >= 2,
                "product mpv must request actual TS segments"
            );
        }
        let decoded = tauri::async_runtime::spawn_blocking(move || {
            command(&executable)
                .args([
                    // The pinned product mpv disables this CLI-only HLS extension
                    // heuristic internally; match it only in this fixture runner.
                    "-extension_picky",
                    "0",
                    "-i",
                    &root,
                    "-map",
                    "0:v:0",
                    "-map",
                    "0:a:0",
                    "-t",
                    "1.5",
                    "-f",
                    "null",
                    "-",
                ])
                .output()
                .unwrap()
        })
        .await
        .unwrap();
        let diagnostics = String::from_utf8_lossy(&decoded.stderr);
        let category = if diagnostics.contains("allowed_extensions")
            || diagnostics.contains("allowed_segment_extensions")
        {
            "hls-extension-denied"
        } else if diagnostics.contains("extension_picky") {
            "hls-extension-picky"
        } else if diagnostics.contains("404") {
            "http-not-found"
        } else if diagnostics.contains("403") {
            "http-forbidden"
        } else if diagnostics.contains("Invalid data") {
            "invalid-media-data"
        } else {
            "decoder-error"
        };
        assert!(
            decoded.status.success(),
            "authenticated HLS fixture decoder exit {:?} category {category}",
            decoded.status.code()
        );
        assert!(
            reads.load(Ordering::SeqCst) >= 2,
            "actual decoder must read media segments"
        );
        proxy.clear().await;
        server.abort();
    });
    for entry in fs::read_dir(&directory).unwrap() {
        fs::remove_file(entry.unwrap().path()).unwrap();
    }
    fs::remove_dir(directory).unwrap();
}

#[cfg(not(mobile))]
fn embedded_mpv_decode(url: &str) -> (bool, bool, bool, f64, bool, i32, bool) {
    use libmpv_sys::*;
    use std::{
        ffi::{CStr, CString},
        ptr,
    };
    struct Handle(*mut mpv_handle);
    impl Drop for Handle {
        fn drop(&mut self) {
            unsafe { mpv_terminate_destroy(self.0) }
        }
    }
    fn property(handle: *mut mpv_handle, key: &str) -> Option<String> {
        let key = CString::new(key).unwrap();
        unsafe {
            let value = mpv_get_property_string(handle, key.as_ptr());
            if value.is_null() {
                return None;
            }
            let text = CStr::from_ptr(value).to_string_lossy().into_owned();
            mpv_free(value.cast());
            Some(text)
        }
    }
    let handle = Handle(unsafe { mpv_create() });
    assert!(!handle.0.is_null());
    for (key, value) in [
        ("config", "no"),
        ("load-scripts", "no"),
        ("input-default-bindings", "no"),
        ("input-vo-keyboard", "no"),
        ("force-window", "no"),
        ("terminal", "no"),
        ("msg-level", "all=no"),
        ("vo", "null"),
        ("ao", "null"),
        ("hwdec", "no"),
        ("cache", "no"),
        ("keep-open", "no"),
    ] {
        let key = CString::new(key).unwrap();
        let value = CString::new(value).unwrap();
        assert!(unsafe { mpv_set_option_string(handle.0, key.as_ptr(), value.as_ptr()) } >= 0);
    }
    assert!(unsafe { mpv_initialize(handle.0) } >= 0);
    let load = CString::new("loadfile").unwrap();
    let url = CString::new(url).unwrap();
    let mut args = [load.as_ptr(), url.as_ptr(), ptr::null()];
    assert!(unsafe { mpv_command(handle.0, args.as_mut_ptr()) } >= 0);
    let mut loaded = false;
    let mut video = false;
    let mut audio = false;
    let mut progress = 0.0_f64;
    let mut eof = false;
    let mut error = 0;
    let mut frame = false;
    let until = Instant::now() + Duration::from_secs(10);
    while Instant::now() < until {
        let event = unsafe { &*mpv_wait_event(handle.0, 0.05) };
        if event.event_id == mpv_event_id_MPV_EVENT_FILE_LOADED {
            loaded = true;
        }
        video |= property(handle.0, "video-format").as_deref() == Some("h264");
        audio |= property(handle.0, "audio-codec-name").as_deref() == Some("aac");
        frame |= property(handle.0, "video-out-params/w").as_deref() == Some("96")
            && property(handle.0, "video-out-params/h").as_deref() == Some("64")
            && matches!(
                property(handle.0, "video-frame-info/picture-type").as_deref(),
                Some("I" | "P" | "B")
            );
        let key = CString::new("time-pos").unwrap();
        let mut position = 0.0_f64;
        if unsafe {
            mpv_get_property(
                handle.0,
                key.as_ptr(),
                mpv_format_MPV_FORMAT_DOUBLE,
                (&mut position as *mut f64).cast(),
            )
        } >= 0
        {
            progress = progress.max(position);
        }
        if event.event_id == mpv_event_id_MPV_EVENT_END_FILE {
            let end = unsafe { &*(event.data as *const mpv_event_end_file) };
            if end.reason == mpv_end_file_reason_MPV_END_FILE_REASON_EOF as i32 {
                eof = true;
                break;
            }
            if end.reason == mpv_end_file_reason_MPV_END_FILE_REASON_ERROR as i32 {
                error = end.error;
                break;
            }
        }
    }
    (loaded, video, audio, progress, eof, error, frame)
}

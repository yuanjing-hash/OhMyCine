use super::*;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, Response as HTTPResponse};
use axum::Router;
use std::net::{IpAddr, SocketAddr};
use std::sync::{Arc, Mutex};

#[derive(Default)]
struct HTTPFixture {
    next: Mutex<String>,
    seen: Mutex<Vec<(String, bool, bool)>>,
}

async fn fixture_handler(
    State(state): State<Arc<HTTPFixture>>,
    request: Request<Body>,
) -> HTTPResponse<Body> {
    state.seen.lock().unwrap().push((
        request
            .headers()
            .get("host")
            .unwrap()
            .to_str()
            .unwrap()
            .to_string(),
        request.headers().contains_key(AUTHORIZATION),
        request.headers().contains_key("referer"),
    ));
    if request.uri().path() == "/start" {
        HTTPResponse::builder()
            .status(302)
            .header(LOCATION, state.next.lock().unwrap().clone())
            .body(Body::empty())
            .unwrap()
    } else {
        HTTPResponse::builder()
            .status(200)
            .header("ETag", "\"dns-byte-fixture\"")
            .body(Body::from("native-dns-byte-fixture"))
            .unwrap()
    }
}

async fn socket_fixture() -> (SocketAddr, Arc<HTTPFixture>, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let state = Arc::new(HTTPFixture::default());
    let app = Router::new()
        .fallback(fixture_handler)
        .with_state(state.clone());
    let task = tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (address, state, task)
}

fn dns_fixture(dial: SocketAddr, first: &str, second: &str) -> DNSTransportFixture {
    DNSTransportFixture {
        resolver: crate::commands::public_dns::TestResolver::new(),
        system: HashMap::from([
            (
                "first.example.com".to_string(),
                vec![first.parse().unwrap()],
            ),
            (
                "next.example.com".to_string(),
                vec![second.parse().unwrap()],
            ),
        ]),
        public: vec!["8.8.8.8".parse::<IpAddr>().unwrap()],
        dial,
        pinned: Mutex::new(Vec::new()),
    }
}

fn remote(url: String) -> ResolvedRemote {
    ResolvedRemote {
        url: Url::parse(&url).unwrap(),
        headers: HeaderMap::from_iter([(
            HeaderName::from_static("referer"),
            HeaderValue::from_static("https://www.example.com/"),
        )]),
        lease_until: None,
        online: true,
    }
}

#[tokio::test]
async fn direct_transport_repairs_fake_answers_pins_each_hop_and_reads_real_bytes() {
    let (address, state, server) = socket_fixture().await;
    *state.next.lock().unwrap() = format!("http://next.example.com:{}/finish", address.port());
    let dns = dns_fixture(address, "198.18.0.8", "fdfe:dcba:9876::8");
    let request = remote(format!("http://first.example.com:{}/start", address.port()));
    let response = request_transport_inner(&request, None, Some(&dns))
        .await
        .unwrap();
    assert_eq!(
        &response.bytes().await.unwrap()[..],
        b"native-dns-byte-fixture"
    );
    let pins = dns.pinned.lock().unwrap();
    assert_eq!(pins.len(), 2);
    assert_eq!(pins[0].0, "first.example.com");
    assert_eq!(pins[1].0, "next.example.com");
    assert!(pins
        .iter()
        .all(|(_, ips)| ips == &[SocketAddr::new("8.8.8.8".parse().unwrap(), address.port())]));
    let seen = state.seen.lock().unwrap();
    assert_eq!(seen.len(), 2);
    assert!(seen[0].2 && !seen[1].2); // cross-origin redirect drops supplied headers
    assert!(!seen[0].1 && !seen[1].1); // CDN never receives Server Bearer
    server.abort();
}

#[tokio::test]
async fn direct_transport_rejects_private_redirect_before_dial() {
    let (address, state, server) = socket_fixture().await;
    *state.next.lock().unwrap() = format!("http://next.example.com:{}/finish", address.port());
    let dns = dns_fixture(address, "198.18.0.8", "10.0.0.8");
    let request = remote(format!("http://first.example.com:{}/start", address.port()));
    assert!(request_transport_inner(&request, None, Some(&dns))
        .await
        .is_err());
    assert_eq!(state.seen.lock().unwrap().len(), 1);
    assert_eq!(dns.pinned.lock().unwrap().len(), 1);
    server.abort();
}

#[tokio::test]
async fn gateway_keeps_owning_origin_and_expired_lease_does_not_resolve_or_dial() {
    let (address, state, server) = socket_fixture().await;
    let dns = dns_fixture(address, "10.0.0.8", "10.0.0.8");
    let mut request = remote(format!(
        "http://{address}/api/v1/player/online-libraries/library/offline-assets/asset"
    ));
    request.headers.insert(
        AUTHORIZATION,
        HeaderValue::from_static("Bearer omc_player_fixture"),
    );
    assert_eq!(
        &request_transport_inner(&request, None, Some(&dns))
            .await
            .unwrap()
            .bytes()
            .await
            .unwrap()[..],
        b"native-dns-byte-fixture"
    );
    assert!(dns.pinned.lock().unwrap().is_empty());
    assert!(state.seen.lock().unwrap()[0].1);
    request.lease_until = Some(1);
    assert!(request_transport_inner(&request, None, Some(&dns))
        .await
        .is_err());
    assert_eq!(state.seen.lock().unwrap().len(), 1);
    server.abort();
}

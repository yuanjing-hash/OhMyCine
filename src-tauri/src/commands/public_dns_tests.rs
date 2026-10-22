use super::*;
use std::sync::atomic::{AtomicUsize, Ordering};

fn ip(value: &str) -> IpAddr {
    value.parse().unwrap()
}
fn observation() -> Observation {
    Observation {
        addresses: vec![ip("8.8.8.8")],
        ttl: Duration::from_secs(30),
        observed: Instant::now(),
    }
}

fn name_wire(name: &str) -> Vec<u8> {
    let mut wire = Vec::new();
    for label in name.split('.') {
        wire.push(label.len() as u8);
        wire.extend_from_slice(label.as_bytes());
    }
    wire.push(0);
    wire
}

fn response(
    name: &str,
    kind: u16,
    id: u16,
    ttl: u32,
    target: Option<&str>,
    address: Option<IpAddr>,
) -> Vec<u8> {
    let mut wire = dns_query(name, kind, id).unwrap();
    wire[2..4].copy_from_slice(&0x8180u16.to_be_bytes());
    let count = target.is_some() as u16 + address.is_some() as u16;
    wire[6..8].copy_from_slice(&count.to_be_bytes());
    if let Some(target) = target {
        wire.extend_from_slice(&[0xc0, 0x0c]);
        wire.extend_from_slice(&5u16.to_be_bytes());
        wire.extend_from_slice(&1u16.to_be_bytes());
        wire.extend_from_slice(&ttl.to_be_bytes());
        let cname = name_wire(target);
        wire.extend_from_slice(&(cname.len() as u16).to_be_bytes());
        wire.extend_from_slice(&cname);
    }
    if let Some(address) = address {
        if let Some(target) = target {
            wire.extend_from_slice(&name_wire(target));
        } else {
            wire.extend_from_slice(&[0xc0, 0x0c]);
        }
        wire.extend_from_slice(&kind.to_be_bytes());
        wire.extend_from_slice(&1u16.to_be_bytes());
        wire.extend_from_slice(&ttl.to_be_bytes());
        let bytes = match address {
            IpAddr::V4(ip) => ip.octets().to_vec(),
            IpAddr::V6(ip) => ip.octets().to_vec(),
        };
        wire.extend_from_slice(&(bytes.len() as u16).to_be_bytes());
        wire.extend_from_slice(&bytes);
    }
    wire
}

fn resource(owner: &str, kind: u16, class: u16, bytes: Vec<u8>) -> Vec<u8> {
    let mut wire = name_wire(owner);
    wire.extend_from_slice(&kind.to_be_bytes());
    wire.extend_from_slice(&class.to_be_bytes());
    wire.extend_from_slice(&30u32.to_be_bytes());
    wire.extend_from_slice(&(bytes.len() as u16).to_be_bytes());
    wire.extend(bytes);
    wire
}

fn records(records: Vec<Vec<u8>>) -> Vec<u8> {
    let mut wire = dns_query("cdn.example.com", 1, 7).unwrap();
    wire[2..4].copy_from_slice(&0x8180u16.to_be_bytes());
    wire[6..8].copy_from_slice(&(records.len() as u16).to_be_bytes());
    for record in records {
        wire.extend(record);
    }
    wire
}

#[test]
fn dns_wire_enforces_complete_binding_cname_chain_and_record_capacity() {
    let address = |owner: &str, class: u16| resource(owner, 1, class, vec![8, 8, 8, 8]);
    assert!(parse_answer(
        &records(vec![address("other.example.com", 1)]),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
    assert!(parse_answer(
        &records(vec![address("cdn.example.com", 2)]),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
    assert!(parse_answer(
        &records(vec![
            resource("cdn.example.com", 5, 1, name_wire("edge.example.com")),
            resource("cdn.example.com", 5, 1, name_wire("different.example.com")),
            address("edge.example.com", 1),
        ]),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
    assert!(parse_answer(
        &records(vec![
            resource("cdn.example.com", 5, 1, name_wire("edge.example.com")),
            address("cdn.example.com", 1),
            address("edge.example.com", 1),
        ]),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
    assert!(parse_answer(
        &records(vec![
            resource("other.example.com", 5, 1, name_wire("edge.example.com")),
            address("cdn.example.com", 1),
        ]),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
    for count in [8, 9] {
        let mut chain = Vec::new();
        let mut owner = "cdn.example.com".to_string();
        for i in 0..count {
            let next = format!("alias{i}.example.com");
            chain.push(resource(&owner, 5, 1, name_wire(&next)));
            owner = next;
        }
        chain.push(address(&owner, 1));
        assert_eq!(
            parse_answer(&records(chain), "cdn.example.com", 1, 7).is_ok(),
            count == 8
        );
    }
    let allowed = records((0..64).map(|_| address("cdn.example.com", 1)).collect());
    assert_eq!(
        parse_answer(&allowed, "cdn.example.com", 1, 7)
            .unwrap()
            .addresses
            .len(),
        64
    );
    let excessive = records((0..65).map(|_| address("cdn.example.com", 1)).collect());
    assert!(excessive.len() < MAX_BODY);
    assert!(parse_answer(&excessive, "cdn.example.com", 1, 7).is_err());
}

#[test]
fn dns_wire_validates_question_terminal_cname_and_ttl() {
    let a = response(
        "cdn.example.com",
        1,
        7,
        300,
        Some("edge.example.com"),
        Some(ip("8.8.8.8")),
    );
    let parsed = parse_answer(&a, "CDN.example.com", 1, 7).unwrap();
    assert_eq!(parsed.addresses, vec![ip("8.8.8.8")]);
    assert_eq!(parsed.ttl, Duration::from_secs(300));
    let aaaa = response(
        "cdn.example.com",
        28,
        8,
        15,
        None,
        Some(ip("2606:4700:4700::1111")),
    );
    assert_eq!(
        parse_answer(&aaaa, "cdn.example.com", 28, 8)
            .unwrap()
            .addresses,
        vec![ip("2606:4700:4700::1111")]
    );
    let empty = response("cdn.example.com", 28, 8, 15, None, None);
    assert!(parse_answer(&empty, "cdn.example.com", 28, 8)
        .unwrap()
        .addresses
        .is_empty());
    assert_eq!(
        parse_answer(&empty, "cdn.example.com", 28, 8).unwrap().ttl,
        Duration::ZERO
    );
}

#[test]
fn dns_wire_rejects_spoofed_private_loop_and_truncated_answers() {
    let good = response("cdn.example.com", 1, 7, 300, None, Some(ip("8.8.8.8")));
    assert!(parse_answer(&good, "other.example.com", 1, 7).is_err());
    assert!(parse_answer(&good, "cdn.example.com", 28, 7).is_err());
    assert!(parse_answer(&good, "cdn.example.com", 1, 8).is_err());
    for flags in [0x0180u16, 0x8380, 0x8980, 0x8183] {
        let mut wire = good.clone();
        wire[2..4].copy_from_slice(&flags.to_be_bytes());
        assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    }
    for value in [
        "127.0.0.1",
        "10.0.0.1",
        "198.18.0.7",
        "192.0.2.1",
        "fdfe:dcba:9876::7",
        "::1",
    ] {
        let kind = if value.contains(':') { 28 } else { 1 };
        assert!(parse_answer(
            &response("cdn.example.com", kind, 7, 30, None, Some(ip(value))),
            "cdn.example.com",
            kind,
            7
        )
        .is_err());
    }
    let mut wire = good.clone();
    wire[4..6].copy_from_slice(&2u16.to_be_bytes());
    assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    let mut wire = good.clone();
    wire[6..8].copy_from_slice(&65u16.to_be_bytes());
    assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    let mut wire = good.clone();
    wire.extend_from_slice(&[0]);
    assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    let mut wire = good.clone();
    wire.pop();
    assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    let mut wire = good.clone();
    wire[12] = 0xc0;
    wire[13] = 0x0c;
    assert!(parse_answer(&wire, "cdn.example.com", 1, 7).is_err());
    let mut cursor = 0;
    assert!(read_name(&[0xc0, 0x01], &mut cursor).is_err());
    let mut cursor = 0;
    assert!(read_name(&[64, 0], &mut cursor).is_err());
    assert!(parse_answer(
        &response(
            "cdn.example.com",
            1,
            7,
            30,
            Some("cdn.example.com"),
            Some(ip("8.8.8.8"))
        ),
        "cdn.example.com",
        1,
        7
    )
    .is_err());
}

#[test]
fn cache_headers_and_hostname_bounds_are_strict() {
    assert_eq!(
        cache_control_limit(Some("max-age=120, s-maxage=5")).unwrap(),
        Duration::from_secs(5)
    );
    assert_eq!(
        cache_control_limit(Some("no-store, max-age=100")).unwrap(),
        Duration::ZERO
    );
    assert_eq!(
        cache_control_limit(Some("no-cache")).unwrap(),
        Duration::ZERO
    );
    assert!(cache_control_limit(Some("max-age=oops")).is_err());
    assert!(dns_hostname("127.0.0.1").is_none());
    assert!(dns_hostname("device.local").is_none());
    assert!(dns_hostname("device.localhost").is_none());
    assert!(dns_hostname("bad..example.com").is_none());
    assert!(dns_hostname(&format!("{}.example.com", "a".repeat(64))).is_none());
    assert!(fake_ip(ip("198.19.255.255")));
    assert!(fake_ip(ip("fdfe:dcba:9876::abcd")));
    assert!(!fake_ip(ip("fdfe:dcba:9876:1::7")));
    assert!(fake_ip(ip("::ffff:198.18.0.7")));
}

#[tokio::test]
async fn repair_only_known_fake_answers_and_rechecks_system_before_cache() {
    let resolver = PublicResolver::new();
    let calls = AtomicUsize::new(0);
    for fake in ["198.18.0.9", "fdfe:dcba:9876::9"] {
        let result = resolver
            .repair("cdn.example.com", vec![ip(fake)], |_| async {
                calls.fetch_add(1, Ordering::SeqCst);
                Ok(observation())
            })
            .await
            .unwrap();
        assert_eq!(result, vec![ip("8.8.8.8")]);
    }
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    assert!(resolver
        .repair(
            "cdn.example.com",
            vec![ip("198.18.0.9"), ip("10.0.0.8")],
            |_| async { panic!("mixed private response invoked DoH") }
        )
        .await
        .is_err());
    assert!(resolver
        .repair("cdn.example.com", vec![], |_| async {
            panic!("empty DNS invoked DoH")
        })
        .await
        .is_err());
    assert!(resolver
        .repair("device.local", vec![ip("198.18.0.9")], |_| async {
            panic!("local hostname invoked DoH")
        })
        .await
        .is_err());
    assert_eq!(
        resolver
            .repair("cdn.example.com", vec![ip("1.1.1.1")], |_| async {
                panic!("public DNS invoked DoH")
            })
            .await
            .unwrap(),
        vec![ip("1.1.1.1")]
    );
    let mut copy = resolver
        .repair("cdn.example.com", vec![ip("198.18.0.9")], |_| async {
            panic!("cache missed")
        })
        .await
        .unwrap();
    copy[0] = ip("1.1.1.1");
    assert_eq!(
        resolver
            .repair("cdn.example.com", vec![ip("198.18.0.9")], |_| async {
                panic!("cache missed")
            })
            .await
            .unwrap(),
        vec![ip("8.8.8.8")]
    );
}

#[tokio::test]
async fn repair_coalesces_flights_and_bounds_cache_ttl_and_cancellation() {
    let resolver = PublicResolver::new();
    let calls = AtomicUsize::new(0);
    let requests = (0..8).map(|_| {
        resolver.repair("cdn.example.com", vec![ip("198.18.0.9")], |_| async {
            calls.fetch_add(1, Ordering::SeqCst);
            tokio::time::sleep(Duration::from_millis(20)).await;
            Ok(Observation {
                ttl: Duration::from_secs(3600),
                ..observation()
            })
        })
    });
    for result in futures_util::future::join_all(requests).await {
        assert!(result.is_ok());
    }
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    assert!(
        resolver.state.lock().unwrap().cache["cdn.example.com"]
            .observation
            .ttl
            <= MAX_TTL
    );
    resolver
        .state
        .lock()
        .unwrap()
        .cache
        .get_mut("cdn.example.com")
        .unwrap()
        .expires = Instant::now();
    resolver
        .repair("cdn.example.com", vec![ip("198.18.0.9")], |_| async {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok(observation())
        })
        .await
        .unwrap();
    assert_eq!(calls.load(Ordering::SeqCst), 2);
    let cancelled = tokio::time::timeout(
        Duration::from_millis(20),
        resolver.repair("cancel.example.com", vec![ip("198.18.0.9")], |_| {
            std::future::pending()
        }),
    )
    .await;
    assert!(cancelled.is_err());
    assert!(resolver.state.lock().unwrap().flights.is_empty());
    resolver
        .repair("cancel.example.com", vec![ip("198.18.0.9")], |_| async {
            Ok(observation())
        })
        .await
        .unwrap();
}

#[tokio::test]
async fn cancelled_flight_wakes_joined_follower_and_releases_slot() {
    let resolver = PublicResolver::new();
    let mut leader = Box::pin(resolver.repair(
        "cancel.example.com",
        vec![ip("198.18.0.9")],
        |_| std::future::pending(),
    ));
    assert!(futures_util::poll!(&mut leader).is_pending());
    let mut follower = Box::pin(resolver.repair(
        "cancel.example.com",
        vec![ip("fdfe:dcba:9876::9")],
        |_| async { panic!("joined follower started a second query") },
    ));
    assert!(futures_util::poll!(&mut follower).is_pending());
    assert_eq!(resolver.slots.available_permits(), 7);
    assert_eq!(resolver.state.lock().unwrap().flights.len(), 1);
    drop(leader);
    let failure = tokio::time::timeout(Duration::from_millis(250), follower)
        .await
        .expect("cancelled leader did not wake its joined follower")
        .unwrap_err();
    assert_eq!(failure, DNS_UNAVAILABLE);
    assert!(resolver.state.lock().unwrap().flights.is_empty());
    assert!(resolver.state.lock().unwrap().cache.is_empty());
    assert_eq!(resolver.slots.available_permits(), 8);
    resolver
        .repair("cancel.example.com", vec![ip("198.18.0.9")], |_| async {
            Ok(observation())
        })
        .await
        .unwrap();
}

#[tokio::test]
async fn repair_never_caches_failures_or_zero_ttl_and_rate_is_bounded() {
    let resolver = PublicResolver::new();
    for _ in 0..2 {
        assert!(resolver
            .repair("fail.example.com", vec![ip("198.18.0.9")], |_| async {
                Err(DNS_INVALID.to_string())
            })
            .await
            .is_err());
        resolver
            .repair("zero.example.com", vec![ip("198.18.0.9")], |_| async {
                Ok(Observation {
                    ttl: Duration::ZERO,
                    ..observation()
                })
            })
            .await
            .unwrap();
    }
    assert!(resolver.state.lock().unwrap().cache.is_empty());
    for i in 0..MAX_CACHE + 4 {
        // Ignore admission rate only in this bounded cache storage test.
        resolver.state.lock().unwrap().tokens = 16.0;
        resolver
            .repair(
                &format!("cdn{i}.example.com"),
                vec![ip("198.18.0.9")],
                |_| async { Ok(observation()) },
            )
            .await
            .unwrap();
    }
    assert_eq!(resolver.state.lock().unwrap().cache.len(), MAX_CACHE);
    resolver.state.lock().unwrap().tokens = 0.0;
    assert_eq!(
        resolver
            .repair("rate.example.com", vec![ip("198.18.0.9")], |_| async {
                panic!("rate limit ignored")
            })
            .await
            .unwrap_err(),
        DNS_BUSY
    );
}

#[tokio::test]
async fn repair_admission_has_eight_slot_limit_and_rejects_untrusted_public_result() {
    let resolver = PublicResolver::new();
    assert!(resolver
        .repair("bad.example.com", vec![ip("198.18.0.9")], |_| async {
            Ok(Observation {
                addresses: vec![ip("8.8.8.8"), ip("10.0.0.1")],
                ttl: Duration::from_secs(30),
                observed: Instant::now(),
            })
        })
        .await
        .is_err());
    let permits = (0..8)
        .map(|_| resolver.slots.clone().try_acquire_owned().unwrap())
        .collect::<Vec<_>>();
    assert_eq!(
        resolver
            .repair("busy.example.com", vec![ip("198.18.0.9")], |_| async {
                panic!("slot cap ignored")
            })
            .await
            .unwrap_err(),
        DNS_BUSY
    );
    assert!(resolver.state.lock().unwrap().flights.is_empty());
    drop(permits);
    resolver
        .repair("busy.example.com", vec![ip("198.18.0.9")], |_| async {
            Ok(observation())
        })
        .await
        .unwrap();
}

#[tokio::test]
async fn fixed_doh_client_rejects_http_without_network() {
    let client = doh_client().unwrap();
    assert!(client
        .get("http://dns.alidns.com/dns-query")
        .send()
        .await
        .is_err());
    assert_eq!(Url::parse(DOH_URL).unwrap().host_str(), Some(DOH_HOST));
    assert!(public_ip(ip("223.5.5.5")) && public_ip(ip("223.6.6.6")));
}

#[tokio::test]
async fn fallback_query_timeout_removes_flight_and_releases_slot() {
    let resolver = PublicResolver::new();
    let started = Instant::now();
    assert!(resolver
        .repair("timeout.example.com", vec![ip("198.18.0.9")], |_| {
            std::future::pending()
        })
        .await
        .is_err());
    assert!(started.elapsed() >= DNS_TIMEOUT && started.elapsed() < Duration::from_secs(6));
    assert!(resolver.state.lock().unwrap().flights.is_empty());
    assert_eq!(resolver.slots.available_permits(), 8);
}

#[tokio::test]
async fn quick_family_expiry_is_not_renewed_when_other_family_is_slow() {
    let a = Observation {
        ttl: Duration::from_secs(1),
        observed: Instant::now() - Duration::from_secs(2),
        ..observation()
    };
    let aaaa = Observation {
        addresses: vec![ip("2606:4700:4700::1111")],
        ..observation()
    };
    let merged = merge_families(a, aaaa).unwrap();
    assert_eq!(merged.ttl, Duration::ZERO);
    let resolver = PublicResolver::new();
    resolver
        .repair("expired.example.com", vec![ip("198.18.0.9")], |_| async {
            Ok(merged)
        })
        .await
        .unwrap();
    assert!(resolver.state.lock().unwrap().cache.is_empty());
}

#[tokio::test]
async fn fixed_doh_live_verified_tls_and_public_answers() {
    if std::env::var("OMC_NATIVE_DNS_LIVE").as_deref() != Ok("1") {
        return;
    }
    let observation = tokio::time::timeout(
        DNS_TIMEOUT,
        query_public_dns("pianku.api.mgtv.com".to_string()),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(!observation.addresses.is_empty());
    assert!(observation.addresses.iter().all(|ip| public_ip(*ip)));
}

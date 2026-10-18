//! Public CDN DNS repair for recognized Clash Fake-IP answers only.
//! Contains hostnames and short-lived public addresses, never media transport or credentials.
use reqwest::{Client, Url};
use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::future::Future;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant};
use tokio::sync::{Notify, OwnedSemaphorePermit, Semaphore};

const DNS_TIMEOUT: Duration = Duration::from_secs(3);
const DNS_FAMILY_TIMEOUT: Duration = Duration::from_secs(2);
const MAX_BODY: usize = 16 * 1024;
const MAX_RECORDS: usize = 64;
const MAX_CNAME: usize = 8;
const MAX_CACHE: usize = 256;
const MAX_TTL: Duration = Duration::from_secs(60);
const DOH_HOST: &str = "dns.alidns.com";
const DOH_URL: &str = "https://dns.alidns.com/dns-query";
const DNS_INVALID: &str = "The public CDN DNS response is invalid.";
const DNS_DENIED: &str = "The online CDN resolved to a private or reserved address.";
const DNS_UNAVAILABLE: &str = "The public CDN DNS resolver could not be reached.";
const DNS_BUSY: &str = "The public CDN DNS resolver is busy; please retry.";
const DNS_TIMED_OUT: &str = "The public CDN DNS resolver timed out.";
const DNS_NO_ANSWER: &str = "The public CDN DNS resolver returned no usable addresses.";
const DNS_SYSTEM: &str = "The online CDN could not be resolved.";

#[derive(Clone)]
struct Observation {
    addresses: Vec<IpAddr>,
    ttl: Duration,
    observed: Instant,
}

struct CacheEntry {
    observation: Observation,
    expires: Instant,
}

#[derive(Default)]
struct Flight {
    done: Notify,
    result: Mutex<Option<Result<Observation, String>>>,
}

struct State {
    cache: HashMap<String, CacheEntry>,
    flights: HashMap<String, Arc<Flight>>,
    tokens: f64,
    refilled: Instant,
}

struct PublicResolver {
    state: Mutex<State>,
    slots: Arc<Semaphore>,
}

impl PublicResolver {
    fn new() -> Self {
        Self {
            state: Mutex::new(State {
                cache: HashMap::new(),
                flights: HashMap::new(),
                tokens: 16.0,
                refilled: Instant::now(),
            }),
            slots: Arc::new(Semaphore::new(8)),
        }
    }

    async fn repair<F, Fut>(
        &self,
        hostname: &str,
        system: Vec<IpAddr>,
        query: F,
    ) -> Result<Vec<IpAddr>, String>
    where
        F: FnOnce(String) -> Fut,
        Fut: Future<Output = Result<Observation, String>>,
    {
        if system.is_empty() || system.len() > MAX_RECORDS {
            return Err(DNS_DENIED.to_string());
        }
        let mut fake = false;
        for address in &system {
            if fake_ip(*address) {
                fake = true;
            } else if !public_ip(*address) {
                return Err(DNS_DENIED.to_string());
            }
        }
        if !fake {
            return Ok(system);
        }
        let name = dns_hostname(hostname).ok_or_else(|| DNS_DENIED.to_string())?;
        enum Decision {
            Cached(Vec<IpAddr>),
            Wait(Arc<Flight>),
            Lead(Arc<Flight>, OwnedSemaphorePermit),
        }
        let decision = {
            let mut state = self.state.lock().map_err(|_| DNS_UNAVAILABLE.to_string())?;
            let now = Instant::now();
            state.cache.retain(|_, entry| now < entry.expires);
            if let Some(entry) = state.cache.get(&name) {
                Decision::Cached(entry.observation.addresses.clone())
            } else if let Some(flight) = state.flights.get(&name) {
                Decision::Wait(flight.clone())
            } else {
                let permit = self
                    .slots
                    .clone()
                    .try_acquire_owned()
                    .map_err(|_| DNS_BUSY.to_string())?;
                state.tokens = (state.tokens
                    + now.duration_since(state.refilled).as_secs_f64() * 8.0)
                    .min(16.0);
                state.refilled = now;
                if state.tokens < 1.0 {
                    return Err(DNS_BUSY.to_string());
                }
                state.tokens -= 1.0;
                let flight = Arc::new(Flight::default());
                state.flights.insert(name.clone(), flight.clone());
                Decision::Lead(flight, permit)
            }
        };
        match decision {
            Decision::Cached(addresses) => Ok(addresses),
            Decision::Wait(flight) => {
                let notified = flight.done.notified();
                tokio::pin!(notified);
                // Register before checking the result to prevent a missed wakeup.
                notified.as_mut().enable();
                if let Some(result) = flight
                    .result
                    .lock()
                    .map_err(|_| DNS_UNAVAILABLE.to_string())?
                    .clone()
                {
                    return result.map(|observation| observation.addresses);
                }
                tokio::time::timeout(DNS_TIMEOUT, notified)
                    .await
                    .map_err(|_| DNS_TIMED_OUT.to_string())?;
                let result = flight
                    .result
                    .lock()
                    .map_err(|_| DNS_UNAVAILABLE.to_string())?
                    .clone();
                result
                    .ok_or_else(|| DNS_UNAVAILABLE.to_string())?
                    .map(|observation| observation.addresses)
            }
            Decision::Lead(flight, permit) => {
                let guard = FlightGuard {
                    resolver: self,
                    name: name.clone(),
                    flight: flight.clone(),
                    _permit: permit,
                };
                let result = match tokio::time::timeout(DNS_TIMEOUT, query(name.clone())).await {
                    Ok(result) => result.and_then(|mut observation| {
                        if observation.addresses.is_empty()
                            || observation.addresses.len() > MAX_RECORDS
                            || observation.addresses.iter().any(|ip| !public_ip(*ip))
                        {
                            return Err(DNS_DENIED.to_string());
                        }
                        observation.addresses.sort();
                        observation.addresses.dedup();
                        observation.ttl = observation
                            .ttl
                            .saturating_sub(observation.observed.elapsed())
                            .min(MAX_TTL);
                        observation.observed = Instant::now();
                        Ok(observation)
                    }),
                    Err(_) => Err(DNS_TIMED_OUT.to_string()),
                };
                if let Ok(observation) = &result {
                    if !observation.ttl.is_zero() {
                        let mut state =
                            self.state.lock().map_err(|_| DNS_UNAVAILABLE.to_string())?;
                        if state.cache.len() >= MAX_CACHE {
                            if let Some(oldest) = state
                                .cache
                                .iter()
                                .min_by_key(|(_, value)| value.expires)
                                .map(|(key, _)| key.clone())
                            {
                                state.cache.remove(&oldest);
                            }
                        }
                        state.cache.insert(
                            name,
                            CacheEntry {
                                observation: observation.clone(),
                                expires: observation.observed + observation.ttl,
                            },
                        );
                    }
                }
                *flight
                    .result
                    .lock()
                    .map_err(|_| DNS_UNAVAILABLE.to_string())? = Some(result.clone());
                drop(guard);
                result.map(|observation| observation.addresses)
            }
        }
    }
}

// Cancellation of a download drops its DNS future too. Remove its flight and
// wake followers even if it was cancelled before producing an observation.
struct FlightGuard<'a> {
    resolver: &'a PublicResolver,
    name: String,
    flight: Arc<Flight>,
    _permit: OwnedSemaphorePermit,
}

impl Drop for FlightGuard<'_> {
    fn drop(&mut self) {
        if let Ok(mut result) = self.flight.result.lock() {
            if result.is_none() {
                *result = Some(Err(DNS_UNAVAILABLE.to_string()));
            }
        }
        if let Ok(mut state) = self.resolver.state.lock() {
            state.flights.remove(&self.name);
        }
        self.flight.done.notify_waiters();
    }
}

pub(crate) async fn resolve_cdn(hostname: &str, port: u16) -> Result<Vec<SocketAddr>, String> {
    if let Ok(ip) = hostname.parse::<IpAddr>() {
        return if public_ip(ip) {
            Ok(vec![SocketAddr::new(ip, port)])
        } else {
            Err(DNS_DENIED.to_string())
        };
    }
    // Always repeat system DNS before consulting fallback cache, including
    // redirects and repeated units. A new unknown private answer fails closed.
    let system = tokio::net::lookup_host((hostname, port))
        .await
        .map_err(|_| DNS_SYSTEM.to_string())?
        .collect::<Vec<_>>();
    if system
        .iter()
        .any(|address| matches!(address, SocketAddr::V6(ip) if ip.scope_id() != 0))
    {
        return Err(DNS_DENIED.to_string());
    }
    let addresses = system.into_iter().map(|address| address.ip()).collect();
    static RESOLVER: OnceLock<PublicResolver> = OnceLock::new();
    let ips = RESOLVER
        .get_or_init(PublicResolver::new)
        .repair(hostname, addresses, query_public_dns)
        .await?;
    Ok(ips
        .into_iter()
        .map(|ip| SocketAddr::new(ip, port))
        .collect())
}

fn doh_client() -> Result<Client, String> {
    let bootstrap = [
        SocketAddr::new(Ipv4Addr::new(223, 5, 5, 5).into(), 443),
        SocketAddr::new(Ipv4Addr::new(223, 6, 6, 6).into(), 443),
    ];
    Client::builder()
        .no_proxy()
        .https_only(true)
        .redirect(reqwest::redirect::Policy::none())
        .resolve_to_addrs(DOH_HOST, &bootstrap)
        .connect_timeout(Duration::from_secs(1))
        .read_timeout(Duration::from_secs(2))
        .timeout(DNS_TIMEOUT)
        .build()
        .map_err(|_| DNS_UNAVAILABLE.to_string())
}

async fn query_public_dns(name: String) -> Result<Observation, String> {
    let client = doh_client()?;
    let (a, aaaa) = tokio::join!(
        bounded_family(query_family(&client, &name, 1)),
        bounded_family(query_family(&client, &name, 28))
    );
    merge_family_results(a, aaaa)
}

async fn bounded_family<F>(query: F) -> Result<Observation, String>
where
    F: Future<Output = Result<Observation, String>>,
{
    tokio::time::timeout(DNS_FAMILY_TIMEOUT, query)
        .await
        .map_err(|_| DNS_TIMED_OUT.to_string())?
}

fn transient_family_error(error: &str) -> bool {
    matches!(error, DNS_TIMED_OUT | DNS_UNAVAILABLE)
}

fn merge_family_results(
    a: Result<Observation, String>,
    aaaa: Result<Observation, String>,
) -> Result<Observation, String> {
    match (a, aaaa) {
        (Ok(a), Ok(aaaa)) => merge_families(a, aaaa),
        (Ok(answer), Err(error)) | (Err(error), Ok(answer)) => {
            // A transport outage in one family must not discard an already
            // validated public answer. Invalid/unsafe responses stay fatal.
            if !transient_family_error(&error) || answer.addresses.is_empty() {
                return Err(error);
            }
            merge_families(
                answer,
                Observation {
                    addresses: Vec::new(),
                    ttl: Duration::ZERO,
                    observed: Instant::now(),
                },
            )
        }
        (Err(a), Err(aaaa)) => {
            if !transient_family_error(&a) {
                Err(a)
            } else if !transient_family_error(&aaaa) {
                Err(aaaa)
            } else if a == DNS_TIMED_OUT || aaaa == DNS_TIMED_OUT {
                Err(DNS_TIMED_OUT.to_string())
            } else {
                Err(DNS_UNAVAILABLE.to_string())
            }
        }
    }
}

fn merge_families(a: Observation, aaaa: Observation) -> Result<Observation, String> {
    let a_remaining = a.ttl.saturating_sub(a.observed.elapsed());
    let aaaa_remaining = aaaa.ttl.saturating_sub(aaaa.observed.elapsed());
    let ttl = match (a.addresses.is_empty(), aaaa.addresses.is_empty()) {
        (false, false) => a_remaining.min(aaaa_remaining),
        (false, true) => a_remaining,
        (true, false) => aaaa_remaining,
        (true, true) => return Err(DNS_NO_ANSWER.to_string()),
    };
    let mut addresses = a.addresses;
    addresses.extend(aaaa.addresses);
    Ok(Observation {
        addresses,
        ttl,
        observed: Instant::now(),
    })
}

async fn query_family(client: &Client, name: &str, kind: u16) -> Result<Observation, String> {
    let id = rand::random::<u16>();
    let wire = dns_query(name, kind, id)?;
    let response = client
        .post(Url::parse(DOH_URL).map_err(|_| DNS_INVALID.to_string())?)
        .header("Accept", "application/dns-message")
        .header("Content-Type", "application/dns-message")
        .header("Cache-Control", "no-cache")
        .body(wire)
        .send()
        .await
        .map_err(dns_request_error)?;
    let observed = Instant::now();
    if response.status() != reqwest::StatusCode::OK
        || response
            .headers()
            .get("Content-Type")
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.split(';').next())
            .map(str::trim)
            != Some("application/dns-message")
        || response
            .content_length()
            .is_some_and(|size| size > MAX_BODY as u64)
    {
        return Err(DNS_INVALID.to_string());
    }
    let age = response
        .headers()
        .get("Age")
        .map(|value| {
            value
                .to_str()
                .ok()
                .and_then(|v| v.parse::<u32>().ok())
                .ok_or_else(|| DNS_INVALID.to_string())
        })
        .transpose()?
        .unwrap_or(0);
    let cache_limit = cache_control_limit(
        response
            .headers()
            .get("Cache-Control")
            .map(|value| value.to_str().map_err(|_| DNS_INVALID.to_string()))
            .transpose()?,
    )?;
    let mut body = Vec::new();
    let mut response = response;
    while let Some(chunk) = response.chunk().await.map_err(dns_request_error)? {
        if body.len() + chunk.len() > MAX_BODY {
            return Err(DNS_INVALID.to_string());
        }
        body.extend_from_slice(&chunk);
    }
    let mut observation = parse_answer(&body, name, kind, id)?;
    observation.observed = observed;
    let age = Duration::from_secs(age.into());
    observation.ttl = observation
        .ttl
        .saturating_sub(age)
        .min(cache_limit.saturating_sub(age))
        .min(MAX_TTL);
    Ok(observation)
}

fn dns_request_error(error: reqwest::Error) -> String {
    if error.is_timeout() {
        return DNS_TIMED_OUT.to_string();
    }
    let mut source = error.source();
    while let Some(cause) = source {
        if let Some(io) = cause.downcast_ref::<std::io::Error>() {
            if matches!(
                io.kind(),
                std::io::ErrorKind::ConnectionRefused
                    | std::io::ErrorKind::ConnectionReset
                    | std::io::ErrorKind::ConnectionAborted
                    | std::io::ErrorKind::NotConnected
                    | std::io::ErrorKind::BrokenPipe
                    | std::io::ErrorKind::AddrNotAvailable
                    | std::io::ErrorKind::NetworkUnreachable
                    | std::io::ErrorKind::HostUnreachable
                    | std::io::ErrorKind::NetworkDown
            ) {
                return DNS_UNAVAILABLE.to_string();
            }
        }
        source = cause.source();
    }
    // TLS, malformed HTTP/body framing and other unclassified failures are not
    // proof of a transient outage and must not be ignored by family merging.
    DNS_INVALID.to_string()
}

fn cache_control_limit(header: Option<&str>) -> Result<Duration, String> {
    let mut limit = Duration::from_secs(u32::MAX.into());
    if let Some(value) = header {
        if value.len() > 4096 {
            return Err(DNS_INVALID.to_string());
        }
        for directive in value.split(',') {
            let directive = directive.trim().to_ascii_lowercase();
            if matches!(directive.as_str(), "no-store" | "no-cache") {
                return Ok(Duration::ZERO);
            }
            if let Some((key, value)) = directive.split_once('=') {
                if key.trim() == "max-age" || key.trim() == "s-maxage" {
                    let seconds = value
                        .trim()
                        .trim_matches('"')
                        .parse::<u32>()
                        .map_err(|_| DNS_INVALID.to_string())?;
                    limit = limit.min(Duration::from_secs(seconds.into()));
                }
            }
        }
    }
    Ok(limit)
}

fn fake_ip(address: IpAddr) -> bool {
    if let IpAddr::V6(ip) = address {
        if let Some(ipv4) = ip.to_ipv4_mapped() {
            return fake_ip(ipv4.into());
        }
    }
    match address {
        IpAddr::V4(ip) => ip.octets()[0] == 198 && (18..=19).contains(&ip.octets()[1]),
        IpAddr::V6(ip) => ip.segments()[..4] == [0xfdfe, 0xdcba, 0x9876, 0],
    }
}

pub(crate) fn public_ip(address: IpAddr) -> bool {
    match address {
        IpAddr::V4(ip) => {
            let octets = ip.octets();
            !ip.is_private()
                && !ip.is_loopback()
                && !ip.is_link_local()
                && !ip.is_multicast()
                && !ip.is_broadcast()
                && !ip.is_documentation()
                && octets[0] != 0
                && octets[0] < 240
                && !(octets[0] == 100 && (64..=127).contains(&octets[1]))
                && !(octets[0] == 198 && (18..=19).contains(&octets[1]))
                && !(octets[0] == 192 && octets[1] == 0 && octets[2] == 0)
        }
        IpAddr::V6(ip) => {
            let segments = ip.segments();
            (segments[0] & 0xe000) == 0x2000
                && segments[0] != 0x2002
                && !(segments[0] == 0x2001 && (segments[1] == 0x0db8 || segments[1] < 0x0200))
        }
    }
}

fn dns_hostname(hostname: &str) -> Option<String> {
    let name = hostname
        .strip_suffix('.')
        .unwrap_or(hostname)
        .to_ascii_lowercase();
    if name.len() > 253
        || name.parse::<IpAddr>().is_ok()
        || !name.contains('.')
        || name.ends_with(".local")
        || name.ends_with(".localhost")
    {
        return None;
    }
    if name.split('.').any(|label| {
        label.is_empty()
            || label.len() > 63
            || label.starts_with('-')
            || label.ends_with('-')
            || !label
                .bytes()
                .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
    }) {
        return None;
    }
    Some(name)
}

fn dns_query(name: &str, kind: u16, id: u16) -> Result<Vec<u8>, String> {
    let name = dns_hostname(name).ok_or_else(|| DNS_INVALID.to_string())?;
    if !matches!(kind, 1 | 28) {
        return Err(DNS_INVALID.to_string());
    }
    let mut wire = Vec::new();
    for value in [id, 0x0100, 1, 0, 0, 0] {
        wire.extend_from_slice(&value.to_be_bytes());
    }
    for label in name.split('.') {
        wire.push(label.len() as u8);
        wire.extend_from_slice(label.as_bytes());
    }
    wire.push(0);
    wire.extend_from_slice(&kind.to_be_bytes());
    wire.extend_from_slice(&1u16.to_be_bytes());
    Ok(wire)
}

fn read_u16(wire: &[u8], cursor: &mut usize) -> Result<u16, String> {
    let bytes = wire
        .get(*cursor..*cursor + 2)
        .ok_or_else(|| DNS_INVALID.to_string())?;
    *cursor += 2;
    Ok(u16::from_be_bytes([bytes[0], bytes[1]]))
}

fn read_name(wire: &[u8], cursor: &mut usize) -> Result<String, String> {
    let mut offset = *cursor;
    let mut consumed = None;
    let mut labels = Vec::new();
    let mut visited = HashSet::new();
    let mut bytes = 1usize;
    loop {
        if !visited.insert(offset) || visited.len() > 128 {
            return Err(DNS_INVALID.to_string());
        }
        let length = *wire.get(offset).ok_or_else(|| DNS_INVALID.to_string())?;
        if length & 0xc0 == 0xc0 {
            let low = *wire
                .get(offset + 1)
                .ok_or_else(|| DNS_INVALID.to_string())?;
            consumed.get_or_insert(offset + 2);
            let target = (((length & 0x3f) as usize) << 8) | low as usize;
            if target >= offset {
                return Err(DNS_INVALID.to_string());
            }
            offset = target;
            continue;
        }
        if length > 63 {
            return Err(DNS_INVALID.to_string());
        }
        offset += 1;
        if length == 0 {
            *cursor = consumed.unwrap_or(offset);
            break;
        }
        let label = wire
            .get(offset..offset + length as usize)
            .ok_or_else(|| DNS_INVALID.to_string())?;
        bytes += label.len() + 1;
        if bytes > 255 || !label.is_ascii() {
            return Err(DNS_INVALID.to_string());
        }
        labels.push(
            std::str::from_utf8(label)
                .map_err(|_| DNS_INVALID.to_string())?
                .to_ascii_lowercase(),
        );
        offset += length as usize;
    }
    Ok(labels.join("."))
}

struct Record {
    owner: String,
    kind: u16,
    ttl: u32,
    address: Option<IpAddr>,
    cname: Option<String>,
}

fn parse_answer(wire: &[u8], hostname: &str, kind: u16, id: u16) -> Result<Observation, String> {
    if wire.len() < 12 || wire.len() > MAX_BODY || !matches!(kind, 1 | 28) {
        return Err(DNS_INVALID.to_string());
    }
    let mut cursor = 0;
    let actual_id = read_u16(wire, &mut cursor)?;
    let flags = read_u16(wire, &mut cursor)?;
    let questions = read_u16(wire, &mut cursor)?;
    let answer_count = read_u16(wire, &mut cursor)? as usize;
    let authority_count = read_u16(wire, &mut cursor)? as usize;
    let additional_count = read_u16(wire, &mut cursor)? as usize;
    if actual_id != id
        || flags & 0x8000 == 0
        || flags & 0x7a4f != 0
        || questions != 1
        || answer_count + authority_count + additional_count > MAX_RECORDS
    {
        return Err(DNS_INVALID.to_string());
    }
    let expected = dns_hostname(hostname).ok_or_else(|| DNS_INVALID.to_string())?;
    if read_name(wire, &mut cursor)? != expected
        || read_u16(wire, &mut cursor)? != kind
        || read_u16(wire, &mut cursor)? != 1
    {
        return Err(DNS_INVALID.to_string());
    }
    let mut answers = Vec::new();
    for index in 0..answer_count + authority_count + additional_count {
        let owner = read_name(wire, &mut cursor)?;
        let record_kind = read_u16(wire, &mut cursor)?;
        let class = read_u16(wire, &mut cursor)?;
        let high = read_u16(wire, &mut cursor)? as u32;
        let low = read_u16(wire, &mut cursor)? as u32;
        let ttl = (high << 16) | low;
        let length = read_u16(wire, &mut cursor)? as usize;
        let end = cursor
            .checked_add(length)
            .filter(|end| *end <= wire.len())
            .ok_or_else(|| DNS_INVALID.to_string())?;
        if index < answer_count {
            if class != 1
                || (record_kind != kind && record_kind != 5)
                || dns_hostname(&owner).is_none()
            {
                return Err(DNS_INVALID.to_string());
            }
            let mut record = Record {
                owner,
                kind: record_kind,
                ttl,
                address: None,
                cname: None,
            };
            match record_kind {
                1 if length == 4 => {
                    record.address = Some(
                        Ipv4Addr::new(
                            wire[cursor],
                            wire[cursor + 1],
                            wire[cursor + 2],
                            wire[cursor + 3],
                        )
                        .into(),
                    )
                }
                28 if length == 16 => {
                    let mut bytes = [0u8; 16];
                    bytes.copy_from_slice(&wire[cursor..end]);
                    record.address = Some(Ipv6Addr::from(bytes).into());
                }
                5 => {
                    let target = read_name(wire, &mut cursor)?;
                    if cursor != end || dns_hostname(&target).is_none() {
                        return Err(DNS_INVALID.to_string());
                    }
                    record.cname = Some(target);
                }
                _ => return Err(DNS_INVALID.to_string()),
            }
            answers.push(record);
        }
        cursor = end;
    }
    if cursor != wire.len() {
        return Err(DNS_INVALID.to_string());
    }
    let mut aliases = HashMap::new();
    for record in &answers {
        if let Some(target) = &record.cname {
            if aliases
                .insert(record.owner.clone(), target.clone())
                .is_some_and(|previous| previous != *target)
            {
                return Err(DNS_INVALID.to_string());
            }
        }
    }
    let mut terminal = expected.clone();
    let mut chain = HashSet::from([expected]);
    while let Some(target) = aliases.get(&terminal) {
        if chain.len() > MAX_CNAME || !chain.insert(target.clone()) {
            return Err(DNS_INVALID.to_string());
        }
        terminal = target.clone();
    }
    let mut addresses = Vec::new();
    let mut ttl = Duration::from_secs(u32::MAX.into());
    for record in &answers {
        if !chain.contains(&record.owner) {
            return Err(DNS_INVALID.to_string());
        }
        ttl = ttl.min(Duration::from_secs(record.ttl.into()));
        if let Some(address) = record.address {
            if record.kind != kind || record.owner != terminal || !public_ip(address) {
                return Err(DNS_INVALID.to_string());
            }
            addresses.push(address);
        }
    }
    if addresses.is_empty() {
        ttl = Duration::ZERO;
    }
    Ok(Observation {
        addresses,
        ttl,
        observed: Instant::now(),
    })
}

#[cfg(test)]
#[path = "public_dns_tests.rs"]
mod tests;

// A test-only capability for real transport fixtures; production has no
// resolver, bootstrap, origin, trust or private-address override.
#[cfg(test)]
pub(crate) struct TestResolver(PublicResolver);

#[cfg(test)]
impl TestResolver {
    pub(crate) fn new() -> Self {
        Self(PublicResolver::new())
    }
    pub(crate) async fn repair(
        &self,
        name: &str,
        system: Vec<IpAddr>,
        public: Vec<IpAddr>,
        port: u16,
    ) -> Result<Vec<SocketAddr>, String> {
        let addresses = self
            .0
            .repair(name, system, |_| async {
                Ok(Observation {
                    addresses: public,
                    ttl: Duration::from_secs(30),
                    observed: Instant::now(),
                })
            })
            .await?;
        Ok(addresses
            .into_iter()
            .map(|ip| SocketAddr::new(ip, port))
            .collect())
    }
}

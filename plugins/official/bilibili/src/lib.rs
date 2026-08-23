use base64::{
    engine::general_purpose::{STANDARD as BASE64, URL_SAFE_NO_PAD as BASE64_URL},
    Engine as _,
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{mem, slice};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

const HOST_HTTP: i32 = 1;
const HOST_LOG: i32 = 4;
const HOST_STORAGE_GET: i32 = 2;
const HOST_STORAGE_SET: i32 = 3;
const HOST_ASSET_REGISTER: i32 = 7;
const HOST_CREDENTIAL_COMMIT: i32 = 8;
const HOST_CONFIG_GET: i32 = 9;
const HOST_BUFFER_BYTES: usize = 4 * 1024 * 1024;
const SESSION_SCOPE: &str = "bilibili.session";
const USER_AGENT: &str = "Mozilla/5.0 OhMyCine/0.1 BilibiliPlugin/0.1";

#[cfg(target_arch = "wasm32")]
#[link(wasm_import_module = "ohmycine")]
extern "C" {
    fn host_call(
        operation: i32,
        request_pointer: i32,
        request_length: i32,
        response_pointer: i32,
        response_capacity: i32,
    ) -> i32;
}

#[cfg(not(target_arch = "wasm32"))]
unsafe fn host_call(
    _operation: i32,
    _request_pointer: i32,
    _request_length: i32,
    _response_pointer: i32,
    _response_capacity: i32,
) -> i32 {
    -4
}

#[no_mangle]
pub extern "C" fn omc_api_version() -> i32 {
    1
}

#[no_mangle]
pub extern "C" fn omc_start() {}

#[no_mangle]
pub extern "C" fn omc_alloc(size: i32) -> i32 {
    if size < 0 {
        return 0;
    }
    let mut buffer = vec![0_u8; size as usize].into_boxed_slice();
    let pointer = buffer.as_mut_ptr();
    mem::forget(buffer);
    pointer as i32
}

#[no_mangle]
/// Releases a guest buffer previously returned by [`omc_alloc`].
///
/// # Safety
///
/// `pointer` and `length` must identify one live allocation returned by
/// `omc_alloc`, with the original length, and must not have been freed before.
pub unsafe extern "C" fn omc_free(pointer: i32, length: i32) {
    if pointer > 0 && length >= 0 {
        let raw = std::ptr::slice_from_raw_parts_mut(pointer as *mut u8, length as usize);
        drop(Box::from_raw(raw));
    }
}

#[no_mangle]
/// Invokes one versioned plugin operation with a copied JSON request.
///
/// # Safety
///
/// `request_pointer..request_pointer + request_length` must be a readable,
/// live range in this module's linear memory for the duration of the call.
pub unsafe extern "C" fn omc_invoke(
    operation: i32,
    request_pointer: i32,
    request_length: i32,
) -> i64 {
    if request_pointer <= 0 || request_length < 0 {
        return encode_response(plugin_error("invalid-request", "请求无效"));
    }
    let request = slice::from_raw_parts(request_pointer as *const u8, request_length as usize);
    let result = match operation {
        1 => parse_request(request).and_then(navigation),
        2 => parse_request(request).and_then(feed),
        3 => parse_request(request).and_then(search),
        4 => parse_request(request).and_then(detail),
        5 => parse_request(request).and_then(playback),
        6 => parse_request(request).and_then(download_plan),
        7 => parse_request(request).and_then(history),
        8 => parse_request(request).and_then(progress_sync),
        9 => parse_request(request).and_then(site_action),
        10 => parse_request(request).and_then(auth_start),
        11 => parse_request(request).and_then(auth_poll),
        12 => parse_request(request).and_then(metadata),
        13 => parse_request(request).and_then(library_artwork_candidates),
        _ => Err(PluginError::new("invalid-request", "不支持的插件操作")),
    };
    encode_response(match result {
        Ok(value) => value,
        Err(error) => plugin_error(error.code, error.message),
    })
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct LibraryArtworkRequest {
    connection_id: String,
}

fn library_artwork_candidates(request: LibraryArtworkRequest) -> Result<Value, PluginError> {
    if !valid_opaque_key(&request.connection_id) {
        return Err(PluginError::new("invalid-request", "媒体库封面请求无效"));
    }
    let payload = bili_get(&request.connection_id, &recommendation_url(1, None), true)?;
    let items = payload
        .pointer("/data/item")
        .or_else(|| payload.pointer("/data/items"))
        .and_then(Value::as_array)
        .ok_or_else(|| PluginError::new("invalid-response", "推荐封面响应无效"))?;
    let mut candidates = Vec::new();
    for item in items {
        let Some(bvid) = item.get("bvid").and_then(Value::as_str) else {
            continue;
        };
        if validate_bvid(bvid).is_err() {
            continue;
        }
        let Some(url) = item
            .get("pic")
            .or_else(|| item.get("cover"))
            .and_then(Value::as_str)
            .and_then(safe_https_url)
        else {
            continue;
        };
        let headers = json!({"Referer":"https://www.bilibili.com/","User-Agent":USER_AGENT});
        let Ok((asset_ref, _)) = register_asset(&request.connection_id, &url, headers) else {
            continue;
        };
        candidates.push(json!({"id":bvid,"assetRef":asset_ref}));
        if candidates.len() == 9 {
            break;
        }
    }
    if candidates.is_empty() {
        return Err(PluginError::new(
            "upstream-unavailable",
            "暂时没有可用的媒体库封面候选",
        ));
    }
    host_log(
        "info",
        "library.artwork_candidates",
        "library artwork candidates resolved",
        candidates.len(),
    );
    Ok(Value::Array(candidates))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct NavigationRequest {
    connection_id: String,
    parent_node_key: Option<String>,
    depth: Option<u8>,
}

fn navigation(request: NavigationRequest) -> Result<Value, PluginError> {
    if !valid_opaque_key(&request.connection_id) || request.depth.unwrap_or(0) > 8 {
        return Err(PluginError::new("invalid-request", "导航请求无效"));
    }
    let nodes = match request.parent_node_key.as_deref() {
        None => json!([
            {"id":"recommended","title":"推荐","kind":"feed","routeKey":"recommended","refreshable":true},
            {"id":"popular","title":"热门","kind":"feed","routeKey":"popular","refreshable":true},
            {"id":"ranking","title":"排行","kind":"feed","routeKey":"ranking","refreshable":true},
            {"id":"anime","title":"番剧与动画","kind":"branch","nodeKey":"anime","hasChildren":true},
            {"id":"cinephile","title":"影视专区","kind":"branch","nodeKey":"cinephile","hasChildren":true},
            {"id":"documentary","title":"纪录片","kind":"feed","routeKey":"documentary","refreshable":true},
            {"id":"personal","title":"我的 Bilibili","kind":"branch","nodeKey":"personal","hasChildren":true}
        ]),
        Some("anime") => json!([
            {"id":"anime-jp","title":"日本番剧","kind":"feed","routeKey":"anime-jp","refreshable":true},
            {"id":"anime-cn","title":"国产动画","kind":"feed","routeKey":"anime-cn","refreshable":true},
            {"id":"anime-other","title":"动画专区","kind":"feed","routeKey":"anime-other","refreshable":true}
        ]),
        Some("cinephile") => json!([
            {"id":"movies","title":"电影","kind":"feed","routeKey":"movies","refreshable":true},
            {"id":"tv-series","title":"电视剧","kind":"feed","routeKey":"tv-series","refreshable":true},
            {"id":"cinephile-talk","title":"影视杂谈","kind":"feed","routeKey":"cinephile","refreshable":true}
        ]),
        Some("personal") => json!([
            {"id":"favorites","title":"收藏","kind":"user-library","routeKey":"favorites","refreshable":true},
            {"id":"watch-later","title":"稍后再看","kind":"user-library","routeKey":"watch-later","refreshable":true},
            {"id":"following","title":"关注","kind":"user-library","routeKey":"following","refreshable":true},
            {"id":"subscriptions","title":"追更","kind":"user-library","routeKey":"subscriptions","refreshable":true}
        ]),
        Some(_) => return Err(PluginError::new("invalid-request", "导航分支无效")),
    };
    Ok(json!({"version":2,"mode":"hierarchical","nodes":nodes}))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AuthStartRequest {
    connection_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AuthPollRequest {
    connection_id: String,
    login_session: String,
}

fn auth_start(request: AuthStartRequest) -> Result<Value, PluginError> {
    let payload = bili_get(
        &request.connection_id,
        "https://passport.bilibili.com/x/passport-login/web/qrcode/generate",
        false,
    )?;
    let (qr_url, key) = parse_qr_generate(&payload)?;
    let expires_at = host_now()?.saturating_add(time::Duration::minutes(3));
    storage_set(
        &request.connection_id,
        "auth/qr",
        &json!({"key":key,"expiresAt":format_rfc3339(expires_at)?}),
    )?;
    host_log("info", "site.auth_start", "qr login started", 1);
    Ok(json!({
        "loginSession":"current",
        "qrCodeUrl":qr_url,
        "expiresAt":format_rfc3339(expires_at)?,
        "pollAfterSeconds":2
    }))
}

fn auth_poll(request: AuthPollRequest) -> Result<Value, PluginError> {
    if request.login_session != "current" {
        return Err(PluginError::new("invalid-request", "扫码登录会话无效"));
    }
    let state = storage_get(&request.connection_id, "auth/qr")?
        .ok_or_else(|| PluginError::new("not-found", "扫码登录会话不存在"))?;
    let key = state
        .get("key")
        .and_then(Value::as_str)
        .filter(|value| valid_opaque_key(value))
        .ok_or_else(|| PluginError::new("invalid-response", "扫码登录状态无效"))?;
    let expires_at = state
        .get("expiresAt")
        .and_then(Value::as_str)
        .and_then(|value| OffsetDateTime::parse(value, &Rfc3339).ok())
        .ok_or_else(|| PluginError::new("invalid-response", "扫码登录状态无效"))?;
    let now = host_now()?;
    if now >= expires_at {
        return Ok(json!({"state":"expired","authenticated":false}));
    }
    if let Some(previous) = storage_get(&request.connection_id, "auth/last-poll")?
        .and_then(|value| value.get("at").and_then(Value::as_str).map(str::to_owned))
        .and_then(|value| OffsetDateTime::parse(&value, &Rfc3339).ok())
    {
        if now - previous < time::Duration::seconds(1) {
            return Err(PluginError::new("rate-limited", "扫码轮询过于频繁"));
        }
    }
    storage_set(
        &request.connection_id,
        "auth/last-poll",
        &json!({"at":format_rfc3339(now)?}),
    )?;
    let response = host_json(
        HOST_HTTP,
        &json!({
            "connectionId":request.connection_id,
            "method":"GET",
            "url":format!("https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key={}", urlencoding::encode(key)),
            "headers":{"Accept":"application/json","Referer":"https://www.bilibili.com/","User-Agent":USER_AGENT},
            "captureCredentialScope":SESSION_SCOPE,
            "timeoutMs":12000
        }),
    )?;
    let payload = decode_http_json(&response, "扫码登录轮询失败")?;
    if payload.get("code").and_then(Value::as_i64).unwrap_or(-1) != 0 {
        return Err(PluginError::new(
            "upstream-unavailable",
            "扫码登录轮询被拒绝",
        ));
    }
    match parse_qr_poll(&payload)? {
        QRLoginState::Pending => {
            Ok(json!({"state":"pending","authenticated":false,"pollAfterSeconds":2}))
        }
        QRLoginState::Scanned => {
            Ok(json!({"state":"scanned","authenticated":false,"pollAfterSeconds":2}))
        }
        QRLoginState::Expired => Ok(json!({"state":"expired","authenticated":false})),
        QRLoginState::Confirmed => {
            let capture_ref = response
                .get("credentialCaptureRef")
                .and_then(Value::as_str)
                .filter(|value| valid_opaque_key(value))
                .ok_or_else(|| PluginError::new("invalid-response", "登录凭据捕获失败"))?;
            let committed = host_json(
                HOST_CREDENTIAL_COMMIT,
                &json!({"connectionId":request.connection_id,"scope":SESSION_SCOPE,"captureRef":capture_ref}),
            )?;
            if !committed
                .get("credentialUpdated")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                return Err(PluginError::new("internal", "登录凭据保存失败"));
            }
            let account = account_summary(&request.connection_id)?;
            storage_set(
                &request.connection_id,
                "auth/qr",
                &json!({"completed":true}),
            )?;
            host_log("info", "site.auth_poll", "qr login confirmed", 1);
            Ok(json!({"state":"confirmed","authenticated":true,"account":account}))
        }
    }
}

fn parse_qr_generate(payload: &Value) -> Result<(String, String), PluginError> {
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("invalid-response", "扫码登录响应无效"))?;
    let qr_url = data
        .get("url")
        .and_then(Value::as_str)
        .and_then(safe_https_url)
        .ok_or_else(|| PluginError::new("invalid-response", "扫码登录地址无效"))?;
    let key = data
        .get("qrcode_key")
        .and_then(Value::as_str)
        .filter(|value| valid_opaque_key(value))
        .ok_or_else(|| PluginError::new("invalid-response", "扫码登录会话无效"))?;
    Ok((qr_url, key.to_owned()))
}

#[derive(Debug, PartialEq, Eq)]
enum QRLoginState {
    Pending,
    Scanned,
    Confirmed,
    Expired,
}

fn parse_qr_poll(payload: &Value) -> Result<QRLoginState, PluginError> {
    match payload.pointer("/data/code").and_then(Value::as_i64) {
        Some(86101) => Ok(QRLoginState::Pending),
        Some(86090) => Ok(QRLoginState::Scanned),
        Some(86038) => Ok(QRLoginState::Expired),
        Some(0) => Ok(QRLoginState::Confirmed),
        _ => Err(PluginError::new("upstream-unavailable", "扫码登录状态未知")),
    }
}

fn host_now() -> Result<OffsetDateTime, PluginError> {
    let response = host_json(5, &json!({}))?;
    response
        .get("now")
        .and_then(Value::as_str)
        .and_then(|value| OffsetDateTime::parse(value, &Rfc3339).ok())
        .ok_or_else(|| PluginError::new("invalid-response", "宿主时间响应无效"))
}

fn format_rfc3339(value: OffsetDateTime) -> Result<String, PluginError> {
    value
        .format(&Rfc3339)
        .map_err(|_| PluginError::new("internal", "时间格式化失败"))
}

fn account_summary(connection_id: &str) -> Result<Value, PluginError> {
    let payload = bili_get_required(
        connection_id,
        "https://api.bilibili.com/x/web-interface/nav",
    )?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("not-authenticated", "Bilibili 会话无效"))?;
    if !data
        .get("isLogin")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        return Err(PluginError::new("not-authenticated", "Bilibili 会话无效"));
    }
    let mid = data
        .get("mid")
        .and_then(Value::as_u64)
        .filter(|value| *value > 0)
        .ok_or_else(|| PluginError::new("invalid-response", "Bilibili 账号身份无效"))?;
    Ok(json!({
        "id":mid.to_string(),
        "name":data.get("uname").and_then(Value::as_str).unwrap_or("Bilibili 用户"),
        "avatarUrl":data.get("face").and_then(Value::as_str).and_then(safe_https_url)
    }))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct HistoryRequest {
    connection_id: String,
    cursor: Option<String>,
    page_size: Option<u8>,
}

fn history(request: HistoryRequest) -> Result<Value, PluginError> {
    let cursor = decode_history_cursor(request.cursor.as_deref())?;
    let page_size = request.page_size.unwrap_or(24).clamp(1, 100);
    let url = history_url(page_size, cursor.as_ref());
    let payload = bili_get_required(&request.connection_id, &url)?;
    parse_history_page(&payload)
}

fn history_url(page_size: u8, cursor: Option<&HistoryCursor>) -> String {
    let mut url = format!(
        "https://api.bilibili.com/x/web-interface/history/cursor?ps={page_size}&type=archive"
    );
    if let Some(cursor) = cursor {
        if cursor.max > 0 {
            url.push_str("&max=");
            url.push_str(&cursor.max.to_string());
        }
        if cursor.view_at > 0 {
            url.push_str("&view_at=");
            url.push_str(&cursor.view_at.to_string());
        }
        if !cursor.business.is_empty() {
            url.push_str("&business=");
            url.push_str(&urlencoding::encode(&cursor.business));
        }
    }
    url
}

fn parse_history_page(payload: &Value) -> Result<Value, PluginError> {
    let list = payload
        .pointer("/data/list")
        .and_then(Value::as_array)
        .ok_or_else(|| PluginError::new("invalid-response", "历史响应无效"))?;
    let items = list.iter().filter_map(history_item).collect::<Vec<_>>();
    let cursor = payload.pointer("/data/cursor").unwrap_or(&Value::Null);
    let next = HistoryCursor {
        max: cursor.get("max").and_then(Value::as_u64).unwrap_or(0),
        view_at: cursor.get("view_at").and_then(Value::as_u64).unwrap_or(0),
        business: cursor
            .get("business")
            .and_then(Value::as_str)
            .unwrap_or("")
            .chars()
            .take(32)
            .collect(),
    };
    let next = if !items.is_empty() && (next.max > 0 || next.view_at > 0) {
        Some(encode_history_cursor(&next)?)
    } else {
        None
    };
    let has_more = next.is_some();
    Ok(json!({"list":items,"cursor":next,"hasMore":has_more}))
}

#[derive(Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct HistoryCursor {
    max: u64,
    view_at: u64,
    #[serde(default)]
    business: String,
}

fn decode_history_cursor(value: Option<&str>) -> Result<Option<HistoryCursor>, PluginError> {
    let Some(value) = value.filter(|value| !value.is_empty()) else {
        return Ok(None);
    };
    if value.len() > 512 {
        return Err(PluginError::new("invalid-request", "历史游标无效"));
    }
    // Preserve an in-flight page created by the early single-timestamp cursor.
    if value.bytes().all(|byte| byte.is_ascii_digit()) {
        let view_at = value
            .parse::<u64>()
            .map_err(|_| PluginError::new("invalid-request", "历史游标无效"))?;
        return Ok(Some(HistoryCursor {
            max: 0,
            view_at,
            business: String::new(),
        }));
    }
    let decoded = BASE64_URL
        .decode(value)
        .map_err(|_| PluginError::new("invalid-request", "历史游标无效"))?;
    if decoded.len() > 256 {
        return Err(PluginError::new("invalid-request", "历史游标无效"));
    }
    let cursor: HistoryCursor = serde_json::from_slice(&decoded)
        .map_err(|_| PluginError::new("invalid-request", "历史游标无效"))?;
    if (cursor.max == 0 && cursor.view_at == 0)
        || cursor.business.len() > 32
        || !cursor
            .business
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
    {
        return Err(PluginError::new("invalid-request", "历史游标无效"));
    }
    Ok(Some(cursor))
}

fn encode_history_cursor(cursor: &HistoryCursor) -> Result<String, PluginError> {
    serde_json::to_vec(cursor)
        .map(|encoded| BASE64_URL.encode(encoded))
        .map_err(|_| PluginError::new("invalid-response", "历史游标编码失败"))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct ProgressRequest {
    connection_id: String,
    item_id: String,
    segment_id: String,
    version_id: String,
    event: String,
    position_seconds: f64,
    duration_seconds: Option<f64>,
    idempotency_key: String,
    occurred_at: Option<String>,
}

fn progress_sync(request: ProgressRequest) -> Result<Value, PluginError> {
    let bvid = validate_bvid(&request.item_id)?;
    let cid = request
        .segment_id
        .strip_prefix("cid:")
        .unwrap_or(&request.segment_id);
    if cid.is_empty()
        || !cid.bytes().all(|byte| byte.is_ascii_digit())
        || request.version_id != format!("bilibili:{bvid}:{cid}")
        || request.idempotency_key.is_empty()
        || request.idempotency_key.len() > 128
        || !request
            .idempotency_key
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
        || !matches!(
            request.event.as_str(),
            "started" | "progress" | "paused" | "resumed" | "stopped" | "completed"
        )
        || !request.position_seconds.is_finite()
        || request.position_seconds < 0.0
    {
        return Err(PluginError::new("invalid-request", "播放进度请求无效"));
    }
    let state_key = format!("progress/{bvid}/{cid}");
    if let Some(previous) = storage_get(&request.connection_id, &state_key)? {
        match progress_decision(&previous, &request) {
            ProgressDecision::Duplicate => {
                return Ok(json!({"accepted":true,"remote":true,"duplicate":true}));
            }
            ProgressDecision::Throttled => {
                return Ok(json!({"accepted":false,"remote":false,"retryAfterSeconds":5}));
            }
            ProgressDecision::Submit => {}
        }
    }
    let progress = if request.event == "completed" {
        -1_i64
    } else {
        request.position_seconds.round() as i64
    };
    let duration = request.duration_seconds.unwrap_or(0.0).max(0.0).round() as i64;
    let form = format!(
        "bvid={}&cid={}&progress={progress}&duration={duration}&platform=web",
        urlencoding::encode(bvid),
        urlencoding::encode(cid)
    );
    bili_post_form(
        &request.connection_id,
        "https://api.bilibili.com/x/v2/history/report",
        &form,
    )?;
    storage_set(
        &request.connection_id,
        &state_key,
        &json!({
            "idempotencyKey":request.idempotency_key,
            "positionSeconds":request.position_seconds,
            "event":request.event,
            "versionId":request.version_id,
            "occurredAt":request.occurred_at
        }),
    )?;
    host_log(
        "info",
        "playback.progress_sync",
        "playback progress synchronized",
        1,
    );
    Ok(json!({"accepted":true,"remote":true}))
}

#[derive(Debug, PartialEq, Eq)]
enum ProgressDecision {
    Duplicate,
    Throttled,
    Submit,
}

fn progress_decision(previous: &Value, request: &ProgressRequest) -> ProgressDecision {
    if previous.get("idempotencyKey").and_then(Value::as_str)
        == Some(request.idempotency_key.as_str())
    {
        return ProgressDecision::Duplicate;
    }
    if request.event == "progress" {
        let previous_position = previous
            .get("positionSeconds")
            .and_then(Value::as_f64)
            .unwrap_or(-10.0);
        if (request.position_seconds - previous_position).abs() < 5.0 {
            return ProgressDecision::Throttled;
        }
    }
    ProgressDecision::Submit
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SiteActionRequest {
    connection_id: String,
    action: String,
    item_id: String,
    segment_id: Option<String>,
    version_id: Option<String>,
    value: Option<bool>,
    idempotency_key: String,
}

fn site_action(request: SiteActionRequest) -> Result<Value, PluginError> {
    if !valid_opaque_key(&request.idempotency_key)
        || request.idempotency_key.len() > 96
        || request
            .segment_id
            .as_deref()
            .is_some_and(|value| !valid_identity_key(value))
        || request
            .version_id
            .as_deref()
            .is_some_and(|value| !valid_identity_key(value))
    {
        return Err(PluginError::new("invalid-request", "站点操作幂等标识无效"));
    }
    let state = if request.action.ends_with(".add") {
        true
    } else if request.action.ends_with(".remove") {
        false
    } else {
        return Err(PluginError::new("invalid-request", "站点操作不受支持"));
    };
    if request.value.is_some_and(|value| value != state) {
        return Err(PluginError::new("invalid-request", "站点操作状态冲突"));
    }
    let mut recent_actions = storage_get(&request.connection_id, "actions/recent")?
        .and_then(|value| value.as_array().cloned())
        .unwrap_or_default();
    if recent_actions.iter().any(|entry| {
        entry.get("idempotencyKey").and_then(Value::as_str)
            == Some(request.idempotency_key.as_str())
    }) {
        return Ok(json!({"accepted":true,"state":state,"duplicate":true}));
    }
    match request.action.as_str() {
        "follow.add" | "follow.remove" => {
            let mid = request
                .item_id
                .strip_prefix("up:")
                .filter(|value| {
                    !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit())
                })
                .ok_or_else(|| PluginError::new("invalid-request", "UP 主身份无效"))?;
            let form = format!("fid={mid}&act={}&re_src=11", if state { 1 } else { 2 });
            bili_post_form(
                &request.connection_id,
                "https://api.bilibili.com/x/relation/modify",
                &form,
            )?;
        }
        "like.add" | "like.remove" | "watch-later.add" | "watch-later.remove" | "favorite.add"
        | "favorite.remove" => {
            let aid = video_aid(&request.connection_id, &request.item_id)?;
            match request.action.as_str() {
                "like.add" | "like.remove" => {
                    bili_post_form(
                        &request.connection_id,
                        "https://api.bilibili.com/x/web-interface/archive/like",
                        &format!("aid={aid}&like={}", if state { 1 } else { 2 }),
                    )?;
                }
                "watch-later.add" | "watch-later.remove" => {
                    bili_post_form(
                        &request.connection_id,
                        if state {
                            "https://api.bilibili.com/x/v2/history/toview/add"
                        } else {
                            "https://api.bilibili.com/x/v2/history/toview/del"
                        },
                        &format!("aid={aid}"),
                    )?;
                }
                _ => {
                    let folder_id = favorite_folder_id(&request.connection_id)?;
                    bili_post_form(
                        &request.connection_id,
                        "https://api.bilibili.com/x/v3/fav/resource/deal",
                        &format!(
                            "rid={aid}&type=2&{}_media_ids={folder_id}",
                            if state { "add" } else { "del" }
                        ),
                    )?;
                }
            };
        }
        _ => return Err(PluginError::new("invalid-request", "站点操作不受支持")),
    }
    recent_actions.push(json!({
        "idempotencyKey":request.idempotency_key,
        "action":request.action,
        "state":state
    }));
    if recent_actions.len() > 64 {
        recent_actions.drain(..recent_actions.len() - 64);
    }
    storage_set(
        &request.connection_id,
        "actions/recent",
        &Value::Array(recent_actions),
    )?;
    host_log("info", "site.interaction", "remote action completed", 1);
    Ok(json!({"accepted":true,"state":state}))
}

fn video_aid(connection_id: &str, item_id: &str) -> Result<u64, PluginError> {
    let bvid = validate_bvid(item_id)?;
    let payload = bili_get(
        connection_id,
        &format!("https://api.bilibili.com/x/web-interface/view?bvid={bvid}"),
        true,
    )?;
    payload
        .pointer("/data/aid")
        .and_then(Value::as_u64)
        .filter(|value| *value > 0)
        .ok_or_else(|| PluginError::new("invalid-response", "视频身份响应无效"))
}

fn favorite_folder_id(connection_id: &str) -> Result<u64, PluginError> {
    let account = account_summary(connection_id)?;
    let mid = account
        .get("id")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "账号身份无效"))?;
    let payload = bili_get_required(
        connection_id,
        &format!("https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid={mid}"),
    )?;
    payload
        .pointer("/data/list/0/id")
        .and_then(Value::as_u64)
        .filter(|value| *value > 0)
        .ok_or_else(|| PluginError::new("not-found", "账号暂无可写收藏夹"))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct FeedRequest {
    connection_id: String,
    route_key: String,
    cursor: Option<String>,
    refresh_session: Option<String>,
}

fn feed(request: FeedRequest) -> Result<Value, PluginError> {
    let page = parse_page_cursor(request.cursor.as_deref())?;
    if request
        .refresh_session
        .as_deref()
        .is_some_and(|value| !valid_opaque_key(value))
    {
        return Err(PluginError::new("invalid-request", "推荐刷新会话无效"));
    }
    if matches!(
        request.route_key.as_str(),
        "favorites" | "watch-later" | "following" | "subscriptions"
    ) {
        return personal_feed(&request, page);
    }
    let (url, title, layout) = match request.route_key.as_str() {
        "recommended" => (
            recommendation_url(page, request.refresh_session.as_deref()),
            "Bilibili 推荐",
            "hero",
        ),
        "ranking" => (
            "https://api.bilibili.com/x/web-interface/ranking/v2?rid=0&type=all".to_owned(),
            "全站排行",
            "video-list",
        ),
        "popular" => (
            format!("https://api.bilibili.com/x/web-interface/popular?ps=20&pn={page}"),
            "热门视频",
            "poster-grid",
        ),
        "anime" | "anime-jp" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=13"
            ),
            "番剧",
            "poster-grid",
        ),
        "anime-cn" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=168"
            ),
            "国产动画",
            "poster-grid",
        ),
        "anime-other" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=1"
            ),
            "动画专区",
            "poster-grid",
        ),
        "movies" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=23"
            ),
            "电影",
            "poster-grid",
        ),
        "tv-series" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=11"
            ),
            "电视剧",
            "poster-grid",
        ),
        "cinephile" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=181"
            ),
            "影视",
            "poster-grid",
        ),
        "documentary" => (
            format!(
                "https://api.bilibili.com/x/web-interface/dynamic/region?ps=20&pn={page}&rid=177"
            ),
            "纪录片",
            "poster-grid",
        ),
        _ => return Err(PluginError::new("invalid-request", "站点栏目无效")),
    };
    let payload = bili_get(&request.connection_id, &url, true)?;
    let list = if request.route_key == "recommended" {
        payload.pointer("/data/item")
    } else {
        payload.pointer("/data/list")
    }
    .and_then(Value::as_array)
    .ok_or_else(|| PluginError::new("invalid-response", "站点内容响应无效"))?;
    let items: Vec<Value> = list.iter().filter_map(feed_item).collect();
    host_log("info", "site.feed", "feed resolved", items.len());
    let refresh_session = request
        .refresh_session
        .unwrap_or_else(|| format!("{}-{page}", request.route_key));
    Ok(json!([{
        "id": request.route_key,
        "title": title,
        "layout": layout,
        "items": items,
        "cursor": (page + 1).to_string(),
        "refreshSession": refresh_session,
        "homeEligible": request.route_key == "recommended",
        "refreshable":true
    }]))
}

fn recommendation_url(page: u32, refresh_session: Option<&str>) -> String {
    let session_index = refresh_session
        .map(|value| {
            value.bytes().fold(0_u32, |state, byte| {
                state.wrapping_mul(33).wrapping_add(u32::from(byte))
            }) % 10_000
        })
        .unwrap_or(page);
    format!(
        "https://api.bilibili.com/x/web-interface/index/top/feed/rcmd?ps=20&fresh_type=3&fresh_idx={session_index}&fresh_idx_1h={session_index}&brush={page}"
    )
}

fn personal_feed(request: &FeedRequest, page: u32) -> Result<Value, PluginError> {
    let account = account_summary(&request.connection_id)?;
    let mid = account
        .get("id")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "账号身份无效"))?;
    let (title, layout, items, has_more) = match request.route_key.as_str() {
        "favorites" => {
            let folders = bili_get_required(
                &request.connection_id,
                &format!("https://api.bilibili.com/x/v3/fav/folder/created/list-all?up_mid={mid}"),
            )?;
            let folder_id = folders
                .pointer("/data/list/0/id")
                .and_then(Value::as_u64)
                .ok_or_else(|| PluginError::new("not-found", "账号暂无收藏夹"))?;
            let payload = bili_get_required(
                &request.connection_id,
                &format!("https://api.bilibili.com/x/v3/fav/resource/list?media_id={folder_id}&pn={page}&ps=20&platform=web&type=0"),
            )?;
            let list = payload
                .pointer("/data/medias")
                .and_then(Value::as_array)
                .ok_or_else(|| PluginError::new("invalid-response", "收藏响应无效"))?;
            let items = list
                .iter()
                .filter_map(personal_video_item)
                .collect::<Vec<_>>();
            let has_more = payload
                .pointer("/data/has_more")
                .and_then(Value::as_bool)
                .unwrap_or(items.len() == 20);
            ("我的收藏", "poster-grid", items, has_more)
        }
        "watch-later" => {
            let payload = bili_get_required(
                &request.connection_id,
                "https://api.bilibili.com/x/v2/history/toview/web",
            )?;
            let list = payload
                .pointer("/data/list")
                .and_then(Value::as_array)
                .ok_or_else(|| PluginError::new("invalid-response", "稍后再看响应无效"))?;
            (
                "稍后再看",
                "video-list",
                list.iter().filter_map(personal_video_item).collect(),
                false,
            )
        }
        "following" => {
            let payload = bili_get_required(
                &request.connection_id,
                &format!("https://api.bilibili.com/x/relation/followings?vmid={mid}&pn={page}&ps=20&order=desc"),
            )?;
            let list = payload
                .pointer("/data/list")
                .and_then(Value::as_array)
                .ok_or_else(|| PluginError::new("invalid-response", "关注响应无效"))?;
            let items = list.iter().filter_map(creator_item).collect::<Vec<_>>();
            ("我的关注", "video-list", items.clone(), items.len() == 20)
        }
        "subscriptions" => {
            let payload = bili_get_required(
                &request.connection_id,
                &format!("https://api.bilibili.com/x/space/bangumi/follow/list?type=1&pn={page}&ps=20&vmid={mid}"),
            )?;
            let list = payload
                .pointer("/data/list")
                .and_then(Value::as_array)
                .ok_or_else(|| PluginError::new("invalid-response", "追更响应无效"))?;
            let items = list
                .iter()
                .filter_map(subscription_item)
                .collect::<Vec<_>>();
            let total = payload
                .pointer("/data/total")
                .and_then(Value::as_u64)
                .unwrap_or(0);
            (
                "我的追更",
                "poster-grid",
                items,
                u64::from(page) * 20 < total,
            )
        }
        _ => return Err(PluginError::new("invalid-request", "个人栏目无效")),
    };
    let refresh_session = request
        .refresh_session
        .clone()
        .unwrap_or_else(|| format!("{}-{page}", request.route_key));
    Ok(json!([{
        "id":request.route_key,
        "title":title,
        "layout":layout,
        "items":items,
        "cursor":if has_more { Some((page + 1).to_string()) } else { None },
        "refreshSession":refresh_session,
        "homeEligible":false,
        "refreshable":true
    }]))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SearchRequest {
    connection_id: String,
    query: String,
    cursor: Option<String>,
}

fn search(request: SearchRequest) -> Result<Value, PluginError> {
    let query = request.query.trim();
    if query.is_empty() || query.chars().count() > 100 {
        return Err(PluginError::new("invalid-request", "搜索词无效"));
    }
    let page = parse_page_cursor(request.cursor.as_deref())?;
    let url = format!(
        "https://api.bilibili.com/x/web-interface/search/type?search_type=video&page={page}&keyword={}",
        urlencoding::encode(query)
    );
    let payload = bili_get(&request.connection_id, &url, true)?;
    let list = payload
        .pointer("/data/result")
        .and_then(Value::as_array)
        .ok_or_else(|| PluginError::new("invalid-response", "搜索响应无效"))?;
    let items: Vec<Value> = list.iter().filter_map(search_item).collect();
    Ok(json!([{
        "id":"search",
        "title":"搜索结果",
        "layout":"video-list",
        "items":items,
        "cursor":(page + 1).to_string()
    }]))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct DetailRequest {
    connection_id: String,
    item_id: String,
}

fn detail(request: DetailRequest) -> Result<Value, PluginError> {
    if let Some(mid) = request.item_id.strip_prefix("up:") {
        if mid.is_empty() || !mid.bytes().all(|byte| byte.is_ascii_digit()) {
            return Err(PluginError::new("invalid-request", "UP 主身份无效"));
        }
        let payload = bili_get(
            &request.connection_id,
            &format!("https://api.bilibili.com/x/web-interface/card?mid={mid}"),
            true,
        )?;
        return creator_detail(mid, &payload);
    }
    if let Some(season_id) = request.item_id.strip_prefix("season:") {
        if season_id.is_empty() || !season_id.bytes().all(|byte| byte.is_ascii_digit()) {
            return Err(PluginError::new("invalid-request", "追更内容身份无效"));
        }
        let payload = bili_get(
            &request.connection_id,
            &format!("https://api.bilibili.com/pgc/view/web/season?season_id={season_id}"),
            true,
        )?;
        return season_detail(season_id, &payload);
    }
    let bvid = validate_bvid(&request.item_id)?;
    let url = format!("https://api.bilibili.com/x/web-interface/view?bvid={bvid}");
    let payload = bili_get(&request.connection_id, &url, true)?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("not-found", "视频不存在或不可访问"))?;
    Ok(detail_work(data))
}

fn creator_detail(mid: &str, payload: &Value) -> Result<Value, PluginError> {
    let card = payload
        .pointer("/data/card")
        .ok_or_else(|| PluginError::new("not-found", "UP 主不存在或不可访问"))?;
    Ok(json!({
        "id":format!("up:{mid}"),
        "title":card.get("name").and_then(Value::as_str).unwrap_or("Bilibili UP 主"),
        "kind":"creator",
        "identity":{"scheme":"bilibili.mid","value":mid},
        "overview":card.get("sign").and_then(Value::as_str),
        "posterUrl":card.get("face").and_then(Value::as_str).and_then(safe_https_url),
        "author":card.get("name").and_then(Value::as_str),
        "segments":[]
    }))
}

fn season_detail(season_id: &str, payload: &Value) -> Result<Value, PluginError> {
    let result = payload
        .get("result")
        .or_else(|| payload.get("data"))
        .ok_or_else(|| PluginError::new("not-found", "追更内容不存在或不可访问"))?;
    let episodes = result
        .get("episodes")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let segments = episodes
        .iter()
        .enumerate()
        .filter_map(|(index, episode)| {
            let bvid = episode.get("bvid")?.as_str()?;
            validate_bvid(bvid).ok()?;
            let cid = episode.get("cid")?.as_u64()?;
            Some(json!({
                "id":format!("cid:{cid}"),
                "title":episode.get("long_title").or_else(|| episode.get("share_copy")).and_then(Value::as_str).unwrap_or("分集"),
                "index":index + 1,
                "episodeNumber":episode.get("title").and_then(Value::as_str).and_then(|value| value.parse::<u32>().ok()),
                "versions":[{"id":format!("bilibili:{bvid}:{cid}"),"label":"Bilibili","sourceLabel":"Bilibili","delivery":"online","variants":[]}]
            }))
        })
        .collect::<Vec<_>>();
    Ok(json!({
        "id":format!("season:{season_id}"),
        "title":result.get("title").and_then(Value::as_str).unwrap_or("Bilibili 追更内容"),
        "kind":"series",
        "identity":{"scheme":"bilibili.season","value":season_id},
        "overview":result.get("evaluate").and_then(Value::as_str),
        "posterUrl":result.get("cover").and_then(Value::as_str).and_then(safe_https_url),
        "segments":segments
    }))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct PlaybackRequest {
    connection_id: String,
    item_id: String,
    segment_id: String,
    version_id: String,
    variant_id: Option<String>,
}

fn playback(request: PlaybackRequest) -> Result<Value, PluginError> {
    let resolved = resolve_playback(&request)?;
    let danmaku = register_danmaku(&request.connection_id, &request.segment_id).ok();
    let subtitles = resolve_subtitles(
        &request.connection_id,
        &request.item_id,
        &request.segment_id,
        &request.version_id,
    )
    .unwrap_or_default();
    Ok(json!({
        "workId": request.item_id,
        "segmentId": request.segment_id,
        "versionId": request.version_id,
        "variantId": resolved.variant_id,
        "variants": resolved.variants,
        "assets": resolved.assets,
        "delivery":"server-gateway",
        "expiresAt":resolved.expires_at,
        "subtitles":subtitles,
        "danmaku":danmaku.map(|track| vec![track]).unwrap_or_default()
    }))
}

fn register_danmaku(connection_id: &str, segment_id: &str) -> Result<Value, PluginError> {
    let cid = segment_id.strip_prefix("cid:").unwrap_or(segment_id);
    if cid.is_empty() || !cid.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "弹幕分 P 身份无效"));
    }
    let xml = host_get_bytes(
        connection_id,
        &format!("https://api.bilibili.com/x/v1/dm/list.so?oid={cid}"),
        false,
        false,
    )?;
    let mut comments = parse_danmaku_xml(&xml)?;
    let body = loop {
        let encoded = serde_json::to_vec(&json!({"comments":&comments}))
            .map_err(|_| PluginError::new("invalid-response", "弹幕编码失败"))?;
        if encoded.len() <= 2_500_000 {
            break encoded;
        }
        if comments.len() <= 1 {
            return Err(PluginError::new("response-too-large", "弹幕响应过大"));
        }
        comments.truncate(comments.len() * 3 / 4);
    };
    let asset = host_json(
        HOST_ASSET_REGISTER,
        &json!({
            "connectionId":connection_id,
            "bodyBase64":BASE64.encode(body),
            "contentType":"application/json",
            "ttlSeconds":300
        }),
    )?;
    let reference = asset
        .get("ref")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "弹幕资产注册失败"))?;
    Ok(json!({
        "id":format!("danmaku:{cid}"),
        "label":"Bilibili 弹幕",
        "language":"zh-CN",
        "format":"ohmycine-danmaku-v1+json",
        "urlRef":reference
    }))
}

fn resolve_subtitles(
    connection_id: &str,
    item_id: &str,
    segment_id: &str,
    version_id: &str,
) -> Result<Vec<Value>, PluginError> {
    let cid = segment_id.strip_prefix("cid:").unwrap_or(segment_id);
    if cid.is_empty() || !cid.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "字幕分 P 身份无效"));
    }
    let bvid = resolve_version_bvid(item_id, version_id, cid)?;
    let payload = bili_get_required(
        connection_id,
        &format!("https://api.bilibili.com/x/player/v2?bvid={bvid}&cid={cid}"),
    )?;
    let tracks = payload
        .pointer("/data/subtitle/subtitles")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let mut result = Vec::new();
    for track in tracks.into_iter().take(16) {
        let Some(url) = track
            .get("subtitle_url")
            .and_then(Value::as_str)
            .and_then(safe_https_url)
        else {
            continue;
        };
        // CDN requests intentionally omit the account cookie. Authentication
        // is only sent to api.bilibili.com when obtaining the authorized URL.
        let body = host_get_bytes(connection_id, &url, false, false)?;
        let vtt = subtitle_json_to_vtt(&body)?;
        let registered = host_json(
            HOST_ASSET_REGISTER,
            &json!({
                "connectionId":connection_id,
                "bodyBase64":BASE64.encode(vtt.as_bytes()),
                "contentType":"text/vtt; charset=utf-8",
                "ttlSeconds":300
            }),
        )?;
        let reference = registered
            .get("ref")
            .and_then(Value::as_str)
            .ok_or_else(|| PluginError::new("invalid-response", "字幕资产注册失败"))?;
        let language = track.get("lan").and_then(Value::as_str).unwrap_or("und");
        let label = track
            .get("lan_doc")
            .and_then(Value::as_str)
            .unwrap_or(language);
        result.push(json!({
            "id":format!("subtitle:{cid}:{}", result.len() + 1),
            "label":label,
            "language":language,
            "format":"vtt",
            "urlRef":reference
        }));
    }
    Ok(result)
}

fn subtitle_json_to_vtt(body: &[u8]) -> Result<String, PluginError> {
    let payload: Value = serde_json::from_slice(body)
        .map_err(|_| PluginError::new("invalid-response", "字幕响应格式无效"))?;
    let entries = payload
        .get("body")
        .and_then(Value::as_array)
        .ok_or_else(|| PluginError::new("invalid-response", "字幕正文无效"))?;
    let mut output = String::from("WEBVTT\n\n");
    for (index, entry) in entries.iter().take(20_000).enumerate() {
        let Some(start) = entry.get("from").and_then(Value::as_f64) else {
            continue;
        };
        let Some(end) = entry.get("to").and_then(Value::as_f64) else {
            continue;
        };
        let Some(text) = entry.get("content").and_then(Value::as_str) else {
            continue;
        };
        if !start.is_finite()
            || !end.is_finite()
            || start < 0.0
            || end <= start
            || end > 7.0 * 24.0 * 60.0 * 60.0
            || text.chars().count() > 2_000
        {
            continue;
        }
        output.push_str(&(index + 1).to_string());
        output.push('\n');
        output.push_str(&format_vtt_time(start));
        output.push_str(" --> ");
        output.push_str(&format_vtt_time(end));
        output.push('\n');
        output.push_str(&text.replace("-->", "→").replace('\0', ""));
        output.push_str("\n\n");
        if output.len() > 3_500_000 {
            break;
        }
    }
    Ok(output)
}

fn format_vtt_time(seconds: f64) -> String {
    let millis = (seconds.max(0.0) * 1_000.0).round() as u64;
    let hours = millis / 3_600_000;
    let minutes = (millis / 60_000) % 60;
    let secs = (millis / 1_000) % 60;
    let remainder = millis % 1_000;
    format!("{hours:02}:{minutes:02}:{secs:02}.{remainder:03}")
}

fn parse_danmaku_xml(xml: &[u8]) -> Result<Vec<Value>, PluginError> {
    let source = std::str::from_utf8(xml)
        .map_err(|_| PluginError::new("invalid-response", "弹幕响应编码无效"))?;
    let document = roxmltree::Document::parse(source)
        .map_err(|_| PluginError::new("invalid-response", "弹幕响应格式无效"))?;
    let mut comments = Vec::new();
    for node in document.descendants().filter(|node| node.has_tag_name("d")) {
        if comments.len() >= 50_000 {
            break;
        }
        let properties = match node.attribute("p") {
            Some(value) => value.split(',').collect::<Vec<_>>(),
            None => continue,
        };
        if properties.len() < 4 {
            continue;
        }
        let time = match properties[0].parse::<f64>() {
            Ok(value) if value.is_finite() && value >= 0.0 => value,
            _ => continue,
        };
        let mode = match properties[1] {
            "4" => "bottom",
            "5" => "top",
            _ => "scroll",
        };
        let color = properties[3]
            .parse::<u32>()
            .map(|value| format!("#{:06x}", value & 0x00ff_ffff))
            .unwrap_or_else(|_| "#ffffff".to_owned());
        let text = node.text().unwrap_or("").trim();
        if text.is_empty() || text.chars().count() > 500 {
            continue;
        }
        let id = properties.get(7).copied().unwrap_or("");
        comments.push(json!({
            "id":if id.is_empty() { format!("dm:{}", comments.len() + 1) } else { format!("dm:{id}") },
            "time":time,
            "mode":mode,
            "color":color,
            "text":text
        }));
    }
    Ok(comments)
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct DownloadRequest {
    connection_id: String,
    item_id: String,
    segment_id: String,
    version_id: String,
    variant_id: Option<String>,
}

fn download_plan(request: DownloadRequest) -> Result<Value, PluginError> {
    let config = connection_config(&request.connection_id);
    let configured_variant = download_quality_variant(&config, request.variant_id.clone());
    let resolved = resolve_playback(&PlaybackRequest {
        connection_id: request.connection_id.clone(),
        item_id: request.item_id.clone(),
        segment_id: request.segment_id.clone(),
        version_id: request.version_id.clone(),
        variant_id: configured_variant,
    })?;
    let mut assets = resolved
        .assets
        .iter()
        .filter_map(|asset| {
            let kind = asset.get("kind")?.as_str()?;
            let url_ref = asset.get("urlRef")?.as_str()?;
            let download_kind = match kind {
                "dash-video" | "progressive" => "video",
                "dash-audio" => "audio",
                _ => return None,
            };
            Some(json!({
                "id":if download_kind == "video" { "video" } else { "audio" },
                "kind":download_kind,
                "urlRef":url_ref,
                "expectedContentType":if download_kind == "video" { "video/mp4" } else { "audio/mp4" }
            }))
        })
        .collect::<Vec<_>>();
    let subtitles = if config
        .get("downloadSubtitles")
        .and_then(Value::as_bool)
        .unwrap_or(true)
    {
        resolve_subtitles(
            &request.connection_id,
            &request.item_id,
            &request.segment_id,
            &request.version_id,
        )
        .unwrap_or_default()
    } else {
        Vec::new()
    };
    // Server v1 accepts at most eight assets. Reserve video, optional audio,
    // and danmaku slots, then keep the first five authorized subtitle tracks.
    for (index, track) in subtitles.iter().take(5).enumerate() {
        assets.push(json!({
            "id":format!("subtitle-{index}"),
            "kind":"subtitle",
            "urlRef":track.get("urlRef").and_then(Value::as_str).unwrap_or(""),
            "expectedContentType":"text/vtt"
        }));
    }
    if config
        .get("downloadDanmaku")
        .and_then(Value::as_bool)
        .unwrap_or(true)
    {
        if let Ok(track) = register_danmaku(&request.connection_id, &request.segment_id) {
            if let Some(reference) = track.get("urlRef").and_then(Value::as_str) {
                assets.push(json!({
                    "id":"danmaku",
                    "kind":"danmaku",
                    "urlRef":reference,
                    "expectedContentType":"application/json"
                }));
            }
        }
    }
    let merge = if resolved
        .assets
        .iter()
        .any(|asset| asset.get("kind") == Some(&json!("dash-audio")))
    {
        Some(json!({"kind":"dash-av","videoAssetId":"video","audioAssetId":"audio"}))
    } else {
        None
    };
    let display_title = resolve_download_title(
        &request.connection_id,
        &request.item_id,
        &request.segment_id,
        &request.version_id,
    )
    .unwrap_or_else(|_| request.item_id.clone());
    Ok(json!({
        "workId":request.item_id,
        "segmentId":request.segment_id,
        "versionId":request.version_id,
        "variantId":resolved.variant_id,
        "suggestedFileName":suggested_file_name(&display_title, &request.item_id, &resolved.variant_id),
        "assets":assets,
        "merge":merge
    }))
}

fn download_quality_variant(config: &Value, requested: Option<String>) -> Option<String> {
    if requested.is_some() {
        return requested;
    }
    match config
        .get("defaultQuality")
        .and_then(Value::as_str)
        .unwrap_or("auto")
    {
        "highest" => Some("qn:127".to_owned()),
        "1080p" => Some("qn:80".to_owned()),
        "720p" => Some("qn:64".to_owned()),
        _ => None,
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct MetadataRequest {
    connection_id: String,
    item_id: String,
    segment_id: String,
    version_id: String,
}

fn metadata(request: MetadataRequest) -> Result<Value, PluginError> {
    let cid = request
        .segment_id
        .strip_prefix("cid:")
        .unwrap_or(&request.segment_id);
    if cid.is_empty() || !cid.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "元数据分 P 身份无效"));
    }
    let bvid = resolve_version_bvid(&request.item_id, &request.version_id, cid)?;
    let payload = bili_get(
        &request.connection_id,
        &format!("https://api.bilibili.com/x/web-interface/view?bvid={bvid}"),
        true,
    )?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("not-found", "视频元数据不存在"))?;
    let work_title = data
        .get("title")
        .and_then(Value::as_str)
        .unwrap_or("Bilibili 视频");
    let segment_title = data
        .get("pages")
        .and_then(Value::as_array)
        .and_then(|pages| {
            pages.iter().find(|page| {
                page.get("cid")
                    .and_then(Value::as_u64)
                    .map(|value| value.to_string() == cid)
                    .unwrap_or(false)
            })
        })
        .and_then(|page| page.get("part"))
        .and_then(Value::as_str);
    let title = match segment_title {
        Some(part)
            if part != work_title
                && data
                    .get("pages")
                    .and_then(Value::as_array)
                    .is_some_and(|pages| pages.len() > 1) =>
        {
            format!("{work_title} - {part}")
        }
        _ => work_title.to_owned(),
    };
    let published_at = data
        .get("pubdate")
        .and_then(Value::as_i64)
        .and_then(|timestamp| OffsetDateTime::from_unix_timestamp(timestamp).ok())
        .and_then(|value| value.format(&Rfc3339).ok());
    let mut artwork = Vec::new();
    if let Some(url) = data
        .get("pic")
        .and_then(Value::as_str)
        .and_then(safe_https_url)
    {
        if let Ok((reference, _)) = register_asset(&request.connection_id, &url, json!({})) {
            artwork.push(json!({"kind":"poster","assetRef":reference}));
        }
    }
    let overview = data
        .get("desc")
        .and_then(Value::as_str)
        .map(normalize_metadata_text)
        .filter(|value| !value.is_empty());
    Ok(json!({
        "version":1,
        "workId":request.item_id,
        "segmentId":request.segment_id,
        "kind":"video",
        "title":title,
        "overview":overview,
        "author":data.pointer("/owner/name").and_then(Value::as_str),
        "publishedAt":published_at,
        "durationSeconds":data.get("duration").and_then(Value::as_u64),
        "uniqueIds":{"bilibili.bvid":bvid,"bilibili.cid":cid},
        "artwork":artwork
    }))
}

fn normalize_metadata_text(value: &str) -> String {
    value
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
        .chars()
        .take(16_000)
        .collect()
}

fn connection_config(connection_id: &str) -> Value {
    host_json(HOST_CONFIG_GET, &json!({"connectionId":connection_id})).unwrap_or_else(|_| json!({}))
}

fn resolve_download_title(
    connection_id: &str,
    item_id: &str,
    segment_id: &str,
    version_id: &str,
) -> Result<String, PluginError> {
    let cid = segment_id.strip_prefix("cid:").unwrap_or(segment_id);
    let bvid = resolve_version_bvid(item_id, version_id, cid)?;
    let payload = bili_get(
        connection_id,
        &format!("https://api.bilibili.com/x/web-interface/view?bvid={bvid}"),
        true,
    )?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("invalid-response", "视频详情响应无效"))?;
    let title = data.get("title").and_then(Value::as_str).unwrap_or(bvid);
    let part = data
        .get("pages")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .find(|page| {
            page.get("cid")
                .and_then(Value::as_u64)
                .is_some_and(|value| value.to_string() == cid)
        })
        .and_then(|page| page.get("part"))
        .and_then(Value::as_str);
    Ok(match part {
        Some(part) if part != title => format!("{title} - {part}"),
        _ => title.to_owned(),
    })
}

fn suggested_file_name(title: &str, item_id: &str, variant_id: &str) -> String {
    let mut safe_title = String::new();
    for character in title.chars() {
        if character.is_control()
            || matches!(
                character,
                '\0' | '\r' | '\n' | '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|'
            )
        {
            continue;
        }
        if safe_title.len() + character.len_utf8() > 140 {
            break;
        }
        safe_title.push(character);
    }
    let safe_item = item_id
        .chars()
        .filter(|character| character.is_ascii_alphanumeric() || matches!(character, '-' | '_'))
        .take(32)
        .collect::<String>();
    let quality = variant_id.strip_prefix("qn:").unwrap_or("auto");
    let safe_title = safe_title.trim().trim_end_matches(['.', ' ']);
    let title = if safe_title.is_empty() {
        "Bilibili 视频"
    } else {
        safe_title
    };
    format!("{title} [Bilibili {safe_item} Q{quality}].mp4")
}

#[derive(Debug)]
struct ResolvedPlayback {
    variant_id: String,
    variants: Vec<Value>,
    assets: Vec<Value>,
    expires_at: String,
}

fn resolve_playback(request: &PlaybackRequest) -> Result<ResolvedPlayback, PluginError> {
    host_log(
        "debug",
        "media.playback.identity",
        "playback identity validation started",
        1,
    );
    let cid = request
        .segment_id
        .strip_prefix("cid:")
        .unwrap_or(&request.segment_id);
    if cid.is_empty() || !cid.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "分 P 身份无效"));
    }
    let bvid = playback_stage(
        "identity",
        resolve_version_bvid(&request.item_id, &request.version_id, cid),
    )?;
    if validate_bvid(&request.item_id).is_ok()
        && request.item_id != bvid
        && !playback_stage(
            "identity_collection",
            collection_contains_version(&request.connection_id, &request.item_id, bvid, cid),
        )?
    {
        return Err(PluginError::new("invalid-request", "合集媒体版本身份无效"));
    }
    let qn = request
        .variant_id
        .as_deref()
        .and_then(|value| value.strip_prefix("qn:"))
        .unwrap_or("127");
    if !qn.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "清晰度身份无效"));
    }
    let endpoint = if request.item_id.starts_with("season:") {
        "https://api.bilibili.com/pgc/player/web/playurl"
    } else {
        "https://api.bilibili.com/x/player/playurl"
    };
    let url = format!("{endpoint}?bvid={bvid}&cid={cid}&qn={qn}&fnver=0&fnval=4048&fourk=1");
    let payload = playback_stage("playurl", bili_get(&request.connection_id, &url, true))?;
    let data = payload
        .get("data")
        .or_else(|| payload.get("result"))
        .ok_or_else(|| PluginError::new("invalid-response", "播放响应无效"));
    let data = playback_stage("playurl_shape", data)?;
    let source = playback_stage("parse_dash", parse_playback_data(data, qn))?;
    let headers = json!({"Referer":"https://www.bilibili.com/","User-Agent":USER_AGENT});
    let (video_ref, expires_at) = playback_stage(
        "register_video",
        register_asset(&request.connection_id, &source.video_url, headers.clone()),
    )?;
    let mut assets = vec![json!({
        "kind":if source.audio_url.is_some() { "dash-video" } else { "progressive" },
        "urlRef":video_ref
    })];
    if let Some(audio_url) = source.audio_url {
        let (audio_ref, _) = playback_stage(
            "register_audio",
            register_asset(&request.connection_id, &audio_url, headers),
        )?;
        assets.push(json!({"kind":"dash-audio","urlRef":audio_ref}));
    }
    host_log(
        "info",
        "media.playback.ready",
        "playback plan resolved",
        assets.len(),
    );
    Ok(ResolvedPlayback {
        variant_id: source.variant_id,
        variants: source.variants,
        assets,
        expires_at,
    })
}

fn playback_stage<T>(stage: &str, result: Result<T, PluginError>) -> Result<T, PluginError> {
    match result {
        Ok(value) => {
            host_log(
                "debug",
                &format!("media.playback.{stage}"),
                "playback stage completed",
                1,
            );
            Ok(value)
        }
        Err(error) => {
            host_log(
                "warn",
                &format!("media.playback.{stage}"),
                "playback stage failed",
                0,
            );
            Err(error)
        }
    }
}

fn resolve_version_bvid<'a>(
    item_id: &'a str,
    version_id: &'a str,
    cid: &str,
) -> Result<&'a str, PluginError> {
    if validate_bvid(item_id).is_err() && !item_id.starts_with("season:") {
        return Err(PluginError::new("invalid-request", "媒体作品身份无效"));
    }
    let mut parts = version_id.split(':');
    if parts.next() != Some("bilibili") {
        return Err(PluginError::new("invalid-request", "媒体版本身份无效"));
    }
    let bvid = parts
        .next()
        .ok_or_else(|| PluginError::new("invalid-request", "媒体版本身份无效"))?;
    let version_cid = parts
        .next()
        .ok_or_else(|| PluginError::new("invalid-request", "媒体版本身份无效"))?;
    if parts.next().is_some() || version_cid != cid {
        return Err(PluginError::new("invalid-request", "媒体版本身份无效"));
    }
    validate_bvid(bvid)
}

fn collection_contains_version(
    connection_id: &str,
    item_bvid: &str,
    version_bvid: &str,
    cid: &str,
) -> Result<bool, PluginError> {
    let payload = bili_get(
        connection_id,
        &format!("https://api.bilibili.com/x/web-interface/view?bvid={item_bvid}"),
        true,
    )?;
    Ok(payload
        .pointer("/data/ugc_season/sections")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .flat_map(|section| {
            section
                .get("episodes")
                .and_then(Value::as_array)
                .into_iter()
                .flatten()
        })
        .any(|episode| {
            episode.get("bvid").and_then(Value::as_str) == Some(version_bvid)
                && episode
                    .get("cid")
                    .and_then(Value::as_u64)
                    .is_some_and(|value| value.to_string() == cid)
        }))
}

#[derive(Debug)]
struct PlaybackSource {
    variant_id: String,
    variants: Vec<Value>,
    video_url: String,
    audio_url: Option<String>,
}

fn parse_playback_data(
    data: &Value,
    requested_quality: &str,
) -> Result<PlaybackSource, PluginError> {
    if let Some(video_tracks) = data.pointer("/dash/video").and_then(Value::as_array) {
        let mut audio_tracks = data
            .pointer("/dash/audio")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
            .collect::<Vec<_>>();
        if let Some(flac) = data
            .pointer("/dash/flac/audio")
            .filter(|value| value.is_object())
        {
            audio_tracks.push(flac);
        }
        audio_tracks.extend(
            data.pointer("/dash/dolby/audio")
                .and_then(Value::as_array)
                .into_iter()
                .flatten(),
        );
        if audio_tracks.is_empty() {
            host_log(
                "warn",
                "media.playback.parse_dash_audio_tracks",
                "DASH audio tracks are unavailable",
                0,
            );
            return Err(PluginError::new(
                "playback-audio-unavailable",
                "DASH 音轨缺失",
            ));
        }
        let selected_quality = requested_quality
            .parse::<i64>()
            .ok()
            .filter(|quality| {
                video_tracks
                    .iter()
                    .any(|track| track.get("id").and_then(Value::as_i64) == Some(*quality))
            })
            .or_else(|| data.get("quality").and_then(Value::as_i64))
            .ok_or_else(|| PluginError::new("quality-unavailable", "DASH 清晰度无效"))?;
        let video = video_tracks
            .iter()
            .filter(|track| track.get("id").and_then(Value::as_i64) == Some(selected_quality))
            .max_by_key(|track| playback_track_rank(track, TrackKind::Video))
            .ok_or_else(|| PluginError::new("not-found", "请求的清晰度不可用"))?;
        let audio = audio_tracks
            .into_iter()
            .max_by_key(|track| playback_track_rank(track, TrackKind::Audio))
            .ok_or_else(|| PluginError::new("invalid-response", "DASH 音轨缺失"))?;
        let video_url = playback_stage("parse_dash_video_url", dash_url(video))?;
        let audio_url = playback_stage("parse_dash_audio_url", dash_url(audio))?;
        let descriptions = quality_descriptions(data);
        let audio_codec = audio.get("codecs").and_then(Value::as_str).unwrap_or("");
        let mut qualities = video_tracks
            .iter()
            .filter_map(|track| track.get("id").and_then(Value::as_i64))
            .collect::<Vec<_>>();
        qualities.sort_unstable_by(|left, right| right.cmp(left));
        qualities.dedup();
        let variants = qualities
            .into_iter()
            .filter_map(|quality| {
                let track = video_tracks
                    .iter()
                    .filter(|track| track.get("id").and_then(Value::as_i64) == Some(quality))
                    .max_by_key(|track| playback_track_rank(track, TrackKind::Video))?;
                let mut variant = json!({
                    "id":format!("qn:{quality}"),
                    "label":descriptions.get(&quality).cloned().unwrap_or_else(|| format!("清晰度 {quality}")),
                    "available":true,
                    "audioCodec":audio_codec,
                    "container":"mp4"
                });
                for field in ["width", "height", "bandwidth"] {
                    if let Some(value) = track.get(field).and_then(Value::as_u64) {
                        let target = if field == "bandwidth" { "bitrate" } else { field };
                        variant[target] = Value::from(value);
                    }
                }
                if let Some(codec) = track.get("codecs").and_then(Value::as_str) {
                    variant["videoCodec"] = Value::String(codec.to_owned());
                }
                if let Some(rate) = track.get("frame_rate").and_then(Value::as_str).and_then(parse_frame_rate) {
                    variant["frameRate"] = Value::from(rate);
                }
                match quality {
                    125 => {
                        variant["dynamicRange"] = Value::String("HDR".to_owned());
                        variant["hdr"] = Value::Bool(true);
                    }
                    126 => {
                        variant["dynamicRange"] = Value::String("Dolby Vision".to_owned());
                        variant["hdr"] = Value::Bool(true);
                        variant["dolbyVision"] = Value::Bool(true);
                    }
                    _ => {}
                }
                Some(variant)
            })
            .collect();
        return Ok(PlaybackSource {
            variant_id: format!("qn:{selected_quality}"),
            variants,
            video_url,
            audio_url: Some(audio_url),
        });
    }
    let (variant_id, variants, video_url) = parse_progressive_data(data, requested_quality)?;
    Ok(PlaybackSource {
        variant_id,
        variants,
        video_url,
        audio_url: None,
    })
}

#[derive(Clone, Copy)]
enum TrackKind {
    Video,
    Audio,
}

fn playback_track_rank(track: &Value, kind: TrackKind) -> (u8, u64) {
    let codec = track
        .get("codecs")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_ascii_lowercase();
    let compatibility = match kind {
        TrackKind::Video if codec.starts_with("avc1") || codec.starts_with("avc3") => 3,
        TrackKind::Video if codec.starts_with("hev1") || codec.starts_with("hvc1") => 2,
        TrackKind::Video if codec.starts_with("av01") => 1,
        TrackKind::Audio if codec.starts_with("mp4a") => 3,
        TrackKind::Audio if codec.starts_with("ec-3") || codec.starts_with("ac-3") => 2,
        TrackKind::Audio => 1,
        TrackKind::Video => 0,
    };
    (
        compatibility,
        track.get("bandwidth").and_then(Value::as_u64).unwrap_or(0),
    )
}

fn parse_frame_rate(value: &str) -> Option<f64> {
    if let Some((numerator, denominator)) = value.split_once('/') {
        let numerator = numerator.parse::<f64>().ok()?;
        let denominator = denominator.parse::<f64>().ok()?;
        let result = numerator / denominator;
        return (result.is_finite() && result > 0.0 && result <= 240.0).then_some(result);
    }
    let result = value.parse::<f64>().ok()?;
    (result.is_finite() && result > 0.0 && result <= 240.0).then_some(result)
}

fn dash_url(track: &Value) -> Result<String, PluginError> {
    let mut candidates = Vec::new();
    for key in ["baseUrl", "base_url"] {
        if let Some(value) = track.get(key).and_then(Value::as_str) {
            candidates.push(value);
        }
    }
    for key in ["backupUrl", "backup_url"] {
        candidates.extend(
            track
                .get(key)
                .and_then(Value::as_array)
                .into_iter()
                .flatten()
                .filter_map(Value::as_str),
        );
    }
    candidates
        .into_iter()
        .filter_map(safe_https_url)
        .max_by_key(|url| dash_url_rank(url))
        .ok_or_else(|| PluginError::new("asset-domain-denied", "DASH 资源地址无效"))
}

fn dash_url_rank(value: &str) -> (u8, u8) {
    let authority = value
        .strip_prefix("https://")
        .and_then(|remainder| remainder.split('/').next())
        .unwrap_or_default()
        .to_ascii_lowercase();
    let standard_port = !authority.contains(':') || authority.ends_with(":443");
    let hostname = authority.split(':').next().unwrap_or_default();
    let provider_rank = if hostname == "bilivideo.com" || hostname.ends_with(".bilivideo.com") {
        2
    } else if hostname == "bilivideo.cn" || hostname.ends_with(".bilivideo.cn") {
        1
    } else {
        0
    };
    (u8::from(standard_port), provider_rank)
}

fn quality_descriptions(data: &Value) -> std::collections::HashMap<i64, String> {
    let qualities = data.get("accept_quality").and_then(Value::as_array);
    let descriptions = data.get("accept_description").and_then(Value::as_array);
    qualities
        .into_iter()
        .flatten()
        .enumerate()
        .filter_map(|(index, quality)| {
            Some((
                quality.as_i64()?,
                descriptions?.get(index)?.as_str()?.to_owned(),
            ))
        })
        .collect()
}

fn parse_progressive_data(
    data: &Value,
    requested_quality: &str,
) -> Result<(String, Vec<Value>, String), PluginError> {
    let qualities = data
        .get("accept_quality")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let descriptions = data
        .get("accept_description")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let variants = qualities
        .iter()
        .enumerate()
        .filter_map(|(index, quality)| {
            let quality = quality.as_i64()?;
            let label = descriptions
                .get(index)
                .and_then(Value::as_str)
                .unwrap_or("可用清晰度");
            let availability = data
                .get("support_formats")
                .and_then(Value::as_array)
                .and_then(|formats| {
                    formats.iter().find(|format| {
                        format.get("quality").and_then(Value::as_i64) == Some(quality)
                    })
                })
                .map(|format| {
                    !format
                        .get("need_login")
                        .and_then(Value::as_bool)
                        .unwrap_or(false)
                        && !format
                            .get("need_vip")
                            .and_then(Value::as_bool)
                            .unwrap_or(false)
                })
                .unwrap_or(true);
            let mut variant = json!({
                "id":format!("qn:{quality}"),
                "label":label,
                "available":availability
            });
            if !availability {
                variant["unavailableReason"] = Value::String("当前账号不可用".to_owned());
            }
            Some(variant)
        })
        .collect::<Vec<_>>();
    let selected = data
        .get("quality")
        .and_then(Value::as_i64)
        .map(|quality| format!("qn:{quality}"))
        .unwrap_or_else(|| format!("qn:{requested_quality}"));
    let durl = data
        .get("durl")
        .and_then(Value::as_array)
        .and_then(|items| items.first())
        .and_then(|item| item.get("url"))
        .and_then(Value::as_str)
        .ok_or_else(|| {
            PluginError::new(
                "upstream-unavailable",
                "该清晰度仅提供 DASH，当前未返回不完整视频流",
            )
        })?
        .to_owned();
    Ok((selected, variants, durl))
}

fn register_asset(
    connection_id: &str,
    url: &str,
    headers: Value,
) -> Result<(String, String), PluginError> {
    let registered = host_json(
        HOST_ASSET_REGISTER,
        &json!({"connectionId":connection_id,"url":url,"headers":headers,"ttlSeconds":300}),
    )?;
    let reference = registered
        .get("ref")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "播放资产注册失败"))?
        .to_owned();
    let expires_at = registered
        .get("expiresAt")
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_owned();
    Ok((reference, expires_at))
}

fn bili_get(connection_id: &str, url: &str, authenticated: bool) -> Result<Value, PluginError> {
    let decoded = host_get_bytes(connection_id, url, authenticated, true)?;
    parse_bili_json(&decoded)
}

fn bili_get_required(connection_id: &str, url: &str) -> Result<Value, PluginError> {
    let decoded = host_get_bytes(connection_id, url, true, false).map_err(|error| {
        if error.code == "permission-denied" {
            PluginError::new("not-authenticated", "Bilibili 登录已失效，请重新扫码")
        } else {
            error
        }
    })?;
    parse_bili_json(&decoded)
}

fn parse_bili_json(decoded: &[u8]) -> Result<Value, PluginError> {
    let payload: Value = serde_json::from_slice(decoded)
        .map_err(|_| PluginError::new("invalid-response", "站点响应格式无效"))?;
    let code = payload.get("code").and_then(Value::as_i64).unwrap_or(-1);
    if code == -101 {
        return Err(PluginError::new(
            "not-authenticated",
            "Bilibili 登录已失效，请重新扫码",
        ));
    }
    if code == -404 {
        return Err(PluginError::new("not-found", "站点内容不存在或不可访问"));
    }
    if matches!(code, -412 | -509) {
        return Err(PluginError::new("rate-limited", "站点请求受到限流"));
    }
    if code == -10403 {
        return Err(PluginError::new(
            "access-restricted",
            "当前账号或地区无权播放该内容",
        ));
    }
    if code != 0 {
        return Err(PluginError::new(
            "upstream-unavailable",
            "站点拒绝了当前请求",
        ));
    }
    Ok(payload)
}

fn decode_http_json(response: &Value, error_message: &'static str) -> Result<Value, PluginError> {
    let status = response.get("status").and_then(Value::as_u64).unwrap_or(0);
    if !(200..300).contains(&status) {
        return Err(PluginError::new("upstream-unavailable", error_message));
    }
    let body = response
        .get("bodyBase64")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "站点响应正文缺失"))?;
    let decoded = BASE64
        .decode(body)
        .map_err(|_| PluginError::new("invalid-response", "站点响应正文无效"))?;
    serde_json::from_slice(&decoded)
        .map_err(|_| PluginError::new("invalid-response", "站点响应格式无效"))
}

fn host_get_bytes(
    connection_id: &str,
    url: &str,
    authenticated: bool,
    allow_anonymous_fallback: bool,
) -> Result<Vec<u8>, PluginError> {
    let mut request = json!({
        "connectionId":connection_id,
        "method":"GET",
        "url":url,
        "headers":{"Accept":"application/json","Referer":"https://www.bilibili.com/","User-Agent":USER_AGENT},
        "timeoutMs":12000
    });
    if authenticated {
        request["credentialRef"] = Value::String(SESSION_SCOPE.to_owned());
    }
    let response = match host_json(HOST_HTTP, &request) {
        Ok(response) => response,
        Err(error)
            if authenticated && allow_anonymous_fallback && error.code == "permission-denied" =>
        {
            request.as_object_mut().unwrap().remove("credentialRef");
            host_json(HOST_HTTP, &request)?
        }
        Err(error) => return Err(error),
    };
    let status = response.get("status").and_then(Value::as_u64).unwrap_or(0);
    if !(200..300).contains(&status) {
        return Err(PluginError::new("upstream-unavailable", "站点请求失败"));
    }
    let body = response
        .get("bodyBase64")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "站点响应正文缺失"))?;
    let decoded = BASE64
        .decode(body)
        .map_err(|_| PluginError::new("invalid-response", "站点响应正文无效"))?;
    Ok(decoded)
}

fn bili_post_form(connection_id: &str, url: &str, body: &str) -> Result<Value, PluginError> {
    let response = host_json(
        HOST_HTTP,
        &json!({
            "connectionId":connection_id,
            "method":"POST",
            "url":url,
            "headers":{
                "Accept":"application/json",
                "Content-Type":"application/x-www-form-urlencoded",
                "Referer":"https://www.bilibili.com/",
                "User-Agent":USER_AGENT
            },
            "credentialRef":SESSION_SCOPE,
            "credentialBindings":[
                {"target":"form","name":"csrf","source":"cookie","key":"bili_jct"},
                {"target":"form","name":"csrf_token","source":"cookie","key":"bili_jct"}
            ],
            "bodyBase64":BASE64.encode(body.as_bytes()),
            "timeoutMs":12000
        }),
    )?;
    let status = response.get("status").and_then(Value::as_u64).unwrap_or(0);
    if !(200..300).contains(&status) {
        return Err(PluginError::new("upstream-unavailable", "站点写操作失败"));
    }
    let body = response
        .get("bodyBase64")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "站点写操作响应缺失"))?;
    let decoded = BASE64
        .decode(body)
        .map_err(|_| PluginError::new("invalid-response", "站点写操作响应无效"))?;
    let payload: Value = serde_json::from_slice(&decoded)
        .map_err(|_| PluginError::new("invalid-response", "站点写操作响应格式无效"))?;
    if payload.get("code").and_then(Value::as_i64).unwrap_or(-1) != 0 {
        return Err(PluginError::new("upstream-unavailable", "站点写操作被拒绝"));
    }
    Ok(payload)
}

fn storage_get(connection_id: &str, key: &str) -> Result<Option<Value>, PluginError> {
    let response = host_json(
        HOST_STORAGE_GET,
        &json!({"connectionId":connection_id,"key":key}),
    )?;
    if !response
        .get("found")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        return Ok(None);
    }
    let value = response
        .get("valueBase64")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "插件状态响应无效"))?;
    let decoded = BASE64
        .decode(value)
        .map_err(|_| PluginError::new("invalid-response", "插件状态响应无效"))?;
    serde_json::from_slice(&decoded)
        .map(Some)
        .map_err(|_| PluginError::new("invalid-response", "插件状态响应无效"))
}

fn storage_set(connection_id: &str, key: &str, value: &Value) -> Result<(), PluginError> {
    let encoded =
        serde_json::to_vec(value).map_err(|_| PluginError::new("internal", "插件状态编码失败"))?;
    host_json(
        HOST_STORAGE_SET,
        &json!({"connectionId":connection_id,"key":key,"valueBase64":BASE64.encode(encoded)}),
    )?;
    Ok(())
}

fn feed_item(item: &Value) -> Option<Value> {
    let bvid = item.get("bvid")?.as_str()?;
    if validate_bvid(bvid).is_err() {
        return None;
    }
    Some(json!({"work":work_summary(
        bvid,
        item.get("title")?.as_str()?,
        item.get("pic").and_then(Value::as_str),
        item.pointer("/owner/name").and_then(Value::as_str),
        item.get("duration").and_then(Value::as_u64)
    ),"actions":video_action_descriptors(None, None)}))
}

fn personal_video_item(item: &Value) -> Option<Value> {
    let bvid = item.get("bvid")?.as_str()?;
    if validate_bvid(bvid).is_err() {
        return None;
    }
    Some(json!({
        "work":work_summary(
            bvid,
            item.get("title")?.as_str()?,
            item.get("cover").or_else(|| item.get("pic")).and_then(Value::as_str),
            item.pointer("/upper/name").or_else(|| item.pointer("/owner/name")).and_then(Value::as_str),
            item.get("duration").and_then(Value::as_u64)
        ),
        "actions":video_action_descriptors(Some(true), None)
    }))
}

fn creator_item(item: &Value) -> Option<Value> {
    let mid = item.get("mid")?.as_u64()?;
    if mid == 0 {
        return None;
    }
    Some(json!({
        "work":{
            "id":format!("up:{mid}"),
            "title":item.get("uname")?.as_str()?,
            "kind":"creator",
            "identity":{"scheme":"bilibili.mid","value":mid.to_string()},
            "posterUrl":item.get("face").and_then(Value::as_str).and_then(safe_https_url),
            "overview":item.get("sign").and_then(Value::as_str)
        },
        "actions":[{"id":"follow.remove","label":"取消关注","state":true,"requiresConfirmation":true,"destructive":true}]
    }))
}

fn subscription_item(item: &Value) -> Option<Value> {
    let season_id = item.get("season_id").and_then(Value::as_u64)?;
    if season_id == 0 {
        return None;
    }
    Some(json!({
        "work":{
            "id":format!("season:{season_id}"),
            "title":item.get("title")?.as_str()?,
            "kind":"series",
            "identity":{"scheme":"bilibili.season","value":season_id.to_string()},
            "posterUrl":item.get("cover").and_then(Value::as_str).and_then(safe_https_url),
            "overview":item.get("evaluate").and_then(Value::as_str)
        }
    }))
}

fn search_item(item: &Value) -> Option<Value> {
    let bvid = item.get("bvid")?.as_str()?;
    if validate_bvid(bvid).is_err() {
        return None;
    }
    let title = strip_search_markup(item.get("title")?.as_str()?);
    Some(json!({"work":work_summary(
        bvid,
        &title,
        item.get("pic").and_then(Value::as_str),
        item.get("author").and_then(Value::as_str),
        parse_duration(item.get("duration").and_then(Value::as_str))
    ),"actions":video_action_descriptors(None, None)}))
}

fn video_action_descriptors(favorite: Option<bool>, watch_later: Option<bool>) -> Vec<Value> {
    let favorite_id = if favorite == Some(true) {
        "favorite.remove"
    } else {
        "favorite.add"
    };
    let favorite_label = if favorite == Some(true) {
        "取消收藏"
    } else {
        "收藏"
    };
    let watch_later_id = if watch_later == Some(true) {
        "watch-later.remove"
    } else {
        "watch-later.add"
    };
    let watch_later_label = if watch_later == Some(true) {
        "移出稍后再看"
    } else {
        "稍后再看"
    };
    let mut favorite_action = json!({"id":favorite_id,"label":favorite_label});
    let mut watch_later_action = json!({"id":watch_later_id,"label":watch_later_label});
    if let Some(state) = favorite {
        favorite_action["state"] = Value::Bool(state);
    }
    if let Some(state) = watch_later {
        watch_later_action["state"] = Value::Bool(state);
    }
    vec![
        json!({"id":"like.add","label":"点赞"}),
        favorite_action,
        watch_later_action,
    ]
}

fn history_item(item: &Value) -> Option<Value> {
    let bvid = item
        .pointer("/history/bvid")
        .or_else(|| item.get("bvid"))?
        .as_str()?;
    if validate_bvid(bvid).is_err() {
        return None;
    }
    let cid = item.pointer("/history/cid").and_then(Value::as_u64);
    let segment_id = cid.map(|value| format!("cid:{value}"));
    let version_id = cid.map(|value| format!("bilibili:{bvid}:{value}"));
    let updated_at = item
        .get("view_at")
        .and_then(Value::as_i64)
        .filter(|value| *value > 0)
        .and_then(|value| OffsetDateTime::from_unix_timestamp(value).ok())
        .and_then(|value| value.format(&Rfc3339).ok());
    Some(json!({
        "work":work_summary(
            bvid,
            item.get("title")?.as_str()?,
            item.get("cover").and_then(Value::as_str),
            item.get("author_name").and_then(Value::as_str),
            item.get("duration").and_then(Value::as_u64)
        ),
        "segmentId":segment_id,
        "versionId":version_id,
        "positionSeconds":item.get("progress").and_then(Value::as_f64),
        "durationSeconds":item.get("duration").and_then(Value::as_f64),
        "updatedAt":updated_at
    }))
}

fn work_summary(
    bvid: &str,
    title: &str,
    poster: Option<&str>,
    author: Option<&str>,
    duration: Option<u64>,
) -> Value {
    json!({
        "id":bvid,
        "title":title,
        "kind":"video",
        "identity":{"scheme":"bilibili.bvid","value":bvid},
        "posterUrl":poster.and_then(safe_https_url),
        "author":author,
        "durationSeconds":duration
    })
}

fn detail_work(data: &Value) -> Value {
    let bvid = data.get("bvid").and_then(Value::as_str).unwrap_or("");
    let collection_segments = data
        .pointer("/ugc_season/sections")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .flat_map(|section| {
            section
                .get("episodes")
                .and_then(Value::as_array)
                .into_iter()
                .flatten()
        })
        .enumerate()
        .filter_map(|(index, episode)| {
            let episode_bvid = episode.get("bvid").and_then(Value::as_str)?;
            validate_bvid(episode_bvid).ok()?;
            let cid = episode.get("cid").and_then(Value::as_u64)?;
            Some(json!({
                "id":format!("cid:{cid}"),
                "title":episode.get("title").and_then(Value::as_str).unwrap_or("合集分集"),
                "index":index + 1,
                "versions":[{"id":format!("bilibili:{episode_bvid}:{cid}"),"label":"Bilibili","sourceLabel":"Bilibili","delivery":"online","variants":[]}]
            }))
        })
        .collect::<Vec<_>>();
    let page_segments = data
        .get("pages")
        .and_then(Value::as_array)
        .map(|pages| {
            pages
                .iter()
                .enumerate()
                .filter_map(|(index, page)| {
                    let cid = page.get("cid")?.as_u64()?;
                    let title = page.get("part").and_then(Value::as_str).unwrap_or("分 P");
                    Some(json!({
                        "id":format!("cid:{cid}"),
                        "title":title,
                        "index":index + 1,
                        "versions":[{"id":format!("bilibili:{bvid}:{cid}"),"label":"Bilibili","sourceLabel":"Bilibili","delivery":"online","variants":[]}]
                    }))
                })
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    let segments = if collection_segments.is_empty() {
        page_segments
    } else {
        collection_segments
    };
    json!({
        "id":bvid,
        "title":data.get("title").and_then(Value::as_str).unwrap_or("Bilibili 视频"),
        "kind":"video",
        "identity":{"scheme":"bilibili.bvid","value":bvid},
        "overview":data.get("desc").and_then(Value::as_str),
        "posterUrl":data.get("pic").and_then(Value::as_str).and_then(safe_https_url),
        "author":data.pointer("/owner/name").and_then(Value::as_str),
        "durationSeconds":data.get("duration").and_then(Value::as_u64),
        "segments":segments
    })
}

fn host_json(operation: i32, request: &Value) -> Result<Value, PluginError> {
    let request = serde_json::to_vec(request)
        .map_err(|_| PluginError::new("internal", "插件请求编码失败"))?;
    let mut response = vec![0_u8; HOST_BUFFER_BYTES];
    let length = unsafe {
        host_call(
            operation,
            request.as_ptr() as i32,
            request.len() as i32,
            response.as_mut_ptr() as i32,
            response.len() as i32,
        )
    };
    if length < 0 {
        let code = if length == -2 {
            "permission-denied"
        } else {
            "internal"
        };
        return Err(PluginError::new(code, "宿主能力调用失败"));
    }
    response.truncate(length as usize);
    let envelope: Value = serde_json::from_slice(&response)
        .map_err(|_| PluginError::new("invalid-response", "宿主响应无效"))?;
    envelope
        .get("data")
        .cloned()
        .ok_or_else(|| PluginError::new("invalid-response", "宿主响应缺少数据"))
}

fn host_log(level: &str, operation: &str, message: &str, count: usize) {
    let _ = host_json(
        HOST_LOG,
        &json!({"level":level,"operation":operation,"message":message,"fields":{"count":count}}),
    );
}

fn host_log_domain_denied(hostname: &str) {
    if hostname.len() > 253 {
        return;
    }
    let _ = host_json(
        HOST_LOG,
        &json!({
            "level":"warn",
            "operation":"media.playback.asset_domain",
            "message":"playback asset domain denied",
            "fields":{"domain":hostname}
        }),
    );
}

fn parse_request<T: for<'de> Deserialize<'de>>(request: &[u8]) -> Result<T, PluginError> {
    serde_json::from_slice(request)
        .map_err(|_| PluginError::new("invalid-request", "插件请求格式无效"))
}

fn validate_bvid(value: &str) -> Result<&str, PluginError> {
    if value.len() < 10
        || value.len() > 20
        || !value.starts_with("BV")
        || !value.bytes().all(|byte| byte.is_ascii_alphanumeric())
    {
        return Err(PluginError::new("invalid-request", "BVID 无效"));
    }
    Ok(value)
}

fn safe_https_url(value: &str) -> Option<String> {
    if value.len() > 4096 || value.chars().any(char::is_control) {
        return None;
    }
    let normalized = if value.starts_with("//") {
        format!("https:{value}")
    } else if let Some(path) = value.strip_prefix("http://") {
        format!("https://{path}")
    } else {
        value.to_owned()
    };
    let remainder = normalized.strip_prefix("https://")?;
    let authority = remainder.split(['/', '?', '#']).next()?;
    if authority.is_empty() || authority.contains('@') {
        return None;
    }
    let (hostname, port) = match authority.rsplit_once(':') {
        Some((hostname, port))
            if !hostname.contains(':') && matches!(port.parse::<u16>(), Ok(443 | 4483 | 8082)) =>
        {
            (hostname, Some(port))
        }
        Some(_) => {
            host_log_domain_denied(authority);
            return None;
        }
        None => (authority, None),
    };
    if hostname.is_empty()
        || !hostname
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-'))
    {
        host_log_domain_denied(authority);
        return None;
    }
    let hostname = hostname.to_ascii_lowercase();
    if !hostname.ends_with(".bilibili.com")
        && hostname != "bilibili.com"
        && !hostname.ends_with(".hdslb.com")
        && hostname != "hdslb.com"
        && !hostname.ends_with(".bilivideo.com")
        && hostname != "bilivideo.com"
        && !hostname.ends_with(".bilivideo.cn")
        && hostname != "bilivideo.cn"
    {
        host_log_domain_denied(&hostname);
        return None;
    }
    let _ = port;
    Some(normalized)
}

fn parse_page_cursor(cursor: Option<&str>) -> Result<u32, PluginError> {
    match cursor {
        None | Some("") => Ok(1),
        Some(value)
            if value.len() <= 3
                && value.bytes().all(|byte| byte.is_ascii_digit())
                && value
                    .parse::<u32>()
                    .is_ok_and(|page| (1..=100).contains(&page)) =>
        {
            Ok(value.parse::<u32>().unwrap_or(1))
        }
        _ => Err(PluginError::new("invalid-request", "分页游标无效")),
    }
}

fn valid_opaque_key(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
}

fn valid_identity_key(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b':'))
}

fn strip_search_markup(value: &str) -> String {
    value
        .replace("<em class=\"keyword\">", "")
        .replace("</em>", "")
}

fn parse_duration(value: Option<&str>) -> Option<u64> {
    let parts = value?.split(':').collect::<Vec<_>>();
    match parts.as_slice() {
        [minutes, seconds] => {
            Some(minutes.parse::<u64>().ok()? * 60 + seconds.parse::<u64>().ok()?)
        }
        [hours, minutes, seconds] => Some(
            hours.parse::<u64>().ok()? * 3600
                + minutes.parse::<u64>().ok()? * 60
                + seconds.parse::<u64>().ok()?,
        ),
        _ => None,
    }
}

fn encode_response(value: Value) -> i64 {
    let response = serde_json::to_vec(&value)
        .unwrap_or_else(|_| b"{\"pluginError\":{\"code\":\"internal\"}}".to_vec());
    let mut response = response.into_boxed_slice();
    let pointer = response.as_mut_ptr() as u32;
    let length = response.len() as u32;
    mem::forget(response);
    ((pointer as i64) << 32) | length as i64
}

fn plugin_error(code: &str, message: &str) -> Value {
    json!({"pluginError":{"code":code,"message":message}})
}

#[derive(Debug)]
struct PluginError {
    code: &'static str,
    message: &'static str,
}

impl PluginError {
    fn new(code: &'static str, message: &'static str) -> Self {
        Self { code, message }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(name: &str) -> Value {
        let source = match name {
            "recommended" => include_str!("../fixtures/recommended.json"),
            "detail" => include_str!("../fixtures/detail.json"),
            "playback" => include_str!("../fixtures/playback-progressive.json"),
            "history" => include_str!("../fixtures/history.json"),
            "progress" => include_str!("../fixtures/progress-state.json"),
            "qr-generate" => include_str!("../fixtures/qr-generate.json"),
            "qr-poll" => include_str!("../fixtures/qr-poll.json"),
            "dash" => include_str!("../fixtures/playback-dash.json"),
            "personal" => include_str!("../fixtures/personal.json"),
            _ => panic!("unknown fixture"),
        };
        serde_json::from_str(source).expect("fixture must contain valid JSON")
    }

    #[test]
    fn navigation_and_recommendation_map_to_generic_site_contract() {
        let navigation_value = navigation(NavigationRequest {
            connection_id: "connection".to_owned(),
            parent_node_key: None,
            depth: Some(0),
        })
        .expect("navigation");
        let routes = navigation_value
            .pointer("/nodes")
            .and_then(Value::as_array)
            .expect("navigation list");
        assert!(routes
            .iter()
            .any(|item| item.get("routeKey") == Some(&json!("recommended"))));
        assert!(routes
            .iter()
            .any(|item| item.get("nodeKey") == Some(&json!("anime"))));

        let anime = navigation(NavigationRequest {
            connection_id: "connection".to_owned(),
            parent_node_key: Some("anime".to_owned()),
            depth: Some(1),
        })
        .expect("anime navigation");
        assert!(anime
            .pointer("/nodes")
            .and_then(Value::as_array)
            .is_some_and(|nodes| nodes
                .iter()
                .any(|item| item.get("routeKey") == Some(&json!("anime-jp")))));

        let payload = fixture("recommended");
        let items = payload
            .pointer("/data/item")
            .and_then(Value::as_array)
            .expect("recommended items")
            .iter()
            .filter_map(feed_item)
            .collect::<Vec<_>>();
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].pointer("/work/id"), Some(&json!("BV1xx411c7mD")));
        assert_eq!(
            items[0].pointer("/work/posterUrl"),
            Some(&json!("https://i0.hdslb.com/bfs/archive/poster.jpg"))
        );
        assert!(items[0].to_string().find("cookie").is_none());
        assert_eq!(safe_https_url("javascript:alert(1)"), None);
        assert_eq!(safe_https_url("https://user@example.com/poster.jpg"), None);
        assert_eq!(
            safe_https_url("https://xy113x207x104x18xy.mcdn.bilivideo.cn/audio.m4s"),
            Some("https://xy113x207x104x18xy.mcdn.bilivideo.cn/audio.m4s".to_owned())
        );
        assert_eq!(
            safe_https_url("https://xy113x207x104x18xy.mcdn.bilivideo.cn:4483/audio.m4s"),
            Some("https://xy113x207x104x18xy.mcdn.bilivideo.cn:4483/audio.m4s".to_owned())
        );
        assert_eq!(
            safe_https_url("https://xy113x207x104x18xy.mcdn.bilivideo.cn:22/audio.m4s"),
            None
        );
        assert_eq!(
            safe_https_url("https://xy113x207x104x18xy.mcdn.bilivideo.cn:8082/audio.m4s"),
            Some("https://xy113x207x104x18xy.mcdn.bilivideo.cn:8082/audio.m4s".to_owned())
        );
        assert_eq!(
            safe_https_url("https://bilivideo.cn.evil.example/audio.m4s"),
            None
        );
    }

    #[test]
    fn cursors_refresh_sessions_and_version_identity_fail_closed() {
        assert_eq!(parse_page_cursor(None).expect("first page"), 1);
        assert_eq!(parse_page_cursor(Some("100")).expect("last page"), 100);
        assert!(parse_page_cursor(Some("0")).is_err());
        assert!(parse_page_cursor(Some("101")).is_err());
        assert!(parse_page_cursor(Some("not-a-page")).is_err());
        assert!(valid_opaque_key("refresh-session_1"));
        assert!(!valid_opaque_key("refresh/session"));

        let request = PlaybackRequest {
            connection_id: "connection".to_owned(),
            item_id: "BV1xx411c7mD".to_owned(),
            segment_id: "cid:101".to_owned(),
            version_id: "bilibili:BV1xx411c7mD:other".to_owned(),
            variant_id: Some("qn:80".to_owned()),
        };
        let error = resolve_playback(&request).expect_err("version identity mismatch");
        assert_eq!(error.code, "invalid-request");
    }

    #[test]
    fn detail_preserves_segments_versions_and_safe_metadata() {
        let payload = fixture("detail");
        let work = detail_work(payload.get("data").expect("detail data"));
        assert_eq!(work.get("id"), Some(&json!("BV1xx411c7mD")));
        assert_eq!(work.pointer("/segments/0/id"), Some(&json!("cid:101")));
        assert_eq!(
            work.pointer("/segments/1/versions/0/id"),
            Some(&json!("bilibili:BV1xx411c7mD:102"))
        );
        assert_eq!(
            work.get("posterUrl"),
            Some(&json!("https://i0.hdslb.com/bfs/archive/detail.jpg"))
        );
    }

    #[test]
    fn progressive_playback_uses_only_complete_durl_and_real_variants() {
        let payload = fixture("playback");
        let (selected, variants, url) =
            parse_progressive_data(payload.get("data").expect("playback data"), "64")
                .expect("progressive response");
        assert_eq!(selected, "qn:80");
        assert_eq!(variants.len(), 3);
        assert_eq!(variants[0].get("label"), Some(&json!("1080P 高清")));
        assert_eq!(
            url,
            "https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/test.mp4"
        );

        let dash_only = json!({"quality":80,"accept_quality":[80],"accept_description":["1080P"],"dash":{"video":[]}});
        let error = parse_progressive_data(&dash_only, "80")
            .expect_err("DASH-only response must not masquerade as complete progressive media");
        assert_eq!(error.code, "upstream-unavailable");
    }

    #[test]
    fn bilibili_playback_business_errors_map_to_stable_codes() {
        for (code, expected) in [
            (-101, "not-authenticated"),
            (-404, "not-found"),
            (-412, "rate-limited"),
            (-509, "rate-limited"),
            (-10403, "access-restricted"),
        ] {
            let payload =
                serde_json::to_vec(&json!({"code":code,"message":"untrusted"})).expect("fixture");
            let error = parse_bili_json(&payload).expect_err("business error");
            assert_eq!(error.code, expected);
            assert!(!error.message.contains("untrusted"));
        }
    }

    #[test]
    fn history_maps_stable_cursor_and_rfc3339_timestamp() {
        let page = parse_history_page(&fixture("history")).expect("history page");
        let cursor = page
            .get("cursor")
            .and_then(Value::as_str)
            .expect("history cursor");
        assert_eq!(
            decode_history_cursor(Some(cursor)).expect("decode cursor"),
            Some(HistoryCursor {
                max: 1_700_000_000,
                view_at: 1_699_999_900,
                business: "archive".to_owned(),
            })
        );
        assert_eq!(
            history_url(
                24,
                decode_history_cursor(Some(cursor))
                    .expect("decode cursor")
                    .as_ref()
            ),
            "https://api.bilibili.com/x/web-interface/history/cursor?ps=24&type=archive&max=1700000000&view_at=1699999900&business=archive"
        );
        assert_eq!(
            history_url(
                24,
                decode_history_cursor(Some("1700000000"))
                    .expect("decode legacy cursor")
                    .as_ref()
            ),
            "https://api.bilibili.com/x/web-interface/history/cursor?ps=24&type=archive&view_at=1700000000"
        );
        assert_eq!(page.get("hasMore"), Some(&json!(true)));
        assert_eq!(
            page.pointer("/list/0/work/id"),
            Some(&json!("BV1xx411c7mD"))
        );
        assert_eq!(page.pointer("/list/0/segmentId"), Some(&json!("cid:101")));
        assert_eq!(
            page.pointer("/list/0/updatedAt"),
            Some(&json!("2023-11-14T22:15:00Z"))
        );
    }

    #[test]
    fn progress_idempotency_and_throttle_are_deterministic() {
        let previous = fixture("progress");
        let mut request = ProgressRequest {
            connection_id: "connection".to_owned(),
            item_id: "BV1xx411c7mD".to_owned(),
            segment_id: "cid:101".to_owned(),
            version_id: "bilibili:BV1xx411c7mD:101".to_owned(),
            event: "progress".to_owned(),
            position_seconds: 121.0,
            duration_seconds: Some(600.0),
            idempotency_key: "event-existing".to_owned(),
            occurred_at: Some("2026-08-23T12:00:00Z".to_owned()),
        };
        assert_eq!(
            progress_decision(&previous, &request),
            ProgressDecision::Duplicate
        );
        request.idempotency_key = "event-next".to_owned();
        assert_eq!(
            progress_decision(&previous, &request),
            ProgressDecision::Throttled
        );
        request.position_seconds = 130.0;
        assert_eq!(
            progress_decision(&previous, &request),
            ProgressDecision::Submit
        );
        request.event = "paused".to_owned();
        request.position_seconds = 121.0;
        assert_eq!(
            progress_decision(&previous, &request),
            ProgressDecision::Submit
        );
    }

    #[test]
    fn danmaku_fixture_normalizes_modes_colors_and_rejects_invalid_rows() {
        let comments =
            parse_danmaku_xml(include_bytes!("../fixtures/danmaku.xml")).expect("danmaku");
        assert_eq!(comments.len(), 3);
        assert_eq!(comments[0].get("mode"), Some(&json!("scroll")));
        assert_eq!(comments[1].get("mode"), Some(&json!("bottom")));
        assert_eq!(comments[2].get("mode"), Some(&json!("top")));
        assert_eq!(comments[1].get("color"), Some(&json!("#ff0000")));
        assert_eq!(comments[2].get("color"), Some(&json!("#00ff00")));
    }

    #[test]
    fn qr_login_fixtures_cover_generate_scan_confirm_and_expiry() {
        let (url, key) = parse_qr_generate(&fixture("qr-generate")).expect("qr generate");
        assert_eq!(
            url,
            "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&qrcode_key=fixture-key&from="
        );
        assert_eq!(key, "fixture-key");
        let manifest: Value =
            serde_json::from_str(include_str!("../plugin.template.json")).expect("manifest");
        let network_domains = manifest["permissions"]
            .as_array()
            .and_then(|permissions| {
                permissions.iter().find(|permission| {
                    permission.get("kind").and_then(Value::as_str) == Some("network.http")
                })
            })
            .and_then(|permission| permission.get("domains"))
            .and_then(Value::as_array)
            .expect("network domains");
        assert!(network_domains
            .iter()
            .any(|domain| domain.as_str() == Some("account.bilibili.com")));
        assert!(!network_domains
            .iter()
            .any(|domain| domain.as_str() == Some("*.bilibili.com")));
        let states = fixture("qr-poll");
        assert_eq!(
            parse_qr_poll(&states["pending"]).unwrap(),
            QRLoginState::Pending
        );
        assert_eq!(
            parse_qr_poll(&states["scanned"]).unwrap(),
            QRLoginState::Scanned
        );
        assert_eq!(
            parse_qr_poll(&states["confirmed"]).unwrap(),
            QRLoginState::Confirmed
        );
        assert_eq!(
            parse_qr_poll(&states["expired"]).unwrap(),
            QRLoginState::Expired
        );
        assert!(parse_qr_poll(&json!({"data":{"code":999}})).is_err());
    }

    #[test]
    fn dash_fixture_keeps_variants_separate_from_audio_track() {
        let payload = fixture("dash");
        let source = parse_playback_data(payload.get("data").unwrap(), "120").expect("dash");
        assert_eq!(source.variant_id, "qn:120");
        assert_eq!(source.variants.len(), 3);
        assert_eq!(source.variants[0].get("height"), Some(&json!(2160)));
        assert_eq!(
            source.variants[0].get("audioCodec"),
            Some(&json!("mp4a.40.2"))
        );
        assert!(source.video_url.ends_with("video-4k.m4s"));
        assert_eq!(
            source.audio_url.as_deref(),
            Some("https://upos-sz-mirrorcos.bilivideo.com/audio.m4s")
        );
    }

    #[test]
    fn subtitle_fixture_converts_to_bounded_valid_webvtt() {
        let vtt = subtitle_json_to_vtt(include_bytes!("../fixtures/subtitle.json")).expect("vtt");
        assert!(vtt.starts_with("WEBVTT\n\n"));
        assert!(vtt.contains("00:00:01.250 --> 00:00:03.500"));
        assert!(vtt.contains("第二行 → 已清理"));
        assert!(!vtt.contains("无效字幕"));
    }

    #[test]
    fn personal_content_fixtures_preserve_provider_identities() {
        let payload = fixture("personal");
        assert_eq!(
            personal_video_item(&payload["favorite"])
                .unwrap()
                .pointer("/work/id"),
            Some(&json!("BV1xx411c7mD"))
        );
        assert_eq!(
            personal_video_item(&payload["favorite"])
                .unwrap()
                .pointer("/actions/1/id"),
            Some(&json!("favorite.remove"))
        );
        assert_eq!(
            creator_item(&payload["creator"])
                .unwrap()
                .pointer("/work/identity/scheme"),
            Some(&json!("bilibili.mid"))
        );
        assert_eq!(
            creator_item(&payload["creator"])
                .unwrap()
                .pointer("/actions/0/requiresConfirmation"),
            Some(&json!(true))
        );
        assert_eq!(
            creator_item(&payload["creator"])
                .unwrap()
                .pointer("/actions/0/destructive"),
            Some(&json!(true))
        );
        assert_eq!(
            subscription_item(&payload["subscription"])
                .unwrap()
                .pointer("/work/kind"),
            Some(&json!("series"))
        );
        let filename = suggested_file_name("七武士：导演剪辑版/测试", "BV1xx411c7mD", "qn:120");
        assert_eq!(
            filename,
            "七武士：导演剪辑版测试 [Bilibili BV1xx411c7mD Q120].mp4"
        );
        assert!(filename.len() < 240);
    }

    #[test]
    fn configured_download_quality_is_applied_without_overriding_user_choice() {
        assert_eq!(
            download_quality_variant(&json!({"defaultQuality":"1080p"}), None),
            Some("qn:80".to_owned())
        );
        assert_eq!(
            download_quality_variant(
                &json!({"defaultQuality":"highest"}),
                Some("qn:64".to_owned())
            ),
            Some("qn:64".to_owned())
        );
        assert_eq!(
            download_quality_variant(&json!({"defaultQuality":"auto"}), None),
            None
        );
    }
}

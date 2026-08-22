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
        1 => navigation(),
        2 => parse_request(request).and_then(feed),
        3 => parse_request(request).and_then(search),
        4 => parse_request(request).and_then(detail),
        5 => parse_request(request).and_then(playback),
        6 => parse_request(request).and_then(download_plan),
        7 => parse_request(request).and_then(history),
        8 => parse_request(request).and_then(progress_sync),
        _ => Err(PluginError::new("invalid-request", "不支持的插件操作")),
    };
    encode_response(match result {
        Ok(value) => value,
        Err(error) => plugin_error(error.code, error.message),
    })
}

fn navigation() -> Result<Value, PluginError> {
    Ok(json!([
        {"id":"recommended","title":"推荐","pageType":"feed","iconKey":"home","routeKey":"recommended","refreshable":true},
        {"id":"popular","title":"热门","pageType":"feed","iconKey":"flame","routeKey":"popular","refreshable":true},
        {"id":"ranking","title":"排行","pageType":"feed","iconKey":"chart","routeKey":"ranking","refreshable":true},
        {"id":"search","title":"搜索","pageType":"search","iconKey":"search","routeKey":"search"}
        ,{"id":"history","title":"历史","pageType":"user-library","iconKey":"history","routeKey":"history","refreshable":true}
    ]))
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
    let (url, title, layout) = match request.route_key.as_str() {
        "recommended" => (
            "https://api.bilibili.com/x/web-interface/index/top/feed/rcmd?ps=20&fresh_type=3"
                .to_owned(),
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
        "homeEligible": request.route_key == "recommended"
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
    let bvid = validate_bvid(&request.item_id)?;
    let url = format!("https://api.bilibili.com/x/web-interface/view?bvid={bvid}");
    let payload = bili_get(&request.connection_id, &url, true)?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("not-found", "视频不存在或不可访问"))?;
    Ok(detail_work(data))
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
    let resolved = resolve_progressive(&request)?;
    let danmaku = register_danmaku(&request.connection_id, &request.segment_id).ok();
    Ok(json!({
        "workId": request.item_id,
        "segmentId": request.segment_id,
        "versionId": request.version_id,
        "variantId": resolved.variant_id,
        "variants": resolved.variants,
        "assets": [{"kind":"progressive","urlRef":resolved.asset_ref}],
        "delivery":"server-gateway",
        "expiresAt":resolved.expires_at,
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
    variant_id: String,
}

fn download_plan(request: DownloadRequest) -> Result<Value, PluginError> {
    let resolved = resolve_progressive(&PlaybackRequest {
        connection_id: request.connection_id,
        item_id: request.item_id.clone(),
        segment_id: request.segment_id.clone(),
        version_id: request.version_id.clone(),
        variant_id: Some(request.variant_id.clone()),
    })?;
    Ok(json!({
        "workId":request.item_id,
        "segmentId":request.segment_id,
        "versionId":request.version_id,
        "variantId":resolved.variant_id,
        "suggestedFileName":format!("{} [{}].mp4", request.item_id, resolved.variant_id),
        "assets":[{"id":"video","kind":"video","urlRef":resolved.asset_ref,"expectedContentType":"video/mp4"}]
    }))
}

#[derive(Debug)]
struct ResolvedProgressive {
    variant_id: String,
    variants: Vec<Value>,
    asset_ref: String,
    expires_at: String,
}

fn resolve_progressive(request: &PlaybackRequest) -> Result<ResolvedProgressive, PluginError> {
    let bvid = validate_bvid(&request.item_id)?;
    let cid = request
        .segment_id
        .strip_prefix("cid:")
        .unwrap_or(&request.segment_id);
    if cid.is_empty() || !cid.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "分 P 身份无效"));
    }
    if request.version_id != format!("bilibili:{bvid}:{cid}") {
        return Err(PluginError::new("invalid-request", "媒体版本身份无效"));
    }
    let qn = request
        .variant_id
        .as_deref()
        .and_then(|value| value.strip_prefix("qn:"))
        .unwrap_or("80");
    if !qn.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(PluginError::new("invalid-request", "清晰度身份无效"));
    }
    let url = format!(
        "https://api.bilibili.com/x/player/playurl?bvid={bvid}&cid={cid}&qn={qn}&fnval=0&fourk=1"
    );
    let payload = bili_get(&request.connection_id, &url, true)?;
    let data = payload
        .get("data")
        .ok_or_else(|| PluginError::new("invalid-response", "播放响应无效"))?;
    let (selected, variants, durl) = parse_progressive_data(data, qn)?;
    let (asset_ref, expires_at) = register_asset(
        &durl,
        json!({"Referer":"https://www.bilibili.com/","User-Agent":USER_AGENT}),
    )?;
    Ok(ResolvedProgressive {
        variant_id: selected,
        variants,
        asset_ref,
        expires_at,
    })
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
            Some(json!({"id":format!("qn:{quality}"),"label":label,"available":true}))
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
                "progressive-unavailable",
                "该清晰度仅提供 DASH，当前未返回不完整视频流",
            )
        })?
        .to_owned();
    Ok((selected, variants, durl))
}

fn register_asset(url: &str, headers: Value) -> Result<(String, String), PluginError> {
    let registered = host_json(
        HOST_ASSET_REGISTER,
        &json!({"url":url,"headers":headers,"ttlSeconds":300}),
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
    let decoded = host_get_bytes(connection_id, url, true, false)?;
    parse_bili_json(&decoded)
}

fn parse_bili_json(decoded: &[u8]) -> Result<Value, PluginError> {
    let payload: Value = serde_json::from_slice(decoded)
        .map_err(|_| PluginError::new("invalid-response", "站点响应格式无效"))?;
    if payload.get("code").and_then(Value::as_i64).unwrap_or(-1) != 0 {
        return Err(PluginError::new(
            "upstream-unavailable",
            "站点拒绝了当前请求",
        ));
    }
    Ok(payload)
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
            if authenticated && allow_anonymous_fallback && error.code == "host-call-denied" =>
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
        return Err(PluginError::new("upstream-unavailable", "播放进度回传失败"));
    }
    let body = response
        .get("bodyBase64")
        .and_then(Value::as_str)
        .ok_or_else(|| PluginError::new("invalid-response", "播放进度响应缺失"))?;
    let decoded = BASE64
        .decode(body)
        .map_err(|_| PluginError::new("invalid-response", "播放进度响应无效"))?;
    let payload: Value = serde_json::from_slice(&decoded)
        .map_err(|_| PluginError::new("invalid-response", "播放进度响应格式无效"))?;
    if payload.get("code").and_then(Value::as_i64).unwrap_or(-1) != 0 {
        return Err(PluginError::new(
            "upstream-unavailable",
            "播放进度回传被拒绝",
        ));
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
    )}))
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
    )}))
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
    let segments = data
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
                        "versions":[{"id":format!("bilibili:{bvid}:{cid}"),"label":"Bilibili","sourceLabel":"Bilibili","variants":[]}]
                    }))
                })
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
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
            "host-call-denied"
        } else {
            "host-call-failed"
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
    let normalized = if value.starts_with("//") {
        format!("https:{value}")
    } else if let Some(path) = value.strip_prefix("http://") {
        format!("https://{path}")
    } else {
        value.to_owned()
    };
    let remainder = normalized.strip_prefix("https://")?;
    let authority = remainder.split(['/', '?', '#']).next()?;
    if authority.is_empty()
        || authority.contains(['@', ':'])
        || !authority
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-'))
    {
        return None;
    }
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
            _ => panic!("unknown fixture"),
        };
        serde_json::from_str(source).expect("fixture must contain valid JSON")
    }

    #[test]
    fn navigation_and_recommendation_map_to_generic_site_contract() {
        let navigation = navigation().expect("navigation");
        let routes = navigation.as_array().expect("navigation list");
        assert!(routes
            .iter()
            .any(|item| item.get("routeKey") == Some(&json!("recommended"))));
        assert!(routes
            .iter()
            .any(|item| item.get("routeKey") == Some(&json!("history"))));

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
        let error = resolve_progressive(&request).expect_err("version identity mismatch");
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
        assert_eq!(error.code, "progressive-unavailable");
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
}

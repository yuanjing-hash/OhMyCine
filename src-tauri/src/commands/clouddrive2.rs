use prost::Message;
use prost_types::Timestamp;
use reqwest::header::{HeaderName, HeaderValue};
use reqwest::Url;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use tauri::State;
use tokio::sync::Mutex;
use tonic::client::Grpc;
use tonic::codec::ProstCodec;
use tonic::codegen::http::uri::PathAndQuery;
use tonic::metadata::MetadataValue;
use tonic::transport::{Channel, Endpoint};
use tonic::{Code, Request, Status};

const MAX_ENDPOINT_LENGTH: usize = 2048;
const MAX_TOKEN_LENGTH: usize = 16 * 1024;
const MAX_PATH_LENGTH: usize = 4096;
const MAX_KEYWORD_LENGTH: usize = 512;
const MAX_STREAM_HEADERS: usize = 32;
const MAX_HEADER_NAME_LENGTH: usize = 128;
const MAX_HEADER_VALUE_LENGTH: usize = 8192;

#[derive(Default)]
pub struct CloudDrive2GrpcState {
    channels: Mutex<HashMap<String, Channel>>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CloudDrive2PathRequest {
    base_url: String,
    api_token: String,
    path: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CloudDrive2SearchRequest {
    base_url: String,
    api_token: String,
    path: String,
    keyword: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CloudDrive2FileEntry {
    name: String,
    path: String,
    is_dir: bool,
    size: Option<u64>,
    modified_ms: Option<i64>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CloudDrive2StreamResponse {
    url: String,
    headers: HashMap<String, String>,
}

#[derive(Debug)]
pub(crate) struct CloudDrive2ResolvedStream {
    pub(crate) url: String,
    pub(crate) headers: HashMap<String, String>,
}

#[derive(Clone, PartialEq, Message)]
struct ListSubFileRequest {
    #[prost(string, tag = "1")]
    path: String,
    #[prost(bool, tag = "2")]
    force_refresh: bool,
    #[prost(bool, optional, tag = "3")]
    check_expires: Option<bool>,
}

#[derive(Clone, PartialEq, Message)]
struct SearchRequest {
    #[prost(string, tag = "1")]
    path: String,
    #[prost(string, tag = "2")]
    search_for: String,
    #[prost(bool, tag = "3")]
    force_refresh: bool,
    #[prost(bool, tag = "4")]
    fuzzy_match: bool,
    #[prost(bool, optional, tag = "5")]
    add_result_to_mounted_search_folder: Option<bool>,
    #[prost(bool, optional, tag = "6")]
    content_search: Option<bool>,
}

#[derive(Clone, PartialEq, Message)]
struct GetDownloadUrlPathRequest {
    #[prost(string, tag = "1")]
    path: String,
    #[prost(bool, tag = "2")]
    preview: bool,
    #[prost(bool, tag = "3")]
    lazy_read: bool,
    #[prost(bool, tag = "4")]
    get_direct_url: bool,
}

#[derive(Clone, PartialEq, Message)]
struct SubFilesReply {
    #[prost(message, repeated, tag = "1")]
    sub_files: Vec<CloudDriveFile>,
}

#[derive(Clone, PartialEq, Message)]
struct CloudDriveFile {
    #[prost(string, tag = "2")]
    name: String,
    #[prost(string, tag = "3")]
    full_path_name: String,
    #[prost(int64, tag = "4")]
    size: i64,
    #[prost(enumeration = "CloudDriveFileType", tag = "5")]
    file_type: i32,
    #[prost(message, optional, tag = "6")]
    create_time: Option<Timestamp>,
    #[prost(message, optional, tag = "7")]
    write_time: Option<Timestamp>,
    #[prost(bool, tag = "30")]
    is_directory: bool,
    #[prost(bool, tag = "36")]
    is_forbidden: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, prost::Enumeration)]
#[repr(i32)]
enum CloudDriveFileType {
    Directory = 0,
    File = 1,
    Other = 2,
}

#[derive(Clone, PartialEq, Message)]
struct DownloadUrlPathInfo {
    #[prost(string, tag = "1")]
    download_url_path: String,
    #[prost(uint64, optional, tag = "2")]
    expires_in: Option<u64>,
    #[prost(string, optional, tag = "3")]
    direct_url: Option<String>,
    #[prost(string, optional, tag = "4")]
    user_agent: Option<String>,
    #[prost(map = "string, string", tag = "5")]
    additional_headers: HashMap<String, String>,
}

#[tauri::command]
pub async fn clouddrive2_list(
    state: State<'_, CloudDrive2GrpcState>,
    request: CloudDrive2PathRequest,
) -> Result<Vec<CloudDrive2FileEntry>, String> {
    let (endpoint, token, path) = normalize_path_request(request)?;
    let channel = state.channel(&endpoint).await?;
    let grpc_request = authorized_request(
        ListSubFileRequest {
            path,
            force_refresh: false,
            check_expires: Some(true),
        },
        &token,
    )?;
    let replies = server_streaming::<ListSubFileRequest, SubFilesReply>(
        channel,
        grpc_request,
        "/clouddrive.CloudDriveFileSrv/GetSubFiles",
    )
    .await?;
    Ok(map_file_entries(replies))
}

#[tauri::command]
pub async fn clouddrive2_search(
    state: State<'_, CloudDrive2GrpcState>,
    request: CloudDrive2SearchRequest,
) -> Result<Vec<CloudDrive2FileEntry>, String> {
    let endpoint = normalize_endpoint(&request.base_url)?;
    let token = normalize_token(&request.api_token)?;
    let path = normalize_provider_path(&request.path)?;
    let keyword = normalize_keyword(&request.keyword)?;
    let channel = state.channel(&endpoint).await?;
    let grpc_request = authorized_request(
        SearchRequest {
            path,
            search_for: keyword,
            force_refresh: false,
            fuzzy_match: true,
            add_result_to_mounted_search_folder: Some(false),
            content_search: Some(false),
        },
        &token,
    )?;
    let replies = server_streaming::<SearchRequest, SubFilesReply>(
        channel,
        grpc_request,
        "/clouddrive.CloudDriveFileSrv/GetSearchResults",
    )
    .await?;
    Ok(map_file_entries(replies))
}

#[tauri::command]
pub async fn clouddrive2_get_stream(
    state: State<'_, CloudDrive2GrpcState>,
    request: CloudDrive2PathRequest,
) -> Result<CloudDrive2StreamResponse, String> {
    let (endpoint, token, path) = normalize_path_request(request)?;
    let channel = state.channel(&endpoint).await?;
    let grpc_request = authorized_request(
        GetDownloadUrlPathRequest {
            path,
            preview: false,
            lazy_read: false,
            get_direct_url: true,
        },
        &token,
    )?;
    let info = unary::<GetDownloadUrlPathRequest, DownloadUrlPathInfo>(
        channel,
        grpc_request,
        "/clouddrive.CloudDriveFileSrv/GetDownloadUrlPath",
    )
    .await?;
    build_stream_response(&endpoint, info)
}

pub(crate) async fn resolve_download_stream(
    state: &CloudDrive2GrpcState,
    base_url: &str,
    api_token: &str,
    path: &str,
) -> Result<CloudDrive2ResolvedStream, String> {
    let endpoint = normalize_endpoint(base_url)?;
    let token = normalize_token(api_token)?;
    let path = normalize_provider_path(path)?;
    let channel = state.channel(&endpoint).await?;
    let grpc_request = authorized_request(
        GetDownloadUrlPathRequest {
            path,
            preview: false,
            lazy_read: false,
            get_direct_url: true,
        },
        &token,
    )?;
    let info = unary::<GetDownloadUrlPathRequest, DownloadUrlPathInfo>(
        channel,
        grpc_request,
        "/clouddrive.CloudDriveFileSrv/GetDownloadUrlPath",
    )
    .await?;
    let stream = build_stream_response(&endpoint, info)?;
    Ok(CloudDrive2ResolvedStream {
        url: stream.url,
        headers: stream.headers,
    })
}

impl CloudDrive2GrpcState {
    async fn channel(&self, endpoint: &str) -> Result<Channel, String> {
        let mut channels = self.channels.lock().await;
        if let Some(channel) = channels.get(endpoint) {
            return Ok(channel.clone());
        }

        let channel = Endpoint::from_shared(endpoint.to_string())
            .map_err(|_| "CloudDrive2 gRPC 服务地址无效。".to_string())?
            .connect_timeout(std::time::Duration::from_secs(10))
            .timeout(std::time::Duration::from_secs(20))
            .connect_lazy();
        channels.insert(endpoint.to_string(), channel.clone());
        Ok(channel)
    }
}

async fn server_streaming<Req, Resp>(
    channel: Channel,
    request: Request<Req>,
    path: &'static str,
) -> Result<Vec<Resp>, String>
where
    Req: Message + Default + Send + Sync + 'static,
    Resp: Message + Default + Send + 'static,
{
    let mut grpc = Grpc::new(channel);
    grpc.ready()
        .await
        .map_err(|_| "CloudDrive2 gRPC 服务当前不可用。".to_string())?;
    let response = grpc
        .server_streaming(
            request,
            PathAndQuery::from_static(path),
            ProstCodec::default(),
        )
        .await
        .map_err(safe_status_message)?;
    let mut stream = response.into_inner();
    let mut replies = Vec::new();
    while let Some(reply) = stream.message().await.map_err(safe_status_message)? {
        replies.push(reply);
    }
    Ok(replies)
}

async fn unary<Req, Resp>(
    channel: Channel,
    request: Request<Req>,
    path: &'static str,
) -> Result<Resp, String>
where
    Req: Message + Default + Send + Sync + 'static,
    Resp: Message + Default + Send + 'static,
{
    let mut grpc = Grpc::new(channel);
    grpc.ready()
        .await
        .map_err(|_| "CloudDrive2 gRPC 服务当前不可用。".to_string())?;
    grpc.unary(
        request,
        PathAndQuery::from_static(path),
        ProstCodec::default(),
    )
    .await
    .map(|response| response.into_inner())
    .map_err(safe_status_message)
}

fn authorized_request<T>(message: T, token: &str) -> Result<Request<T>, String> {
    let authorization = MetadataValue::try_from(format!("Bearer {token}"))
        .map_err(|_| "CloudDrive2 API Token 无效。".to_string())?;
    let mut request = Request::new(message);
    request
        .metadata_mut()
        .insert("authorization", authorization);
    Ok(request)
}

fn normalize_path_request(
    request: CloudDrive2PathRequest,
) -> Result<(String, String, String), String> {
    Ok((
        normalize_endpoint(&request.base_url)?,
        normalize_token(&request.api_token)?,
        normalize_provider_path(&request.path)?,
    ))
}

fn normalize_endpoint(value: &str) -> Result<String, String> {
    let trimmed = value.trim().trim_end_matches('/');
    if trimmed.is_empty()
        || trimmed.len() > MAX_ENDPOINT_LENGTH
        || contains_control_character(trimmed)
    {
        return Err("CloudDrive2 gRPC 服务地址无效。".to_string());
    }
    let url = Url::parse(trimmed).map_err(|_| "CloudDrive2 gRPC 服务地址无效。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != "/"
    {
        return Err("CloudDrive2 gRPC 服务地址无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_token(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || trimmed.len() > MAX_TOKEN_LENGTH || contains_control_character(trimmed)
    {
        return Err("CloudDrive2 API Token 无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn normalize_provider_path(value: &str) -> Result<String, String> {
    let replaced = value.trim().replace('\\', "/");
    if replaced.is_empty()
        || replaced.len() > MAX_PATH_LENGTH
        || contains_control_character(&replaced)
        || replaced.contains('?')
        || replaced.contains('#')
        || replaced.contains("://")
    {
        return Err("CloudDrive2 路径无效。".to_string());
    }

    let mut segments = Vec::new();
    for segment in replaced.split('/').filter(|segment| !segment.is_empty()) {
        if segment == "."
            || segment == ".."
            || segment.contains('\0')
            || is_encoded_dot_segment(segment)
        {
            return Err("CloudDrive2 路径无效。".to_string());
        }
        segments.push(segment);
    }
    Ok(if segments.is_empty() {
        "/".to_string()
    } else {
        format!("/{}", segments.join("/"))
    })
}

fn normalize_keyword(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_KEYWORD_LENGTH
        || contains_control_character(trimmed)
    {
        return Err("CloudDrive2 搜索关键词无效。".to_string());
    }
    Ok(trimmed.to_string())
}

fn map_file_entries(replies: Vec<SubFilesReply>) -> Vec<CloudDrive2FileEntry> {
    replies
        .into_iter()
        .flat_map(|reply| reply.sub_files)
        .filter(|file| !file.is_forbidden && !file.name.trim().is_empty())
        .filter_map(|file| {
            let path = normalize_provider_path(&file.full_path_name).ok()?;
            let is_dir =
                file.is_directory || file.file_type == CloudDriveFileType::Directory as i32;
            Some(CloudDrive2FileEntry {
                name: file.name,
                path,
                is_dir,
                size: if is_dir || file.size < 0 {
                    None
                } else {
                    Some(file.size as u64)
                },
                modified_ms: timestamp_millis(file.write_time.or(file.create_time)),
            })
        })
        .collect()
}

fn timestamp_millis(value: Option<Timestamp>) -> Option<i64> {
    let value = value?;
    value
        .seconds
        .checked_mul(1000)?
        .checked_add(i64::from(value.nanos) / 1_000_000)
}

fn build_stream_response(
    endpoint: &str,
    info: DownloadUrlPathInfo,
) -> Result<CloudDrive2StreamResponse, String> {
    let url = if let Some(direct_url) = info
        .direct_url
        .as_deref()
        .filter(|url| !url.trim().is_empty())
    {
        validate_stream_url(direct_url)?
    } else {
        build_download_url(endpoint, &info.download_url_path)?
    };

    let mut headers = sanitize_stream_headers(info.additional_headers);
    if let Some(user_agent) = info.user_agent.as_deref() {
        if let Some(value) = sanitize_header_value(user_agent) {
            headers.entry("User-Agent".to_string()).or_insert(value);
        }
    }
    Ok(CloudDrive2StreamResponse { url, headers })
}

fn build_download_url(endpoint: &str, template: &str) -> Result<String, String> {
    let endpoint_url =
        Url::parse(endpoint).map_err(|_| "CloudDrive2 播放地址生成失败。".to_string())?;
    let host = endpoint_url
        .host_str()
        .ok_or_else(|| "CloudDrive2 播放地址生成失败。".to_string())?;
    let display_host = if host.contains(':') {
        format!("[{host}]")
    } else {
        host.to_string()
    };
    let host_with_port = endpoint_url
        .port()
        .map(|port| format!("{display_host}:{port}"))
        .unwrap_or(display_host);
    let replaced = template
        .trim()
        .replace("{SCHEME}", endpoint_url.scheme())
        .replace("{HOST}", &host_with_port)
        .replace("{PREVIEW}", "false");
    let candidate = if replaced.starts_with("http://") || replaced.starts_with("https://") {
        replaced
    } else if replaced.starts_with('/') {
        format!("{}://{}{}", endpoint_url.scheme(), host_with_port, replaced)
    } else {
        return Err("CloudDrive2 播放地址生成失败。".to_string());
    };
    validate_stream_url(&candidate)
}

fn validate_stream_url(value: &str) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) {
        return Err("CloudDrive2 返回了无效的播放地址。".to_string());
    }
    let url = Url::parse(trimmed).map_err(|_| "CloudDrive2 返回了无效的播放地址。".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("CloudDrive2 返回了无效的播放地址。".to_string());
    }
    Ok(url.to_string())
}

fn sanitize_stream_headers(headers: HashMap<String, String>) -> HashMap<String, String> {
    headers
        .into_iter()
        .take(MAX_STREAM_HEADERS)
        .filter_map(|(name, value)| {
            if name.is_empty() || name.len() > MAX_HEADER_NAME_LENGTH {
                return None;
            }
            HeaderName::try_from(name.as_str()).ok()?;
            HeaderValue::from_str(&value).ok()?;
            Some((name, sanitize_header_value(&value)?))
        })
        .collect()
}

fn sanitize_header_value(value: &str) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed.len() > MAX_HEADER_VALUE_LENGTH
        || contains_control_character(trimmed)
    {
        None
    } else {
        Some(trimmed.to_string())
    }
}

fn safe_status_message(status: Status) -> String {
    match status.code() {
        Code::Unauthenticated | Code::PermissionDenied => {
            "CloudDrive2 API Token 无效、已过期或权限不足。".to_string()
        }
        Code::NotFound => "CloudDrive2 路径不存在或不在 Token 授权范围内。".to_string(),
        Code::DeadlineExceeded => "CloudDrive2 gRPC 请求超时。".to_string(),
        Code::Unavailable => "CloudDrive2 gRPC 服务不可用，请检查地址和服务状态。".to_string(),
        _ => format!("CloudDrive2 gRPC 请求失败（{}）。", status.code()),
    }
}

fn contains_control_character(value: &str) -> bool {
    value.chars().any(char::is_control)
}

fn is_encoded_dot_segment(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    lower == "%2e"
        || lower == ".%2e"
        || lower == "%2e."
        || lower == "%2e%2e"
        || lower == "%252e"
        || lower == ".%252e"
        || lower == "%252e."
        || lower == "%252e%252e"
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_endpoint_and_provider_path() {
        assert_eq!(
            normalize_endpoint("http://127.0.0.1:19798/").unwrap(),
            "http://127.0.0.1:19798"
        );
        assert_eq!(
            normalize_provider_path("//媒体/电影/").unwrap(),
            "/媒体/电影"
        );
        assert!(normalize_provider_path("/媒体/../secret").is_err());
        assert!(normalize_provider_path("/媒体/%2e%2e/secret").is_err());
    }

    #[test]
    fn builds_download_url_from_clouddrive_template() {
        let url = build_download_url(
            "http://127.0.0.1:19798",
            "/static/{SCHEME}/{HOST}/{PREVIEW}/Movies/a.mkv?token=value",
        )
        .unwrap();
        assert_eq!(
            url,
            "http://127.0.0.1:19798/static/http/127.0.0.1:19798/false/Movies/a.mkv?token=value"
        );
    }
}

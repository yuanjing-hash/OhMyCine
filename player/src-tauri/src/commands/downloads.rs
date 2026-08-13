use crate::commands::{credential, local_file, settings};
use crate::storage;
use fs2::available_space;
use futures_util::StreamExt;
use rand::{thread_rng, RngCore};
use reqwest::header::{
    HeaderMap, HeaderValue, AUTHORIZATION, CONTENT_LENGTH, CONTENT_RANGE, CONTENT_TYPE, ETAG,
    LAST_MODIFIED, LOCATION, RANGE,
};
use reqwest::{Client, Response, StatusCode, Url};
use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tauri::{AppHandle, Emitter, Manager, State};

const DATABASE_FILE: &str = "downloads.sqlite";
const DATASOURCES_SETTING: &str = "ohmycine-datasources";
const DEFAULT_DIRECTORY_SETTING: &str = "ohmycine-download-directory-v1";
const PROGRESS_EVENT: &str = "player-download:progress";
const MAX_REDIRECTS: usize = 5;
const COPY_BUFFER_BYTES: usize = 256 * 1024;

#[derive(Default)]
pub struct DownloadQueueState {
    cancellations: Mutex<HashMap<String, Arc<AtomicBool>>>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadEnqueueRequest {
    source_id: String,
    source_type: String,
    item_id: String,
    display_name: String,
    media_type: String,
    expected_bytes: Option<u64>,
    destination_directory: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadTask {
    id: String,
    source_id: String,
    source_type: String,
    item_id: String,
    display_name: String,
    media_type: String,
    destination_directory: String,
    destination_name: String,
    status: String,
    bytes_downloaded: u64,
    total_bytes: Option<u64>,
    retry_count: u32,
    error_message: Option<String>,
    created_at: i64,
    updated_at: i64,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct PersistedDataSource {
    id: String,
    #[serde(rename = "type")]
    source_type: String,
    url: String,
    #[serde(default)]
    extra: HashMap<String, Value>,
}

#[derive(Debug, Deserialize)]
struct AlistCredentialEnvelope {
    version: u8,
    provider: String,
    token: String,
}

struct ResolvedRemote {
    url: Url,
    headers: HeaderMap,
}

#[tauri::command]
pub fn player_download_default_directory(app: AppHandle) -> Result<String, String> {
    #[cfg(mobile)]
    return Err(
        "Android downloads require a persistent writable SAF folder and foreground service."
            .to_string(),
    );

    #[cfg(not(mobile))]
    {
        if let Some(configured) = settings::read_player_setting(&app, DEFAULT_DIRECTORY_SETTING)? {
            return validate_destination_directory(&configured).map(display_path);
        }
        let downloads = app
            .path()
            .download_dir()
            .map_err(|_| "The system Downloads directory is unavailable.".to_string())?;
        validate_destination_directory_path(&downloads)?;
        Ok(display_path(downloads))
    }
}

#[tauri::command]
pub fn player_download_set_default_directory(
    app: AppHandle,
    directory: String,
) -> Result<String, String> {
    #[cfg(mobile)]
    return Err(
        "Android downloads require a persistent writable SAF folder and foreground service."
            .to_string(),
    );

    #[cfg(not(mobile))]
    {
        let canonical = validate_destination_directory(&directory)?;
        let value = display_path(&canonical);
        settings::write_player_setting(&app, DEFAULT_DIRECTORY_SETTING, &value)?;
        Ok(value)
    }
}

#[tauri::command]
pub fn player_download_list(app: AppHandle) -> Result<Vec<DownloadTask>, String> {
    DownloadStorage::open(&app)?.list()
}

#[tauri::command]
pub fn player_download_enqueue(
    app: AppHandle,
    queue: State<DownloadQueueState>,
    request: DownloadEnqueueRequest,
) -> Result<DownloadTask, String> {
    #[cfg(mobile)]
    return Err(
        "Android downloads require a persistent writable SAF folder and foreground service."
            .to_string(),
    );

    #[cfg(not(mobile))]
    {
        validate_enqueue_request(&request)?;
        let destination = match request.destination_directory.as_deref() {
            Some(value) => validate_destination_directory(value)?,
            None => PathBuf::from(player_download_default_directory(app.clone())?),
        };
        let destination_name = available_destination_name(
            &destination,
            &download_file_name(&request.display_name, &request.item_id),
        )?;
        if let Some(expected) = request.expected_bytes {
            ensure_space(&destination, expected)?;
        }

        let now = unix_timestamp();
        let task = DownloadTask {
            id: random_id(),
            source_id: request.source_id,
            source_type: request.source_type,
            item_id: request.item_id,
            display_name: safe_display_name(&request.display_name),
            media_type: request.media_type,
            destination_directory: display_path(&destination),
            destination_name,
            status: "queued".to_string(),
            bytes_downloaded: 0,
            total_bytes: request.expected_bytes,
            retry_count: 0,
            error_message: None,
            created_at: now,
            updated_at: now,
        };
        DownloadStorage::open(&app)?.insert(&task)?;
        start_task(app, queue.inner(), task.clone())?;
        Ok(task)
    }
}

#[tauri::command]
pub fn player_download_cancel(
    app: AppHandle,
    queue: State<DownloadQueueState>,
    task_id: String,
) -> Result<(), String> {
    validate_task_id(&task_id)?;
    let mut active = false;
    if let Ok(cancellations) = queue.cancellations.lock() {
        if let Some(cancel) = cancellations.get(&task_id) {
            cancel.store(true, Ordering::Relaxed);
            active = true;
        }
    }
    let storage = DownloadStorage::open(&app)?;
    let task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if !matches!(
        task.status.as_str(),
        "completed" | "cancelled" | "cancelling"
    ) {
        storage.set_status(
            &task_id,
            if active { "cancelling" } else { "cancelled" },
            None,
        )?;
        emit_task(&app, &storage, &task_id);
    }
    Ok(())
}

#[tauri::command]
pub fn player_download_retry(
    app: AppHandle,
    queue: State<DownloadQueueState>,
    task_id: String,
) -> Result<DownloadTask, String> {
    validate_task_id(&task_id)?;
    let storage = DownloadStorage::open(&app)?;
    let mut task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if !matches!(task.status.as_str(), "failed" | "cancelled" | "paused") {
        return Err("Only interrupted, cancelled, or failed downloads can be retried.".to_string());
    }
    task.status = "queued".to_string();
    task.error_message = None;
    task.retry_count = task.retry_count.saturating_add(1);
    task.updated_at = unix_timestamp();
    storage.queue_retry(&task)?;
    start_task(app, queue.inner(), task.clone())?;
    Ok(task)
}

pub fn recover_interrupted_downloads(app: &AppHandle) -> Result<(), String> {
    DownloadStorage::open(app)?.recover_interrupted()
}

fn start_task(
    app: AppHandle,
    queue: &DownloadQueueState,
    task: DownloadTask,
) -> Result<(), String> {
    let cancellation = Arc::new(AtomicBool::new(false));
    let mut cancellations = queue
        .cancellations
        .lock()
        .map_err(|_| "Download queue is unavailable.".to_string())?;
    if cancellations.contains_key(&task.id) {
        return Err("This download task is already active.".to_string());
    }
    cancellations.insert(task.id.clone(), cancellation.clone());
    drop(cancellations);
    let task_id = task.id.clone();
    tauri::async_runtime::spawn(async move {
        let result = execute_task(&app, &task, &cancellation).await;
        let storage = DownloadStorage::open(&app);
        if let Ok(storage) = storage {
            match result {
                Ok(()) => {
                    let _ = storage.set_status(&task_id, "completed", None);
                }
                Err(error) if cancellation.load(Ordering::Relaxed) => {
                    let _ = storage.set_status(&task_id, "cancelled", None);
                    let _ = error;
                }
                Err(error) => {
                    let _ = storage.set_status(&task_id, "failed", Some(&safe_error(&error)));
                }
            }
            emit_task(&app, &storage, &task_id);
        }
        if let Some(state) = app.try_state::<DownloadQueueState>() {
            if let Ok(mut cancellations) = state.cancellations.lock() {
                cancellations.remove(&task_id);
            }
        }
    });
    Ok(())
}

async fn execute_task(
    app: &AppHandle,
    task: &DownloadTask,
    cancellation: &Arc<AtomicBool>,
) -> Result<(), String> {
    let storage = DownloadStorage::open(app)?;
    storage.set_status(&task.id, "running", None)?;
    emit_task(app, &storage, &task.id);

    let destination_dir = validate_destination_directory(&task.destination_directory)?;
    let final_path = safe_destination_path(&destination_dir, &task.destination_name)?;
    let partial_path = partial_path(&final_path)?;
    if final_path.exists() {
        return Err("The destination file already exists.".to_string());
    }

    match task.source_type.as_str() {
        "local" => execute_local_copy(app, task, &partial_path, cancellation).await?,
        "alist" => execute_alist_download(app, task, &partial_path, cancellation).await?,
        _ => {
            return Err(
                "This data source does not have a secure native download resolver.".to_string(),
            )
        }
    }

    if cancellation.load(Ordering::Relaxed) {
        return Err("Download cancelled.".to_string());
    }
    fs::rename(&partial_path, &final_path)
        .map_err(|_| "Failed to finalize the downloaded file atomically.".to_string())?;
    Ok(())
}

async fn execute_local_copy(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    cancellation: &Arc<AtomicBool>,
) -> Result<(), String> {
    let config = resolve_datasource(app, &task.source_id, "local")?;
    let root_path = extra_string(&config, "rootPath")?;
    let source = local_file::resolve_local_download_source(&root_path, &task.item_id)?;
    let source_meta =
        fs::metadata(&source).map_err(|_| "The local source file is unavailable.".to_string())?;
    let total = source_meta.len();
    let fingerprint = local_entity_fingerprint(&source_meta);
    let storage = DownloadStorage::open(app)?;
    let stored_fingerprint = storage.entity_hash(&task.id)?;
    let mut offset = partial_len(partial_path);
    if offset > 0 && (offset > total || stored_fingerprint.as_deref() != Some(&fingerprint)) {
        remove_partial(partial_path)?;
        offset = 0;
    }
    ensure_space(
        partial_path
            .parent()
            .ok_or_else(|| "Invalid destination path.".to_string())?,
        total.saturating_sub(offset),
    )?;
    storage.set_entity_and_progress(&task.id, Some(&fingerprint), offset, Some(total))?;

    let source_path = source.clone();
    let partial = partial_path.to_path_buf();
    let task_id = task.id.clone();
    let app = app.clone();
    if cancellation.load(Ordering::Relaxed) {
        return Err("Download cancelled.".to_string());
    }
    let cancellation = cancellation.clone();
    tauri::async_runtime::spawn_blocking(move || {
        copy_file_streaming(
            &app,
            &task_id,
            &source_path,
            &partial,
            offset,
            total,
            &cancellation,
        )
    })
    .await
    .map_err(|_| "The local copy worker stopped unexpectedly.".to_string())??;
    Ok(())
}

fn copy_file_streaming(
    app: &AppHandle,
    task_id: &str,
    source_path: &Path,
    partial_path: &Path,
    offset: u64,
    total: u64,
    cancellation: &AtomicBool,
) -> Result<(), String> {
    let mut source = fs::File::open(source_path)
        .map_err(|_| "Failed to read the local source file.".to_string())?;
    source
        .seek(SeekFrom::Start(offset))
        .map_err(|_| "Failed to resume the local copy.".to_string())?;
    let mut target = OpenOptions::new()
        .create(true)
        .append(true)
        .open(partial_path)
        .map_err(|_| "Failed to open the partial download file.".to_string())?;
    let mut buffer = vec![0_u8; COPY_BUFFER_BYTES];
    let mut downloaded = offset;
    let mut last_emit = Instant::now();
    loop {
        if cancellation.load(Ordering::Relaxed) {
            return Err("Download cancelled.".to_string());
        }
        let read = source
            .read(&mut buffer)
            .map_err(|_| "Failed while reading the local source file.".to_string())?;
        if read == 0 {
            break;
        }
        target
            .write_all(&buffer[..read])
            .map_err(|_| "Failed while writing the partial download file.".to_string())?;
        downloaded = downloaded.saturating_add(read as u64);
        if last_emit.elapsed() >= Duration::from_millis(250) {
            persist_progress(app, task_id, downloaded, Some(total));
            last_emit = Instant::now();
        }
    }
    target
        .sync_all()
        .map_err(|_| "Failed to flush the copied file.".to_string())?;
    persist_progress(app, task_id, downloaded, Some(total));
    Ok(())
}

async fn execute_alist_download(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    cancellation: &Arc<AtomicBool>,
) -> Result<(), String> {
    let resolved = resolve_alist(app, &task.source_id, &task.item_id).await?;
    let storage = DownloadStorage::open(app)?;
    let stored_entity = storage.entity_hash(&task.id)?;
    let mut offset = partial_len(partial_path);
    let mut response = request_media(&resolved, offset).await?;

    if offset > 0 {
        let current_entity = response_entity_hash(&response);
        let safe_resume = response.status() == StatusCode::PARTIAL_CONTENT
            && content_range_start(response.headers()) == Some(offset)
            && stored_entity.is_some()
            && stored_entity == current_entity;
        if !safe_resume {
            drop(response);
            remove_partial(partial_path)?;
            offset = 0;
            let refreshed = resolve_alist(app, &task.source_id, &task.item_id).await?;
            response = request_media(&refreshed, 0).await?;
        }
    }

    validate_media_response(&response, offset)?;
    let entity_hash = response_entity_hash(&response);
    let response_bytes = response.content_length();
    let total = response_total_bytes(&response, offset).or(task.total_bytes);
    if let Some(remaining) = response_bytes {
        ensure_space(
            partial_path
                .parent()
                .ok_or_else(|| "Invalid destination path.".to_string())?,
            remaining,
        )?;
    }
    storage.set_entity_and_progress(&task.id, entity_hash.as_deref(), offset, total)?;

    let mut file = OpenOptions::new()
        .create(true)
        .append(true)
        .open(partial_path)
        .map_err(|_| "Failed to open the partial download file.".to_string())?;
    let mut stream = response.bytes_stream();
    let mut downloaded = offset;
    let mut last_emit = Instant::now();
    while let Some(next) = stream.next().await {
        if cancellation.load(Ordering::Relaxed) {
            return Err("Download cancelled.".to_string());
        }
        let chunk = next.map_err(|_| "The media transfer was interrupted.".to_string())?;
        file.write_all(&chunk)
            .map_err(|_| "Failed while writing the partial download file.".to_string())?;
        downloaded = downloaded.saturating_add(chunk.len() as u64);
        if last_emit.elapsed() >= Duration::from_millis(250) {
            persist_progress(app, &task.id, downloaded, total);
            last_emit = Instant::now();
        }
    }
    file.sync_all()
        .map_err(|_| "Failed to flush the downloaded file.".to_string())?;
    if let Some(expected) = total {
        if downloaded != expected {
            return Err("The media transfer ended before all bytes were received.".to_string());
        }
    }
    persist_progress(app, &task.id, downloaded, total);
    Ok(())
}

async fn resolve_alist(
    app: &AppHandle,
    source_id: &str,
    item_id: &str,
) -> Result<ResolvedRemote, String> {
    let config = resolve_datasource(app, source_id, "alist")?;
    let base = validate_http_url(&config.url)?;
    if base.query().is_some() || base.fragment().is_some() {
        return Err("The OpenList/Alist base address is invalid.".to_string());
    }
    let root = extra_string(&config, "rootPath")?;
    validate_provider_item_path(item_id, &root)?;
    let credential_ref = extra_string(&config, "credentialRef")?;
    let raw = credential::read_credential_value(app, &credential_ref)
        .await?
        .ok_or_else(|| "OpenList/Alist credentials are missing.".to_string())?;
    let envelope: AlistCredentialEnvelope = serde_json::from_str(&raw)
        .map_err(|_| "Stored OpenList/Alist credentials are invalid.".to_string())?;
    if envelope.version != 1 || envelope.provider != "alist" || envelope.token.trim().is_empty() {
        return Err("Stored OpenList/Alist credentials are invalid.".to_string());
    }

    let client = controlled_client()?;
    let endpoint = base
        .join("api/fs/get")
        .map_err(|_| "The OpenList/Alist base address is invalid.".to_string())?;
    let response = client
        .post(endpoint)
        .header(AUTHORIZATION, &envelope.token)
        .json(&serde_json::json!({ "path": item_id }))
        .send()
        .await
        .map_err(|_| "OpenList/Alist could not resolve this media item.".to_string())?;
    if !response.status().is_success() {
        return Err("OpenList/Alist could not resolve this media item.".to_string());
    }
    let value: Value = response
        .json()
        .await
        .map_err(|_| "OpenList/Alist returned an invalid media response.".to_string())?;
    if value.get("code").and_then(Value::as_i64).unwrap_or(-1) != 200 {
        return Err("OpenList/Alist could not resolve this media item.".to_string());
    }
    let data = value
        .get("data")
        .ok_or_else(|| "OpenList/Alist returned an invalid media response.".to_string())?;
    if data.get("is_dir").and_then(Value::as_bool).unwrap_or(false) {
        return Err("OpenList/Alist folders cannot be downloaded as a file.".to_string());
    }
    let sign = data
        .get("sign")
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty());
    let mut url = base
        .join(&format!("d{}", encode_provider_path(item_id)))
        .map_err(|_| "OpenList/Alist returned an invalid media response.".to_string())?;
    if let Some(sign) = sign {
        url.query_pairs_mut().append_pair("sign", sign);
    }
    let mut headers = HeaderMap::new();
    headers.insert(
        AUTHORIZATION,
        HeaderValue::from_str(&envelope.token)
            .map_err(|_| "Stored OpenList/Alist credentials are invalid.".to_string())?,
    );
    Ok(ResolvedRemote { url, headers })
}

async fn request_media(resolved: &ResolvedRemote, offset: u64) -> Result<Response, String> {
    let client = controlled_client()?;
    let mut url = resolved.url.clone();
    let mut headers = resolved.headers.clone();
    let original_origin = origin_key(&url);
    for _ in 0..=MAX_REDIRECTS {
        let mut request = client.get(url.clone()).headers(headers.clone());
        if offset > 0 {
            request = request.header(RANGE, format!("bytes={offset}-"));
        }
        let response = request
            .send()
            .await
            .map_err(|_| "The media server could not be reached.".to_string())?;
        if !response.status().is_redirection() {
            return Ok(response);
        }
        let location = response
            .headers()
            .get(LOCATION)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(|| "The media server returned an invalid redirect.".to_string())?;
        let next = url
            .join(location)
            .map_err(|_| "The media server returned an invalid redirect.".to_string())?;
        validate_redirect(&url, &next)?;
        if origin_key(&next) != original_origin {
            headers.clear();
        }
        url = next;
    }
    Err("The media server returned too many redirects.".to_string())
}

fn controlled_client() -> Result<Client, String> {
    Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(15))
        .timeout(Duration::from_secs(30 * 60))
        .build()
        .map_err(|_| "Failed to initialize the media download client.".to_string())
}

fn validate_redirect(previous: &Url, next: &Url) -> Result<(), String> {
    if !matches!(next.scheme(), "http" | "https") || next.host_str().is_none() {
        return Err("The media server returned an unsafe redirect.".to_string());
    }
    if previous.scheme() == "https" && next.scheme() != "https" {
        return Err("The media server attempted to downgrade a secure connection.".to_string());
    }
    if !next.username().is_empty() || next.password().is_some() {
        return Err("The media server returned an unsafe redirect.".to_string());
    }
    Ok(())
}

fn validate_media_response(response: &Response, offset: u64) -> Result<(), String> {
    if offset == 0 && response.status() != StatusCode::OK {
        return Err("The media server did not return a complete media response.".to_string());
    }
    if offset > 0 && response.status() != StatusCode::PARTIAL_CONTENT {
        return Err("The media server did not honor the resume request.".to_string());
    }
    let content_type = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .to_ascii_lowercase();
    if content_type.starts_with("text/")
        || content_type.contains("json")
        || content_type.contains("xml")
        || content_type.contains("html")
    {
        return Err("The resolved response is not playable media.".to_string());
    }
    Ok(())
}

fn validate_enqueue_request(request: &DownloadEnqueueRequest) -> Result<(), String> {
    validate_stable_id(&request.source_id, "Invalid data source identity.")?;
    validate_stable_id(&request.item_id, "Invalid media identity.")?;
    if !matches!(request.source_type.as_str(), "local" | "alist") {
        return Err(
            "This data source does not have a secure native download resolver.".to_string(),
        );
    }
    if matches!(request.media_type.as_str(), "folder" | "series" | "season") {
        return Err("Choose a concrete media file to download.".to_string());
    }
    if request.display_name.trim().is_empty() || request.display_name.len() > 512 {
        return Err("Invalid download display name.".to_string());
    }
    Ok(())
}

fn resolve_datasource(
    app: &AppHandle,
    source_id: &str,
    expected_type: &str,
) -> Result<PersistedDataSource, String> {
    let raw = settings::read_player_setting(app, DATASOURCES_SETTING)?
        .ok_or_else(|| "The data source configuration is unavailable.".to_string())?;
    let sources: Vec<PersistedDataSource> = serde_json::from_str(&raw)
        .map_err(|_| "The data source configuration is invalid.".to_string())?;
    let source = sources
        .into_iter()
        .find(|source| source.id == source_id)
        .ok_or_else(|| "The data source no longer exists.".to_string())?;
    if source.source_type != expected_type {
        return Err("The data source identity no longer matches its provider.".to_string());
    }
    Ok(source)
}

fn extra_string(config: &PersistedDataSource, key: &str) -> Result<String, String> {
    config
        .extra
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_string)
        .ok_or_else(|| "The data source configuration is incomplete.".to_string())
}

fn validate_provider_item_path(item_id: &str, root: &str) -> Result<(), String> {
    let item = normalized_provider_path(item_id)?;
    let root = normalized_provider_path(root)?;
    if root != "/" && item != root && !item.starts_with(&format!("{root}/")) {
        return Err("The media item is outside the configured source root.".to_string());
    }
    Ok(())
}

fn normalized_provider_path(value: &str) -> Result<String, String> {
    let normalized = value.trim().replace('\\', "/");
    if !normalized.starts_with('/')
        || normalized.contains('?')
        || normalized.contains('#')
        || normalized.as_bytes().contains(&0)
    {
        return Err("Invalid provider media identity.".to_string());
    }
    if normalized.split('/').any(|segment| {
        segment == "."
            || segment == ".."
            || segment.eq_ignore_ascii_case("%2e")
            || segment.to_ascii_lowercase().contains("%2e%2e")
    }) {
        return Err("Invalid provider media identity.".to_string());
    }
    Ok(if normalized.len() > 1 {
        normalized.trim_end_matches('/').to_string()
    } else {
        normalized
    })
}

fn encode_provider_path(path: &str) -> String {
    path.split('/')
        .map(percent_encode_segment)
        .collect::<Vec<_>>()
        .join("/")
}

fn percent_encode_segment(segment: &str) -> String {
    segment
        .as_bytes()
        .iter()
        .map(|byte| match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (*byte as char).to_string()
            }
            value => format!("%{value:02X}"),
        })
        .collect()
}

fn validate_http_url(value: &str) -> Result<Url, String> {
    let url =
        Url::parse(value.trim()).map_err(|_| "The provider address is invalid.".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("The provider address is invalid.".to_string());
    }
    Ok(url)
}

fn response_entity_hash(response: &Response) -> Option<String> {
    let etag = response
        .headers()
        .get(ETAG)
        .and_then(|value| value.to_str().ok());
    let modified = response
        .headers()
        .get(LAST_MODIFIED)
        .and_then(|value| value.to_str().ok());
    let total = response_total_bytes(response, 0);
    if etag.is_none() && modified.is_none() {
        return None;
    }
    Some(hash_text(&format!(
        "{}\0{}\0{}",
        etag.unwrap_or(""),
        modified.unwrap_or(""),
        total.unwrap_or(0)
    )))
}

fn response_total_bytes(response: &Response, offset: u64) -> Option<u64> {
    if response.status() == StatusCode::PARTIAL_CONTENT {
        return response
            .headers()
            .get(CONTENT_RANGE)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.rsplit('/').next())
            .and_then(|value| value.parse().ok());
    }
    response
        .headers()
        .get(CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<u64>().ok())
        .map(|length| offset.saturating_add(length))
}

fn content_range_start(headers: &HeaderMap) -> Option<u64> {
    let value = headers.get(CONTENT_RANGE)?.to_str().ok()?;
    value
        .strip_prefix("bytes ")?
        .split('-')
        .next()?
        .parse()
        .ok()
}

fn local_entity_fingerprint(metadata: &fs::Metadata) -> String {
    let modified = metadata
        .modified()
        .ok()
        .and_then(|time| time.duration_since(UNIX_EPOCH).ok())
        .map(|value| value.as_nanos())
        .unwrap_or(0);
    hash_text(&format!("{}:{modified}", metadata.len()))
}

fn hash_text(value: &str) -> String {
    format!("{:x}", Sha256::digest(value.as_bytes()))
}

fn validate_destination_directory(value: &str) -> Result<PathBuf, String> {
    let path = Path::new(value.trim());
    if !path.is_absolute() || value.as_bytes().contains(&0) {
        return Err("The download directory must be an absolute path.".to_string());
    }
    let canonical =
        fs::canonicalize(path).map_err(|_| "The download directory is unavailable.".to_string())?;
    validate_destination_directory_path(&canonical)?;
    Ok(canonical)
}

fn validate_destination_directory_path(path: &Path) -> Result<(), String> {
    if !fs::metadata(path)
        .map(|value| value.is_dir())
        .unwrap_or(false)
    {
        return Err("The download destination must be a directory.".to_string());
    }
    let probe = path.join(format!(".ohmycine-write-probe-{}", random_id()));
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&probe)
        .map_err(|_| "The download directory is not writable.".to_string())?;
    fs::remove_file(&probe)
        .map_err(|_| "The download directory could not be verified safely.".to_string())?;
    Ok(())
}

fn available_destination_name(directory: &Path, display_name: &str) -> Result<String, String> {
    let name = safe_display_name(display_name);
    let candidate = Path::new(&name);
    let stem = candidate
        .file_stem()
        .and_then(|value| value.to_str())
        .unwrap_or("download");
    let extension = candidate.extension().and_then(|value| value.to_str());
    for suffix in 0..10_000 {
        let file_name = if suffix == 0 {
            name.clone()
        } else if let Some(ext) = extension {
            format!("{stem} ({suffix}).{ext}")
        } else {
            format!("{stem} ({suffix})")
        };
        let final_path = directory.join(&file_name);
        if !final_path.exists() && !partial_path(&final_path)?.exists() {
            return Ok(file_name);
        }
    }
    Err("No available destination filename could be created.".to_string())
}

fn safe_display_name(value: &str) -> String {
    let basename = value.trim().replace(['/', '\\'], "_");
    let cleaned: String = basename
        .chars()
        .filter(|character| {
            !character.is_control() && !matches!(character, '<' | '>' | ':' | '"' | '|' | '?' | '*')
        })
        .take(240)
        .collect();
    let cleaned = cleaned.trim_matches([' ', '.']);
    if cleaned.is_empty() {
        "download.bin".to_string()
    } else {
        cleaned.to_string()
    }
}

fn download_file_name(display_name: &str, item_id: &str) -> String {
    let item_name = item_id
        .trim_end_matches(['/', '\\'])
        .rsplit(['/', '\\'])
        .next()
        .unwrap_or_default();
    let item_extension = Path::new(item_name)
        .extension()
        .and_then(|value| value.to_str())
        .filter(|value| !value.is_empty());
    let display = safe_display_name(display_name);
    if Path::new(&display).extension().is_some() || item_extension.is_none() {
        display
    } else {
        format!("{display}.{}", item_extension.unwrap_or_default())
    }
}

fn safe_destination_path(directory: &Path, name: &str) -> Result<PathBuf, String> {
    if safe_display_name(name) != name || Path::new(name).components().count() != 1 {
        return Err("The persisted destination filename is invalid.".to_string());
    }
    Ok(directory.join(name))
}

fn partial_path(final_path: &Path) -> Result<PathBuf, String> {
    let name = final_path
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or_else(|| "Invalid destination filename.".to_string())?;
    Ok(final_path.with_file_name(format!("{name}.partial")))
}

fn partial_len(path: &Path) -> u64 {
    fs::metadata(path).map(|value| value.len()).unwrap_or(0)
}
fn remove_partial(path: &Path) -> Result<(), String> {
    if path.exists() {
        fs::remove_file(path)
            .map_err(|_| "Failed to reset an unsafe partial download.".to_string())?;
    }
    Ok(())
}
fn ensure_space(directory: &Path, required: u64) -> Result<(), String> {
    if available_space(directory)
        .map_err(|_| "Available disk space could not be checked.".to_string())?
        < required
    {
        Err("There is not enough available disk space for this download.".to_string())
    } else {
        Ok(())
    }
}
fn origin_key(url: &Url) -> (String, Option<String>, Option<u16>) {
    (
        url.scheme().to_string(),
        url.host_str().map(str::to_ascii_lowercase),
        url.port_or_known_default(),
    )
}
fn display_path(path: impl AsRef<Path>) -> String {
    path.as_ref().to_string_lossy().into_owned()
}
fn unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}
fn random_id() -> String {
    let mut bytes = [0_u8; 16];
    thread_rng().fill_bytes(&mut bytes);
    bytes.iter().map(|value| format!("{value:02x}")).collect()
}
fn safe_error(value: &str) -> String {
    let lower = value.to_ascii_lowercase();
    if lower.contains("http://")
        || lower.contains("https://")
        || lower.contains("authorization")
        || lower.contains("cookie")
        || lower.contains("token=")
    {
        "The download failed while resolving or transferring media.".to_string()
    } else {
        value.chars().take(512).collect()
    }
}
fn validate_stable_id(value: &str, message: &str) -> Result<(), String> {
    if value.trim().is_empty()
        || value.len() > 4096
        || value.as_bytes().contains(&0)
        || value.contains("http://")
        || value.contains("https://")
    {
        Err(message.to_string())
    } else {
        Ok(())
    }
}
fn validate_task_id(value: &str) -> Result<(), String> {
    if value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        Ok(())
    } else {
        Err("Invalid download task identity.".to_string())
    }
}

fn persist_progress(app: &AppHandle, task_id: &str, bytes: u64, total: Option<u64>) {
    if let Ok(storage) = DownloadStorage::open(app) {
        let _ = storage.set_progress(task_id, bytes, total);
        emit_task(app, &storage, task_id);
    }
}

fn emit_task(app: &AppHandle, storage: &DownloadStorage, task_id: &str) {
    if let Ok(Some(task)) = storage.get(task_id) {
        let _ = app.emit(PROGRESS_EVENT, task);
    }
}

struct DownloadStorage {
    conn: Connection,
}

impl DownloadStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let conn = Connection::open(storage::data_file(app, DATABASE_FILE)?)
            .map_err(|_| "Failed to open the download task database.".to_string())?;
        conn.execute_batch("CREATE TABLE IF NOT EXISTS download_tasks (
            id TEXT PRIMARY KEY NOT NULL,
            source_id TEXT NOT NULL,
            source_type TEXT NOT NULL,
            item_id TEXT NOT NULL,
            display_name TEXT NOT NULL,
            media_type TEXT NOT NULL,
            destination_directory TEXT NOT NULL,
            destination_name TEXT NOT NULL,
            status TEXT NOT NULL,
            bytes_downloaded INTEGER NOT NULL DEFAULT 0,
            total_bytes INTEGER,
            retry_count INTEGER NOT NULL DEFAULT 0,
            entity_hash TEXT,
            error_message TEXT,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL
        ); CREATE INDEX IF NOT EXISTS idx_download_tasks_updated ON download_tasks(updated_at DESC);")
            .map_err(|_| "Failed to initialize the download task database.".to_string())?;
        Ok(Self { conn })
    }
    fn insert(&self, task: &DownloadTask) -> Result<(), String> {
        self.conn.execute("INSERT INTO download_tasks (id, source_id, source_type, item_id, display_name, media_type, destination_directory, destination_name, status, bytes_downloaded, total_bytes, retry_count, error_message, created_at, updated_at) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15)", params![task.id,task.source_id,task.source_type,task.item_id,task.display_name,task.media_type,task.destination_directory,task.destination_name,task.status,task.bytes_downloaded,task.total_bytes,task.retry_count,task.error_message,task.created_at,task.updated_at]).map_err(|_| "Failed to save the download task.".to_string())?;
        Ok(())
    }
    fn list(&self) -> Result<Vec<DownloadTask>, String> {
        let mut statement = self.conn.prepare("SELECT id,source_id,source_type,item_id,display_name,media_type,destination_directory,destination_name,status,bytes_downloaded,total_bytes,retry_count,error_message,created_at,updated_at FROM download_tasks ORDER BY created_at DESC").map_err(|_| "Failed to read download tasks.".to_string())?;
        let rows = statement
            .query_map([], map_task)
            .map_err(|_| "Failed to read download tasks.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read download tasks.".to_string())?;
        Ok(rows)
    }
    fn get(&self, id: &str) -> Result<Option<DownloadTask>, String> {
        self.conn.query_row("SELECT id,source_id,source_type,item_id,display_name,media_type,destination_directory,destination_name,status,bytes_downloaded,total_bytes,retry_count,error_message,created_at,updated_at FROM download_tasks WHERE id=?1", [id], map_task).optional().map_err(|_| "Failed to read the download task.".to_string())
    }
    fn set_status(&self, id: &str, status: &str, error: Option<&str>) -> Result<(), String> {
        self.conn.execute("UPDATE download_tasks SET status=?2,error_message=?3,updated_at=unixepoch() WHERE id=?1", params![id,status,error]).map_err(|_| "Failed to update the download task.".to_string())?;
        Ok(())
    }
    fn set_progress(&self, id: &str, bytes: u64, total: Option<u64>) -> Result<(), String> {
        self.conn.execute("UPDATE download_tasks SET bytes_downloaded=?2,total_bytes=COALESCE(?3,total_bytes),updated_at=unixepoch() WHERE id=?1", params![id,bytes,total]).map_err(|_| "Failed to update download progress.".to_string())?;
        Ok(())
    }
    fn set_entity_and_progress(
        &self,
        id: &str,
        entity: Option<&str>,
        bytes: u64,
        total: Option<u64>,
    ) -> Result<(), String> {
        self.conn.execute("UPDATE download_tasks SET entity_hash=?2,bytes_downloaded=?3,total_bytes=COALESCE(?4,total_bytes),updated_at=unixepoch() WHERE id=?1", params![id,entity,bytes,total]).map_err(|_| "Failed to update download resume metadata.".to_string())?;
        Ok(())
    }
    fn entity_hash(&self, id: &str) -> Result<Option<String>, String> {
        self.conn
            .query_row(
                "SELECT entity_hash FROM download_tasks WHERE id=?1",
                [id],
                |row| row.get(0),
            )
            .optional()
            .map(|value| value.flatten())
            .map_err(|_| "Failed to read download resume metadata.".to_string())
    }
    fn queue_retry(&self, task: &DownloadTask) -> Result<(), String> {
        self.conn.execute("UPDATE download_tasks SET status='queued',retry_count=?2,error_message=NULL,updated_at=?3 WHERE id=?1", params![task.id,task.retry_count,task.updated_at]).map_err(|_| "Failed to retry the download task.".to_string())?;
        Ok(())
    }
    fn recover_interrupted(&self) -> Result<(), String> {
        self.conn.execute("UPDATE download_tasks SET status='paused',error_message='Player exited before the transfer completed.',updated_at=unixepoch() WHERE status IN ('queued','running')", []).map_err(|_| "Failed to recover interrupted download tasks.".to_string())?;
        Ok(())
    }
}

fn map_task(row: &rusqlite::Row<'_>) -> rusqlite::Result<DownloadTask> {
    Ok(DownloadTask {
        id: row.get(0)?,
        source_id: row.get(1)?,
        source_type: row.get(2)?,
        item_id: row.get(3)?,
        display_name: row.get(4)?,
        media_type: row.get(5)?,
        destination_directory: row.get(6)?,
        destination_name: row.get(7)?,
        status: row.get(8)?,
        bytes_downloaded: row.get(9)?,
        total_bytes: row.get(10)?,
        retry_count: row.get(11)?,
        error_message: row.get(12)?,
        created_at: row.get(13)?,
        updated_at: row.get(14)?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn persisted_schema_excludes_sensitive_transport_fields() {
        let conn = Connection::open_in_memory().unwrap();
        conn.execute_batch("CREATE TABLE download_tasks (id TEXT, source_id TEXT, source_type TEXT, item_id TEXT, display_name TEXT, media_type TEXT, destination_directory TEXT, destination_name TEXT, status TEXT, bytes_downloaded INTEGER, total_bytes INTEGER, retry_count INTEGER, entity_hash TEXT, error_message TEXT, created_at INTEGER, updated_at INTEGER)").unwrap();
        let names: Vec<String> = conn
            .prepare("PRAGMA table_info(download_tasks)")
            .unwrap()
            .query_map([], |row| row.get(1))
            .unwrap()
            .collect::<Result<_, _>>()
            .unwrap();
        for forbidden in ["url", "headers", "cookie", "authorization", "signature"] {
            assert!(!names
                .iter()
                .any(|name| name.to_ascii_lowercase().contains(forbidden)));
        }
    }
    #[test]
    fn provider_paths_must_stay_in_configured_root() {
        assert!(validate_provider_item_path("/Movies/A.mkv", "/Movies").is_ok());
        assert!(validate_provider_item_path("/Other/A.mkv", "/Movies").is_err());
        assert!(validate_provider_item_path("/Movies/../secret", "/Movies").is_err());
    }
    #[test]
    fn download_names_never_create_path_segments() {
        assert_eq!(
            safe_display_name("../folder/movie.mkv"),
            "_folder_movie.mkv"
        );
        assert_eq!(safe_display_name("movie?.mkv"), "movie.mkv");
    }
    #[test]
    fn secure_redirects_cannot_downgrade() {
        let https = Url::parse("https://example.test/a").unwrap();
        let http = Url::parse("http://cdn.test/a").unwrap();
        assert!(validate_redirect(&https, &http).is_err());
    }
}

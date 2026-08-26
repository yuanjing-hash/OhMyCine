#[cfg(target_os = "android")]
use crate::commands::download_android;
use crate::commands::{credential, local_file, provider_file, settings};
use crate::storage;
use base64::{engine::general_purpose::STANDARD as BASE64_STANDARD, Engine};
use fs2::available_space;
use futures_util::{future::try_join_all, StreamExt};
use rand::{thread_rng, RngCore};
use reqwest::header::{
    HeaderMap, HeaderName, HeaderValue, AUTHORIZATION, CONTENT_LENGTH, CONTENT_RANGE, CONTENT_TYPE,
    ETAG, LAST_MODIFIED, LOCATION, RANGE,
};
use reqwest::{Client, Response, StatusCode, Url};
use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::fs::{self, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tauri::{AppHandle, Emitter, Manager, State};

const DATABASE_FILE: &str = "downloads.sqlite";
const OFFLINE_DATABASE_FILE: &str = "offline_media.sqlite";
const DATASOURCES_SETTING: &str = "ohmycine-datasources";
const DEFAULT_DIRECTORY_SETTING: &str = "ohmycine-download-directory-v1";
const DOWNLOAD_SETTINGS_KEY: &str = "ohmycine-download-settings-v2";
const PROGRESS_EVENT: &str = "player-download:progress";
const REMOVED_EVENT: &str = "player-download:removed";
const MAX_REDIRECTS: usize = 5;
const COPY_BUFFER_BYTES: usize = 256 * 1024;
const MAX_OFFLINE_ARTWORK_BYTES: usize = 16 * 1024 * 1024;
const MAX_OFFLINE_SUBTITLE_BYTES: usize = 4 * 1024 * 1024;
const MAX_OFFLINE_DANMAKU_BYTES: usize = 16 * 1024 * 1024;
const MAX_OFFLINE_DANMAKU_ENTRIES: usize = 200_000;
const MAX_OFFLINE_ATTACHMENT_HEADERS: usize = 32;
const MAX_OFFLINE_ATTACHMENT_HEADER_BYTES: usize = 16 * 1024;
const MAX_OFFLINE_ATTACHMENT_HEADER_VALUE_BYTES: usize = 4 * 1024;
const DEFAULT_CONCURRENT_TASKS: u8 = 2;
const DEFAULT_SEGMENTS_PER_TASK: u8 = 1;
const MAX_CONCURRENT_TASKS: u8 = 8;
const MAX_SEGMENTS_PER_TASK: u8 = 16;
const CONTROL_RUNNING: u8 = 0;
const CONTROL_PAUSE: u8 = 1;
const CONTROL_CANCEL: u8 = 2;

pub struct DownloadQueueState {
    controls: Mutex<HashMap<String, Arc<AtomicU8>>>,
    dispatch: Mutex<()>,
    progress: Mutex<HashMap<String, ProgressMeter>>,
    limiter: GlobalRateLimiter,
}

impl Default for DownloadQueueState {
    fn default() -> Self {
        Self {
            controls: Mutex::new(HashMap::new()),
            dispatch: Mutex::new(()),
            progress: Mutex::new(HashMap::new()),
            limiter: GlobalRateLimiter::default(),
        }
    }
}

struct ProgressMeter {
    bytes: u64,
    measured_at: Instant,
}

#[derive(Default)]
struct GlobalRateLimiter {
    schedule: Mutex<Option<RateLimitSchedule>>,
}

struct RateLimitSchedule {
    bytes_per_second: u64,
    next_available: Instant,
}

impl GlobalRateLimiter {
    fn reserve(&self, bytes_per_second: Option<u64>, bytes: u64) -> Duration {
        self.reserve_at(bytes_per_second, bytes, Instant::now())
    }

    fn reserve_at(&self, bytes_per_second: Option<u64>, bytes: u64, now: Instant) -> Duration {
        let Ok(mut schedule) = self.schedule.lock() else {
            return Duration::ZERO;
        };
        let Some(bytes_per_second) = bytes_per_second.filter(|value| *value > 0) else {
            *schedule = None;
            return Duration::ZERO;
        };
        let current = schedule.get_or_insert_with(|| RateLimitSchedule {
            bytes_per_second,
            next_available: now,
        });
        if current.bytes_per_second != bytes_per_second {
            current.bytes_per_second = bytes_per_second;
            current.next_available = now;
        }
        let reserved_at = current.next_available.max(now);
        let wait = reserved_at.saturating_duration_since(now);
        let service = Duration::from_secs_f64(bytes as f64 / bytes_per_second as f64);
        current.next_available = reserved_at.checked_add(service).unwrap_or(reserved_at);
        wait
    }

    fn reset(&self, bytes_per_second: Option<u64>) {
        let Ok(mut schedule) = self.schedule.lock() else {
            return;
        };
        *schedule = bytes_per_second
            .filter(|value| *value > 0)
            .map(|bytes_per_second| RateLimitSchedule {
                bytes_per_second,
                next_available: Instant::now(),
            });
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct DownloadSegment {
    index: u8,
    range_start: u64,
    range_end: u64,
    completed_bytes: u64,
    status: String,
}

impl DownloadSegment {
    fn length(&self) -> u64 {
        self.range_end
            .saturating_sub(self.range_start)
            .saturating_add(1)
    }

    fn next_offset(&self) -> u64 {
        self.range_start.saturating_add(self.completed_bytes)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct TransferProbe {
    total_bytes: u64,
    entity_hash: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct ParsedContentRange {
    start: u64,
    end: u64,
    total: u64,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadSettings {
    concurrent_tasks: u8,
    segments_per_task: u8,
    global_speed_limit_bytes_per_second: Option<u64>,
}

impl Default for DownloadSettings {
    fn default() -> Self {
        Self {
            concurrent_tasks: DEFAULT_CONCURRENT_TASKS,
            segments_per_task: DEFAULT_SEGMENTS_PER_TASK,
            global_speed_limit_bytes_per_second: None,
        }
    }
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadOnlineIdentity {
    library_id: String,
    work_id: String,
    segment_id: String,
    version_id: String,
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
    parent_id: Option<String>,
    group_name: Option<String>,
    media_source_id: Option<String>,
    #[serde(default)]
    variant_id: Option<String>,
    #[serde(default)]
    library_id: Option<String>,
    #[serde(default)]
    online_identity: Option<DownloadOnlineIdentity>,
    #[serde(default)]
    detail_snapshot: Option<OfflineDetailSnapshot>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineDetailSnapshot {
    name: String,
    original_title: Option<String>,
    media_type: String,
    year: Option<u16>,
    rating: Option<f32>,
    overview: Option<String>,
    tagline: Option<String>,
    duration: Option<u64>,
    genres: Vec<String>,
    directors: Vec<String>,
    writers: Vec<String>,
    cast: Vec<String>,
    imdb_id: Option<String>,
    tmdb_id: Option<u64>,
    series_name: Option<String>,
    season_number: Option<u32>,
    episode_number: Option<u32>,
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
    parent_id: Option<String>,
    group_name: Option<String>,
    media_source_id: Option<String>,
    variant_id: Option<String>,
    library_id: Option<String>,
    online_identity: Option<DownloadOnlineIdentity>,
    speed_bytes_per_second: u64,
    eta_seconds: Option<u64>,
    active_segments: u8,
    attachment_state: String,
    created_at: i64,
    updated_at: i64,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineItemSummary {
    id: String,
    source_id: String,
    item_id: String,
    media_source_id: Option<String>,
    variant_id: Option<String>,
    display_name: String,
    media_type: String,
    video_bytes: u64,
    completed_at: i64,
    attachment_state: String,
    series_name: Option<String>,
    season_number: Option<u32>,
    episode_number: Option<u32>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineDetailRecord {
    id: String,
    source_id: String,
    item_id: String,
    media_source_id: Option<String>,
    variant_id: Option<String>,
    display_name: String,
    media_type: String,
    video_bytes: u64,
    completed_at: i64,
    attachment_state: String,
    snapshot: OfflineDetailSnapshot,
    assets: Vec<OfflineAssetSummary>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAssetSummary {
    id: String,
    kind: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAssetContent {
    kind: String,
    data_url: Option<String>,
    local_path: Option<String>,
    text: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAttachmentInput {
    kind: String,
    #[serde(default)]
    data_url: Option<String>,
    #[serde(default)]
    remote_url: Option<String>,
    #[serde(default)]
    headers: HashMap<String, String>,
    #[serde(default)]
    extension: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAttachmentSyncRequest {
    task_id: String,
    attachments: Vec<OfflineAttachmentInput>,
    #[serde(default)]
    failed_kinds: Vec<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAttachmentSyncResult {
    attachment_state: String,
    saved: usize,
    failed: usize,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct DownloadRemovedEvent<'a> {
    task_id: &'a str,
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
    #[cfg(target_os = "android")]
    {
        let configured = settings::read_player_setting(&app, DEFAULT_DIRECTORY_SETTING)?
            .ok_or_else(|| "尚未选择 Android 默认下载目录，请先通过 SAF 授权目录。".to_string())?;
        if !is_android_tree_uri(&configured) {
            return Err("Android 默认下载目录授权无效，请重新选择目录。".to_string());
        }
        return Ok(configured);
    }

    #[cfg(all(mobile, not(target_os = "android")))]
    return Err("Downloads are not implemented for this mobile platform.".to_string());

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
pub async fn player_download_set_default_directory(
    app: AppHandle,
    directory: String,
) -> Result<String, String> {
    #[cfg(target_os = "android")]
    {
        if !is_android_tree_uri(&directory) {
            return Err("Android 默认下载目录必须来自 SAF 目录选择器。".to_string());
        }
        download_android::validate_directory(&app, &directory).await?;
        settings::write_player_setting(&app, DEFAULT_DIRECTORY_SETTING, &directory)?;
        return Ok(directory);
    }

    #[cfg(all(mobile, not(target_os = "android")))]
    return Err("Downloads are not implemented for this mobile platform.".to_string());

    #[cfg(not(mobile))]
    {
        let canonical = validate_destination_directory(&directory)?;
        let value = display_path(&canonical);
        settings::write_player_setting(&app, DEFAULT_DIRECTORY_SETTING, &value)?;
        Ok(value)
    }
}

#[derive(Debug, Deserialize)]
struct ServerCredentialEnvelope {
    version: u8,
    provider: String,
    #[serde(rename = "accessToken")]
    access_token: String,
}

#[tauri::command]
pub fn player_download_settings(app: AppHandle) -> Result<DownloadSettings, String> {
    read_download_settings(&app)
}

#[tauri::command]
pub fn player_download_update_settings(
    app: AppHandle,
    value: DownloadSettings,
) -> Result<DownloadSettings, String> {
    validate_download_settings(&value)?;
    let serialized = serde_json::to_string(&value)
        .map_err(|_| "Failed to serialize download settings.".to_string())?;
    settings::write_player_setting(&app, DOWNLOAD_SETTINGS_KEY, &serialized)?;
    if let Some(queue) = app.try_state::<DownloadQueueState>() {
        queue
            .limiter
            .reset(value.global_speed_limit_bytes_per_second);
    }
    schedule_queued(&app)?;
    Ok(value)
}

#[tauri::command]
pub fn player_download_list(app: AppHandle) -> Result<Vec<DownloadTask>, String> {
    DownloadStorage::open(&app)?.list()
}

#[tauri::command]
pub fn player_download_offline_list(app: AppHandle) -> Result<Vec<OfflineItemSummary>, String> {
    list_offline_items(&app)
}

#[tauri::command]
pub fn player_download_offline_detail(
    app: AppHandle,
    source_id: String,
    item_id: String,
    offline_id: Option<String>,
) -> Result<Option<OfflineDetailRecord>, String> {
    validate_stable_id(&source_id, "Invalid source identity.")?;
    validate_stable_id(&item_id, "Invalid media identity.")?;
    if let Some(id) = offline_id.as_deref() {
        validate_task_id(id)?;
    }
    find_offline_detail(&app, &source_id, &item_id, offline_id.as_deref())
}

#[tauri::command]
pub async fn player_download_sync_attachments(
    app: AppHandle,
    request: OfflineAttachmentSyncRequest,
) -> Result<OfflineAttachmentSyncResult, String> {
    validate_task_id(&request.task_id)?;
    if request.attachments.len() > 24 || request.failed_kinds.len() > 24 {
        return Err("Too many offline attachments were requested.".to_string());
    }
    let storage = DownloadStorage::open(&app)?;
    let task = storage
        .get(&request.task_id)?
        .filter(|task| task.status == "completed")
        .ok_or_else(|| "Only completed downloads can synchronize attachments.".to_string())?;
    let package_id = offline_package_id_for_task(&app, &task.id)?;
    let directory = offline_asset_directory_for_write(&app, &package_id)?;
    let mut saved = 0usize;
    let mut failed = 0usize;
    let mut incomplete_kinds = HashSet::new();
    let mut reported_failed_kinds = Vec::new();
    for value in &request.failed_kinds {
        match validate_attachment_kind(value) {
            Ok(kind) => reported_failed_kinds.push(kind.to_string()),
            Err(_) => failed += 1,
        }
    }
    let conn = Connection::open(storage::data_file(&app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    storage.set_attachment_state(&task.id, "syncing")?;
    conn.execute(
        "UPDATE offline_packages SET attachment_state='syncing',updated_at=unixepoch() WHERE id=?1",
        [&package_id],
    )
    .map_err(|_| "Failed to update offline attachment state.".to_string())?;
    emit_task(&app, &storage, &task.id);
    let existing_assets = {
        let mut statement = conn
            .prepare(
                "SELECT id,asset_kind,relative_asset_path,status FROM offline_assets
                 WHERE package_id=?1 AND item_id=?2",
            )
            .map_err(|_| "Failed to inspect offline attachments.".to_string())?;
        let records = statement
            .query_map(params![package_id, task.item_id], |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, String>(3)?,
                ))
            })
            .map_err(|_| "Failed to inspect offline attachments.".to_string())?
            .filter_map(Result::ok)
            .map(|record| (record.0.clone(), record))
            .collect::<HashMap<_, _>>();
        records
    };
    for kind in reported_failed_kinds {
        let already_complete = existing_assets
            .values()
            .any(|(_, asset_kind, _, status)| asset_kind == &kind && status == "complete");
        if !already_complete {
            incomplete_kinds.insert(kind);
            failed += 1;
        }
    }
    let mut attempted_kinds = HashSet::new();
    let mut saved_ids_by_kind: HashMap<String, HashSet<String>> = HashMap::new();
    for (index, input) in request.attachments.into_iter().enumerate() {
        let kind = match validate_attachment_kind(&input.kind) {
            Ok(value) => value,
            Err(_) => {
                failed += 1;
                continue;
            }
        };
        attempted_kinds.insert(kind.to_string());
        let asset_id = hash_text(&format!("{}\0{}\0{}", task.id, kind, index));
        match load_attachment_bytes(kind, &input).await.and_then(|bytes| {
            validate_attachment_bytes(kind, input.extension.as_deref(), &bytes)?;
            let extension = attachment_extension(kind, input.extension.as_deref(), &bytes)?;
            let digest = hash_bytes(&bytes);
            let file_name = format!("{asset_id}-{}.{extension}", &digest[..16]);
            persist_offline_asset_file(&directory, &file_name, &bytes)?;
            if let Err(error) = conn.execute(
                "INSERT INTO offline_assets(id,package_id,item_id,asset_kind,relative_asset_path,status,safe_error_code,created_at,updated_at)
                 VALUES (?1,?2,?3,?4,?5,'complete',NULL,unixepoch(),unixepoch())
                 ON CONFLICT(id) DO UPDATE SET relative_asset_path=excluded.relative_asset_path,
                    status='complete',safe_error_code=NULL,updated_at=unixepoch()",
                params![asset_id, package_id, task.item_id, kind, file_name],
            ) {
                let _ = remove_safe_asset_file(&directory, &file_name);
                let _ = error;
                return Err("Failed to register an offline attachment.".to_string());
            }
            if let Some((_, _, previous_path, previous_status)) = existing_assets.get(&asset_id) {
                if previous_status == "complete" && previous_path != &file_name {
                    let _ = remove_safe_asset_file(&directory, previous_path);
                }
            }
            Ok(())
        }) {
            Ok(()) => {
                saved += 1;
                saved_ids_by_kind
                    .entry(kind.to_string())
                    .or_default()
                    .insert(asset_id);
            }
            Err(_) => {
                let already_complete = existing_assets
                    .get(&asset_id)
                    .is_some_and(|(_, _, _, status)| status == "complete");
                if already_complete {
                    saved_ids_by_kind
                        .entry(kind.to_string())
                        .or_default()
                        .insert(asset_id);
                } else {
                    failed += 1;
                    incomplete_kinds.insert(kind.to_string());
                    let _ = record_attachment_failure(
                        &conn,
                        &asset_id,
                        &package_id,
                        &task.item_id,
                        kind,
                    );
                }
            }
        }
    }

    // A retry is allowed to replace a complete asset only after its new bytes are fully
    // validated and registered. Prune obsolete slots only for a kind whose complete,
    // authoritative input set succeeded; a transient retry must never destroy good assets.
    for kind in attempted_kinds {
        if incomplete_kinds.contains(&kind) {
            continue;
        }
        let retained = saved_ids_by_kind.get(&kind).cloned().unwrap_or_default();
        for (asset_id, (_, asset_kind, relative_path, _)) in &existing_assets {
            if asset_kind != &kind || retained.contains(asset_id) {
                continue;
            }
            conn.execute(
                "DELETE FROM offline_assets WHERE id=?1 AND package_id=?2 AND item_id=?3",
                params![asset_id, package_id, task.item_id],
            )
            .map_err(|_| "Failed to replace offline attachments.".to_string())?;
            let _ = remove_safe_asset_file(&directory, relative_path);
        }
    }

    let state = if failed == 0 { "complete" } else { "partial" };
    conn.execute(
        "UPDATE offline_packages SET attachment_state=?2,updated_at=unixepoch() WHERE id=?1",
        params![package_id, state],
    )
    .map_err(|_| "Failed to update offline attachment state.".to_string())?;
    storage.set_attachment_state(&task.id, state)?;
    emit_task(&app, &storage, &task.id);
    Ok(OfflineAttachmentSyncResult {
        attachment_state: state.to_string(),
        saved,
        failed,
    })
}

fn record_attachment_failure(
    conn: &Connection,
    asset_id: &str,
    package_id: &str,
    item_id: &str,
    kind: &str,
) -> Result<(), String> {
    conn.execute(
        "INSERT INTO offline_assets(id,package_id,item_id,asset_kind,relative_asset_path,status,safe_error_code,created_at,updated_at)
         VALUES (?1,?2,?3,?4,'','failed','attachment_unavailable',unixepoch(),unixepoch())
         ON CONFLICT(id) DO UPDATE SET status='failed',safe_error_code='attachment_unavailable',updated_at=unixepoch()
         WHERE offline_assets.status<>'complete'",
        params![asset_id, package_id, item_id, kind],
    )
    .map(|_| ())
    .map_err(|_| "Failed to register an offline attachment failure.".to_string())
}

#[tauri::command]
pub fn player_download_offline_asset(
    app: AppHandle,
    asset_id: String,
) -> Result<Option<OfflineAssetContent>, String> {
    if asset_id.len() != 64 || !asset_id.bytes().all(|value| value.is_ascii_hexdigit()) {
        return Err("Invalid offline attachment identity.".to_string());
    }
    let conn = Connection::open(storage::data_file(&app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    let record = conn
        .query_row(
            "SELECT a.asset_kind,a.relative_asset_path,a.package_id FROM offline_assets a
             WHERE a.id=?1 AND a.status='complete'",
            [&asset_id],
            |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                ))
            },
        )
        .optional()
        .map_err(|_| "Failed to read the offline attachment.".to_string())?;
    let Some((kind, relative_path, package_id)) = record else {
        return Ok(None);
    };
    if Path::new(&relative_path)
        .file_name()
        .and_then(|value| value.to_str())
        != Some(relative_path.as_str())
    {
        return Err("Offline attachment path is invalid.".to_string());
    }
    let path = offline_asset_directory_for_read(&app, &package_id)?.join(&relative_path);
    let bytes = fs::read(&path).map_err(|_| "Offline attachment is missing.".to_string())?;
    validate_attachment_bytes(
        &kind,
        path.extension().and_then(|value| value.to_str()),
        &bytes,
    )?;
    let content = match kind.as_str() {
        "poster" | "backdrop" | "still" => {
            let mime = crate::commands::image_cache::detect_image_mime(&bytes)
                .ok_or_else(|| "Offline artwork is invalid.".to_string())?;
            OfflineAssetContent {
                kind,
                data_url: Some(format!(
                    "data:{mime};base64,{}",
                    BASE64_STANDARD.encode(bytes)
                )),
                local_path: None,
                text: None,
            }
        }
        "subtitle" => OfflineAssetContent {
            kind,
            data_url: None,
            local_path: Some(display_path(path)),
            text: None,
        },
        "danmaku" => OfflineAssetContent {
            kind,
            data_url: None,
            local_path: None,
            text: Some(
                String::from_utf8(bytes).map_err(|_| "Offline danmaku is invalid.".to_string())?,
            ),
        },
        _ => return Ok(None),
    };
    Ok(Some(content))
}

#[tauri::command]
pub async fn player_download_resolve_local(
    app: AppHandle,
    source_id: String,
    item_id: String,
    media_source_id: Option<String>,
    variant_id: Option<String>,
) -> Result<Option<String>, String> {
    validate_stable_id(&source_id, "Invalid source identity.")?;
    validate_stable_id(&item_id, "Invalid media identity.")?;
    let storage = DownloadStorage::open(&app)?;
    let task = storage.find_completed(
        &source_id,
        &item_id,
        media_source_id.as_deref(),
        variant_id.as_deref(),
    )?;
    // The offline record owns the post-finalization file fingerprint. Prefer it even while
    // the completed history task still exists; falling back to the task first would reduce
    // validation to size-only until the user removed the history row.
    let location = find_offline_location(
        &app,
        &source_id,
        &item_id,
        media_source_id.as_deref(),
        variant_id.as_deref(),
    )?
    .or_else(|| {
        task.as_ref().map(|task| OfflineLocation {
            id: task.id.clone(),
            root_reference: task.destination_directory.clone(),
            relative_video_path: task.destination_name.clone(),
            video_bytes: task.total_bytes,
            entity_hash: None,
        })
    });
    let Some(location) = location else {
        return Ok(None);
    };

    #[cfg(target_os = "android")]
    {
        if !is_android_tree_uri(&location.root_reference) {
            remove_offline_location(&app, &location.id)?;
            return Ok(None);
        }
        let resolved = download_android::resolve_completed_document(
            &app,
            &location.root_reference,
            &location.relative_video_path,
        )
        .await?;
        let Some((uri, size, entity_hash)) = resolved else {
            if let Some(task) = task.as_ref() {
                storage.set_status(&task.id, "failed", Some("The downloaded file is missing."))?;
                emit_task(&app, &storage, &task.id);
            }
            remove_offline_location(&app, &location.id)?;
            return Ok(None);
        };
        if location
            .video_bytes
            .is_some_and(|expected| expected != size)
            || location
                .entity_hash
                .as_deref()
                .is_some_and(|expected| expected != entity_hash)
        {
            if let Some(task) = task.as_ref() {
                storage.set_status(
                    &task.id,
                    "failed",
                    Some("The downloaded file no longer matches the completed transfer."),
                )?;
                emit_task(&app, &storage, &task.id);
            }
            remove_offline_location(&app, &location.id)?;
            return Ok(None);
        }
        return Ok(Some(uri));
    }

    #[cfg(not(target_os = "android"))]
    {
        let root = validate_destination_directory(&location.root_reference)?;
        let path = safe_destination_path(&root, &location.relative_video_path)?;
        let metadata = match fs::metadata(&path) {
            Ok(value) if value.is_file() => value,
            _ => {
                if let Some(task) = task.as_ref() {
                    storage.set_status(
                        &task.id,
                        "failed",
                        Some("The downloaded file is missing."),
                    )?;
                    emit_task(&app, &storage, &task.id);
                }
                remove_offline_location(&app, &location.id)?;
                return Ok(None);
            }
        };
        if location
            .video_bytes
            .is_some_and(|expected| expected != metadata.len())
            || location
                .entity_hash
                .as_deref()
                .is_some_and(|expected| expected != local_entity_fingerprint(&metadata))
        {
            if let Some(task) = task.as_ref() {
                storage.set_status(
                    &task.id,
                    "failed",
                    Some("The downloaded file no longer matches the completed transfer."),
                )?;
                emit_task(&app, &storage, &task.id);
            }
            remove_offline_location(&app, &location.id)?;
            return Ok(None);
        }
        Ok(Some(display_path(path)))
    }
}

#[tauri::command]
pub async fn player_download_remove(
    app: AppHandle,
    task_id: String,
    delete_file: bool,
) -> Result<(), String> {
    validate_task_id(&task_id)?;
    let task = DownloadStorage::open(&app)?
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if task.status != "completed" && task.status != "failed" {
        return Err("Only completed or failed downloads can be removed here.".to_string());
    }
    if task.status == "failed" {
        // A failed transfer may still own a resumable partial. Once its history row is
        // removed there is no safe identity left to resume or clean that file later, so
        // deleting a failed task always performs the same exact-owned cleanup as cancel.
        #[cfg(target_os = "android")]
        return cleanup_cancelled_android_task(&app, &task).await;
        #[cfg(not(target_os = "android"))]
        return cleanup_cancelled_task(&app, &DownloadStorage::open(&app)?, &task);
    }
    if delete_file && task.status == "completed" {
        #[cfg(target_os = "android")]
        download_android::delete_completed_document(
            &app,
            &task.destination_directory,
            &task.destination_name,
        )
        .await?;
        #[cfg(not(target_os = "android"))]
        {
            let root = validate_destination_directory(&task.destination_directory)?;
            let path = safe_destination_path(&root, &task.destination_name)?;
            if path.exists() {
                fs::remove_file(path)
                    .map_err(|_| "Failed to delete the downloaded file.".to_string())?;
            }
        }
        delete_offline_item(&app, &task)?;
    }
    DownloadStorage::open(&app)?.delete_task_and_segments(&task.id, None)?;
    let _ = app.emit(REMOVED_EVENT, DownloadRemovedEvent { task_id: &task.id });
    Ok(())
}

#[cfg(target_os = "android")]
#[tauri::command]
pub async fn player_download_pick_directory(
    app: AppHandle,
    persistent: bool,
) -> Result<Option<String>, String> {
    let picked = download_android::pick_directory(&app).await?;
    if picked.cancelled {
        return Ok(None);
    }
    let uri = picked
        .uri
        .filter(|value| is_android_tree_uri(value))
        .ok_or_else(|| "Android 目录选择器返回了无效授权。".to_string())?;
    download_android::validate_directory(&app, &uri).await?;
    if persistent {
        settings::write_player_setting(&app, DEFAULT_DIRECTORY_SETTING, &uri)?;
    }
    Ok(Some(uri))
}

#[cfg(not(target_os = "android"))]
#[tauri::command]
pub async fn player_download_pick_directory(
    _app: AppHandle,
    _persistent: bool,
) -> Result<Option<String>, String> {
    Err("Desktop downloads use the native directory dialog.".to_string())
}

#[tauri::command]
pub async fn player_download_enqueue(
    app: AppHandle,
    queue: State<'_, DownloadQueueState>,
    request: DownloadEnqueueRequest,
) -> Result<DownloadTask, String> {
    #[cfg(target_os = "android")]
    {
        validate_enqueue_request(&request)?;
        let destination = match request.destination_directory.as_deref() {
            Some(value) => value.to_string(),
            None => player_download_default_directory(app.clone())?,
        };
        if !is_android_tree_uri(&destination) {
            return Err("Android 下载目录授权无效，请重新选择目录。".to_string());
        }
        download_android::validate_directory(&app, &destination).await?;
        let requested_name = download_file_name(&request.display_name, &request.item_id);
        let destination_name =
            download_android::available_name(&app, &destination, &requested_name).await?;
        let now = unix_timestamp();
        let task = DownloadTask {
            id: random_id(),
            source_id: request.source_id,
            source_type: request.source_type,
            item_id: request.item_id,
            display_name: safe_display_name(&request.display_name),
            media_type: request.media_type,
            destination_directory: destination,
            destination_name,
            status: "queued".to_string(),
            bytes_downloaded: 0,
            total_bytes: request.expected_bytes,
            retry_count: 0,
            error_message: None,
            parent_id: request.parent_id,
            group_name: request.group_name,
            media_source_id: request.media_source_id,
            variant_id: request.variant_id,
            library_id: request.library_id,
            online_identity: request.online_identity,
            speed_bytes_per_second: 0,
            eta_seconds: None,
            active_segments: 0,
            attachment_state: "none".to_string(),
            created_at: now,
            updated_at: now,
        };
        DownloadStorage::open(&app)?.insert(&task)?;
        if let Some(snapshot) = request.detail_snapshot.as_ref() {
            DownloadStorage::open(&app)?.save_snapshot(&task.id, snapshot)?;
        }
        let _ = queue;
        schedule_queued(&app)?;
        return Ok(task);
    }

    #[cfg(all(mobile, not(target_os = "android")))]
    return Err("Downloads are not implemented for this mobile platform.".to_string());

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
            parent_id: request.parent_id,
            group_name: request.group_name,
            media_source_id: request.media_source_id,
            variant_id: request.variant_id,
            library_id: request.library_id,
            online_identity: request.online_identity,
            speed_bytes_per_second: 0,
            eta_seconds: None,
            active_segments: 0,
            attachment_state: "none".to_string(),
            created_at: now,
            updated_at: now,
        };
        DownloadStorage::open(&app)?.insert(&task)?;
        if let Some(snapshot) = request.detail_snapshot.as_ref() {
            DownloadStorage::open(&app)?.save_snapshot(&task.id, snapshot)?;
        }
        let _ = queue;
        schedule_queued(&app)?;
        Ok(task)
    }
}

#[tauri::command]
pub async fn player_download_cancel(
    app: AppHandle,
    queue: State<'_, DownloadQueueState>,
    task_id: String,
) -> Result<(), String> {
    validate_task_id(&task_id)?;
    let task = DownloadStorage::open(&app)?
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if task.status == "completed" {
        return Err("Completed downloads must be removed from the offline library.".to_string());
    }
    let active = queue
        .controls
        .lock()
        .map_err(|_| "Download queue is unavailable.".to_string())?
        .get(&task_id)
        .cloned();
    if let Some(control) = active {
        DownloadStorage::open(&app)?.set_status(&task_id, "cancel_requested", None)?;
        control.store(CONTROL_CANCEL, Ordering::Release);
    } else {
        #[cfg(target_os = "android")]
        return cleanup_cancelled_android_task(&app, &task).await;
        #[cfg(not(target_os = "android"))]
        cleanup_cancelled_task(&app, &DownloadStorage::open(&app)?, &task)?;
    }
    Ok(())
}

#[tauri::command]
pub fn player_download_pause(
    app: AppHandle,
    queue: State<DownloadQueueState>,
    task_id: String,
) -> Result<(), String> {
    validate_task_id(&task_id)?;
    let storage = DownloadStorage::open(&app)?;
    let task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if !matches!(
        task.status.as_str(),
        "queued" | "interrupted" | "resolving" | "downloading" | "finalizing"
    ) {
        return Err("Only queued or active downloads can be paused.".to_string());
    }
    let active = queue
        .controls
        .lock()
        .map_err(|_| "Download queue is unavailable.".to_string())?
        .get(&task_id)
        .cloned();
    if let Some(control) = active {
        storage.set_status(&task_id, "pause_requested", None)?;
        control.store(CONTROL_PAUSE, Ordering::Release);
    } else {
        storage.set_status(&task_id, "paused", None)?;
        emit_task(&app, &storage, &task_id);
    }
    Ok(())
}

#[tauri::command]
pub fn player_download_resume(app: AppHandle, task_id: String) -> Result<DownloadTask, String> {
    validate_task_id(&task_id)?;
    let storage = DownloadStorage::open(&app)?;
    let task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if task.status != "paused" {
        return Err("Only user-paused downloads can be resumed.".to_string());
    }
    storage.set_status(&task_id, "queued", None)?;
    let task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    schedule_queued(&app)?;
    Ok(task)
}

#[tauri::command]
pub fn player_download_retry(
    app: AppHandle,
    _queue: State<DownloadQueueState>,
    task_id: String,
) -> Result<DownloadTask, String> {
    validate_task_id(&task_id)?;
    let storage = DownloadStorage::open(&app)?;
    let mut task = storage
        .get(&task_id)?
        .ok_or_else(|| "Download task not found.".to_string())?;
    if task.status != "failed" {
        return Err("Only failed downloads can be retried.".to_string());
    }
    task.status = "queued".to_string();
    task.error_message = None;
    task.retry_count = task.retry_count.saturating_add(1);
    task.updated_at = unix_timestamp();
    storage.queue_retry(&task)?;
    schedule_queued(&app)?;
    Ok(task)
}

pub fn recover_interrupted_downloads(app: &AppHandle) -> Result<(), String> {
    initialize_offline_storage(app)?;
    let storage = DownloadStorage::open(app)?;
    storage.recover_requested_controls(app)?;
    storage.recover_interrupted()?;
    #[cfg(not(target_os = "android"))]
    storage.retry_cleanup()?;
    #[cfg(target_os = "android")]
    {
        let app = app.clone();
        tauri::async_runtime::spawn(async move {
            let _ = retry_android_cleanup(&app).await;
        });
    }
    schedule_queued(app)
}

fn schedule_queued(app: &AppHandle) -> Result<(), String> {
    let queue = app
        .try_state::<DownloadQueueState>()
        .ok_or_else(|| "Download queue is unavailable.".to_string())?;
    let _dispatch = queue
        .dispatch
        .lock()
        .map_err(|_| "Download scheduler is unavailable.".to_string())?;
    let settings = read_download_settings(app)?;
    loop {
        let active = queue
            .controls
            .lock()
            .map_err(|_| "Download queue is unavailable.".to_string())?
            .len();
        if active >= usize::from(settings.concurrent_tasks) {
            break;
        }
        let storage = DownloadStorage::open(app)?;
        let Some(task) = storage.claim_next_runnable()? else {
            break;
        };
        if let Err(error) = start_task(app.clone(), queue.inner(), task.clone()) {
            let _ = storage.set_status(&task.id, "queued", None);
            return Err(error);
        }
    }
    Ok(())
}

fn start_task(
    app: AppHandle,
    queue: &DownloadQueueState,
    task: DownloadTask,
) -> Result<(), String> {
    let control = Arc::new(AtomicU8::new(CONTROL_RUNNING));
    let mut controls = queue
        .controls
        .lock()
        .map_err(|_| "Download queue is unavailable.".to_string())?;
    if controls.contains_key(&task.id) {
        return Err("This download task is already active.".to_string());
    }
    controls.insert(task.id.clone(), control.clone());
    drop(controls);
    let claimed = DownloadStorage::open(&app)?
        .get(&task.id)?
        .is_some_and(|stored| stored.status == "resolving");
    if !claimed {
        if let Ok(mut controls) = queue.controls.lock() {
            controls.remove(&task.id);
        }
        return Ok(());
    }
    let task_id = task.id.clone();
    tauri::async_runtime::spawn(async move {
        #[cfg(target_os = "android")]
        download_android::notify(
            &app,
            &task.id,
            &task.display_name,
            task.bytes_downloaded,
            task.total_bytes,
            "running",
        )
        .await;
        let result = execute_task(&app, &task, &control).await;
        match result {
            Ok(()) if control.load(Ordering::Acquire) == CONTROL_CANCEL => {
                #[cfg(not(target_os = "android"))]
                if let Ok(storage) = DownloadStorage::open(&app) {
                    let _ = cleanup_cancelled_final(&storage, &task);
                    let _ = cleanup_cancelled_task(&app, &storage, &task);
                }
                #[cfg(target_os = "android")]
                let _ = cleanup_cancelled_android_final(&app, &task).await;
            }
            Ok(()) => {
                let completion = DownloadStorage::open(&app).and_then(|storage| {
                    let transitioned = storage.try_set_completed(&task_id)?;
                    let completed = if transitioned {
                        storage.get(&task_id)?
                    } else {
                        None
                    };
                    Ok((transitioned, completed))
                });
                match completion {
                    Ok((true, Some(completed))) => {
                        // Transfer workers persist the authoritative byte count while they run.
                        // Finalizing from the enqueue-time clone would record zero for responses
                        // whose size was unknown until the transfer started.
                        let _ = finalize_offline_item(&app, &completed).await;
                    }
                    Ok((true, None)) => {}
                    Ok((false, _)) | Err(_) => {
                        #[cfg(not(target_os = "android"))]
                        if let Ok(storage) = DownloadStorage::open(&app) {
                            let _ = cleanup_cancelled_final(&storage, &task);
                            let _ = cleanup_cancelled_task(&app, &storage, &task);
                        }
                        #[cfg(target_os = "android")]
                        let _ = cleanup_cancelled_android_final(&app, &task).await;
                    }
                }
            }
            Err(_) if control.load(Ordering::Acquire) == CONTROL_CANCEL => {
                #[cfg(target_os = "android")]
                let _ = cleanup_cancelled_android_task(&app, &task).await;
                #[cfg(not(target_os = "android"))]
                if let Ok(storage) = DownloadStorage::open(&app) {
                    let _ = cleanup_cancelled_task(&app, &storage, &task);
                }
            }
            Err(_) if control.load(Ordering::Acquire) == CONTROL_PAUSE => {
                if let Ok(storage) = DownloadStorage::open(&app) {
                    let _ = storage.set_status(&task_id, "paused", None);
                }
            }
            Err(error) => {
                if let Ok(storage) = DownloadStorage::open(&app) {
                    let _ = storage.set_status(&task_id, "failed", Some(&safe_error(&error)));
                }
            }
        }
        #[cfg(target_os = "android")]
        let finished = DownloadStorage::open(&app).ok().and_then(|storage| {
            let finished = storage.get(&task_id).ok().flatten();
            if finished.is_some() {
                emit_task(&app, &storage, &task_id);
            }
            finished
        });
        #[cfg(not(target_os = "android"))]
        if let Ok(storage) = DownloadStorage::open(&app) {
            if storage.get(&task_id).ok().flatten().is_some() {
                emit_task(&app, &storage, &task_id);
            }
        }
        #[cfg(target_os = "android")]
        if let Some(finished) = finished {
            download_android::notify(
                &app,
                &finished.id,
                &finished.display_name,
                finished.bytes_downloaded,
                finished.total_bytes,
                &finished.status,
            )
            .await;
        }
        if let Some(state) = app.try_state::<DownloadQueueState>() {
            if let Ok(mut controls) = state.controls.lock() {
                controls.remove(&task_id);
            }
            if let Ok(mut progress) = state.progress.lock() {
                progress.remove(&task_id);
            }
        }
        let _ = schedule_queued(&app);
    });
    Ok(())
}

async fn execute_task(
    app: &AppHandle,
    task: &DownloadTask,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    if control.load(Ordering::Acquire) != CONTROL_RUNNING {
        return Err("Download stopped by user request.".to_string());
    }
    let storage = DownloadStorage::open(app)?;
    storage.set_transfer_state(&task.id, "downloading", 1)?;
    emit_task(app, &storage, &task.id);

    #[cfg(target_os = "android")]
    {
        return execute_android_task(app, task, control).await;
    }

    #[cfg(not(target_os = "android"))]
    let destination_dir = validate_destination_directory(&task.destination_directory)?;
    #[cfg(not(target_os = "android"))]
    let final_path = safe_destination_path(&destination_dir, &task.destination_name)?;
    #[cfg(not(target_os = "android"))]
    let partial_path = partial_path(&final_path)?;
    #[cfg(not(target_os = "android"))]
    if final_path.exists() {
        return Err("The destination file already exists.".to_string());
    }

    #[cfg(not(target_os = "android"))]
    match task.source_type.as_str() {
        "local" => execute_local_copy(app, task, &partial_path, control).await?,
        "alist" | "clouddrive2" | "webdav" | "123" | "quark" | "emby" | "jellyfin" | "server" => {
            execute_remote_download(app, task, &partial_path, control).await?
        }
        _ => {
            return Err(
                "This data source does not have a secure native download resolver.".to_string(),
            )
        }
    }

    #[cfg(not(target_os = "android"))]
    if control.load(Ordering::Acquire) != CONTROL_RUNNING {
        return Err("Download stopped by user request.".to_string());
    }
    #[cfg(not(target_os = "android"))]
    storage.set_transfer_state(&task.id, "finalizing", 0)?;
    #[cfg(not(target_os = "android"))]
    emit_task(app, &storage, &task.id);
    #[cfg(not(target_os = "android"))]
    fs::rename(&partial_path, &final_path)
        .map_err(|_| "Failed to finalize the downloaded file atomically.".to_string())?;
    #[cfg(not(target_os = "android"))]
    if control.load(Ordering::Acquire) == CONTROL_CANCEL {
        cleanup_cancelled_final(&storage, task)?;
        return Err("Download stopped by user request.".to_string());
    }
    #[cfg(not(target_os = "android"))]
    Ok(())
}

#[cfg(target_os = "android")]
async fn execute_android_task(
    app: &AppHandle,
    task: &DownloadTask,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    if !is_android_tree_uri(&task.destination_directory) {
        return Err("Android 下载目录授权无效，请重新选择目录后重试。".to_string());
    }
    download_android::validate_directory(app, &task.destination_directory).await?;
    let document = download_android::prepare_document(
        app,
        &task.destination_directory,
        &task.destination_name,
    )
    .await?;
    let storage = DownloadStorage::open(app)?;
    if document.destination_name != task.destination_name {
        return Err("Android 下载目标文件名已发生变化，请重新创建任务。".to_string());
    }
    let mut offset = document.existing_bytes;
    match task.source_type.as_str() {
        "local" => {
            let config = resolve_datasource(app, &task.source_id, "local")?;
            let root = extra_string(&config, "rootPath")?;
            let mut truncate = offset == 0;
            let mut resume_pending = offset > 0;
            loop {
                ensure_android_transfer_running(app, task, control).await?;
                let (chunk, total, entity_hash) = download_android::read_local_chunk(
                    app,
                    &root,
                    &task.item_id,
                    offset,
                    COPY_BUFFER_BYTES,
                )
                .await?;
                if resume_pending
                    && (storage.entity_hash(&task.id)?.as_deref() != Some(&entity_hash)
                        || task
                            .total_bytes
                            .is_some_and(|expected| Some(expected) != total))
                {
                    offset = 0;
                    truncate = true;
                    resume_pending = false;
                    continue;
                }
                resume_pending = false;
                if chunk.is_empty() {
                    break;
                }
                acquire_global_budget_async(app, chunk.len() as u64, control).await?;
                download_android::write_chunk(app, &document.partial_uri, &chunk, truncate).await?;
                truncate = false;
                offset = offset.saturating_add(chunk.len() as u64);
                storage.set_entity_and_progress(&task.id, Some(&entity_hash), offset, total)?;
                emit_task(app, &storage, &task.id);
                download_android::notify(
                    app,
                    &task.id,
                    &task.display_name,
                    offset,
                    total,
                    "running",
                )
                .await;
            }
        }
        "alist" | "clouddrive2" | "webdav" | "123" | "quark" | "emby" | "jellyfin" | "server" => {
            let resolved = resolve_task_remote(app, task).await?;
            let stored_entity = storage.entity_hash(&task.id)?;
            let mut response = request_media(&resolved, offset).await?;
            if offset > 0 {
                let current_entity = response_entity_hash(&response);
                let safe_resume = response.status() == StatusCode::PARTIAL_CONTENT
                    && content_range_start(response.headers()) == Some(offset)
                    && stored_entity.is_some()
                    && stored_entity == current_entity;
                if !safe_resume {
                    offset = 0;
                    response = request_media(&resolve_task_remote(app, task).await?, 0).await?;
                }
            }
            validate_media_response(&response, offset)?;
            let entity = response_entity_hash(&response);
            let total = response_total_bytes(&response, offset).or(task.total_bytes);
            storage.set_entity_and_progress(&task.id, entity.as_deref(), offset, total)?;
            let mut stream = response.bytes_stream();
            let mut truncate = offset == 0;
            while let Some(next) = stream.next().await {
                ensure_android_transfer_running(app, task, control).await?;
                let chunk = next.map_err(|_| "The media transfer was interrupted.".to_string())?;
                acquire_global_budget_async(app, chunk.len() as u64, control).await?;
                download_android::write_chunk(app, &document.partial_uri, &chunk, truncate).await?;
                truncate = false;
                offset = offset.saturating_add(chunk.len() as u64);
                storage.set_progress(&task.id, offset, total)?;
                emit_task(app, &storage, &task.id);
                download_android::notify(
                    app,
                    &task.id,
                    &task.display_name,
                    offset,
                    total,
                    "running",
                )
                .await;
            }
            if total.is_some_and(|expected| expected != offset) {
                return Err("The media transfer ended before all bytes were received.".to_string());
            }
        }
        _ => {
            return Err(
                "This data source does not have a secure native download resolver.".to_string(),
            )
        }
    }
    if control.load(Ordering::Acquire) != CONTROL_RUNNING {
        return Err("Download stopped by user request.".to_string());
    }
    storage.set_transfer_state(&task.id, "finalizing", 0)?;
    emit_task(app, &storage, &task.id);
    download_android::finalize_document(app, &document.partial_uri, &task.destination_name).await?;
    Ok(())
}

#[cfg(target_os = "android")]
async fn ensure_android_transfer_running(
    app: &AppHandle,
    task: &DownloadTask,
    control: &AtomicU8,
) -> Result<(), String> {
    let state = control.load(Ordering::Acquire);
    if state == CONTROL_RUNNING {
        return Ok(());
    }
    if state == CONTROL_CANCEL {
        download_android::delete_partial_document(
            app,
            &task.destination_directory,
            &task.destination_name,
        )
        .await?;
    }
    Err("Download stopped by user request.".to_string())
}

async fn execute_local_copy(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &Arc<AtomicU8>,
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
    if control.load(Ordering::Acquire) != CONTROL_RUNNING {
        return Err("Download stopped by user request.".to_string());
    }
    let control = control.clone();
    tauri::async_runtime::spawn_blocking(move || {
        copy_file_streaming(
            &app,
            &task_id,
            &source_path,
            &partial,
            offset,
            total,
            &control,
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
    control: &AtomicU8,
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
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        let read = source
            .read(&mut buffer)
            .map_err(|_| "Failed while reading the local source file.".to_string())?;
        if read == 0 {
            break;
        }
        acquire_global_budget_blocking(app, read as u64, control)?;
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

async fn execute_remote_download(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    let mut last_error = "The media transfer failed.".to_string();
    for attempt in 0..3_u64 {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        match execute_remote_download_once(app, task, partial_path, control).await {
            Ok(()) => return Ok(()),
            Err(error) => last_error = error,
        }
        if attempt < 2 {
            tokio::time::sleep(Duration::from_millis(350 * (1_u64 << attempt))).await;
        }
    }
    Err(last_error)
}

async fn execute_remote_download_once(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    let settings = read_download_settings(app)?;
    let existing_segments = DownloadStorage::open(app)?.segments(&task.id)?;
    if settings.segments_per_task > 1 || !existing_segments.is_empty() {
        if let Some(probe) = probe_segment_transfer(app, task).await? {
            return execute_segmented_download(
                app,
                task,
                partial_path,
                control,
                settings.segments_per_task,
                probe,
            )
            .await;
        }
        reset_unsafe_segment_transfer(app, task, partial_path)?;
    }
    DownloadStorage::open(app)?.set_transfer_state(&task.id, "downloading", 1)?;
    execute_single_stream_download(app, task, partial_path, control).await
}

async fn execute_single_stream_download(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    let resolved = resolve_task_remote(app, task).await?;
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
            let refreshed = resolve_task_remote(app, task).await?;
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
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        let chunk = next.map_err(|_| "The media transfer was interrupted.".to_string())?;
        acquire_global_budget_async(app, chunk.len() as u64, control).await?;
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

async fn probe_segment_transfer(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<Option<TransferProbe>, String> {
    let resolved = resolve_task_remote(app, task).await?;
    let response = request_media_range(&resolved, Some((0, Some(0)))).await?;
    validate_media_content_type(&response)?;
    Ok(segment_probe_from_headers(
        response.status(),
        response.headers(),
        response.content_length(),
    ))
}

fn segment_probe_from_headers(
    status: StatusCode,
    headers: &HeaderMap,
    content_length: Option<u64>,
) -> Option<TransferProbe> {
    if status != StatusCode::PARTIAL_CONTENT || content_length != Some(1) {
        return None;
    }
    let content_range = parse_content_range(headers)?;
    if content_range.start != 0 || content_range.end != 0 || content_range.total == 0 {
        return None;
    }
    let etag = headers.get(ETAG).and_then(|value| value.to_str().ok());
    let modified = headers
        .get(LAST_MODIFIED)
        .and_then(|value| value.to_str().ok());
    let entity_hash = entity_identity_hash(etag, modified, Some(content_range.total))?;
    Some(TransferProbe {
        total_bytes: content_range.total,
        entity_hash,
    })
}

async fn execute_segmented_download(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &Arc<AtomicU8>,
    requested_segments: u8,
    probe: TransferProbe,
) -> Result<(), String> {
    let segments = prepare_segment_transfer(app, task, partial_path, requested_segments, &probe)?;
    let initial_downloaded = segments
        .iter()
        .map(|segment| segment.completed_bytes)
        .sum::<u64>();
    let active = segments
        .iter()
        .filter(|segment| segment.completed_bytes < segment.length())
        .count();
    DownloadStorage::open(app)?.set_transfer_state(
        &task.id,
        "downloading",
        u8::try_from(active).unwrap_or(u8::MAX),
    )?;
    persist_progress(app, &task.id, initial_downloaded, Some(probe.total_bytes));

    let aggregate = Arc::new(AtomicU64::new(initial_downloaded));
    let workers = segments
        .iter()
        .filter(|segment| segment.completed_bytes < segment.length())
        .cloned()
        .map(|segment| {
            download_segment(
                app.clone(),
                task.clone(),
                partial_path.to_path_buf(),
                control.clone(),
                probe.clone(),
                segment,
                aggregate.clone(),
            )
        });
    try_join_all(workers).await?;

    let storage = DownloadStorage::open(app)?;
    let completed = storage.segments(&task.id)?;
    validate_segment_coverage(&completed, probe.total_bytes, true)?;
    let actual_size = fs::metadata(partial_path)
        .map_err(|_| "The partial download file is unavailable.".to_string())?
        .len();
    if actual_size != probe.total_bytes {
        return Err("The segmented download did not cover the expected media size.".to_string());
    }
    persist_progress(app, &task.id, probe.total_bytes, Some(probe.total_bytes));
    Ok(())
}

fn prepare_segment_transfer(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    requested_segments: u8,
    probe: &TransferProbe,
) -> Result<Vec<DownloadSegment>, String> {
    let storage = DownloadStorage::open(app)?;
    let existing = storage.segments(&task.id)?;
    let stored_entity = storage.entity_hash(&task.id)?;
    let reusable = !existing.is_empty()
        && stored_entity.as_deref() == Some(probe.entity_hash.as_str())
        && validate_segment_coverage(&existing, probe.total_bytes, false).is_ok()
        && fs::metadata(partial_path)
            .map(|metadata| metadata.is_file() && metadata.len() == probe.total_bytes)
            .unwrap_or(false);

    let segments = if reusable {
        existing
    } else {
        remove_partial(partial_path)?;
        let planned = plan_segments(probe.total_bytes, requested_segments)?;
        storage.replace_segments(&task.id, &planned)?;
        planned
    };

    if !partial_path.exists() {
        ensure_space(
            partial_path
                .parent()
                .ok_or_else(|| "Invalid destination path.".to_string())?,
            probe.total_bytes,
        )?;
        let file = OpenOptions::new()
            .create_new(true)
            .read(true)
            .write(true)
            .open(partial_path)
            .map_err(|_| "Failed to create the partial download file.".to_string())?;
        file.set_len(probe.total_bytes)
            .map_err(|_| "Failed to prepare the partial download file.".to_string())?;
        file.sync_all()
            .map_err(|_| "Failed to flush the partial download file.".to_string())?;
    }
    let downloaded = segments.iter().map(|segment| segment.completed_bytes).sum();
    storage.set_entity_and_progress(
        &task.id,
        Some(&probe.entity_hash),
        downloaded,
        Some(probe.total_bytes),
    )?;
    Ok(segments)
}

fn reset_unsafe_segment_transfer(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
) -> Result<(), String> {
    remove_partial(partial_path)?;
    DownloadStorage::open(app)?.reset_transfer_facts(&task.id)
}

async fn download_segment(
    app: AppHandle,
    task: DownloadTask,
    partial_path: PathBuf,
    control: Arc<AtomicU8>,
    probe: TransferProbe,
    segment: DownloadSegment,
    aggregate: Arc<AtomicU64>,
) -> Result<(), String> {
    let mut last_error = "The segmented media transfer failed.".to_string();
    for attempt in 0..3_u64 {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        match download_segment_once(
            &app,
            &task,
            &partial_path,
            &control,
            &probe,
            &segment,
            &aggregate,
        )
        .await
        {
            Ok(()) => {
                let storage = DownloadStorage::open(&app)?;
                storage.decrement_active_segments(&task.id)?;
                emit_task(&app, &storage, &task.id);
                return Ok(());
            }
            Err(error) => last_error = error,
        }
        if attempt < 2 {
            tokio::time::sleep(Duration::from_millis(350 * (1_u64 << attempt))).await;
        }
    }
    Err(last_error)
}

async fn download_segment_once(
    app: &AppHandle,
    task: &DownloadTask,
    partial_path: &Path,
    control: &AtomicU8,
    probe: &TransferProbe,
    original: &DownloadSegment,
    aggregate: &AtomicU64,
) -> Result<(), String> {
    let stored = DownloadStorage::open(app)?
        .segment(&task.id, original.index)?
        .ok_or_else(|| "The download segment checkpoint is unavailable.".to_string())?;
    if stored.completed_bytes >= stored.length() {
        DownloadStorage::open(app)?.update_segment_checkpoint(
            &task.id,
            stored.index,
            stored.length(),
            "completed",
        )?;
        return Ok(());
    }
    let request_start = stored.next_offset();
    let resolved = resolve_task_remote(app, task).await?;
    let response =
        request_media_range(&resolved, Some((request_start, Some(stored.range_end)))).await?;
    validate_segment_response(
        &response,
        request_start,
        stored.range_end,
        probe.total_bytes,
        &probe.entity_hash,
    )?;

    let mut file = OpenOptions::new()
        .write(true)
        .open(partial_path)
        .map_err(|_| "Failed to open the partial download file.".to_string())?;
    file.seek(SeekFrom::Start(request_start))
        .map_err(|_| "Failed to seek within the partial download file.".to_string())?;
    DownloadStorage::open(app)?.update_segment_checkpoint(
        &task.id,
        stored.index,
        stored.completed_bytes,
        "running",
    )?;

    let mut completed = stored.completed_bytes;
    let mut stream = response.bytes_stream();
    let mut last_emit = Instant::now();
    while let Some(next) = stream.next().await {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        let chunk =
            next.map_err(|_| "The segmented media transfer was interrupted.".to_string())?;
        let remaining = stored.length().saturating_sub(completed);
        if chunk.is_empty() || chunk.len() as u64 > remaining {
            return Err("The media server returned an invalid segmented response.".to_string());
        }
        acquire_global_budget_async(app, chunk.len() as u64, control).await?;
        file.write_all(&chunk)
            .map_err(|_| "Failed while writing the partial download file.".to_string())?;
        completed = completed.saturating_add(chunk.len() as u64);
        DownloadStorage::open(app)?.update_segment_checkpoint(
            &task.id,
            stored.index,
            completed,
            "running",
        )?;
        let total_downloaded =
            aggregate.fetch_add(chunk.len() as u64, Ordering::AcqRel) + chunk.len() as u64;
        if last_emit.elapsed() >= Duration::from_millis(250) {
            persist_progress(app, &task.id, total_downloaded, Some(probe.total_bytes));
            last_emit = Instant::now();
        }
    }
    if completed != stored.length() {
        return Err("The segmented media transfer ended before its range completed.".to_string());
    }
    file.sync_all()
        .map_err(|_| "Failed to flush the downloaded segment.".to_string())?;
    DownloadStorage::open(app)?.update_segment_checkpoint(
        &task.id,
        stored.index,
        completed,
        "completed",
    )?;
    persist_progress(
        app,
        &task.id,
        aggregate.load(Ordering::Acquire),
        Some(probe.total_bytes),
    );
    Ok(())
}

fn plan_segments(total_bytes: u64, requested: u8) -> Result<Vec<DownloadSegment>, String> {
    if total_bytes == 0 || requested == 0 {
        return Err("The media size is unavailable for segmented download.".to_string());
    }
    let count = u64::from(requested).min(total_bytes);
    let mut segments = Vec::with_capacity(count as usize);
    for index in 0..count {
        let range_start = total_bytes.saturating_mul(index) / count;
        let next_start = total_bytes.saturating_mul(index + 1) / count;
        segments.push(DownloadSegment {
            index: index as u8,
            range_start,
            range_end: next_start.saturating_sub(1),
            completed_bytes: 0,
            status: "queued".to_string(),
        });
    }
    validate_segment_coverage(&segments, total_bytes, false)?;
    Ok(segments)
}

fn validate_segment_coverage(
    segments: &[DownloadSegment],
    total_bytes: u64,
    require_complete: bool,
) -> Result<(), String> {
    if segments.is_empty() || total_bytes == 0 {
        return Err("The segmented download plan is empty.".to_string());
    }
    let mut expected_start = 0_u64;
    for segment in segments {
        if segment.range_start != expected_start
            || segment.range_end < segment.range_start
            || segment.range_end >= total_bytes
            || segment.completed_bytes > segment.length()
            || (require_complete
                && (segment.completed_bytes != segment.length() || segment.status != "completed"))
        {
            return Err("The segmented download coverage is inconsistent.".to_string());
        }
        expected_start = segment.range_end.saturating_add(1);
    }
    if expected_start != total_bytes {
        return Err("The segmented download does not cover the media size.".to_string());
    }
    Ok(())
}

fn validate_segment_response(
    response: &Response,
    expected_start: u64,
    expected_end: u64,
    expected_total: u64,
    expected_entity: &str,
) -> Result<(), String> {
    if response.status() != StatusCode::PARTIAL_CONTENT {
        return Err("The media server did not honor the segmented range request.".to_string());
    }
    let content_range = parse_content_range(response.headers())
        .ok_or_else(|| "The media server returned an invalid segmented range.".to_string())?;
    if content_range
        != (ParsedContentRange {
            start: expected_start,
            end: expected_end,
            total: expected_total,
        })
        || response.content_length()
            != Some(
                expected_end
                    .saturating_sub(expected_start)
                    .saturating_add(1),
            )
        || response_entity_hash(response).as_deref() != Some(expected_entity)
    {
        return Err("The media identity or segmented range changed during transfer.".to_string());
    }
    validate_media_content_type(response)
}

fn parse_content_range(headers: &HeaderMap) -> Option<ParsedContentRange> {
    let value = headers.get(CONTENT_RANGE)?.to_str().ok()?;
    let value = value.strip_prefix("bytes ")?;
    let (range, total) = value.split_once('/')?;
    let (start, end) = range.split_once('-')?;
    let parsed = ParsedContentRange {
        start: start.parse().ok()?,
        end: end.parse().ok()?,
        total: total.parse().ok()?,
    };
    (parsed.start <= parsed.end && parsed.end < parsed.total).then_some(parsed)
}

fn global_rate_limit(app: &AppHandle) -> Option<u64> {
    read_download_settings(app)
        .ok()
        .and_then(|value| value.global_speed_limit_bytes_per_second)
}

fn reserve_global_budget(app: &AppHandle, bytes: u64) -> (Option<u64>, Duration) {
    let rate = global_rate_limit(app);
    let wait = app
        .try_state::<DownloadQueueState>()
        .map(|state| state.limiter.reserve(rate, bytes))
        .unwrap_or(Duration::ZERO);
    (rate, wait)
}

fn acquire_global_budget_blocking(
    app: &AppHandle,
    bytes: u64,
    control: &AtomicU8,
) -> Result<(), String> {
    let (reserved_rate, mut remaining) = reserve_global_budget(app, bytes);
    while !remaining.is_zero() {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        if global_rate_limit(app) != reserved_rate {
            return Ok(());
        }
        let slice = remaining.min(Duration::from_millis(100));
        std::thread::sleep(slice);
        remaining = remaining.saturating_sub(slice);
    }
    Ok(())
}

async fn acquire_global_budget_async(
    app: &AppHandle,
    bytes: u64,
    control: &AtomicU8,
) -> Result<(), String> {
    let (reserved_rate, mut remaining) = reserve_global_budget(app, bytes);
    while !remaining.is_zero() {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING {
            return Err("Download stopped by user request.".to_string());
        }
        if global_rate_limit(app) != reserved_rate {
            return Ok(());
        }
        let slice = remaining.min(Duration::from_millis(100));
        tokio::time::sleep(slice).await;
        remaining = remaining.saturating_sub(slice);
    }
    Ok(())
}

async fn resolve_task_remote(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<ResolvedRemote, String> {
    if task.source_type == "alist" {
        return resolve_alist(app, &task.source_id, &task.item_id).await;
    }
    if task.source_type == "server" {
        return resolve_server_media(app, task).await;
    }
    let resolved = provider_file::resolve_source_download(
        app,
        &task.source_id,
        &task.source_type,
        &task.item_id,
        task.media_source_id.as_deref(),
    )
    .await?;
    Ok(ResolvedRemote {
        url: resolved.url,
        headers: resolved.headers,
    })
}

async fn resolve_server_media(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<ResolvedRemote, String> {
    if task.online_identity.is_some() || task.item_id.starts_with("online-") {
        return Err(
            "This Server online source does not yet expose a safe offline download stream."
                .to_string(),
        );
    }
    let entry_id = server_entry_id(&task.item_id, task.media_source_id.as_deref())
        .ok_or_else(|| "The Server media entry identity is invalid.".to_string())?;
    let config = resolve_datasource(app, &task.source_id, "server")?;
    let base = validate_http_url(&config.url)?;
    if base.query().is_some()
        || base.fragment().is_some()
        || (!base.path().is_empty() && base.path() != "/")
    {
        return Err("The OhMyCine Server address is invalid.".to_string());
    }
    let credential_ref = extra_string(&config, "credentialRef")?;
    let raw = credential::read_credential_value(app, &credential_ref)
        .await?
        .ok_or_else(|| "OhMyCine Server device credentials are missing.".to_string())?;
    let envelope: ServerCredentialEnvelope = serde_json::from_str(&raw)
        .map_err(|_| "Stored OhMyCine Server credentials are invalid.".to_string())?;
    if envelope.version != 1
        || envelope.provider != "server"
        || !envelope.access_token.trim().starts_with("omc_player_")
    {
        return Err("Stored OhMyCine Server credentials are invalid.".to_string());
    }
    let url = base
        .join(&format!("/api/v1/player/media-entries/{entry_id}/stream"))
        .map_err(|_| "The OhMyCine Server media address is invalid.".to_string())?;
    let mut headers = HeaderMap::new();
    headers.insert(
        AUTHORIZATION,
        HeaderValue::from_str(&format!("Bearer {}", envelope.access_token.trim()))
            .map_err(|_| "Stored OhMyCine Server credentials are invalid.".to_string())?,
    );
    Ok(ResolvedRemote { url, headers })
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
    request_media_range(resolved, (offset > 0).then_some((offset, None))).await
}

async fn load_attachment_bytes(
    kind: &str,
    input: &OfflineAttachmentInput,
) -> Result<Vec<u8>, String> {
    match (&input.data_url, &input.remote_url) {
        (Some(value), None) => decode_attachment_data_url(kind, value),
        (None, Some(value)) => {
            let url = Url::parse(value.trim())
                .map_err(|_| "Offline attachment address is invalid.".to_string())?;
            if !matches!(url.scheme(), "http" | "https")
                || url.host_str().is_none()
                || !url.username().is_empty()
                || url.password().is_some()
            {
                return Err("Offline attachment address is invalid.".to_string());
            }
            let mut headers = HeaderMap::new();
            if input.headers.len() > MAX_OFFLINE_ATTACHMENT_HEADERS {
                return Err("Offline attachment headers are invalid.".to_string());
            }
            let mut total_header_bytes = 0usize;
            for (name, value) in &input.headers {
                let name = HeaderName::from_bytes(name.as_bytes())
                    .map_err(|_| "Offline attachment headers are invalid.".to_string())?;
                if !is_allowed_attachment_header(&name)
                    || value.len() > MAX_OFFLINE_ATTACHMENT_HEADER_VALUE_BYTES
                {
                    return Err("Offline attachment headers are invalid.".to_string());
                }
                total_header_bytes = total_header_bytes
                    .saturating_add(name.as_str().len())
                    .saturating_add(value.len());
                if total_header_bytes > MAX_OFFLINE_ATTACHMENT_HEADER_BYTES {
                    return Err("Offline attachment headers are invalid.".to_string());
                }
                let value = HeaderValue::from_str(value)
                    .map_err(|_| "Offline attachment headers are invalid.".to_string())?;
                headers.insert(name, value);
            }
            request_attachment_bytes(kind, url, headers).await
        }
        _ => Err("Offline attachment payload is invalid.".to_string()),
    }
}

fn decode_attachment_data_url(kind: &str, value: &str) -> Result<Vec<u8>, String> {
    let (metadata, payload) = value
        .split_once(',')
        .ok_or_else(|| "Offline attachment payload is invalid.".to_string())?;
    if !metadata.starts_with("data:") || !metadata.ends_with(";base64") {
        return Err("Offline attachment payload is invalid.".to_string());
    }
    let content_type = metadata[5..metadata.len() - ";base64".len()].trim();
    if !attachment_content_type_is_compatible(kind, content_type) {
        return Err("Offline attachment payload is invalid.".to_string());
    }
    let max_bytes = max_attachment_bytes(kind)?;
    if payload.len()
        > max_bytes
            .saturating_mul(4)
            .saturating_div(3)
            .saturating_add(4)
    {
        return Err("Offline attachment payload is too large.".to_string());
    }
    let decoded = BASE64_STANDARD
        .decode(payload)
        .map_err(|_| "Offline attachment payload is invalid.".to_string())?;
    if decoded.is_empty() || decoded.len() > max_bytes {
        return Err("Offline attachment payload is too large.".to_string());
    }
    Ok(decoded)
}

async fn request_attachment_bytes(
    kind: &str,
    mut url: Url,
    mut headers: HeaderMap,
) -> Result<Vec<u8>, String> {
    let client = controlled_client()?;
    let original_origin = origin_key(&url);
    let max_bytes = max_attachment_bytes(kind)?;
    for _ in 0..=MAX_REDIRECTS {
        let mut response = client
            .get(url.clone())
            .headers(headers.clone())
            .send()
            .await
            .map_err(|_| "Offline attachment is temporarily unavailable.".to_string())?;
        if response.status().is_redirection() {
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| "Offline attachment redirect is invalid.".to_string())?;
            let next = url
                .join(location)
                .map_err(|_| "Offline attachment redirect is invalid.".to_string())?;
            validate_redirect(&url, &next)?;
            if origin_key(&next) != original_origin {
                headers.clear();
            }
            url = next;
            continue;
        }
        if !response.status().is_success()
            || response.content_length().unwrap_or(0) > max_bytes as u64
        {
            return Err("Offline attachment is temporarily unavailable.".to_string());
        }
        if let Some(content_type) = response
            .headers()
            .get(CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
        {
            if !attachment_content_type_is_compatible(kind, content_type) {
                return Err("Offline attachment response type is invalid.".to_string());
            }
        }
        let mut bytes = Vec::with_capacity(response.content_length().unwrap_or(0) as usize);
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| "Offline attachment is temporarily unavailable.".to_string())?
        {
            if bytes.len().saturating_add(chunk.len()) > max_bytes {
                return Err("Offline attachment payload is too large.".to_string());
            }
            bytes.extend_from_slice(&chunk);
        }
        return Ok(bytes);
    }
    Err("Offline attachment returned too many redirects.".to_string())
}

fn validate_attachment_kind(value: &str) -> Result<&'static str, String> {
    match value.trim() {
        "poster" => Ok("poster"),
        "backdrop" => Ok("backdrop"),
        "still" => Ok("still"),
        "subtitle" => Ok("subtitle"),
        "danmaku" => Ok("danmaku"),
        _ => Err("Offline attachment kind is invalid.".to_string()),
    }
}

fn max_attachment_bytes(kind: &str) -> Result<usize, String> {
    match kind {
        "poster" | "backdrop" | "still" => Ok(MAX_OFFLINE_ARTWORK_BYTES),
        "subtitle" => Ok(MAX_OFFLINE_SUBTITLE_BYTES),
        "danmaku" => Ok(MAX_OFFLINE_DANMAKU_BYTES),
        _ => Err("Offline attachment kind is invalid.".to_string()),
    }
}

fn is_allowed_attachment_header(name: &HeaderName) -> bool {
    !matches!(
        name.as_str().to_ascii_lowercase().as_str(),
        "host"
            | "content-length"
            | "range"
            | "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
            | "expect"
    )
}

fn attachment_content_type_is_compatible(kind: &str, value: &str) -> bool {
    let content_type = value
        .split(';')
        .next()
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    match kind {
        "poster" | "backdrop" | "still" => {
            content_type.starts_with("image/") || content_type == "application/octet-stream"
        }
        "subtitle" => {
            content_type.starts_with("text/")
                || matches!(
                    content_type.as_str(),
                    "application/octet-stream"
                        | "application/x-subrip"
                        | "application/x-ass"
                        | "application/x-ssa"
                        | "application/ttml+xml"
                        | "application/vtt"
                )
        }
        "danmaku" => matches!(
            content_type.as_str(),
            "application/json" | "text/json" | "text/plain" | "application/octet-stream"
        ),
        _ => false,
    }
}

fn valid_offline_danmaku_entry(value: &Value) -> bool {
    let Some(entry) = value.as_object() else {
        return false;
    };
    let Some(id) = entry.get("id").and_then(Value::as_str) else {
        return false;
    };
    let Some(time) = entry.get("time").and_then(Value::as_f64) else {
        return false;
    };
    let Some(text) = entry.get("text").and_then(Value::as_str) else {
        return false;
    };
    if id.len() > 128 || !time.is_finite() || time < 0.0 || text.len() > 2_000 {
        return false;
    }
    if let Some(mode) = entry.get("mode") {
        if !matches!(mode.as_str(), Some("scroll" | "top" | "bottom")) {
            return false;
        }
    }
    entry
        .get("color")
        .is_none_or(|color| color.as_str().is_some_and(|value| value.len() <= 32))
}

fn validate_attachment_bytes(
    kind: &str,
    extension: Option<&str>,
    bytes: &[u8],
) -> Result<(), String> {
    if bytes.is_empty() || bytes.len() > max_attachment_bytes(kind)? {
        return Err("Offline attachment payload is invalid.".to_string());
    }
    match kind {
        "poster" | "backdrop" | "still" => {
            crate::commands::image_cache::detect_image_mime(bytes)
                .ok_or_else(|| "Offline artwork is invalid.".to_string())?;
        }
        "subtitle" => {
            let extension = extension.unwrap_or_default().trim().to_ascii_lowercase();
            if !matches!(extension.as_str(), "srt" | "ass" | "ssa" | "vtt" | "sub")
                || std::str::from_utf8(bytes).is_err()
            {
                return Err("Offline subtitle is invalid.".to_string());
            }
        }
        "danmaku" => {
            let value: Value = serde_json::from_slice(bytes)
                .map_err(|_| "Offline danmaku is invalid.".to_string())?;
            let entries = value
                .as_array()
                .filter(|entries| entries.len() <= MAX_OFFLINE_DANMAKU_ENTRIES)
                .ok_or_else(|| "Offline danmaku is invalid.".to_string())?;
            if entries
                .iter()
                .any(|entry| !valid_offline_danmaku_entry(entry))
            {
                return Err("Offline danmaku is invalid.".to_string());
            }
        }
        _ => return Err("Offline attachment kind is invalid.".to_string()),
    }
    Ok(())
}

fn attachment_extension(
    kind: &str,
    extension: Option<&str>,
    bytes: &[u8],
) -> Result<&'static str, String> {
    match kind {
        "poster" | "backdrop" | "still" => {
            match crate::commands::image_cache::detect_image_mime(bytes) {
                Some("image/jpeg") => Ok("jpg"),
                Some("image/png") => Ok("png"),
                Some("image/webp") => Ok("webp"),
                Some("image/gif") => Ok("gif"),
                _ => Err("Offline artwork is invalid.".to_string()),
            }
        }
        "subtitle" => match extension
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "srt" => Ok("srt"),
            "ass" => Ok("ass"),
            "ssa" => Ok("ssa"),
            "vtt" => Ok("vtt"),
            "sub" => Ok("sub"),
            _ => Err("Offline subtitle is invalid.".to_string()),
        },
        "danmaku" => Ok("json"),
        _ => Err("Offline attachment kind is invalid.".to_string()),
    }
}

async fn request_media_range(
    resolved: &ResolvedRemote,
    requested_range: Option<(u64, Option<u64>)>,
) -> Result<Response, String> {
    let client = controlled_client()?;
    let mut url = resolved.url.clone();
    let mut headers = resolved.headers.clone();
    let original_origin = origin_key(&url);
    for _ in 0..=MAX_REDIRECTS {
        let mut request = client.get(url.clone()).headers(headers.clone());
        if let Some((start, end)) = requested_range {
            let value = end
                .map(|end| format!("bytes={start}-{end}"))
                .unwrap_or_else(|| format!("bytes={start}-"));
            request = request.header(RANGE, value);
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
    validate_media_content_type(response)
}

fn validate_media_content_type(response: &Response) -> Result<(), String> {
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
    if !matches!(
        request.source_type.as_str(),
        "local"
            | "alist"
            | "clouddrive2"
            | "webdav"
            | "123"
            | "quark"
            | "emby"
            | "jellyfin"
            | "server"
    ) {
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
    if let Some(parent_id) = request.parent_id.as_deref() {
        validate_task_id(parent_id)?;
        let group_name = request.group_name.as_deref().unwrap_or_default().trim();
        if group_name.is_empty()
            || group_name.len() > 512
            || group_name.chars().any(char::is_control)
        {
            return Err("Invalid aggregate download name.".to_string());
        }
    } else if request.group_name.is_some() {
        return Err("Aggregate download identity is missing.".to_string());
    }
    if let Some(media_source_id) = request.media_source_id.as_deref() {
        validate_stable_id(media_source_id, "Invalid media source identity.")?;
    }
    if let Some(variant_id) = request.variant_id.as_deref() {
        validate_stable_id(variant_id, "Invalid media variant identity.")?;
    }
    if let Some(library_id) = request.library_id.as_deref() {
        validate_stable_id(library_id, "Invalid media library identity.")?;
    }
    if let Some(identity) = request.online_identity.as_ref() {
        if request.source_type != "server" || !request.item_id.starts_with("online-") {
            return Err("The online media identity does not match this data source.".to_string());
        }
        validate_stable_id(&identity.library_id, "Invalid online library identity.")?;
        validate_stable_id(&identity.work_id, "Invalid online work identity.")?;
        validate_stable_id(&identity.segment_id, "Invalid online segment identity.")?;
        validate_stable_id(&identity.version_id, "Invalid online version identity.")?;
    }
    if request.source_type == "server" {
        if request.item_id.starts_with("online-") {
            if request.online_identity.is_none() {
                return Err("The Server online media identity is incomplete.".to_string());
            }
            return Err(
                "This Server online source does not yet expose a safe offline download stream."
                    .to_string(),
            );
        }
        if request.online_identity.is_some()
            || server_entry_id(&request.item_id, request.media_source_id.as_deref()).is_none()
        {
            return Err("The Server media entry identity is invalid.".to_string());
        }
    }
    if let Some(snapshot) = request.detail_snapshot.as_ref() {
        validate_offline_snapshot(snapshot)?;
    }
    Ok(())
}

fn validate_offline_snapshot(snapshot: &OfflineDetailSnapshot) -> Result<(), String> {
    if snapshot.name.trim().is_empty()
        || snapshot.name.len() > 512
        || snapshot.media_type.len() > 32
        || !matches!(
            snapshot.media_type.as_str(),
            "movie" | "series" | "season" | "episode" | "file"
        )
        || snapshot
            .rating
            .is_some_and(|value| !value.is_finite() || !(0.0..=10.0).contains(&value))
        || snapshot.duration.is_some_and(|value| value > 31_536_000)
    {
        return Err("The offline media detail snapshot is invalid.".to_string());
    }
    for value in [
        snapshot.original_title.as_deref(),
        snapshot.tagline.as_deref(),
        snapshot.imdb_id.as_deref(),
        snapshot.series_name.as_deref(),
    ]
    .into_iter()
    .flatten()
    {
        if value.len() > 512 || value.as_bytes().contains(&0) {
            return Err("The offline media detail snapshot is invalid.".to_string());
        }
    }
    if snapshot
        .overview
        .as_deref()
        .is_some_and(|value| value.len() > 16_384)
        || [
            &snapshot.genres,
            &snapshot.directors,
            &snapshot.writers,
            &snapshot.cast,
        ]
        .into_iter()
        .any(|values| {
            values.len() > 128
                || values.iter().any(|value| {
                    value.trim().is_empty() || value.len() > 256 || value.as_bytes().contains(&0)
                })
        })
    {
        return Err("The offline media detail snapshot is invalid.".to_string());
    }
    let serialized = serde_json::to_vec(snapshot)
        .map_err(|_| "The offline media detail snapshot is invalid.".to_string())?;
    if serialized.len() > 64 * 1024 {
        return Err("The offline media detail snapshot is too large.".to_string());
    }
    Ok(())
}

fn server_entry_id<'a>(item_id: &'a str, media_source_id: Option<&'a str>) -> Option<&'a str> {
    item_id
        .split('|')
        .nth(3)
        .filter(|value| is_positive_decimal_id(value))
        .or_else(|| media_source_id.filter(|value| is_positive_decimal_id(value)))
}

fn is_positive_decimal_id(value: &str) -> bool {
    !value.is_empty()
        && value.bytes().all(|byte| byte.is_ascii_digit())
        && value.bytes().any(|byte| byte != b'0')
}

fn read_download_settings(app: &AppHandle) -> Result<DownloadSettings, String> {
    let Some(raw) = settings::read_player_setting(app, DOWNLOAD_SETTINGS_KEY)? else {
        return Ok(DownloadSettings::default());
    };
    let value: DownloadSettings = serde_json::from_str(&raw)
        .map_err(|_| "The saved download settings are invalid.".to_string())?;
    validate_download_settings(&value)?;
    Ok(value)
}

fn validate_download_settings(value: &DownloadSettings) -> Result<(), String> {
    if !(1..=MAX_CONCURRENT_TASKS).contains(&value.concurrent_tasks) {
        return Err("Concurrent download tasks must be between 1 and 8.".to_string());
    }
    if !(1..=MAX_SEGMENTS_PER_TASK).contains(&value.segments_per_task) {
        return Err("Download segments per task must be between 1 and 16.".to_string());
    }
    if value
        .global_speed_limit_bytes_per_second
        .is_some_and(|limit| limit < 64 * 1024)
    {
        return Err("The global download speed limit is too small.".to_string());
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
    entity_identity_hash(etag, modified, total)
}

fn entity_identity_hash(
    etag: Option<&str>,
    last_modified: Option<&str>,
    total_bytes: Option<u64>,
) -> Option<String> {
    if let Some(strong_etag) = etag
        .map(str::trim)
        .filter(|value| !value.is_empty() && !value.to_ascii_lowercase().starts_with("w/"))
    {
        return Some(hash_text(&format!("etag\0{strong_etag}")));
    }
    let modified = last_modified
        .map(str::trim)
        .filter(|value| !value.is_empty())?;
    let total = total_bytes?;
    Some(hash_text(&format!("modified-size\0{modified}\0{total}")))
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

fn hash_bytes(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
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

#[cfg(target_os = "android")]
fn is_android_tree_uri(value: &str) -> bool {
    let trimmed = value.trim();
    trimmed.starts_with("content://")
        && trimmed.contains("/tree/")
        && !trimmed.contains('\0')
        && trimmed.len() <= 4096
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
        || lower.contains("bearer ")
        || lower.contains("access_token")
        || lower.contains("token=")
        || lower.contains("signature=")
        || lower.contains("sign=")
        || lower.contains("sig=")
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
        let (speed, eta) = app
            .try_state::<DownloadQueueState>()
            .and_then(|state| {
                let mut meters = state.progress.lock().ok()?;
                let now = Instant::now();
                let previous = meters.insert(
                    task_id.to_string(),
                    ProgressMeter {
                        bytes,
                        measured_at: now,
                    },
                );
                let speed = previous
                    .and_then(|previous| {
                        let elapsed = now.duration_since(previous.measured_at).as_secs_f64();
                        (elapsed > 0.0)
                            .then(|| (bytes.saturating_sub(previous.bytes) as f64 / elapsed) as u64)
                    })
                    .unwrap_or(0);
                let eta = total.and_then(|total| {
                    (speed > 0).then(|| total.saturating_sub(bytes).div_ceil(speed))
                });
                Some((speed, eta))
            })
            .unwrap_or((0, None));
        let _ = storage.set_progress_metrics(task_id, bytes, total, speed, eta);
        emit_task(app, &storage, task_id);
    }
}

fn emit_task(app: &AppHandle, storage: &DownloadStorage, task_id: &str) {
    if let Ok(Some(task)) = storage.get(task_id) {
        let _ = app.emit(PROGRESS_EVENT, task);
    }
}

fn cleanup_cancelled_task(
    app: &AppHandle,
    storage: &DownloadStorage,
    task: &DownloadTask,
) -> Result<(), String> {
    let relative_path = format!("{}.partial", task.destination_name);
    #[cfg(not(target_os = "android"))]
    let cleanup = try_remove_owned_partial(&task.destination_directory, &relative_path)
        .err()
        .map(|error_code| CleanupRecord {
            id: random_id(),
            task_id: task.id.clone(),
            path_kind: "desktop_partial".to_string(),
            root_reference: task.destination_directory.clone(),
            relative_path: relative_path.clone(),
            attempts: 0,
            last_error_code: error_code,
            created_at: unix_timestamp(),
        });
    #[cfg(target_os = "android")]
    let cleanup = Some(CleanupRecord {
        id: random_id(),
        task_id: task.id.clone(),
        path_kind: "android_saf_partial".to_string(),
        root_reference: task.destination_directory.clone(),
        relative_path,
        attempts: 0,
        last_error_code: "android_cleanup_pending".to_string(),
        created_at: unix_timestamp(),
    });
    storage.delete_task_and_segments(&task.id, cleanup.as_ref())?;
    let _ = app.emit(REMOVED_EVENT, DownloadRemovedEvent { task_id: &task.id });
    Ok(())
}

#[cfg(target_os = "android")]
async fn cleanup_cancelled_android_task(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<(), String> {
    let relative_path = format!(".{}.ohmycine-part", task.destination_name);
    let cleanup = download_android::delete_partial_document(
        app,
        &task.destination_directory,
        &task.destination_name,
    )
    .await
    .err()
    .map(|_| CleanupRecord {
        id: random_id(),
        task_id: task.id.clone(),
        path_kind: "android_saf_partial".to_string(),
        root_reference: task.destination_directory.clone(),
        relative_path,
        attempts: 0,
        last_error_code: "android_cleanup_pending".to_string(),
        created_at: unix_timestamp(),
    });
    DownloadStorage::open(app)?.delete_task_and_segments(&task.id, cleanup.as_ref())?;
    let _ = app.emit(REMOVED_EVENT, DownloadRemovedEvent { task_id: &task.id });
    Ok(())
}

#[cfg(target_os = "android")]
async fn cleanup_cancelled_android_final(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<(), String> {
    let cleanup = download_android::delete_completed_document(
        app,
        &task.destination_directory,
        &task.destination_name,
    )
    .await
    .err()
    .map(|_| CleanupRecord {
        id: random_id(),
        task_id: task.id.clone(),
        path_kind: "android_saf_final".to_string(),
        root_reference: task.destination_directory.clone(),
        relative_path: task.destination_name.clone(),
        attempts: 0,
        last_error_code: "android_cleanup_pending".to_string(),
        created_at: unix_timestamp(),
    });
    DownloadStorage::open(app)?.delete_task_and_segments(&task.id, cleanup.as_ref())?;
    let _ = app.emit(REMOVED_EVENT, DownloadRemovedEvent { task_id: &task.id });
    Ok(())
}

#[cfg(target_os = "android")]
async fn retry_android_cleanup(app: &AppHandle) -> Result<(), String> {
    let records = DownloadStorage::open(app)?.android_cleanup_records()?;
    for record in records {
        let destination_name = android_cleanup_destination_name(&record);
        let removed = match (record.path_kind.as_str(), destination_name) {
            ("android_saf_partial", Some(name)) => {
                download_android::delete_partial_document(app, &record.root_reference, name)
                    .await
                    .is_ok()
            }
            ("android_saf_final", Some(name)) => {
                download_android::delete_completed_document(app, &record.root_reference, name)
                    .await
                    .is_ok()
            }
            _ => false,
        };
        DownloadStorage::open(app)?.finish_cleanup_attempt(&record.id, removed)?;
    }
    Ok(())
}

#[cfg(target_os = "android")]
fn android_cleanup_destination_name(record: &CleanupRecord) -> Option<&str> {
    let value = if record.path_kind == "android_saf_partial" {
        record
            .relative_path
            .strip_prefix('.')?
            .strip_suffix(".ohmycine-part")?
    } else {
        record.relative_path.as_str()
    };
    (!value.is_empty()
        && value.len() <= 240
        && value != "."
        && value != ".."
        && !value.contains(['/', '\\'])
        && !value.as_bytes().contains(&0))
    .then_some(value)
}

fn try_remove_owned_partial(root: &str, relative_path: &str) -> Result<(), String> {
    let root = Path::new(root);
    if !root.is_absolute() || root.as_os_str().is_empty() {
        return Err("cleanup_root_invalid".to_string());
    }
    if Path::new(relative_path).components().count() != 1
        || relative_path.as_bytes().contains(&0)
        || relative_path.contains(['/', '\\'])
        || !relative_path.ends_with(".partial")
    {
        return Err("cleanup_relative_path_invalid".to_string());
    }
    let canonical_root = fs::canonicalize(root).map_err(|_| "cleanup_root_missing".to_string())?;
    if !canonical_root.is_dir() {
        return Err("cleanup_root_invalid".to_string());
    }
    let path = canonical_root.join(relative_path);
    if !path.exists() {
        return Ok(());
    }
    fs::remove_file(path).map_err(|_| "cleanup_remove_failed".to_string())
}

#[cfg(not(target_os = "android"))]
fn cleanup_cancelled_final(storage: &DownloadStorage, task: &DownloadTask) -> Result<(), String> {
    if let Err(error) = try_remove_owned_final(&task.destination_directory, &task.destination_name)
    {
        storage.insert_cleanup(&CleanupRecord {
            id: random_id(),
            task_id: task.id.clone(),
            path_kind: "desktop_final".to_string(),
            root_reference: task.destination_directory.clone(),
            relative_path: task.destination_name.clone(),
            attempts: 0,
            last_error_code: error,
            created_at: unix_timestamp(),
        })?;
    }
    Ok(())
}

fn try_remove_owned_final(root: &str, relative_path: &str) -> Result<(), String> {
    let root = Path::new(root);
    if !root.is_absolute() || root.as_os_str().is_empty() {
        return Err("cleanup_root_invalid".to_string());
    }
    if safe_display_name(relative_path) != relative_path
        || Path::new(relative_path).components().count() != 1
        || relative_path.as_bytes().contains(&0)
        || relative_path.contains(['/', '\\'])
        || relative_path.ends_with(".partial")
    {
        return Err("cleanup_relative_path_invalid".to_string());
    }
    let canonical_root = fs::canonicalize(root).map_err(|_| "cleanup_root_missing".to_string())?;
    if !canonical_root.is_dir() {
        return Err("cleanup_root_invalid".to_string());
    }
    let path = canonical_root.join(relative_path);
    if !path.exists() {
        return Ok(());
    }
    fs::remove_file(path).map_err(|_| "cleanup_remove_failed".to_string())
}

struct DownloadStorage {
    conn: Connection,
}

impl DownloadStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let conn = Connection::open(storage::data_file(app, DATABASE_FILE)?)
            .map_err(|_| "Failed to open the download task database.".to_string())?;
        conn.execute_batch("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
            .map_err(|_| "Failed to configure the download task database.".to_string())?;
        initialize_download_schema(&conn)?;
        let storage = Self { conn };
        storage.migrate_legacy_cancelled(app)?;
        Ok(storage)
    }
    fn insert(&self, task: &DownloadTask) -> Result<(), String> {
        let online = task.online_identity.as_ref();
        self.conn
            .execute(
                "INSERT INTO download_tasks (
                id,source_id,source_type,item_id,display_name,media_type,
                destination_directory,destination_name,status,bytes_downloaded,total_bytes,
                retry_count,error_message,created_at,updated_at,parent_id,group_name,
                media_source_id,variant_id,library_id,online_library_id,online_work_id,
                online_segment_id,online_version_id,speed_bytes_per_second,eta_seconds,
                active_segments,attachment_state
             ) VALUES (
                ?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15,?16,?17,?18,
                ?19,?20,?21,?22,?23,?24,?25,?26,?27,?28
             )",
                params![
                    task.id,
                    task.source_id,
                    task.source_type,
                    task.item_id,
                    task.display_name,
                    task.media_type,
                    task.destination_directory,
                    task.destination_name,
                    task.status,
                    task.bytes_downloaded,
                    task.total_bytes,
                    task.retry_count,
                    task.error_message,
                    task.created_at,
                    task.updated_at,
                    task.parent_id,
                    task.group_name,
                    task.media_source_id,
                    task.variant_id,
                    task.library_id,
                    online.map(|value| &value.library_id),
                    online.map(|value| &value.work_id),
                    online.map(|value| &value.segment_id),
                    online.map(|value| &value.version_id),
                    task.speed_bytes_per_second,
                    task.eta_seconds,
                    task.active_segments,
                    task.attachment_state,
                ],
            )
            .map_err(|_| "Failed to save the download task.".to_string())?;
        Ok(())
    }
    fn save_snapshot(&self, task_id: &str, snapshot: &OfflineDetailSnapshot) -> Result<(), String> {
        validate_offline_snapshot(snapshot)?;
        let serialized = serde_json::to_string(snapshot)
            .map_err(|_| "Failed to serialize the offline media detail.".to_string())?;
        self.conn
            .execute(
                "INSERT INTO download_task_snapshots(task_id,snapshot_json,created_at,updated_at)
                 VALUES(?1,?2,unixepoch(),unixepoch())
                 ON CONFLICT(task_id) DO UPDATE SET snapshot_json=excluded.snapshot_json,
                    updated_at=unixepoch()",
                params![task_id, serialized],
            )
            .map_err(|_| "Failed to save the offline media detail.".to_string())?;
        Ok(())
    }
    fn snapshot(&self, task_id: &str) -> Result<Option<OfflineDetailSnapshot>, String> {
        let serialized = self
            .conn
            .query_row(
                "SELECT snapshot_json FROM download_task_snapshots WHERE task_id=?1",
                [task_id],
                |row| row.get::<_, String>(0),
            )
            .optional()
            .map_err(|_| "Failed to read the offline media detail.".to_string())?;
        let Some(serialized) = serialized else {
            return Ok(None);
        };
        let snapshot: OfflineDetailSnapshot = serde_json::from_str(&serialized)
            .map_err(|_| "The saved offline media detail is invalid.".to_string())?;
        validate_offline_snapshot(&snapshot)?;
        Ok(Some(snapshot))
    }
    fn list(&self) -> Result<Vec<DownloadTask>, String> {
        let mut statement = self
            .conn
            .prepare(&format!(
                "SELECT {TASK_COLUMNS} FROM download_tasks
                 WHERE status NOT IN ('cancel_requested','pause_requested')
                 ORDER BY created_at DESC,id DESC"
            ))
            .map_err(|_| "Failed to read download tasks.".to_string())?;
        let tasks = statement
            .query_map([], map_task)
            .map_err(|_| "Failed to read download tasks.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read download tasks.".to_string())?;
        Ok(tasks)
    }
    fn get(&self, id: &str) -> Result<Option<DownloadTask>, String> {
        self.conn
            .query_row(
                &format!("SELECT {TASK_COLUMNS} FROM download_tasks WHERE id=?1"),
                [id],
                map_task,
            )
            .optional()
            .map_err(|_| "Failed to read the download task.".to_string())
    }
    fn find_completed(
        &self,
        source_id: &str,
        item_id: &str,
        media_source_id: Option<&str>,
        variant_id: Option<&str>,
    ) -> Result<Option<DownloadTask>, String> {
        self.conn
            .query_row(
                &format!(
                    "SELECT {TASK_COLUMNS} FROM download_tasks
                     WHERE source_id=?1 AND item_id=?2 AND status='completed'
                       AND (?3 IS NULL OR media_source_id=?3)
                       AND (?4 IS NULL OR variant_id=?4)
                     ORDER BY updated_at DESC LIMIT 1"
                ),
                params![source_id, item_id, media_source_id, variant_id],
                map_task,
            )
            .optional()
            .map_err(|_| "Failed to resolve the local offline media.".to_string())
    }
    fn claim_next_runnable(&self) -> Result<Option<DownloadTask>, String> {
        self.conn
            .query_row(
                &format!(
                    "UPDATE download_tasks
                     SET status='resolving',active_segments=0,speed_bytes_per_second=0,
                         eta_seconds=NULL,error_message=NULL,updated_at=unixepoch()
                     WHERE id=(
                         SELECT id FROM download_tasks
                         WHERE status IN ('queued','interrupted')
                         ORDER BY created_at ASC,rowid ASC LIMIT 1
                     )
                     RETURNING {TASK_COLUMNS}"
                ),
                [],
                map_task,
            )
            .optional()
            .map_err(|_| "Failed to claim the next queued download.".to_string())
    }
    fn set_status(&self, id: &str, status: &str, error: Option<&str>) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE download_tasks
             SET status=?2,error_message=?3,active_segments=0,speed_bytes_per_second=0,
                 eta_seconds=NULL,updated_at=unixepoch() WHERE id=?1",
                params![id, status, error],
            )
            .map_err(|_| "Failed to update the download task.".to_string())?;
        Ok(())
    }
    fn set_attachment_state(&self, id: &str, state: &str) -> Result<(), String> {
        if !matches!(state, "pending" | "syncing" | "complete" | "partial") {
            return Err("Invalid offline attachment state.".to_string());
        }
        self.conn
            .execute(
                "UPDATE download_tasks SET attachment_state=?2,updated_at=unixepoch() WHERE id=?1",
                params![id, state],
            )
            .map_err(|_| "Failed to update offline attachment state.".to_string())?;
        Ok(())
    }
    fn set_transfer_state(&self, id: &str, status: &str, active: u8) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE download_tasks SET status=?2,active_segments=?3,updated_at=unixepoch()
             WHERE id=?1",
                params![id, status, active],
            )
            .map_err(|_| "Failed to update the download transfer state.".to_string())?;
        Ok(())
    }
    fn decrement_active_segments(&self, id: &str) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE download_tasks
                 SET active_segments=CASE WHEN active_segments>0 THEN active_segments-1 ELSE 0 END,
                     updated_at=unixepoch()
                 WHERE id=?1",
                [id],
            )
            .map_err(|_| "Failed to update active download segments.".to_string())?;
        Ok(())
    }
    fn try_set_completed(&self, id: &str) -> Result<bool, String> {
        let changed = self.conn
            .execute(
                "UPDATE download_tasks SET status='completed',active_segments=0,
             total_bytes=COALESCE(total_bytes,bytes_downloaded),
             speed_bytes_per_second=0,eta_seconds=0,error_message=NULL,attachment_state='pending',updated_at=unixepoch()
             WHERE id=?1 AND status!='cancel_requested'",
                [id],
            )
            .map_err(|_| "Failed to complete the download task.".to_string())?;
        Ok(changed == 1)
    }
    #[cfg(target_os = "android")]
    fn set_progress(&self, id: &str, bytes: u64, total: Option<u64>) -> Result<(), String> {
        self.set_progress_metrics(id, bytes, total, 0, None)
    }
    fn set_progress_metrics(
        &self,
        id: &str,
        bytes: u64,
        total: Option<u64>,
        speed: u64,
        eta: Option<u64>,
    ) -> Result<(), String> {
        self.conn.execute(
            "UPDATE download_tasks SET bytes_downloaded=?2,total_bytes=COALESCE(?3,total_bytes),
             speed_bytes_per_second=?4,eta_seconds=?5,updated_at=unixepoch() WHERE id=?1",
            params![id, bytes, total, speed, eta],
        )
        .map_err(|_| "Failed to update download progress.".to_string())?;
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
    fn segments(&self, task_id: &str) -> Result<Vec<DownloadSegment>, String> {
        let mut statement = self
            .conn
            .prepare(
                "SELECT segment_index,range_start,range_end,completed_bytes,status
                 FROM download_segments WHERE task_id=?1 ORDER BY segment_index ASC",
            )
            .map_err(|_| "Failed to read download segment checkpoints.".to_string())?;
        let segments = statement
            .query_map([task_id], |row| {
                Ok(DownloadSegment {
                    index: row.get(0)?,
                    range_start: row.get(1)?,
                    range_end: row.get(2)?,
                    completed_bytes: row.get(3)?,
                    status: row.get(4)?,
                })
            })
            .map_err(|_| "Failed to read download segment checkpoints.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read download segment checkpoints.".to_string())?;
        Ok(segments)
    }
    fn segment(&self, task_id: &str, segment_index: u8) -> Result<Option<DownloadSegment>, String> {
        self.conn
            .query_row(
                "SELECT segment_index,range_start,range_end,completed_bytes,status
                 FROM download_segments WHERE task_id=?1 AND segment_index=?2",
                params![task_id, segment_index],
                |row| {
                    Ok(DownloadSegment {
                        index: row.get(0)?,
                        range_start: row.get(1)?,
                        range_end: row.get(2)?,
                        completed_bytes: row.get(3)?,
                        status: row.get(4)?,
                    })
                },
            )
            .optional()
            .map_err(|_| "Failed to read the download segment checkpoint.".to_string())
    }
    fn replace_segments(&self, task_id: &str, segments: &[DownloadSegment]) -> Result<(), String> {
        self.conn
            .execute_batch("BEGIN IMMEDIATE")
            .map_err(|_| "Failed to begin download segment planning.".to_string())?;
        let result = (|| {
            self.conn
                .execute("DELETE FROM download_segments WHERE task_id=?1", [task_id])
                .map_err(|_| "Failed to reset download segments.".to_string())?;
            for segment in segments {
                self.conn
                    .execute(
                        "INSERT INTO download_segments(
                            task_id,segment_index,range_start,range_end,completed_bytes,status,
                            created_at,updated_at
                         ) VALUES(?1,?2,?3,?4,?5,?6,unixepoch(),unixepoch())",
                        params![
                            task_id,
                            segment.index,
                            segment.range_start,
                            segment.range_end,
                            segment.completed_bytes,
                            segment.status,
                        ],
                    )
                    .map_err(|_| "Failed to save the download segment plan.".to_string())?;
            }
            Ok(())
        })();
        finish_transaction(&self.conn, result)
    }
    fn update_segment_checkpoint(
        &self,
        task_id: &str,
        segment_index: u8,
        completed_bytes: u64,
        status: &str,
    ) -> Result<(), String> {
        let changed = self
            .conn
            .execute(
                "UPDATE download_segments SET completed_bytes=?3,status=?4,updated_at=unixepoch()
                 WHERE task_id=?1 AND segment_index=?2
                   AND ?3 BETWEEN 0 AND (range_end-range_start+1)",
                params![task_id, segment_index, completed_bytes, status],
            )
            .map_err(|_| "Failed to save the download segment checkpoint.".to_string())?;
        if changed != 1 {
            return Err("The download segment checkpoint is inconsistent.".to_string());
        }
        Ok(())
    }
    fn reset_transfer_facts(&self, task_id: &str) -> Result<(), String> {
        self.conn
            .execute_batch("BEGIN IMMEDIATE")
            .map_err(|_| "Failed to begin unsafe resume reset.".to_string())?;
        let result = (|| {
            self.conn
                .execute("DELETE FROM download_segments WHERE task_id=?1", [task_id])
                .map_err(|_| "Failed to reset unsafe download segments.".to_string())?;
            self.conn
                .execute(
                    "UPDATE download_tasks SET entity_hash=NULL,bytes_downloaded=0,total_bytes=NULL,
                     active_segments=1,speed_bytes_per_second=0,eta_seconds=NULL,updated_at=unixepoch()
                     WHERE id=?1",
                    [task_id],
                )
                .map_err(|_| "Failed to reset unsafe download resume metadata.".to_string())?;
            Ok(())
        })();
        finish_transaction(&self.conn, result)
    }
    fn queue_retry(&self, task: &DownloadTask) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE download_tasks SET status='queued',retry_count=?2,error_message=NULL,
             active_segments=0,speed_bytes_per_second=0,eta_seconds=NULL,updated_at=?3
             WHERE id=?1",
                params![task.id, task.retry_count, task.updated_at],
            )
            .map_err(|_| "Failed to retry the download task.".to_string())?;
        Ok(())
    }
    fn recover_interrupted(&self) -> Result<(), String> {
        self.conn
            .execute(
                "UPDATE download_tasks SET status='paused',error_message=NULL,
             active_segments=0,speed_bytes_per_second=0,eta_seconds=NULL,updated_at=unixepoch()
             WHERE status='pause_requested'",
                [],
            )
            .map_err(|_| "Failed to recover user-paused download tasks.".to_string())?;
        self.conn
            .execute(
                "UPDATE download_tasks SET status='interrupted',error_message=NULL,
             active_segments=0,speed_bytes_per_second=0,eta_seconds=NULL,updated_at=unixepoch()
             WHERE status IN ('resolving','downloading','finalizing','running')",
                [],
            )
            .map_err(|_| "Failed to recover interrupted download tasks.".to_string())?;
        Ok(())
    }
    fn recover_requested_controls(&self, app: &AppHandle) -> Result<(), String> {
        let mut statement = self
            .conn
            .prepare(&format!(
                "SELECT {TASK_COLUMNS} FROM download_tasks
             WHERE status='cancel_requested' ORDER BY created_at ASC"
            ))
            .map_err(|_| "Failed to inspect interrupted download cancellation.".to_string())?;
        let tasks = statement
            .query_map([], map_task)
            .map_err(|_| "Failed to inspect interrupted download cancellation.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to inspect interrupted download cancellation.".to_string())?;
        drop(statement);
        for task in tasks {
            #[cfg(target_os = "android")]
            self.insert_cleanup(&CleanupRecord {
                id: random_id(),
                task_id: task.id.clone(),
                path_kind: "android_saf_final".to_string(),
                root_reference: task.destination_directory.clone(),
                relative_path: task.destination_name.clone(),
                attempts: 0,
                last_error_code: "android_cleanup_pending".to_string(),
                created_at: unix_timestamp(),
            })?;
            cleanup_cancelled_task(app, self, &task)?;
        }
        Ok(())
    }
    fn delete_task_and_segments(
        &self,
        task_id: &str,
        cleanup: Option<&CleanupRecord>,
    ) -> Result<(), String> {
        self.conn
            .execute_batch("BEGIN IMMEDIATE")
            .map_err(|_| "Failed to begin download cancellation.".to_string())?;
        let result = (|| {
            self.conn
                .execute("DELETE FROM download_segments WHERE task_id=?1", [task_id])
                .map_err(|_| "Failed to delete download segments.".to_string())?;
            self.conn
                .execute("DELETE FROM download_tasks WHERE id=?1", [task_id])
                .map_err(|_| "Failed to delete the download task.".to_string())?;
            if let Some(cleanup) = cleanup {
                self.insert_cleanup(cleanup)?;
            }
            Ok(())
        })();
        finish_transaction(&self.conn, result)
    }
    fn insert_cleanup(&self, cleanup: &CleanupRecord) -> Result<(), String> {
        self.conn
            .execute(
                "INSERT OR REPLACE INTO download_cleanup
             (id,task_id,path_kind,root_reference,relative_path,attempts,last_error_code,
              created_at,updated_at)
             VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?8)",
                params![
                    cleanup.id,
                    cleanup.task_id,
                    cleanup.path_kind,
                    cleanup.root_reference,
                    cleanup.relative_path,
                    cleanup.attempts,
                    cleanup.last_error_code,
                    cleanup.created_at,
                ],
            )
            .map_err(|_| "Failed to record deferred download cleanup.".to_string())?;
        Ok(())
    }
    fn migrate_legacy_cancelled(&self, app: &AppHandle) -> Result<(), String> {
        let mut statement = self
            .conn
            .prepare(&format!(
                "SELECT {TASK_COLUMNS} FROM download_tasks
             WHERE status IN ('cancelled','cancelling') ORDER BY created_at ASC"
            ))
            .map_err(|_| "Failed to inspect legacy cancelled downloads.".to_string())?;
        let tasks = statement
            .query_map([], map_task)
            .map_err(|_| "Failed to inspect legacy cancelled downloads.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to inspect legacy cancelled downloads.".to_string())?;
        drop(statement);
        for task in tasks {
            #[cfg(target_os = "android")]
            self.insert_cleanup(&CleanupRecord {
                id: random_id(),
                task_id: task.id.clone(),
                path_kind: "android_saf_final".to_string(),
                root_reference: task.destination_directory.clone(),
                relative_path: task.destination_name.clone(),
                attempts: 0,
                last_error_code: "android_cleanup_pending".to_string(),
                created_at: unix_timestamp(),
            })?;
            cleanup_cancelled_task(app, self, &task)?;
        }
        Ok(())
    }
    #[cfg(target_os = "android")]
    fn android_cleanup_records(&self) -> Result<Vec<CleanupRecord>, String> {
        let mut statement = self
            .conn
            .prepare(
                "SELECT id,task_id,path_kind,root_reference,relative_path,attempts,
                    last_error_code,created_at FROM download_cleanup
                 WHERE path_kind IN ('android_saf_partial','android_saf_final')
                 ORDER BY created_at ASC LIMIT 100",
            )
            .map_err(|_| "Failed to read deferred Android download cleanup.".to_string())?;
        let records = statement
            .query_map([], |row| {
                Ok(CleanupRecord {
                    id: row.get(0)?,
                    task_id: row.get(1)?,
                    path_kind: row.get(2)?,
                    root_reference: row.get(3)?,
                    relative_path: row.get(4)?,
                    attempts: row.get(5)?,
                    last_error_code: row.get(6)?,
                    created_at: row.get(7)?,
                })
            })
            .map_err(|_| "Failed to read deferred Android download cleanup.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read deferred Android download cleanup.".to_string())?;
        Ok(records)
    }
    #[cfg(target_os = "android")]
    fn finish_cleanup_attempt(&self, id: &str, removed: bool) -> Result<(), String> {
        if removed {
            self.conn
                .execute("DELETE FROM download_cleanup WHERE id=?1", [id])
                .map_err(|_| "Failed to finish deferred Android download cleanup.".to_string())?;
        } else {
            self.conn
                .execute(
                    "UPDATE download_cleanup SET attempts=attempts+1,updated_at=unixepoch()
                     WHERE id=?1",
                    [id],
                )
                .map_err(|_| "Failed to update deferred Android download cleanup.".to_string())?;
        }
        Ok(())
    }
    #[cfg(not(target_os = "android"))]
    fn retry_cleanup(&self) -> Result<(), String> {
        let mut statement = self
            .conn
            .prepare(
                "SELECT id,task_id,path_kind,root_reference,relative_path,attempts,
                    last_error_code,created_at
             FROM download_cleanup ORDER BY created_at ASC LIMIT 100",
            )
            .map_err(|_| "Failed to read deferred download cleanup.".to_string())?;
        let records = statement
            .query_map([], |row| {
                Ok(CleanupRecord {
                    id: row.get(0)?,
                    task_id: row.get(1)?,
                    path_kind: row.get(2)?,
                    root_reference: row.get(3)?,
                    relative_path: row.get(4)?,
                    attempts: row.get(5)?,
                    last_error_code: row.get(6)?,
                    created_at: row.get(7)?,
                })
            })
            .map_err(|_| "Failed to read deferred download cleanup.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read deferred download cleanup.".to_string())?;
        drop(statement);
        for record in records {
            let removed = match record.path_kind.as_str() {
                "desktop_partial" => {
                    try_remove_owned_partial(&record.root_reference, &record.relative_path).is_ok()
                }
                "desktop_final" => {
                    try_remove_owned_final(&record.root_reference, &record.relative_path).is_ok()
                }
                _ => false,
            };
            if removed {
                self.conn
                    .execute("DELETE FROM download_cleanup WHERE id=?1", [&record.id])
                    .map_err(|_| "Failed to finish deferred download cleanup.".to_string())?;
            } else {
                self.conn
                    .execute(
                        "UPDATE download_cleanup SET attempts=attempts+1,updated_at=unixepoch()
                     WHERE id=?1",
                        [&record.id],
                    )
                    .map_err(|_| "Failed to update deferred download cleanup.".to_string())?;
            }
        }
        Ok(())
    }
}

fn initialize_offline_storage(app: &AppHandle) -> Result<(), String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    conn.execute_batch("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
        .map_err(|_| "Failed to configure the offline media database.".to_string())?;
    initialize_offline_schema(&conn)
}

struct OfflineLocation {
    id: String,
    root_reference: String,
    relative_video_path: String,
    video_bytes: Option<u64>,
    entity_hash: Option<String>,
}

fn find_offline_location(
    app: &AppHandle,
    source_id: &str,
    item_id: &str,
    media_source_id: Option<&str>,
    variant_id: Option<&str>,
) -> Result<Option<OfflineLocation>, String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    conn.query_row(
        "SELECT id,root_reference,relative_video_path,video_bytes,entity_hash FROM offline_items
         WHERE source_id=?1 AND item_id=?2
           AND (?3 IS NULL OR media_source_id=?3)
           AND (?4 IS NULL OR variant_id=?4)
         ORDER BY completed_at DESC LIMIT 1",
        params![source_id, item_id, media_source_id, variant_id],
        |row| {
            Ok(OfflineLocation {
                id: row.get(0)?,
                root_reference: row.get(1)?,
                relative_video_path: row.get(2)?,
                video_bytes: row.get(3)?,
                entity_hash: row.get(4)?,
            })
        },
    )
    .optional()
    .map_err(|_| "Failed to resolve the offline media item.".to_string())
}

fn remove_offline_location(app: &AppHandle, id: &str) -> Result<(), String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    let record = conn
        .query_row(
            "SELECT package_id,item_id FROM offline_items WHERE id=?1",
            [id],
            |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
        )
        .optional()
        .map_err(|_| "Failed to inspect the offline media item.".to_string())?;
    let Some((package_id, item_id)) = record else {
        return Ok(());
    };
    let asset_paths = {
        let mut statement = conn
            .prepare(
                "SELECT relative_asset_path FROM offline_assets
                 WHERE package_id=?1 AND item_id=?2 AND relative_asset_path<>''",
            )
            .map_err(|_| "Failed to inspect owned offline assets.".to_string())?;
        let paths = statement
            .query_map(params![package_id, item_id], |row| row.get::<_, String>(0))
            .map_err(|_| "Failed to inspect owned offline assets.".to_string())?
            .filter_map(Result::ok)
            .collect::<Vec<_>>();
        paths
    };

    conn.execute_batch("BEGIN IMMEDIATE")
        .map_err(|_| "Failed to begin the offline media cleanup.".to_string())?;
    let result = (|| {
        conn.execute(
            "DELETE FROM offline_assets WHERE package_id=?1 AND item_id=?2",
            params![package_id, item_id],
        )
        .map_err(|_| "Failed to delete owned offline assets.".to_string())?;
        conn.execute("DELETE FROM offline_items WHERE id=?1", [id])
            .map_err(|_| "Failed to correct the missing offline media item.".to_string())?;
        conn.execute(
            "DELETE FROM offline_packages
             WHERE id=?1 AND NOT EXISTS(SELECT 1 FROM offline_items WHERE package_id=?1)",
            [&package_id],
        )
        .map_err(|_| "Failed to clean the offline media package.".to_string())?;
        Ok(())
    })();
    finish_transaction(&conn, result)?;

    let package_remains = conn
        .query_row(
            "SELECT EXISTS(SELECT 1 FROM offline_packages WHERE id=?1)",
            [&package_id],
            |row| row.get::<_, bool>(0),
        )
        .unwrap_or(true);
    remove_owned_offline_asset_files(app, &package_id, &asset_paths, package_remains)?;
    Ok(())
}

fn offline_package_id_for_task(app: &AppHandle, task_id: &str) -> Result<String, String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    conn.query_row(
        "SELECT package_id FROM offline_items WHERE id=?1",
        [task_id],
        |row| row.get(0),
    )
    .optional()
    .map_err(|_| "Failed to resolve the offline media package.".to_string())?
    .ok_or_else(|| "Offline media package is unavailable.".to_string())
}

fn offline_asset_directory_path(app: &AppHandle, package_id: &str) -> Result<PathBuf, String> {
    if package_id.len() != 64 || !package_id.bytes().all(|value| value.is_ascii_hexdigit()) {
        return Err("Offline media package identity is invalid.".to_string());
    }
    Ok(storage::initialize(app)?
        .data_dir
        .join("offline")
        .join(package_id)
        .join("assets"))
}

fn offline_asset_directory_for_write(app: &AppHandle, package_id: &str) -> Result<PathBuf, String> {
    let directory = offline_asset_directory_path(app, package_id)?;
    let package = directory
        .parent()
        .ok_or_else(|| "Offline asset directory is invalid.".to_string())?;
    let root = package
        .parent()
        .ok_or_else(|| "Offline asset directory is invalid.".to_string())?;
    create_or_validate_owned_directory(root)?;
    create_or_validate_owned_directory(package)?;
    create_or_validate_owned_directory(&directory)?;
    validate_offline_asset_directory(&directory)?;
    Ok(directory)
}

fn offline_asset_directory_for_read(app: &AppHandle, package_id: &str) -> Result<PathBuf, String> {
    let directory = offline_asset_directory_path(app, package_id)?;
    validate_offline_asset_directory(&directory)?;
    Ok(directory)
}

fn validate_offline_asset_directory(directory: &Path) -> Result<(), String> {
    let package = directory
        .parent()
        .ok_or_else(|| "Offline asset directory is invalid.".to_string())?;
    for path in [package, directory] {
        let metadata = fs::symlink_metadata(path)
            .map_err(|_| "Offline asset directory is unavailable.".to_string())?;
        if !metadata.is_dir() || metadata_is_link_or_reparse(&metadata) {
            return Err("Offline asset directory is unsafe.".to_string());
        }
    }
    Ok(())
}

fn create_or_validate_owned_directory(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() && !metadata_is_link_or_reparse(&metadata) => Ok(()),
        Ok(_) => Err("Offline asset directory is unsafe.".to_string()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => fs::create_dir(path)
            .map_err(|_| "Failed to create the offline asset directory.".to_string()),
        Err(_) => Err("Offline asset directory is unavailable.".to_string()),
    }
}

fn persist_offline_asset_file(
    directory: &Path,
    file_name: &str,
    bytes: &[u8],
) -> Result<(), String> {
    if Path::new(file_name)
        .file_name()
        .and_then(|value| value.to_str())
        != Some(file_name)
    {
        return Err("Offline attachment path is invalid.".to_string());
    }
    let final_path = directory.join(file_name);
    if let Ok(metadata) = fs::symlink_metadata(&final_path) {
        if !metadata.is_file() || metadata_is_link_or_reparse(&metadata) {
            return Err("Offline attachment path is unsafe.".to_string());
        }
        return if fs::read(&final_path).ok().as_deref() == Some(bytes) {
            Ok(())
        } else {
            Err("Offline attachment file identity is invalid.".to_string())
        };
    }

    let mut random = [0u8; 8];
    thread_rng().fill_bytes(&mut random);
    let nonce = random
        .iter()
        .map(|value| format!("{value:02x}"))
        .collect::<String>();
    let temporary_name = format!(".{file_name}.{nonce}.tmp");
    let temporary_path = directory.join(temporary_name);
    let write_result = (|| -> Result<(), String> {
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&temporary_path)
            .map_err(|_| "Failed to write an offline attachment.".to_string())?;
        file.write_all(bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| "Failed to write an offline attachment.".to_string())?;
        fs::rename(&temporary_path, &final_path)
            .map_err(|_| "Failed to finalize an offline attachment.".to_string())
    })();
    if write_result.is_err() {
        let _ = fs::remove_file(&temporary_path);
    }
    write_result
}

fn remove_safe_asset_file(directory: &Path, relative_path: &str) -> Result<(), String> {
    if relative_path.is_empty()
        || Path::new(relative_path)
            .file_name()
            .and_then(|value| value.to_str())
            != Some(relative_path)
    {
        return Err("Offline attachment path is invalid.".to_string());
    }
    let path = directory.join(relative_path);
    match fs::symlink_metadata(&path) {
        Ok(metadata) if metadata.is_file() && !metadata_is_link_or_reparse(&metadata) => {
            fs::remove_file(path).map_err(|_| "Failed to delete an offline attachment.".to_string())
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        _ => Err("Offline attachment path is unsafe.".to_string()),
    }
}

fn metadata_is_link_or_reparse(metadata: &fs::Metadata) -> bool {
    if metadata.file_type().is_symlink() {
        return true;
    }
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::fs::MetadataExt;
        const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x400;
        metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0
    }
    #[cfg(not(target_os = "windows"))]
    false
}

async fn finalize_offline_item(app: &AppHandle, task: &DownloadTask) -> Result<(), String> {
    let download_storage = DownloadStorage::open(app)?;
    let snapshot = download_storage.snapshot(&task.id)?;
    let mut persisted_task = task.clone();
    #[cfg(not(target_os = "android"))]
    let entity_hash = {
        let root = validate_destination_directory(&task.destination_directory)?;
        let path = safe_destination_path(&root, &task.destination_name)?;
        let metadata = fs::metadata(path)
            .map_err(|_| "The completed offline file is unavailable.".to_string())?;
        if !metadata.is_file() {
            return Err("The completed offline file is unavailable.".to_string());
        }
        persisted_task.bytes_downloaded = metadata.len();
        persisted_task.total_bytes = Some(metadata.len());
        Some(local_entity_fingerprint(&metadata))
    };
    #[cfg(target_os = "android")]
    let entity_hash = {
        let Some((_uri, size, entity_hash)) = download_android::resolve_completed_document(
            app,
            &task.destination_directory,
            &task.destination_name,
        )
        .await?
        else {
            return Err("The completed offline file is unavailable.".to_string());
        };
        persisted_task.bytes_downloaded = size;
        persisted_task.total_bytes = Some(size);
        Some(entity_hash)
    };
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    finalize_offline_item_with_snapshot(
        &conn,
        &persisted_task,
        snapshot.as_ref(),
        entity_hash.as_deref(),
    )
}

#[cfg(test)]
fn finalize_offline_item_in(conn: &Connection, task: &DownloadTask) -> Result<(), String> {
    finalize_offline_item_with_snapshot(conn, task, None, None)
}

fn finalize_offline_item_with_snapshot(
    conn: &Connection,
    task: &DownloadTask,
    snapshot: Option<&OfflineDetailSnapshot>,
    entity_hash: Option<&str>,
) -> Result<(), String> {
    let package_id = hash_text(&format!("{}\0{}", task.source_id, task.item_id));
    let video_bytes = task.total_bytes.unwrap_or(task.bytes_downloaded);
    let fallback = OfflineDetailSnapshot {
        name: task.display_name.clone(),
        original_title: None,
        media_type: task.media_type.clone(),
        year: None,
        rating: None,
        overview: None,
        tagline: None,
        duration: None,
        genres: Vec::new(),
        directors: Vec::new(),
        writers: Vec::new(),
        cast: Vec::new(),
        imdb_id: None,
        tmdb_id: None,
        series_name: None,
        season_number: None,
        episode_number: None,
    };
    let snapshot = snapshot.unwrap_or(&fallback);
    validate_offline_snapshot(snapshot)?;
    let snapshot_json = serde_json::to_string(snapshot)
        .map_err(|_| "Failed to serialize the offline media detail.".to_string())?;
    conn.execute_batch("BEGIN IMMEDIATE")
        .map_err(|_| "Failed to begin offline media finalization.".to_string())?;
    let result = (|| {
        conn.execute(
            "INSERT INTO offline_packages(id,source_id,work_id,media_type,detail_snapshot_json,attachment_state,created_at,updated_at)
             VALUES (?1,?2,?3,?4,?5,'pending',unixepoch(),unixepoch())
             ON CONFLICT(source_id,work_id) DO UPDATE SET media_type=excluded.media_type,
                detail_snapshot_json=excluded.detail_snapshot_json,updated_at=excluded.updated_at",
            params![package_id, task.source_id, task.item_id, task.media_type, snapshot_json],
        )
        .map_err(|_| "Failed to save the offline media package.".to_string())?;
        conn.execute(
            "DELETE FROM offline_items WHERE source_id=?1 AND item_id=?2
             AND COALESCE(media_source_id,'')=COALESCE(?3,'')
             AND COALESCE(variant_id,'')=COALESCE(?4,'')",
            params![
                task.source_id,
                task.item_id,
                task.media_source_id,
                task.variant_id
            ],
        )
        .map_err(|_| "Failed to replace the previous offline media item.".to_string())?;
        conn.execute(
            "INSERT INTO offline_items(id,package_id,source_id,item_id,media_source_id,variant_id,
             root_reference,relative_video_path,video_bytes,entity_hash,completed_at,display_name,media_type)
             VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,unixepoch(),?11,?12)",
            params![task.id, package_id, task.source_id, task.item_id, task.media_source_id,
                task.variant_id, task.destination_directory, task.destination_name, video_bytes,
                entity_hash, task.display_name, task.media_type],
        )
        .map_err(|_| "Failed to save the offline media item.".to_string())?;
        Ok(())
    })();
    finish_transaction(conn, result)
}

fn list_offline_items(app: &AppHandle) -> Result<Vec<OfflineItemSummary>, String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    let mut statement = conn
        .prepare(
            "SELECT i.id,i.source_id,i.item_id,i.media_source_id,i.variant_id,
         i.display_name,i.media_type,i.video_bytes,i.completed_at,p.attachment_state,p.detail_snapshot_json
         FROM offline_items i JOIN offline_packages p ON p.id=i.package_id
         ORDER BY i.completed_at DESC,i.id DESC",
        )
        .map_err(|_| "Failed to read offline media items.".to_string())?;
    let items = statement
        .query_map([], |row| {
            let snapshot = row
                .get::<_, Option<String>>(10)?
                .and_then(|value| serde_json::from_str::<OfflineDetailSnapshot>(&value).ok());
            Ok(OfflineItemSummary {
                id: row.get(0)?,
                source_id: row.get(1)?,
                item_id: row.get(2)?,
                media_source_id: row.get(3)?,
                variant_id: row.get(4)?,
                display_name: row.get(5)?,
                media_type: row.get(6)?,
                video_bytes: row.get(7)?,
                completed_at: row.get(8)?,
                attachment_state: row.get(9)?,
                series_name: snapshot
                    .as_ref()
                    .and_then(|value| value.series_name.clone()),
                season_number: snapshot.as_ref().and_then(|value| value.season_number),
                episode_number: snapshot.as_ref().and_then(|value| value.episode_number),
            })
        })
        .map_err(|_| "Failed to read offline media items.".to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| "Failed to read offline media items.".to_string())?;
    Ok(items)
}

fn find_offline_detail(
    app: &AppHandle,
    source_id: &str,
    item_id: &str,
    offline_id: Option<&str>,
) -> Result<Option<OfflineDetailRecord>, String> {
    let conn = Connection::open(storage::data_file(app, OFFLINE_DATABASE_FILE)?)
        .map_err(|_| "Failed to open the offline media database.".to_string())?;
    initialize_offline_schema(&conn)?;
    let record = conn
        .query_row(
            "SELECT i.id,i.source_id,i.item_id,i.media_source_id,i.variant_id,i.display_name,
                i.media_type,i.video_bytes,i.completed_at,p.attachment_state,
                p.detail_snapshot_json
             FROM offline_items i JOIN offline_packages p ON p.id=i.package_id
             WHERE ((?3 IS NOT NULL AND i.id=?3)
                OR (?3 IS NULL AND i.source_id=?1 AND i.item_id=?2))
             ORDER BY i.completed_at DESC LIMIT 1",
            params![source_id, item_id, offline_id],
            |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, Option<String>>(3)?,
                    row.get::<_, Option<String>>(4)?,
                    row.get::<_, String>(5)?,
                    row.get::<_, String>(6)?,
                    row.get::<_, u64>(7)?,
                    row.get::<_, i64>(8)?,
                    row.get::<_, String>(9)?,
                    row.get::<_, Option<String>>(10)?,
                ))
            },
        )
        .optional()
        .map_err(|_| "Failed to read the offline media detail.".to_string())?;
    let Some((
        id,
        source_id,
        item_id,
        media_source_id,
        variant_id,
        display_name,
        media_type,
        video_bytes,
        completed_at,
        attachment_state,
        snapshot_json,
    )) = record
    else {
        return Ok(None);
    };
    let snapshot = snapshot_json
        .as_deref()
        .and_then(|value| serde_json::from_str::<OfflineDetailSnapshot>(value).ok())
        .unwrap_or(OfflineDetailSnapshot {
            name: display_name.clone(),
            original_title: None,
            media_type: media_type.clone(),
            year: None,
            rating: None,
            overview: None,
            tagline: None,
            duration: None,
            genres: Vec::new(),
            directors: Vec::new(),
            writers: Vec::new(),
            cast: Vec::new(),
            imdb_id: None,
            tmdb_id: None,
            series_name: None,
            season_number: None,
            episode_number: None,
        });
    validate_offline_snapshot(&snapshot)?;
    let mut asset_statement = conn
        .prepare(
            "SELECT a.id,a.asset_kind FROM offline_assets a
             JOIN offline_items i ON i.package_id=a.package_id
             WHERE i.id=?1 AND a.status='complete' AND (a.item_id IS NULL OR a.item_id=i.item_id)
             ORDER BY a.asset_kind,a.id",
        )
        .map_err(|_| "Failed to read offline attachments.".to_string())?;
    let assets = asset_statement
        .query_map([&id], |row| {
            Ok(OfflineAssetSummary {
                id: row.get(0)?,
                kind: row.get(1)?,
            })
        })
        .map_err(|_| "Failed to read offline attachments.".to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| "Failed to read offline attachments.".to_string())?;
    Ok(Some(OfflineDetailRecord {
        id,
        source_id,
        item_id,
        media_source_id,
        variant_id,
        display_name,
        media_type,
        video_bytes,
        completed_at,
        attachment_state,
        snapshot,
        assets,
    }))
}

fn delete_offline_item(app: &AppHandle, task: &DownloadTask) -> Result<(), String> {
    remove_offline_location(app, &task.id)
}

fn remove_owned_offline_asset_files(
    app: &AppHandle,
    package_id: &str,
    asset_paths: &[String],
    package_remains: bool,
) -> Result<(), String> {
    if package_id.len() != 64 || !package_id.bytes().all(|value| value.is_ascii_hexdigit()) {
        return Err("Offline media package identity is invalid.".to_string());
    }
    let package = storage::initialize(app)?
        .data_dir
        .join("offline")
        .join(package_id);
    if !package.exists() {
        return Ok(());
    }
    let metadata = fs::symlink_metadata(&package)
        .map_err(|_| "Failed to inspect owned offline assets.".to_string())?;
    if metadata_is_link_or_reparse(&metadata) || !metadata.is_dir() {
        return Err("Offline media package path is unsafe.".to_string());
    }
    if !package_remains {
        return fs::remove_dir_all(package)
            .map_err(|_| "Failed to delete owned offline assets.".to_string());
    }

    let asset_directory = package.join("assets");
    match fs::symlink_metadata(&asset_directory) {
        Ok(value) if value.is_dir() && !metadata_is_link_or_reparse(&value) => {}
        Ok(_) => return Err("Offline asset directory is unsafe.".to_string()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => return Err("Failed to inspect owned offline assets.".to_string()),
    }
    for relative_path in asset_paths {
        if relative_path.is_empty()
            || Path::new(relative_path)
                .file_name()
                .and_then(|value| value.to_str())
                != Some(relative_path.as_str())
        {
            continue;
        }
        let path = asset_directory.join(relative_path);
        match fs::symlink_metadata(&path) {
            Ok(value) if value.is_file() && !metadata_is_link_or_reparse(&value) => {
                let _ = fs::remove_file(path);
            }
            _ => {}
        }
    }
    Ok(())
}

const TASK_COLUMNS: &str = "id,source_id,source_type,item_id,display_name,media_type,
destination_directory,destination_name,status,bytes_downloaded,total_bytes,retry_count,
error_message,created_at,updated_at,parent_id,group_name,media_source_id,variant_id,library_id,
online_library_id,online_work_id,online_segment_id,online_version_id,speed_bytes_per_second,
eta_seconds,active_segments,attachment_state";

struct CleanupRecord {
    id: String,
    task_id: String,
    path_kind: String,
    root_reference: String,
    relative_path: String,
    attempts: u32,
    last_error_code: String,
    created_at: i64,
}

fn initialize_download_schema(conn: &Connection) -> Result<(), String> {
    if schema_version(conn, "download_schema")? >= 3 {
        return Ok(());
    }
    conn.execute_batch("BEGIN IMMEDIATE")
        .map_err(|_| "Failed to begin the download database migration.".to_string())?;
    let result = (|| {
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS download_tasks (
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
            updated_at INTEGER NOT NULL,
            parent_id TEXT,
            group_name TEXT,
            media_source_id TEXT,
            variant_id TEXT,
            library_id TEXT,
            online_library_id TEXT,
            online_work_id TEXT,
            online_segment_id TEXT,
            online_version_id TEXT,
            speed_bytes_per_second INTEGER NOT NULL DEFAULT 0,
            eta_seconds INTEGER,
            active_segments INTEGER NOT NULL DEFAULT 0,
            attachment_state TEXT NOT NULL DEFAULT 'none'
        );",
        )
        .map_err(|_| "Failed to initialize the download task database.".to_string())?;
        for (name, definition) in [
            ("parent_id", "TEXT"),
            ("group_name", "TEXT"),
            ("media_source_id", "TEXT"),
            ("variant_id", "TEXT"),
            ("library_id", "TEXT"),
            ("online_library_id", "TEXT"),
            ("online_work_id", "TEXT"),
            ("online_segment_id", "TEXT"),
            ("online_version_id", "TEXT"),
            ("speed_bytes_per_second", "INTEGER NOT NULL DEFAULT 0"),
            ("eta_seconds", "INTEGER"),
            ("active_segments", "INTEGER NOT NULL DEFAULT 0"),
            ("attachment_state", "TEXT NOT NULL DEFAULT 'none'"),
        ] {
            ensure_optional_column(conn, "download_tasks", name, definition)?;
        }
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS download_segments (
                task_id TEXT NOT NULL,
                segment_index INTEGER NOT NULL,
                range_start INTEGER NOT NULL,
                range_end INTEGER NOT NULL,
                completed_bytes INTEGER NOT NULL DEFAULT 0,
                status TEXT NOT NULL DEFAULT 'queued',
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
                PRIMARY KEY(task_id,segment_index),
                FOREIGN KEY(task_id) REFERENCES download_tasks(id) ON DELETE CASCADE
             );
             CREATE TABLE IF NOT EXISTS download_cleanup (
                id TEXT PRIMARY KEY NOT NULL,
                task_id TEXT NOT NULL,
                path_kind TEXT NOT NULL,
                root_reference TEXT NOT NULL,
                relative_path TEXT NOT NULL,
                attempts INTEGER NOT NULL DEFAULT 0,
                last_error_code TEXT NOT NULL,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL
             );
             CREATE TABLE IF NOT EXISTS download_task_snapshots (
                task_id TEXT PRIMARY KEY NOT NULL,
                snapshot_json TEXT NOT NULL,
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
                FOREIGN KEY(task_id) REFERENCES download_tasks(id) ON DELETE CASCADE
             );
             CREATE TABLE IF NOT EXISTS download_schema (
                singleton INTEGER PRIMARY KEY CHECK(singleton=1),
                version INTEGER NOT NULL
             );
             INSERT INTO download_schema(singleton,version) VALUES(1,3)
             ON CONFLICT(singleton) DO UPDATE SET version=MAX(version,excluded.version);
             CREATE INDEX IF NOT EXISTS idx_download_tasks_updated
                ON download_tasks(updated_at DESC);
             CREATE INDEX IF NOT EXISTS idx_download_tasks_queue
                ON download_tasks(status,created_at,id);
             CREATE INDEX IF NOT EXISTS idx_download_tasks_parent
                ON download_tasks(parent_id,created_at);
             CREATE INDEX IF NOT EXISTS idx_download_cleanup_created
                ON download_cleanup(created_at);",
        )
        .map_err(|_| "Failed to initialize download scheduling tables.".to_string())?;
        conn.execute(
            "UPDATE download_tasks SET status='interrupted',error_message=NULL,
             active_segments=0,speed_bytes_per_second=0,eta_seconds=NULL
             WHERE status='running'",
            [],
        )
        .map_err(|_| "Failed to migrate interrupted download tasks.".to_string())?;
        Ok(())
    })();
    finish_transaction(conn, result)
}

fn map_task(row: &rusqlite::Row<'_>) -> rusqlite::Result<DownloadTask> {
    let online_library_id: Option<String> = row.get(20)?;
    let online_work_id: Option<String> = row.get(21)?;
    let online_segment_id: Option<String> = row.get(22)?;
    let online_version_id: Option<String> = row.get(23)?;
    let online_identity = match (
        online_library_id,
        online_work_id,
        online_segment_id,
        online_version_id,
    ) {
        (Some(library_id), Some(work_id), Some(segment_id), Some(version_id)) => {
            Some(DownloadOnlineIdentity {
                library_id,
                work_id,
                segment_id,
                version_id,
            })
        }
        _ => None,
    };
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
        parent_id: row.get(15)?,
        group_name: row.get(16)?,
        media_source_id: row.get(17)?,
        variant_id: row.get(18)?,
        library_id: row.get(19)?,
        online_identity,
        speed_bytes_per_second: row.get(24)?,
        eta_seconds: row.get(25)?,
        active_segments: row.get(26)?,
        attachment_state: row.get(27)?,
    })
}

fn ensure_optional_column(
    conn: &Connection,
    table: &str,
    name: &str,
    definition: &str,
) -> Result<(), String> {
    let mut statement = conn
        .prepare(&format!("PRAGMA table_info({table})"))
        .map_err(|_| "Failed to inspect the download task database.".to_string())?;
    let exists = statement
        .query_map([], |row| row.get::<_, String>(1))
        .map_err(|_| "Failed to inspect the download task database.".to_string())?
        .filter_map(Result::ok)
        .any(|column| column == name);
    drop(statement);
    if !exists {
        conn.execute_batch(&format!(
            "ALTER TABLE {table} ADD COLUMN {name} {definition}"
        ))
        .map_err(|_| "Failed to migrate the download task database.".to_string())?;
    }
    Ok(())
}

fn initialize_offline_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch(
        "PRAGMA foreign_keys=ON;
         PRAGMA busy_timeout=5000;
         CREATE TABLE IF NOT EXISTS offline_packages (
            id TEXT PRIMARY KEY NOT NULL,
            source_id TEXT NOT NULL,
            work_id TEXT NOT NULL,
            media_type TEXT NOT NULL,
            snapshot_version INTEGER NOT NULL DEFAULT 1,
            detail_snapshot_json TEXT,
            attachment_state TEXT NOT NULL DEFAULT 'pending',
            created_at INTEGER NOT NULL DEFAULT (unixepoch()),
            updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
            UNIQUE(source_id,work_id)
         );
         CREATE TABLE IF NOT EXISTS offline_items (
            id TEXT PRIMARY KEY NOT NULL,
            package_id TEXT NOT NULL,
            source_id TEXT NOT NULL,
            item_id TEXT NOT NULL,
            media_source_id TEXT,
            variant_id TEXT,
            root_reference TEXT NOT NULL,
            relative_video_path TEXT NOT NULL,
            video_bytes INTEGER NOT NULL,
            entity_hash TEXT,
            completed_at INTEGER NOT NULL,
            display_name TEXT NOT NULL DEFAULT '',
            media_type TEXT NOT NULL DEFAULT 'file',
            FOREIGN KEY(package_id) REFERENCES offline_packages(id) ON DELETE CASCADE,
            UNIQUE(source_id,item_id,media_source_id,variant_id)
         );
         CREATE TABLE IF NOT EXISTS offline_assets (
            id TEXT PRIMARY KEY NOT NULL,
            package_id TEXT NOT NULL,
            item_id TEXT,
            asset_kind TEXT NOT NULL,
            relative_asset_path TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'pending',
            safe_error_code TEXT,
            created_at INTEGER NOT NULL DEFAULT (unixepoch()),
            updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
            FOREIGN KEY(package_id) REFERENCES offline_packages(id) ON DELETE CASCADE
         );
         CREATE TABLE IF NOT EXISTS offline_schema (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            version INTEGER NOT NULL
         );
         INSERT INTO offline_schema(singleton,version) VALUES(1,1)
         ON CONFLICT(singleton) DO UPDATE SET version=MAX(version,excluded.version);
         CREATE INDEX IF NOT EXISTS idx_offline_items_identity
            ON offline_items(source_id,item_id,media_source_id,variant_id);
         CREATE INDEX IF NOT EXISTS idx_offline_assets_package
            ON offline_assets(package_id,item_id,asset_kind);",
    )
    .map_err(|_| "Failed to initialize the offline media database.".to_string())?;
    ensure_optional_column(
        conn,
        "offline_items",
        "display_name",
        "TEXT NOT NULL DEFAULT ''",
    )?;
    ensure_optional_column(
        conn,
        "offline_items",
        "media_type",
        "TEXT NOT NULL DEFAULT 'file'",
    )?;
    Ok(())
}

fn schema_version(conn: &Connection, table: &str) -> Result<i64, String> {
    let exists = conn
        .query_row(
            "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1)",
            [table],
            |row| row.get::<_, bool>(0),
        )
        .map_err(|_| "Failed to inspect the media database schema.".to_string())?;
    if !exists {
        return Ok(0);
    }
    conn.query_row(
        &format!("SELECT version FROM {table} WHERE singleton=1"),
        [],
        |row| row.get(0),
    )
    .optional()
    .map(|value| value.unwrap_or(0))
    .map_err(|_| "Failed to inspect the media database schema.".to_string())
}

fn finish_transaction(conn: &Connection, result: Result<(), String>) -> Result<(), String> {
    match result {
        Ok(()) => conn
            .execute_batch("COMMIT")
            .map_err(|_| "Failed to commit the download database transaction.".to_string()),
        Err(error) => {
            let _ = conn.execute_batch("ROLLBACK");
            Err(error)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_task(id: &str, created_at: i64) -> DownloadTask {
        DownloadTask {
            id: id.to_string(),
            source_id: "source-1".to_string(),
            source_type: "local".to_string(),
            item_id: "/Movies/Test.mkv".to_string(),
            display_name: "Test.mkv".to_string(),
            media_type: "movie".to_string(),
            destination_directory: "C:/Downloads".to_string(),
            destination_name: "Test.mkv".to_string(),
            status: "queued".to_string(),
            bytes_downloaded: 0,
            total_bytes: Some(1024),
            retry_count: 0,
            error_message: None,
            parent_id: None,
            group_name: None,
            media_source_id: Some("media-source-1".to_string()),
            variant_id: Some("1080p".to_string()),
            library_id: Some("library-1".to_string()),
            online_identity: None,
            speed_bytes_per_second: 0,
            eta_seconds: None,
            active_segments: 0,
            attachment_state: "none".to_string(),
            created_at,
            updated_at: created_at,
        }
    }

    fn test_enqueue_request(source_type: &str, item_id: &str) -> DownloadEnqueueRequest {
        DownloadEnqueueRequest {
            source_id: "source-1".to_string(),
            source_type: source_type.to_string(),
            item_id: item_id.to_string(),
            display_name: "Test.mkv".to_string(),
            media_type: "movie".to_string(),
            expected_bytes: Some(1024),
            destination_directory: None,
            parent_id: None,
            group_name: None,
            media_source_id: None,
            variant_id: None,
            library_id: None,
            online_identity: None,
            detail_snapshot: None,
        }
    }

    fn table_columns(conn: &Connection, table: &str) -> Vec<String> {
        conn.prepare(&format!("PRAGMA table_info({table})"))
            .unwrap()
            .query_map([], |row| row.get(1))
            .unwrap()
            .collect::<Result<_, _>>()
            .unwrap()
    }

    #[test]
    fn persisted_schema_excludes_sensitive_transport_fields() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&conn).unwrap();
        let offline = Connection::open_in_memory().unwrap();
        initialize_offline_schema(&offline).unwrap();
        for (database, tables) in [
            (
                &conn,
                vec![
                    "download_tasks",
                    "download_segments",
                    "download_cleanup",
                    "download_task_snapshots",
                ],
            ),
            (
                &offline,
                vec!["offline_packages", "offline_items", "offline_assets"],
            ),
        ] {
            for table in tables {
                let names = table_columns(database, table);
                for forbidden in [
                    "url",
                    "header",
                    "cookie",
                    "authorization",
                    "token",
                    "signature",
                ] {
                    assert!(
                        !names
                            .iter()
                            .any(|name| name.to_ascii_lowercase().contains(forbidden)),
                        "{table} persisted forbidden field fragment {forbidden}"
                    );
                }
            }
        }
    }

    #[test]
    fn offline_schema_enforces_owned_asset_cascade() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_offline_schema(&conn).unwrap();
        conn.execute(
            "INSERT INTO offline_packages(id,source_id,work_id,media_type)
             VALUES (?1,'source-1','work-1','movie')",
            ["a".repeat(64)],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO offline_assets(id,package_id,item_id,asset_kind,relative_asset_path)
             VALUES (?1,?2,'item-1','poster','poster.jpg')",
            params!["b".repeat(64), "a".repeat(64)],
        )
        .unwrap();
        conn.execute("DELETE FROM offline_packages WHERE id=?1", ["a".repeat(64)])
            .unwrap();
        let asset_count = conn
            .query_row("SELECT COUNT(*) FROM offline_assets", [], |row| {
                row.get::<_, u64>(0)
            })
            .unwrap();
        assert_eq!(asset_count, 0);
    }

    #[test]
    fn migration_is_idempotent_and_preserves_finished_and_paused_tasks() {
        let conn = Connection::open_in_memory().unwrap();
        conn.execute_batch(
            "CREATE TABLE download_tasks (
                id TEXT PRIMARY KEY NOT NULL,source_id TEXT NOT NULL,source_type TEXT NOT NULL,
                item_id TEXT NOT NULL,display_name TEXT NOT NULL,media_type TEXT NOT NULL,
                destination_directory TEXT NOT NULL,destination_name TEXT NOT NULL,status TEXT NOT NULL,
                bytes_downloaded INTEGER NOT NULL DEFAULT 0,total_bytes INTEGER,retry_count INTEGER NOT NULL DEFAULT 0,
                entity_hash TEXT,error_message TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
                parent_id TEXT,group_name TEXT,media_source_id TEXT
             );
             INSERT INTO download_tasks VALUES
                ('completed','s','local','a','A','movie','C:/Downloads','A.mkv','completed',10,10,0,NULL,NULL,1,1,NULL,NULL,NULL),
                ('paused','s','local','b','B','movie','C:/Downloads','B.mkv','paused',5,10,0,NULL,NULL,2,2,NULL,NULL,NULL),
                ('running','s','local','c','C','movie','C:/Downloads','C.mkv','running',5,10,0,NULL,NULL,3,3,NULL,NULL,NULL),
                ('pause-requested','s','local','d','D','movie','C:/Downloads','D.mkv','pause_requested',5,10,0,NULL,NULL,4,4,NULL,NULL,NULL);",
        )
        .unwrap();

        initialize_download_schema(&conn).unwrap();
        initialize_download_schema(&conn).unwrap();
        let storage = DownloadStorage { conn };
        storage.recover_interrupted().unwrap();

        let states: HashMap<String, String> = storage
            .conn
            .prepare("SELECT id,status FROM download_tasks")
            .unwrap()
            .query_map([], |row| Ok((row.get(0)?, row.get(1)?)))
            .unwrap()
            .collect::<Result<_, _>>()
            .unwrap();
        assert_eq!(
            states.get("completed").map(String::as_str),
            Some("completed")
        );
        assert_eq!(states.get("paused").map(String::as_str), Some("paused"));
        assert_eq!(
            states.get("pause-requested").map(String::as_str),
            Some("paused")
        );
        assert_eq!(
            states.get("running").map(String::as_str),
            Some("interrupted")
        );
        assert_eq!(table_columns(&storage.conn, "download_tasks").len(), 29);
    }

    #[test]
    fn scheduler_claims_oldest_runnable_without_claiming_user_paused() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&conn).unwrap();
        let storage = DownloadStorage { conn };
        let mut paused = test_task("00000000000000000000000000000001", 1);
        paused.status = "paused".to_string();
        storage.insert(&paused).unwrap();
        storage
            .insert(&test_task("00000000000000000000000000000002", 2))
            .unwrap();
        storage
            .insert(&test_task("00000000000000000000000000000003", 3))
            .unwrap();

        let claimed = storage.claim_next_runnable().unwrap().unwrap();
        assert_eq!(claimed.id, "00000000000000000000000000000002");
        assert_eq!(claimed.status, "resolving");
        assert_eq!(storage.get(&paused.id).unwrap().unwrap().status, "paused");
    }

    #[test]
    fn completion_cannot_overwrite_a_late_cancel_request() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&conn).unwrap();
        let storage = DownloadStorage { conn };
        let task = test_task("00000000000000000000000000000004", 4);
        storage.insert(&task).unwrap();
        storage
            .set_status(&task.id, "cancel_requested", None)
            .unwrap();

        assert!(!storage.try_set_completed(&task.id).unwrap());
        assert_eq!(
            storage.get(&task.id).unwrap().unwrap().status,
            "cancel_requested"
        );
    }

    #[test]
    fn offline_record_uses_final_progress_and_survives_history_deletion() {
        let download_conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&download_conn).unwrap();
        let storage = DownloadStorage {
            conn: download_conn,
        };
        let mut task = test_task("00000000000000000000000000000005", 5);
        task.total_bytes = None;
        storage.insert(&task).unwrap();
        storage
            .set_progress_metrics(&task.id, 4096, None, 0, None)
            .unwrap();
        assert!(storage.try_set_completed(&task.id).unwrap());

        let completed = storage.get(&task.id).unwrap().unwrap();
        assert_eq!(completed.bytes_downloaded, 4096);
        assert_eq!(completed.total_bytes, Some(4096));

        let offline = Connection::open_in_memory().unwrap();
        initialize_offline_schema(&offline).unwrap();
        finalize_offline_item_in(&offline, &completed).unwrap();
        storage
            .delete_task_and_segments(&completed.id, None)
            .unwrap();

        assert!(storage.get(&completed.id).unwrap().is_none());
        let persisted: (String, u64) = offline
            .query_row(
                "SELECT relative_video_path,video_bytes FROM offline_items WHERE id=?1",
                [&completed.id],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .unwrap();
        assert_eq!(persisted, ("Test.mkv".to_string(), 4096));
    }

    #[test]
    fn cancellation_cleanup_removes_only_the_owned_partial() {
        let root = std::env::temp_dir().join(format!(
            "ohmycine-download-cleanup-{}-{}",
            std::process::id(),
            random_id()
        ));
        fs::create_dir_all(&root).unwrap();
        let owned = root.join("Movie.mkv.partial");
        let unrelated = root.join("Other.mkv.partial");
        fs::write(&owned, b"partial").unwrap();
        fs::write(&unrelated, b"keep").unwrap();

        try_remove_owned_partial(&display_path(&root), "Movie.mkv.partial").unwrap();

        assert!(!owned.exists());
        assert!(unrelated.exists());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn late_cancellation_cleanup_removes_only_the_owned_final_file() {
        let root = std::env::temp_dir().join(format!(
            "ohmycine-download-final-cleanup-{}-{}",
            std::process::id(),
            random_id()
        ));
        fs::create_dir_all(&root).unwrap();
        let owned = root.join("Movie.mkv");
        let unrelated = root.join("Other.mkv");
        fs::write(&owned, b"complete").unwrap();
        fs::write(&unrelated, b"keep").unwrap();

        try_remove_owned_final(&display_path(&root), "Movie.mkv").unwrap();

        assert!(!owned.exists());
        assert!(unrelated.exists());
        assert!(try_remove_owned_final(&display_path(&root), "../Other.mkv").is_err());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn deferred_cleanup_is_internal_and_task_rows_are_deleted_transactionally() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&conn).unwrap();
        let storage = DownloadStorage { conn };
        let task = test_task("00000000000000000000000000000004", 4);
        storage.insert(&task).unwrap();
        storage
            .conn
            .execute(
                "INSERT INTO download_segments(task_id,segment_index,range_start,range_end)
                 VALUES(?1,0,0,1023)",
                [&task.id],
            )
            .unwrap();
        let cleanup = CleanupRecord {
            id: "cleanup-1".to_string(),
            task_id: task.id.clone(),
            path_kind: "desktop_partial".to_string(),
            root_reference: "C:/missing".to_string(),
            relative_path: "Test.mkv.partial".to_string(),
            attempts: 0,
            last_error_code: "cleanup_root_missing".to_string(),
            created_at: 4,
        };

        storage
            .delete_task_and_segments(&task.id, Some(&cleanup))
            .unwrap();

        assert!(storage.get(&task.id).unwrap().is_none());
        let segments: i64 = storage
            .conn
            .query_row("SELECT COUNT(*) FROM download_segments", [], |row| {
                row.get(0)
            })
            .unwrap();
        let cleanups: i64 = storage
            .conn
            .query_row("SELECT COUNT(*) FROM download_cleanup", [], |row| {
                row.get(0)
            })
            .unwrap();
        assert_eq!(segments, 0);
        assert_eq!(cleanups, 1);
    }

    #[test]
    fn settings_enforce_separate_task_segment_and_rate_limits() {
        assert!(validate_download_settings(&DownloadSettings {
            concurrent_tasks: 2,
            segments_per_task: 4,
            global_speed_limit_bytes_per_second: Some(1024 * 1024),
        })
        .is_ok());
        assert!(validate_download_settings(&DownloadSettings {
            concurrent_tasks: 0,
            ..DownloadSettings::default()
        })
        .is_err());
        assert!(validate_download_settings(&DownloadSettings {
            segments_per_task: MAX_SEGMENTS_PER_TASK + 1,
            ..DownloadSettings::default()
        })
        .is_err());
    }

    #[test]
    fn segment_plan_is_contiguous_and_requires_complete_checkpoints() {
        let mut segments = plan_segments(10_003, 4).unwrap();
        assert_eq!(segments.len(), 4);
        assert_eq!(segments.first().map(|value| value.range_start), Some(0));
        assert_eq!(segments.last().map(|value| value.range_end), Some(10_002));
        assert!(validate_segment_coverage(&segments, 10_003, false).is_ok());
        assert!(validate_segment_coverage(&segments, 10_003, true).is_err());
        for segment in &mut segments {
            segment.completed_bytes = segment.length();
            segment.status = "completed".to_string();
        }
        assert!(validate_segment_coverage(&segments, 10_003, true).is_ok());
        segments[1].range_start += 1;
        assert!(validate_segment_coverage(&segments, 10_003, true).is_err());
    }

    #[test]
    fn shared_rate_limiter_serializes_aggregate_reservations_and_updates_live() {
        let limiter = GlobalRateLimiter::default();
        let now = Instant::now();
        assert_eq!(
            limiter.reserve_at(Some(64 * 1024), 64 * 1024, now),
            Duration::ZERO
        );
        let second = limiter.reserve_at(Some(64 * 1024), 64 * 1024, now);
        assert!(second >= Duration::from_millis(999));
        // A live setting update starts a fresh schedule instead of retaining reservations
        // made under the old rate.
        assert_eq!(
            limiter.reserve_at(Some(128 * 1024), 64 * 1024, now),
            Duration::ZERO
        );
        assert_eq!(limiter.reserve_at(None, 64 * 1024, now), Duration::ZERO);
    }

    #[test]
    fn content_range_parser_rejects_gaps_and_unknown_totals() {
        let mut headers = HeaderMap::new();
        headers.insert(CONTENT_RANGE, HeaderValue::from_static("bytes 20-39/100"));
        assert_eq!(
            parse_content_range(&headers),
            Some(ParsedContentRange {
                start: 20,
                end: 39,
                total: 100,
            })
        );
        headers.insert(CONTENT_RANGE, HeaderValue::from_static("bytes 40-20/100"));
        assert!(parse_content_range(&headers).is_none());
        headers.insert(CONTENT_RANGE, HeaderValue::from_static("bytes 0-0/*"));
        assert!(parse_content_range(&headers).is_none());
    }
    #[test]
    fn server_physical_entries_are_allowed_but_online_items_fail_closed() {
        let physical = test_enqueue_request("server", "entry|9|work-1|77");
        assert!(validate_enqueue_request(&physical).is_ok());

        let mut missing_online_identity =
            test_enqueue_request("server", "online-version|library|work|segment|version");
        assert_eq!(
            validate_enqueue_request(&missing_online_identity).unwrap_err(),
            "The Server online media identity is incomplete."
        );

        missing_online_identity.online_identity = Some(DownloadOnlineIdentity {
            library_id: "library".to_string(),
            work_id: "work".to_string(),
            segment_id: "segment".to_string(),
            version_id: "version".to_string(),
        });
        assert_eq!(
            validate_enqueue_request(&missing_online_identity).unwrap_err(),
            "This Server online source does not yet expose a safe offline download stream."
        );
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

    #[test]
    fn resume_identity_requires_a_strong_etag_or_modified_time_with_size() {
        assert!(entity_identity_hash(Some("\"strong\""), None, None).is_some());
        assert!(entity_identity_hash(Some("W/\"weak\""), None, Some(1024)).is_none());
        assert!(entity_identity_hash(None, Some("Wed, 26 Aug 2026 12:00:00 GMT"), None).is_none());
        assert!(entity_identity_hash(
            Some("W/\"weak\""),
            Some("Wed, 26 Aug 2026 12:00:00 GMT"),
            Some(1024)
        )
        .is_some());
    }

    #[test]
    fn visible_download_errors_redact_signed_parameters() {
        assert_eq!(
            safe_error("upstream failed with sig=secret"),
            "The download failed while resolving or transferring media."
        );
        assert_eq!(
            safe_error("upstream failed with access_token secret"),
            "The download failed while resolving or transferring media."
        );
    }

    #[test]
    fn offline_attachment_payloads_are_bounded_and_type_checked() {
        let json = BASE64_STANDARD
            .encode(br##"[{"id":"1","time":1,"mode":"scroll","color":"#fff","text":"ok"}]"##);
        let bytes =
            decode_attachment_data_url("danmaku", &format!("data:application/json;base64,{json}"))
                .unwrap();
        assert!(validate_attachment_bytes("danmaku", Some("json"), &bytes).is_ok());
        assert!(validate_attachment_bytes("subtitle", Some("exe"), b"subtitle").is_err());
        assert!(
            decode_attachment_data_url("danmaku", "https://example.test/token=secret").is_err()
        );
        assert!(decode_attachment_data_url(
            "poster",
            &format!("data:application/json;base64,{json}")
        )
        .is_err());
        assert!(validate_attachment_bytes(
            "danmaku",
            Some("json"),
            br##"[{"id":"1","time":1,"mode":"sideways","text":"bad"}]"##
        )
        .is_err());
        assert!(validate_attachment_kind("cookie").is_err());
    }

    #[test]
    fn offline_attachment_headers_reject_request_shaping_and_hop_by_hop_fields() {
        assert!(!is_allowed_attachment_header(&HeaderName::from_static(
            "host"
        )));
        assert!(!is_allowed_attachment_header(&HeaderName::from_static(
            "transfer-encoding"
        )));
        assert!(!is_allowed_attachment_header(&HeaderName::from_static(
            "range"
        )));
        assert!(is_allowed_attachment_header(&HeaderName::from_static(
            "authorization"
        )));
        assert!(is_allowed_attachment_header(&HeaderName::from_static(
            "referer"
        )));
    }

    #[test]
    fn attachment_retry_failure_preserves_an_existing_complete_asset() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_offline_schema(&conn).unwrap();
        let package_id = "a".repeat(64);
        let asset_id = "b".repeat(64);
        conn.execute(
            "INSERT INTO offline_packages(id,source_id,work_id,media_type,detail_snapshot_json)
             VALUES (?1,'source','work','movie','{}')",
            [&package_id],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO offline_assets(id,package_id,item_id,asset_kind,relative_asset_path,status)
             VALUES (?1,?2,'item','poster','poster.jpg','complete')",
            params![asset_id, package_id],
        )
        .unwrap();

        record_attachment_failure(&conn, &asset_id, &package_id, "item", "poster").unwrap();

        let persisted = conn
            .query_row(
                "SELECT relative_asset_path,status,safe_error_code FROM offline_assets WHERE id=?1",
                [&asset_id],
                |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, Option<String>>(2)?,
                    ))
                },
            )
            .unwrap();
        assert_eq!(
            persisted,
            ("poster.jpg".to_string(), "complete".to_string(), None)
        );
    }
}

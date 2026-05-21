use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

const DATABASE_FILE: &str = "playback_history.sqlite";
const MIN_RESUME_POSITION_SECONDS: f64 = 30.0;
const COMPLETED_REMAINING_SECONDS: f64 = 90.0;
const COMPLETED_PROGRESS_RATIO: f64 = 0.92;
const DEFAULT_CONTINUE_LIMIT: u32 = 20;
const MAX_CONTINUE_LIMIT: u32 = 100;
const MAX_ID_LENGTH: usize = 512;
const MAX_IDENTITY_LENGTH: usize = 2048;
const MAX_TEXT_LENGTH: usize = 2048;

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackProgressIdentity {
    source_id: String,
    media_identity: String,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackProgressUpsert {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    completed: Option<bool>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistoryEntry {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    progress: Option<f64>,
    updated_at: i64,
    completed: bool,
    progress_source: String,
}

#[tauri::command]
pub fn player_upsert_playback_progress(
    app: AppHandle,
    progress: PlaybackProgressUpsert,
) -> Result<PlaybackHistoryEntry, String> {
    let normalized = NormalizedProgress::from_payload(progress)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.upsert(&normalized)?;
    storage
        .get(&PlaybackProgressIdentity {
            source_id: normalized.source_id,
            media_identity: normalized.media_identity,
        })?
        .ok_or_else(|| "Failed to read saved playback progress.".to_string())
}

#[tauri::command]
pub fn player_get_playback_progress(
    app: AppHandle,
    identity: PlaybackProgressIdentity,
) -> Result<Option<PlaybackHistoryEntry>, String> {
    let identity = normalize_identity(identity)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.get(&identity)
}

#[tauri::command]
pub fn player_list_continue_watching(
    app: AppHandle,
    limit: Option<u32>,
) -> Result<Vec<PlaybackHistoryEntry>, String> {
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.list_continue_watching(
        limit
            .unwrap_or(DEFAULT_CONTINUE_LIMIT)
            .min(MAX_CONTINUE_LIMIT),
    )
}

struct PlaybackHistoryStorage {
    conn: Connection,
}

struct NormalizedProgress {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    completed: bool,
}

impl PlaybackHistoryStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let dir = history_dir(app)?;
        fs::create_dir_all(&dir)
            .map_err(|_| "Failed to prepare playback history storage.".to_string())?;

        let db_path = dir.join(DATABASE_FILE);
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open playback history database.".to_string())?;
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS playback_history (
                identity_key TEXT PRIMARY KEY NOT NULL,
                source_id TEXT NOT NULL,
                library_id TEXT,
                item_id TEXT,
                media_identity TEXT NOT NULL,
                title TEXT NOT NULL,
                stream_identity TEXT,
                media_type TEXT,
                poster_url TEXT,
                backdrop_url TEXT,
                position REAL NOT NULL,
                duration REAL,
                completed INTEGER NOT NULL DEFAULT 0,
                progress_source TEXT NOT NULL DEFAULT 'local',
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE INDEX IF NOT EXISTS idx_playback_history_continue
                ON playback_history (completed, updated_at DESC);",
        )
        .map_err(|_| "Failed to initialize playback history database.".to_string())?;

        Ok(Self { conn })
    }

    fn upsert(&self, progress: &NormalizedProgress) -> Result<(), String> {
        self.conn
            .execute(
                "INSERT INTO playback_history (
                    identity_key, source_id, library_id, item_id, media_identity, title,
                    stream_identity, media_type, poster_url, backdrop_url, position, duration,
                    completed, progress_source, created_at, updated_at
                )
                VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, 'local', unixepoch(), unixepoch())
                ON CONFLICT(identity_key) DO UPDATE SET
                    source_id = excluded.source_id,
                    library_id = excluded.library_id,
                    item_id = excluded.item_id,
                    media_identity = excluded.media_identity,
                    title = excluded.title,
                    stream_identity = excluded.stream_identity,
                    media_type = excluded.media_type,
                    poster_url = excluded.poster_url,
                    backdrop_url = excluded.backdrop_url,
                    position = excluded.position,
                    duration = excluded.duration,
                    completed = excluded.completed,
                    progress_source = 'local',
                    updated_at = unixepoch()",
                params![
                    identity_key(&progress.source_id, &progress.media_identity),
                    progress.source_id,
                    progress.library_id,
                    progress.item_id,
                    progress.media_identity,
                    progress.title,
                    progress.stream_identity,
                    progress.media_type,
                    progress.poster_url,
                    progress.backdrop_url,
                    progress.position,
                    progress.duration,
                    if progress.completed { 1 } else { 0 },
                ],
            )
            .map_err(|_| "Failed to save playback progress.".to_string())?;
        Ok(())
    }

    fn get(
        &self,
        identity: &PlaybackProgressIdentity,
    ) -> Result<Option<PlaybackHistoryEntry>, String> {
        self.conn
            .query_row(
                "SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                    media_type, poster_url, backdrop_url, position, duration, updated_at,
                    completed, progress_source
                 FROM playback_history
                 WHERE identity_key = ?1",
                params![identity_key(&identity.source_id, &identity.media_identity)],
                map_history_entry,
            )
            .optional()
            .map_err(|_| "Failed to read playback progress.".to_string())
    }

    fn list_continue_watching(&self, limit: u32) -> Result<Vec<PlaybackHistoryEntry>, String> {
        let mut statement = self
            .conn
            .prepare(
                "SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                    media_type, poster_url, backdrop_url, position, duration, updated_at,
                    completed, progress_source
                 FROM playback_history
                 WHERE completed = 0
                    AND position >= ?1
                    AND (duration IS NULL OR duration <= 0 OR position < duration - ?2)
                 ORDER BY updated_at DESC
                 LIMIT ?3",
            )
            .map_err(|_| "Failed to read continue watching entries.".to_string())?;

        let entries = statement
            .query_map(
                params![
                    MIN_RESUME_POSITION_SECONDS,
                    COMPLETED_REMAINING_SECONDS,
                    limit
                ],
                map_history_entry,
            )
            .map_err(|_| "Failed to read continue watching entries.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read continue watching entries.".to_string())?;

        Ok(entries)
    }
}

impl NormalizedProgress {
    fn from_payload(payload: PlaybackProgressUpsert) -> Result<Self, String> {
        let identity = normalize_identity(PlaybackProgressIdentity {
            source_id: payload.source_id,
            media_identity: payload.media_identity,
        })?;

        let position = sanitize_position(payload.position)?;
        let duration = payload.duration.map(sanitize_duration).transpose()?;
        let completed = payload.completed.unwrap_or(false) || is_completed(position, duration);
        let title = sanitize_text(payload.title, "Untitled", MAX_TEXT_LENGTH);

        Ok(Self {
            source_id: identity.source_id,
            library_id: payload
                .library_id
                .and_then(|value| sanitize_optional_id(value, "Invalid playback library."))
                .transpose()?,
            item_id: payload
                .item_id
                .and_then(|value| sanitize_optional_id(value, "Invalid playback item."))
                .transpose()?,
            media_identity: identity.media_identity,
            title,
            stream_identity: payload
                .stream_identity
                .and_then(|value| sanitize_optional_display_text(value, true)),
            media_type: payload
                .media_type
                .and_then(|value| sanitize_optional_display_text(value, false)),
            poster_url: payload.poster_url.and_then(sanitize_optional_url),
            backdrop_url: payload.backdrop_url.and_then(sanitize_optional_url),
            position,
            duration,
            completed,
        })
    }
}

fn map_history_entry(row: &rusqlite::Row<'_>) -> rusqlite::Result<PlaybackHistoryEntry> {
    let duration = row.get::<_, Option<f64>>(10)?;
    let position = row.get::<_, f64>(9)?;
    Ok(PlaybackHistoryEntry {
        source_id: row.get(0)?,
        library_id: row.get(1)?,
        item_id: row.get(2)?,
        media_identity: row.get(3)?,
        title: row.get(4)?,
        stream_identity: row.get(5)?,
        media_type: row.get(6)?,
        poster_url: row.get(7)?,
        backdrop_url: row.get(8)?,
        position,
        duration,
        progress: progress_ratio(position, duration),
        updated_at: row.get(11)?,
        completed: row.get::<_, i64>(12)? != 0,
        progress_source: row.get(13)?,
    })
}

fn history_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let mut dir = app
        .path()
        .app_data_dir()
        .map_err(|_| "Failed to resolve app data directory.".to_string())?;
    dir.push("history");
    Ok(dir)
}

fn identity_key(source_id: &str, media_identity: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(source_id.as_bytes());
    hasher.update([0]);
    hasher.update(media_identity.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn normalize_identity(
    identity: PlaybackProgressIdentity,
) -> Result<PlaybackProgressIdentity, String> {
    Ok(PlaybackProgressIdentity {
        source_id: normalize_id(identity.source_id, "Invalid playback source.")?,
        media_identity: normalize_media_identity(identity.media_identity)?,
    })
}

fn normalize_id(value: String, message: &str) -> Result<String, String> {
    let trimmed = value.trim();
    validate_id(trimmed, message)?;
    Ok(trimmed.to_string())
}

fn normalize_media_identity(value: String) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) {
        return Err("Invalid playback identity.".to_string());
    }

    let normalized = if looks_sensitive_url(trimmed) {
        redact_sensitive_url(trimmed)
    } else {
        trimmed.to_string()
    };

    if normalized.len() > MAX_IDENTITY_LENGTH {
        Err("Invalid playback identity.".to_string())
    } else {
        Ok(normalized)
    }
}

fn validate_id(value: &str, message: &str) -> Result<(), String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || trimmed.len() > MAX_ID_LENGTH || contains_control_character(trimmed) {
        Err(message.to_string())
    } else {
        Ok(())
    }
}

fn sanitize_optional_id(value: String, message: &str) -> Option<Result<String, String>> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return None;
    }
    Some(validate_id(trimmed, message).map(|_| trimmed.to_string()))
}

fn sanitize_position(value: f64) -> Result<f64, String> {
    if value.is_finite() && value >= 0.0 {
        Ok(value)
    } else {
        Err("Invalid playback position.".to_string())
    }
}

fn sanitize_duration(value: f64) -> Result<f64, String> {
    if value.is_finite() && value >= 0.0 {
        Ok(value)
    } else {
        Err("Invalid playback duration.".to_string())
    }
}

fn sanitize_text(value: String, fallback: &str, max_len: usize) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return fallback.to_string();
    }

    trimmed.chars().take(max_len).collect()
}

fn sanitize_optional_display_text(value: String, allow_local_path: bool) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) {
        return None;
    }

    let value = sanitize_text(trimmed.to_string(), "", MAX_TEXT_LENGTH);
    if looks_sensitive_url(&value) {
        return Some(redact_sensitive_url(&value));
    }

    if !allow_local_path && (value.contains('/') || value.contains('\\')) {
        return None;
    }

    Some(value)
}

fn sanitize_optional_url(value: String) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) || looks_sensitive_url(trimmed) {
        return None;
    }

    if trimmed.starts_with("http://") || trimmed.starts_with("https://") {
        Some(sanitize_text(trimmed.to_string(), "", MAX_TEXT_LENGTH))
    } else {
        None
    }
}

fn contains_control_character(value: &str) -> bool {
    value.chars().any(|ch| ch.is_control())
}

fn is_completed(position: f64, duration: Option<f64>) -> bool {
    let Some(duration) = duration else {
        return false;
    };
    if duration <= MIN_RESUME_POSITION_SECONDS {
        return false;
    }

    position >= duration * COMPLETED_PROGRESS_RATIO
        || duration - position <= COMPLETED_REMAINING_SECONDS
}

fn progress_ratio(position: f64, duration: Option<f64>) -> Option<f64> {
    let duration = duration?;
    if duration <= 0.0 {
        return None;
    }
    Some((position / duration).clamp(0.0, 1.0))
}

fn looks_sensitive_url(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    (lower.starts_with("http://") || lower.starts_with("https://"))
        && [
            "api_key=",
            "apikey=",
            "access_key=",
            "access_token=",
            "token=",
            "x-emby-token=",
            "expires=",
            "x-amz-expires=",
            "signature=",
            "sig=",
            "sign=",
            "password=",
            "passwd=",
            "pwd=",
            "security-token=",
            "x-amz-signature=",
            "x-amz-credential=",
            "x-amz-security-token=",
            "ossaccesskeyid=",
            "awsaccesskeyid=",
        ]
        .iter()
        .any(|needle| lower.contains(needle))
}

fn redact_sensitive_url(value: &str) -> String {
    let mut redacted = value.to_string();
    for key in [
        "api_key",
        "apikey",
        "access_key",
        "access_token",
        "token",
        "x-emby-token",
        "expires",
        "x-amz-expires",
        "signature",
        "sig",
        "sign",
        "password",
        "passwd",
        "pwd",
        "security-token",
        "x-amz-signature",
        "x-amz-credential",
        "x-amz-security-token",
        "ossaccesskeyid",
        "awsaccesskeyid",
    ] {
        redacted = redact_query_value(&redacted, key);
    }
    redacted
}

fn redact_query_value(value: &str, key: &str) -> String {
    let lower = value.to_ascii_lowercase();
    let needle = format!("{}=", key.to_ascii_lowercase());
    let mut output = String::with_capacity(value.len());
    let mut index = 0;

    while let Some(relative_start) = lower[index..].find(&needle) {
        let start = index + relative_start;
        let value_start = start + needle.len();
        let value_end = value[value_start..]
            .find('&')
            .map(|relative_end| value_start + relative_end)
            .unwrap_or(value.len());
        output.push_str(&value[index..value_start]);
        output.push_str("[redacted]");
        index = value_end;
    }

    output.push_str(&value[index..]);
    output
}

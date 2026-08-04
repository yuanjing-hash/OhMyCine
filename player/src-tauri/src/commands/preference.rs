use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::{Path, PathBuf};
use tauri::AppHandle;

use crate::storage;

const DATABASE_FILE: &str = "player_preferences.sqlite";
const PLAYBACK_SPEED_KEY: &str = "playback_speed";
const DEFAULT_PLAYBACK_SPEED: f64 = 1.0;
const MIN_PLAYBACK_SPEED: f64 = 0.25;
const MAX_PLAYBACK_SPEED: f64 = 4.0;
const MIN_SUBTITLE_DELAY: f64 = -30.0;
const MAX_SUBTITLE_DELAY: f64 = 30.0;
const MIN_VIDEO_BRIGHTNESS: f64 = 0.0;
const MAX_VIDEO_BRIGHTNESS: f64 = 100.0;
const MAX_ID_LENGTH: usize = 512;
const MAX_IDENTITY_LENGTH: usize = 2048;
const MAX_TRACK_TEXT_LENGTH: usize = 256;
const RAW_SCAN_DATABASE_FILE: &str = "raw_scan_cache.sqlite";

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackSpeedPreference {
    playback_speed: f64,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaTrackPreference {
    language: Option<String>,
    title: Option<String>,
    codec: Option<String>,
    channels: Option<u32>,
    track_id: Option<i64>,
}

#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaSubtitlePreference {
    kind: String,
    track: Option<MediaTrackPreference>,
    cached_path: Option<String>,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaPlaybackPreferenceIdentity {
    source_id: String,
    media_identity: String,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaPlaybackPreferenceUpsert {
    source_id: String,
    media_identity: String,
    subtitle: Option<MediaSubtitlePreference>,
    audio: Option<MediaTrackPreference>,
    subtitle_delay: f64,
    playback_speed: f64,
    video_brightness: f64,
    aspect_mode: String,
    fit_mode: String,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaPlaybackPreference {
    source_id: String,
    media_identity: String,
    subtitle: Option<MediaSubtitlePreference>,
    audio: Option<MediaTrackPreference>,
    subtitle_delay: f64,
    playback_speed: f64,
    video_brightness: f64,
    aspect_mode: String,
    fit_mode: String,
    updated_at: i64,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaCacheClearResult {
    playback_preferences_deleted: u64,
    raw_scan_cache_entries_deleted: u64,
}

#[tauri::command]
pub fn player_get_playback_speed_preference(
    app: AppHandle,
) -> Result<PlaybackSpeedPreference, String> {
    let storage = PreferenceStorage::open(&app)?;
    let playback_speed = storage
        .get_playback_speed()?
        .unwrap_or(DEFAULT_PLAYBACK_SPEED);

    Ok(PlaybackSpeedPreference { playback_speed })
}

#[tauri::command]
pub fn player_set_playback_speed_preference(app: AppHandle, speed: f64) -> Result<(), String> {
    validate_playback_speed(speed)?;

    let storage = PreferenceStorage::open(&app)?;
    storage.set_playback_speed(speed)
}

#[tauri::command]
pub fn player_get_media_playback_preference(
    app: AppHandle,
    identity: MediaPlaybackPreferenceIdentity,
) -> Result<Option<MediaPlaybackPreference>, String> {
    let identity = normalize_media_identity(identity)?;
    let storage = PreferenceStorage::open(&app)?;
    storage.get_media_preference(&app, &identity)
}

#[tauri::command]
pub fn player_upsert_media_playback_preference(
    app: AppHandle,
    preference: MediaPlaybackPreferenceUpsert,
) -> Result<(), String> {
    let normalized = normalize_media_preference(&app, preference)?;
    let storage = PreferenceStorage::open(&app)?;
    storage.upsert_media_preference(&normalized)
}

#[tauri::command]
pub fn player_delete_media_playback_preferences_for_source(
    app: AppHandle,
    source_id: String,
) -> Result<u64, String> {
    let source_id = normalize_id(source_id, MAX_ID_LENGTH, "Invalid playback source.")?;
    let storage = PreferenceStorage::open(&app)?;
    let deleted = storage.delete_media_preferences_for_source(&source_id)?;
    remove_source_subtitle_cache(&app, &source_id)?;
    Ok(deleted)
}

#[tauri::command]
pub fn player_clear_media_cache(app: AppHandle) -> Result<MediaCacheClearResult, String> {
    let storage = PreferenceStorage::open(&app)?;
    let playback_preferences_deleted = storage.clear_media_preferences()?;
    let raw_scan_cache_entries_deleted = clear_raw_scan_cache(&app)?;
    clear_cache_directory(&app)?;
    Ok(MediaCacheClearResult {
        playback_preferences_deleted,
        raw_scan_cache_entries_deleted,
    })
}

struct PreferenceStorage {
    conn: Connection,
}

fn ensure_media_preference_column(
    conn: &Connection,
    column: &str,
    definition: &str,
) -> Result<(), String> {
    let mut statement = conn
        .prepare("PRAGMA table_info(media_playback_preferences)")
        .map_err(|_| "Failed to inspect player preferences.".to_string())?;
    let columns = statement
        .query_map([], |row| row.get::<_, String>(1))
        .map_err(|_| "Failed to inspect player preferences.".to_string())?;
    for existing in columns {
        if existing.map_err(|_| "Failed to inspect player preferences.".to_string())? == column {
            return Ok(());
        }
    }
    conn.execute(
        &format!("ALTER TABLE media_playback_preferences ADD COLUMN {column} {definition}"),
        [],
    )
    .map_err(|_| "Failed to upgrade player preferences.".to_string())?;
    Ok(())
}

impl PreferenceStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let db_path = storage::data_file(app, DATABASE_FILE)?;
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open player preferences.".to_string())?;
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS player_preferences (
                key TEXT PRIMARY KEY NOT NULL,
                value REAL NOT NULL,
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE TABLE IF NOT EXISTS media_playback_preferences (
                identity_key TEXT PRIMARY KEY NOT NULL,
                source_id TEXT NOT NULL,
                media_identity TEXT NOT NULL,
                subtitle_json TEXT,
                audio_json TEXT,
                subtitle_delay REAL NOT NULL DEFAULT 0,
                playback_speed REAL NOT NULL DEFAULT 1,
                video_brightness REAL NOT NULL DEFAULT 50,
                aspect_mode TEXT NOT NULL DEFAULT 'default',
                fit_mode TEXT NOT NULL DEFAULT 'fit',
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE INDEX IF NOT EXISTS idx_media_playback_preferences_source
                ON media_playback_preferences (source_id);",
        )
        .map_err(|_| "Failed to initialize player preferences.".to_string())?;
        ensure_media_preference_column(&conn, "video_brightness", "REAL NOT NULL DEFAULT 50")?;

        Ok(Self { conn })
    }

    fn get_playback_speed(&self) -> Result<Option<f64>, String> {
        let speed = self
            .conn
            .query_row(
                "SELECT value FROM player_preferences WHERE key = ?1",
                params![PLAYBACK_SPEED_KEY],
                |row| row.get::<_, f64>(0),
            )
            .optional()
            .map_err(|_| "Failed to read player preferences.".to_string())?;

        match speed {
            Some(value) => {
                validate_playback_speed(value)?;
                Ok(Some(value))
            }
            None => Ok(None),
        }
    }

    fn set_playback_speed(&self, speed: f64) -> Result<(), String> {
        self.conn
            .execute(
                "INSERT INTO player_preferences (key, value, updated_at)
                 VALUES (?1, ?2, unixepoch())
                 ON CONFLICT(key) DO UPDATE SET
                    value = excluded.value,
                    updated_at = unixepoch()",
                params![PLAYBACK_SPEED_KEY, speed],
            )
            .map_err(|_| "Failed to save player preferences.".to_string())?;
        Ok(())
    }

    fn get_media_preference(
        &self,
        app: &AppHandle,
        identity: &MediaPlaybackPreferenceIdentity,
    ) -> Result<Option<MediaPlaybackPreference>, String> {
        let entry = self
            .conn
            .query_row(
                "SELECT source_id, media_identity, subtitle_json, audio_json, subtitle_delay,
                    playback_speed, video_brightness, aspect_mode, fit_mode, updated_at
                 FROM media_playback_preferences WHERE identity_key = ?1",
                params![media_identity_key(
                    &identity.source_id,
                    &identity.media_identity
                )],
                |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, Option<String>>(2)?,
                        row.get::<_, Option<String>>(3)?,
                        row.get::<_, f64>(4)?,
                        row.get::<_, f64>(5)?,
                        row.get::<_, f64>(6)?,
                        row.get::<_, String>(7)?,
                        row.get::<_, String>(8)?,
                        row.get::<_, i64>(9)?,
                    ))
                },
            )
            .optional()
            .map_err(|_| "Failed to read media playback preferences.".to_string())?;

        let Some((
            source_id,
            media_identity,
            subtitle_json,
            audio_json,
            subtitle_delay,
            playback_speed,
            video_brightness,
            aspect_mode,
            fit_mode,
            updated_at,
        )) = entry
        else {
            return Ok(None);
        };
        let subtitle = subtitle_json
            .and_then(|value| serde_json::from_str::<MediaSubtitlePreference>(&value).ok())
            .and_then(|value| normalize_subtitle_preference(app, value).ok());
        let audio = audio_json
            .and_then(|value| serde_json::from_str::<MediaTrackPreference>(&value).ok())
            .and_then(|value| normalize_track_preference(value).ok());
        validate_subtitle_delay(subtitle_delay)?;
        validate_playback_speed(playback_speed)?;
        validate_video_brightness(video_brightness)?;
        validate_aspect_mode(&aspect_mode)?;
        validate_fit_mode(&fit_mode)?;

        Ok(Some(MediaPlaybackPreference {
            source_id,
            media_identity,
            subtitle,
            audio,
            subtitle_delay,
            playback_speed,
            video_brightness,
            aspect_mode,
            fit_mode,
            updated_at,
        }))
    }

    fn upsert_media_preference(
        &self,
        preference: &MediaPlaybackPreferenceUpsert,
    ) -> Result<(), String> {
        let subtitle_json = preference
            .subtitle
            .as_ref()
            .map(serde_json::to_string)
            .transpose()
            .map_err(|_| "Failed to encode media subtitle preference.".to_string())?;
        let audio_json = preference
            .audio
            .as_ref()
            .map(serde_json::to_string)
            .transpose()
            .map_err(|_| "Failed to encode media audio preference.".to_string())?;
        self.conn
            .execute(
                "INSERT INTO media_playback_preferences (
                    identity_key, source_id, media_identity, subtitle_json, audio_json,
                    subtitle_delay, playback_speed, video_brightness, aspect_mode, fit_mode, created_at, updated_at
                ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, unixepoch(), unixepoch())
                ON CONFLICT(identity_key) DO UPDATE SET
                    source_id = excluded.source_id,
                    media_identity = excluded.media_identity,
                    subtitle_json = excluded.subtitle_json,
                    audio_json = excluded.audio_json,
                    subtitle_delay = excluded.subtitle_delay,
                    playback_speed = excluded.playback_speed,
                    video_brightness = excluded.video_brightness,
                    aspect_mode = excluded.aspect_mode,
                    fit_mode = excluded.fit_mode,
                    updated_at = unixepoch()",
                params![
                    media_identity_key(&preference.source_id, &preference.media_identity),
                    preference.source_id,
                    preference.media_identity,
                    subtitle_json,
                    audio_json,
                    preference.subtitle_delay,
                    preference.playback_speed,
                    preference.video_brightness,
                    preference.aspect_mode,
                    preference.fit_mode,
                ],
            )
            .map_err(|_| "Failed to save media playback preferences.".to_string())?;
        Ok(())
    }

    fn delete_media_preferences_for_source(&self, source_id: &str) -> Result<u64, String> {
        let deleted = self
            .conn
            .execute(
                "DELETE FROM media_playback_preferences WHERE source_id = ?1",
                params![source_id],
            )
            .map_err(|_| "Failed to delete media playback preferences for source.".to_string())?;
        Ok(deleted as u64)
    }

    fn clear_media_preferences(&self) -> Result<u64, String> {
        let deleted = self
            .conn
            .execute("DELETE FROM media_playback_preferences", params![])
            .map_err(|_| "Failed to clear media playback preferences.".to_string())?;
        Ok(deleted as u64)
    }
}

fn validate_playback_speed(speed: f64) -> Result<(), String> {
    if speed.is_finite() && (MIN_PLAYBACK_SPEED..=MAX_PLAYBACK_SPEED).contains(&speed) {
        Ok(())
    } else {
        Err("Invalid playback speed.".to_string())
    }
}

fn validate_video_brightness(value: f64) -> Result<(), String> {
    if value.is_finite() && (MIN_VIDEO_BRIGHTNESS..=MAX_VIDEO_BRIGHTNESS).contains(&value) {
        Ok(())
    } else {
        Err("Invalid video brightness.".to_string())
    }
}

fn normalize_media_preference(
    app: &AppHandle,
    preference: MediaPlaybackPreferenceUpsert,
) -> Result<MediaPlaybackPreferenceUpsert, String> {
    let identity = normalize_media_identity(MediaPlaybackPreferenceIdentity {
        source_id: preference.source_id,
        media_identity: preference.media_identity,
    })?;
    validate_subtitle_delay(preference.subtitle_delay)?;
    validate_playback_speed(preference.playback_speed)?;
    validate_video_brightness(preference.video_brightness)?;
    validate_aspect_mode(&preference.aspect_mode)?;
    validate_fit_mode(&preference.fit_mode)?;
    Ok(MediaPlaybackPreferenceUpsert {
        source_id: identity.source_id,
        media_identity: identity.media_identity,
        subtitle: preference
            .subtitle
            .map(|value| normalize_subtitle_preference(app, value))
            .transpose()?,
        audio: preference
            .audio
            .map(normalize_track_preference)
            .transpose()?,
        subtitle_delay: preference.subtitle_delay,
        playback_speed: preference.playback_speed,
        video_brightness: preference.video_brightness,
        aspect_mode: preference.aspect_mode,
        fit_mode: preference.fit_mode,
    })
}

fn normalize_media_identity(
    identity: MediaPlaybackPreferenceIdentity,
) -> Result<MediaPlaybackPreferenceIdentity, String> {
    Ok(MediaPlaybackPreferenceIdentity {
        source_id: normalize_id(
            identity.source_id,
            MAX_ID_LENGTH,
            "Invalid playback source.",
        )?,
        media_identity: normalize_id(
            identity.media_identity,
            MAX_IDENTITY_LENGTH,
            "Invalid media identity.",
        )?,
    })
}

fn normalize_subtitle_preference(
    app: &AppHandle,
    preference: MediaSubtitlePreference,
) -> Result<MediaSubtitlePreference, String> {
    let kind = preference.kind.trim().to_string();
    if !matches!(kind.as_str(), "off" | "embedded" | "cachedExternal") {
        return Err("Invalid subtitle preference kind.".to_string());
    }
    let track = preference
        .track
        .map(normalize_track_preference)
        .transpose()?;
    let cached_path = match (kind.as_str(), preference.cached_path) {
        ("cachedExternal", Some(path)) => Some(validate_cached_subtitle_path(app, &path)?),
        ("cachedExternal", None) => {
            return Err("Cached subtitle preference is missing its path.".to_string())
        }
        (_, _) => None,
    };
    Ok(MediaSubtitlePreference {
        kind,
        track,
        cached_path,
    })
}

fn normalize_track_preference(
    preference: MediaTrackPreference,
) -> Result<MediaTrackPreference, String> {
    let normalized = MediaTrackPreference {
        language: normalize_optional_track_text(preference.language)?,
        title: normalize_optional_track_text(preference.title)?,
        codec: normalize_optional_track_text(preference.codec)?,
        channels: preference.channels.filter(|value| *value <= 64),
        track_id: preference.track_id,
    };
    if normalized.language.is_none()
        && normalized.title.is_none()
        && normalized.codec.is_none()
        && normalized.channels.is_none()
        && normalized.track_id.is_none()
    {
        return Err("Empty media track preference.".to_string());
    }
    Ok(normalized)
}

fn normalize_optional_track_text(value: Option<String>) -> Result<Option<String>, String> {
    let Some(value) = value else {
        return Ok(None);
    };
    let value = value.trim();
    if value.is_empty() {
        return Ok(None);
    }
    if value.len() > MAX_TRACK_TEXT_LENGTH || value.chars().any(char::is_control) {
        return Err("Invalid media track preference text.".to_string());
    }
    Ok(Some(value.to_string()))
}

fn normalize_id(value: String, max_length: usize, message: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > max_length || value.chars().any(char::is_control) {
        return Err(message.to_string());
    }
    Ok(value.to_string())
}

fn validate_subtitle_delay(delay: f64) -> Result<(), String> {
    if delay.is_finite() && (MIN_SUBTITLE_DELAY..=MAX_SUBTITLE_DELAY).contains(&delay) {
        Ok(())
    } else {
        Err("Invalid subtitle delay.".to_string())
    }
}

fn validate_aspect_mode(value: &str) -> Result<(), String> {
    if matches!(value, "default" | "16:9" | "4:3" | "cinema") {
        Ok(())
    } else {
        Err("Invalid video aspect mode.".to_string())
    }
}

fn validate_fit_mode(value: &str) -> Result<(), String> {
    if matches!(value, "fit" | "crop" | "cinemaCrop") {
        Ok(())
    } else {
        Err("Invalid video fit mode.".to_string())
    }
}

fn validate_cached_subtitle_path(app: &AppHandle, value: &str) -> Result<String, String> {
    let layout = storage::initialize(app)?;
    let root = layout.cache_dir.join("subtitles");
    let path = PathBuf::from(value);
    let canonical_root =
        fs::canonicalize(&root).map_err(|_| "Subtitle cache is unavailable.".to_string())?;
    let canonical_path =
        fs::canonicalize(&path).map_err(|_| "Cached subtitle file is unavailable.".to_string())?;
    if !canonical_path.starts_with(&canonical_root) || !is_supported_subtitle_path(&canonical_path)
    {
        return Err("Cached subtitle path is outside the Player cache.".to_string());
    }
    Ok(canonical_path.to_string_lossy().to_string())
}

fn is_supported_subtitle_path(path: &Path) -> bool {
    path.extension()
        .and_then(|value| value.to_str())
        .is_some_and(|value| {
            matches!(
                value.to_ascii_lowercase().as_str(),
                "srt" | "ass" | "ssa" | "vtt" | "sub"
            )
        })
}

fn media_identity_key(source_id: &str, media_identity: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(source_id.as_bytes());
    hasher.update([0]);
    hasher.update(media_identity.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn remove_source_subtitle_cache(app: &AppHandle, source_id: &str) -> Result<(), String> {
    let layout = storage::initialize(app)?;
    let source_dir = layout
        .cache_dir
        .join("subtitles")
        .join(storage::scoped_cache_key("subtitle-source", source_id));
    if source_dir.exists() {
        fs::remove_dir_all(source_dir)
            .map_err(|_| "Failed to delete source subtitle cache.".to_string())?;
    }
    Ok(())
}

fn clear_raw_scan_cache(app: &AppHandle) -> Result<u64, String> {
    let path = storage::data_file(app, RAW_SCAN_DATABASE_FILE)?;
    if !path.exists() {
        return Ok(0);
    }
    let conn = Connection::open(path).map_err(|_| "Failed to open raw scan cache.".to_string())?;
    let table_exists = conn
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'raw_scan_cache'",
            params![],
            |_| Ok(()),
        )
        .optional()
        .map_err(|_| "Failed to inspect raw scan cache.".to_string())?
        .is_some();
    if !table_exists {
        return Ok(0);
    }
    let deleted = conn
        .execute("DELETE FROM raw_scan_cache", params![])
        .map_err(|_| "Failed to clear raw scan cache.".to_string())?;
    Ok(deleted as u64)
}

fn clear_cache_directory(app: &AppHandle) -> Result<(), String> {
    let layout = storage::initialize(app)?;
    if layout.cache_dir.exists() {
        for entry in fs::read_dir(&layout.cache_dir)
            .map_err(|_| "Failed to read Player cache directory.".to_string())?
        {
            let path = entry
                .map_err(|_| "Failed to read Player cache entry.".to_string())?
                .path();
            if path.is_dir() {
                fs::remove_dir_all(path)
                    .map_err(|_| "Failed to clear Player cache directory.".to_string())?;
            } else {
                fs::remove_file(path)
                    .map_err(|_| "Failed to clear Player cache file.".to_string())?;
            }
        }
    }
    fs::create_dir_all(&layout.cache_dir)
        .map_err(|_| "Failed to recreate Player cache directory.".to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{PreferenceStorage, PLAYBACK_SPEED_KEY};
    use rusqlite::{params, Connection};

    fn storage() -> PreferenceStorage {
        let conn = Connection::open_in_memory().expect("open preference test database");
        conn.execute_batch(
            "CREATE TABLE player_preferences (
                key TEXT PRIMARY KEY NOT NULL,
                value REAL NOT NULL,
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE TABLE media_playback_preferences (
                identity_key TEXT PRIMARY KEY NOT NULL,
                source_id TEXT NOT NULL,
                media_identity TEXT NOT NULL,
                subtitle_json TEXT,
                audio_json TEXT,
                subtitle_delay REAL NOT NULL,
                playback_speed REAL NOT NULL,
                video_brightness REAL NOT NULL DEFAULT 50,
                aspect_mode TEXT NOT NULL,
                fit_mode TEXT NOT NULL,
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );",
        )
        .expect("initialize preference test database");
        PreferenceStorage { conn }
    }

    fn insert_media_preference(storage: &PreferenceStorage, key: &str, source_id: &str) {
        storage
            .conn
            .execute(
                "INSERT INTO media_playback_preferences (
                    identity_key, source_id, media_identity, subtitle_delay, playback_speed,
                    aspect_mode, fit_mode
                ) VALUES (?1, ?2, ?3, 0, 1, 'default', 'fit')",
                params![key, source_id, format!("media-{key}")],
            )
            .expect("insert media preference");
    }

    #[test]
    fn deleting_source_preferences_keeps_other_sources() {
        let storage = storage();
        insert_media_preference(&storage, "a", "source-a");
        insert_media_preference(&storage, "b", "source-b");

        assert_eq!(
            storage
                .delete_media_preferences_for_source("source-a")
                .expect("delete source preferences"),
            1
        );
        let remaining: i64 = storage
            .conn
            .query_row(
                "SELECT COUNT(*) FROM media_playback_preferences WHERE source_id = 'source-b'",
                params![],
                |row| row.get(0),
            )
            .expect("count remaining preferences");
        assert_eq!(remaining, 1);
    }

    #[test]
    fn clearing_media_preferences_preserves_global_preferences() {
        let storage = storage();
        storage
            .set_playback_speed(1.5)
            .expect("save global playback speed");
        insert_media_preference(&storage, "a", "source-a");

        assert_eq!(
            storage
                .clear_media_preferences()
                .expect("clear media preferences"),
            1
        );
        let global_count: i64 = storage
            .conn
            .query_row(
                "SELECT COUNT(*) FROM player_preferences WHERE key = ?1",
                params![PLAYBACK_SPEED_KEY],
                |row| row.get(0),
            )
            .expect("count global preferences");
        assert_eq!(global_count, 1);
    }
}

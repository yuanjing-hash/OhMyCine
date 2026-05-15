use rusqlite::{params, Connection, OptionalExtension};
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

const DATABASE_FILE: &str = "player_preferences.sqlite";
const PLAYBACK_SPEED_KEY: &str = "playback_speed";
const DEFAULT_PLAYBACK_SPEED: f64 = 1.0;
const MIN_PLAYBACK_SPEED: f64 = 0.25;
const MAX_PLAYBACK_SPEED: f64 = 4.0;

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackSpeedPreference {
    playback_speed: f64,
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

struct PreferenceStorage {
    conn: Connection,
}

impl PreferenceStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let dir = preference_dir(app)?;
        fs::create_dir_all(&dir)
            .map_err(|_| "Failed to prepare player preferences.".to_string())?;

        let db_path = dir.join(DATABASE_FILE);
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open player preferences.".to_string())?;
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS player_preferences (
                key TEXT PRIMARY KEY NOT NULL,
                value REAL NOT NULL,
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );",
        )
        .map_err(|_| "Failed to initialize player preferences.".to_string())?;

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
}

fn preference_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let mut dir = app
        .path()
        .app_data_dir()
        .map_err(|_| "Failed to resolve app data directory.".to_string())?;
    dir.push("preferences");
    Ok(dir)
}

fn validate_playback_speed(speed: f64) -> Result<(), String> {
    if speed.is_finite() && (MIN_PLAYBACK_SPEED..=MAX_PLAYBACK_SPEED).contains(&speed) {
        Ok(())
    } else {
        Err("Invalid playback speed.".to_string())
    }
}

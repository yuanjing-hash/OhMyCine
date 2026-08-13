use crate::storage;
use rusqlite::{params, Connection, OptionalExtension};
use std::collections::BTreeMap;
use tauri::AppHandle;

const DATABASE_FILE: &str = "settings.sqlite";
const MAX_KEY_LENGTH: usize = 16 * 1024;
const MAX_VALUE_BYTES: usize = 5 * 1024 * 1024;

#[tauri::command]
pub fn player_settings_get_all(app: AppHandle) -> Result<BTreeMap<String, String>, String> {
    let storage = SettingsStorage::open(&app)?;
    storage.get_all()
}

#[tauri::command]
pub fn player_settings_set(app: AppHandle, key: String, value: String) -> Result<(), String> {
    validate_key(&key)?;
    validate_value(&value)?;
    let storage = SettingsStorage::open(&app)?;
    storage.set(&key, &value)
}

#[tauri::command]
pub fn player_settings_delete(app: AppHandle, key: String) -> Result<(), String> {
    validate_key(&key)?;
    let storage = SettingsStorage::open(&app)?;
    storage.delete(&key)
}

#[tauri::command]
pub fn player_get_storage_info(app: AppHandle) -> Result<storage::StorageInfo, String> {
    storage::storage_info(&app)
}

pub(crate) fn read_player_setting(app: &AppHandle, key: &str) -> Result<Option<String>, String> {
    validate_key(key)?;
    let storage = SettingsStorage::open(app)?;
    storage
        .conn
        .query_row(
            "SELECT value FROM app_settings WHERE key = ?1",
            params![key],
            |row| row.get(0),
        )
        .optional()
        .map_err(|_| "Failed to read Player setting.".to_string())
}

pub(crate) fn write_player_setting(app: &AppHandle, key: &str, value: &str) -> Result<(), String> {
    validate_key(key)?;
    validate_value(value)?;
    SettingsStorage::open(app)?.set(key, value)
}

struct SettingsStorage {
    conn: Connection,
}

impl SettingsStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let db_path = storage::data_file(app, DATABASE_FILE)?;
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open Player settings database.".to_string())?;
        initialize_schema(&conn)?;
        Ok(Self { conn })
    }

    fn get_all(&self) -> Result<BTreeMap<String, String>, String> {
        let mut statement = self
            .conn
            .prepare("SELECT key, value FROM app_settings ORDER BY key")
            .map_err(|_| "Failed to read Player settings.".to_string())?;
        let entries = statement
            .query_map([], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
            })
            .map_err(|_| "Failed to read Player settings.".to_string())?
            .collect::<Result<BTreeMap<_, _>, _>>()
            .map_err(|_| "Failed to read Player settings.".to_string())?;
        Ok(entries)
    }

    fn set(&self, key: &str, value: &str) -> Result<(), String> {
        self.conn
            .execute(
                "INSERT INTO app_settings (key, value, updated_at)
                 VALUES (?1, ?2, unixepoch())
                 ON CONFLICT(key) DO UPDATE SET
                    value = excluded.value,
                    updated_at = unixepoch()",
                params![key, value],
            )
            .map_err(|_| "Failed to save Player setting.".to_string())?;
        Ok(())
    }

    fn delete(&self, key: &str) -> Result<(), String> {
        self.conn
            .execute("DELETE FROM app_settings WHERE key = ?1", params![key])
            .map_err(|_| "Failed to delete Player setting.".to_string())?;
        Ok(())
    }
}

fn initialize_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch(
        "CREATE TABLE IF NOT EXISTS app_settings (
            key TEXT PRIMARY KEY NOT NULL,
            value TEXT NOT NULL,
            updated_at INTEGER NOT NULL DEFAULT (unixepoch())
        );",
    )
    .map_err(|_| "Failed to initialize Player settings database.".to_string())
}

fn validate_key(key: &str) -> Result<(), String> {
    let valid_prefix = key.starts_with("ohmycine-") || key.starts_with("ohmycine:");
    if key.is_empty()
        || key.len() > MAX_KEY_LENGTH
        || key.chars().any(char::is_control)
        || !valid_prefix
    {
        Err("Invalid Player setting key.".to_string())
    } else {
        Ok(())
    }
}

fn validate_value(value: &str) -> Result<(), String> {
    if value.len() > MAX_VALUE_BYTES {
        Err("Player setting value is too large.".to_string())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::{initialize_schema, validate_key, validate_value, SettingsStorage};
    use rusqlite::Connection;

    #[test]
    fn settings_keys_are_namespaced() {
        assert!(validate_key("ohmycine-theme").is_ok());
        assert!(validate_key("ohmycine:warning").is_ok());
        assert!(validate_key("theme").is_err());
        assert!(validate_key("ohmycine-theme\ninvalid").is_err());
    }

    #[test]
    fn settings_values_are_bounded() {
        assert!(validate_value("{}").is_ok());
        assert!(validate_value(&"x".repeat(5 * 1024 * 1024 + 1)).is_err());
    }

    #[test]
    fn settings_round_trip_uses_sqlite() {
        let conn = Connection::open_in_memory().expect("open settings test database");
        initialize_schema(&conn).expect("initialize settings schema");
        let storage = SettingsStorage { conn };

        storage.set("ohmycine-theme", "dark").expect("save setting");
        assert_eq!(
            storage
                .get_all()
                .expect("read settings")
                .get("ohmycine-theme")
                .map(String::as_str),
            Some("dark")
        );
        storage.delete("ohmycine-theme").expect("delete setting");
        assert!(storage.get_all().expect("read deleted settings").is_empty());
    }
}

use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

const DATABASE_FILE: &str = "raw_scan_cache.sqlite";
const MAX_SOURCE_ID_LENGTH: usize = 512;
const MAX_SOURCE_TYPE_LENGTH: usize = 32;
const MAX_ROOT_PATH_LENGTH: usize = 4096;
const MAX_PAYLOAD_BYTES: usize = 50 * 1024 * 1024;

#[tauri::command]
pub fn raw_scan_cache_get(
    app: AppHandle,
    source_id: String,
    source_type: String,
    root_path: String,
) -> Result<Option<String>, String> {
    let identity = RawScanCacheIdentity::new(source_id, source_type, root_path)?;
    let storage = RawScanCacheStorage::open(&app)?;
    storage.get(&identity)
}

#[tauri::command]
pub fn raw_scan_cache_set(
    app: AppHandle,
    source_id: String,
    source_type: String,
    root_path: String,
    payload: String,
) -> Result<(), String> {
    let identity = RawScanCacheIdentity::new(source_id, source_type, root_path)?;
    validate_payload(&payload)?;
    let storage = RawScanCacheStorage::open(&app)?;
    storage.set(&identity, &payload)
}

#[tauri::command]
pub fn raw_scan_cache_delete(
    app: AppHandle,
    source_id: String,
    source_type: String,
    root_path: String,
) -> Result<(), String> {
    let identity = RawScanCacheIdentity::new(source_id, source_type, root_path)?;
    let storage = RawScanCacheStorage::open(&app)?;
    storage.delete(&identity)
}

struct RawScanCacheIdentity {
    source_id: String,
    source_type: String,
    root_path: String,
}

struct RawScanCacheStorage {
    conn: Connection,
}

impl RawScanCacheIdentity {
    fn new(source_id: String, source_type: String, root_path: String) -> Result<Self, String> {
        validate_source_id(&source_id)?;
        validate_source_type(&source_type)?;
        validate_root_path(&root_path)?;
        Ok(Self {
            source_id,
            source_type,
            root_path,
        })
    }
}

impl RawScanCacheStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let dir = raw_scan_cache_dir(app)?;
        fs::create_dir_all(&dir)
            .map_err(|_| "Failed to prepare raw scan cache storage.".to_string())?;

        let db_path = dir.join(DATABASE_FILE);
        let conn =
            Connection::open(db_path).map_err(|_| "Failed to open raw scan cache.".to_string())?;
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS raw_scan_cache (
                cache_key TEXT PRIMARY KEY NOT NULL,
                source_id TEXT NOT NULL,
                source_type TEXT NOT NULL,
                root_path TEXT NOT NULL,
                payload TEXT NOT NULL,
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE INDEX IF NOT EXISTS idx_raw_scan_cache_source
                ON raw_scan_cache (source_type, source_id, root_path);",
        )
        .map_err(|_| "Failed to initialize raw scan cache.".to_string())?;

        Ok(Self { conn })
    }

    fn get(&self, identity: &RawScanCacheIdentity) -> Result<Option<String>, String> {
        self.conn
            .query_row(
                "SELECT payload FROM raw_scan_cache WHERE cache_key = ?1",
                params![cache_key(identity)],
                |row| row.get::<_, String>(0),
            )
            .optional()
            .map_err(|_| "Failed to read raw scan cache.".to_string())
    }

    fn set(&self, identity: &RawScanCacheIdentity, payload: &str) -> Result<(), String> {
        self.conn
            .execute(
                "INSERT INTO raw_scan_cache (
                    cache_key, source_id, source_type, root_path, payload, created_at, updated_at
                )
                VALUES (?1, ?2, ?3, ?4, ?5, unixepoch(), unixepoch())
                ON CONFLICT(cache_key) DO UPDATE SET
                    source_id = excluded.source_id,
                    source_type = excluded.source_type,
                    root_path = excluded.root_path,
                    payload = excluded.payload,
                    updated_at = unixepoch()",
                params![
                    cache_key(identity),
                    identity.source_id,
                    identity.source_type,
                    identity.root_path,
                    payload,
                ],
            )
            .map_err(|_| "Failed to save raw scan cache.".to_string())?;
        Ok(())
    }

    fn delete(&self, identity: &RawScanCacheIdentity) -> Result<(), String> {
        self.conn
            .execute(
                "DELETE FROM raw_scan_cache WHERE cache_key = ?1",
                params![cache_key(identity)],
            )
            .map_err(|_| "Failed to delete raw scan cache.".to_string())?;
        Ok(())
    }
}

fn raw_scan_cache_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let mut dir = app
        .path()
        .app_data_dir()
        .map_err(|_| "Failed to resolve app data directory.".to_string())?;
    dir.push("scraper");
    Ok(dir)
}

fn cache_key(identity: &RawScanCacheIdentity) -> String {
    let mut hasher = Sha256::new();
    hasher.update(identity.source_type.as_bytes());
    hasher.update([0]);
    hasher.update(identity.source_id.as_bytes());
    hasher.update([0]);
    hasher.update(identity.root_path.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn validate_source_id(value: &str) -> Result<(), String> {
    if value.is_empty() || value.len() > MAX_SOURCE_ID_LENGTH {
        return Err("Invalid raw scan cache source id.".to_string());
    }
    Ok(())
}

fn validate_source_type(value: &str) -> Result<(), String> {
    if value.is_empty() || value.len() > MAX_SOURCE_TYPE_LENGTH {
        return Err("Invalid raw scan cache source type.".to_string());
    }

    match value {
        "alist" | "clouddrive2" | "local" | "115" | "123" | "quark" => Ok(()),
        _ => Err("Unsupported raw scan cache source type.".to_string()),
    }
}

fn validate_root_path(value: &str) -> Result<(), String> {
    if !value.starts_with('/') || value.len() > MAX_ROOT_PATH_LENGTH {
        return Err("Invalid raw scan cache root path.".to_string());
    }
    Ok(())
}

fn validate_payload(value: &str) -> Result<(), String> {
    if value.is_empty() || value.len() > MAX_PAYLOAD_BYTES {
        return Err("Invalid raw scan cache payload.".to_string());
    }
    Ok(())
}

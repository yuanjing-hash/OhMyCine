use aes_gcm::aead::{Aead, KeyInit, OsRng};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::{engine::general_purpose::STANDARD as BASE64, Engine as _};
use rand::RngCore;
use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

const MASTER_KEY_FILE: &str = "master.key";
const DATABASE_FILE: &str = "credentials.sqlite";
const KEY_LEN: usize = 32;
const NONCE_LEN: usize = 12;

#[tauri::command]
pub fn credential_set(app: AppHandle, ref_name: String, token: String) -> Result<(), String> {
    validate_ref(&ref_name)?;
    if token.is_empty() {
        return Err("Credential token is empty.".into());
    }

    let storage = CredentialStorage::open(&app)?;
    storage.set(&ref_name, token.as_bytes())
}

#[tauri::command]
pub fn credential_get(app: AppHandle, ref_name: String) -> Result<Option<String>, String> {
    validate_ref(&ref_name)?;

    let storage = CredentialStorage::open(&app)?;
    storage
        .get(&ref_name)?
        .map(String::from_utf8)
        .transpose()
        .map_err(|_| "Stored credential is invalid.".to_string())
}

#[tauri::command]
pub fn credential_delete(app: AppHandle, ref_name: String) -> Result<(), String> {
    validate_ref(&ref_name)?;

    let storage = CredentialStorage::open(&app)?;
    storage.delete(&ref_name)?;
    Ok(())
}

struct CredentialStorage {
    key: [u8; KEY_LEN],
    conn: Connection,
}

impl CredentialStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let dir = credential_dir(app)?;
        fs::create_dir_all(&dir).map_err(|_| "Failed to prepare credential storage.".to_string())?;

        let key = load_or_create_master_key(&dir)?;
        let db_path = dir.join(DATABASE_FILE);
        let conn = Connection::open(db_path).map_err(|_| "Failed to open credential database.".to_string())?;
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS credentials (
                ref_hash TEXT PRIMARY KEY NOT NULL,
                nonce TEXT NOT NULL,
                ciphertext TEXT NOT NULL,
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );",
        )
        .map_err(|_| "Failed to initialize credential database.".to_string())?;

        Ok(Self { key, conn })
    }

    fn set(&self, ref_name: &str, plaintext: &[u8]) -> Result<(), String> {
        let cipher = Aes256Gcm::new_from_slice(&self.key)
            .map_err(|_| "Failed to initialize credential encryption.".to_string())?;
        let mut nonce_bytes = [0_u8; NONCE_LEN];
        OsRng.fill_bytes(&mut nonce_bytes);
        let ciphertext = cipher
            .encrypt(Nonce::from_slice(&nonce_bytes), plaintext)
            .map_err(|_| "Failed to encrypt credential.".to_string())?;

        self.conn
            .execute(
                "INSERT INTO credentials (ref_hash, nonce, ciphertext, updated_at)
                 VALUES (?1, ?2, ?3, unixepoch())
                 ON CONFLICT(ref_hash) DO UPDATE SET
                    nonce = excluded.nonce,
                    ciphertext = excluded.ciphertext,
                    updated_at = unixepoch()",
                params![hash_ref(ref_name), BASE64.encode(nonce_bytes), BASE64.encode(ciphertext)],
            )
            .map_err(|_| "Failed to save credential.".to_string())?;

        Ok(())
    }

    fn get(&self, ref_name: &str) -> Result<Option<Vec<u8>>, String> {
        let row = self
            .conn
            .query_row(
                "SELECT nonce, ciphertext FROM credentials WHERE ref_hash = ?1",
                params![hash_ref(ref_name)],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .optional()
            .map_err(|_| "Failed to read credential.".to_string())?;

        let Some((nonce, ciphertext)) = row else {
            return Ok(None);
        };

        let nonce = BASE64
            .decode(nonce)
            .map_err(|_| "Stored credential is invalid.".to_string())?;
        if nonce.len() != NONCE_LEN {
            return Err("Stored credential is invalid.".to_string());
        }
        let ciphertext = BASE64
            .decode(ciphertext)
            .map_err(|_| "Stored credential is invalid.".to_string())?;
        let cipher = Aes256Gcm::new_from_slice(&self.key)
            .map_err(|_| "Failed to initialize credential encryption.".to_string())?;
        cipher
            .decrypt(Nonce::from_slice(&nonce), ciphertext.as_ref())
            .map(Some)
            .map_err(|_| "Failed to decrypt credential.".to_string())
    }

    fn delete(&self, ref_name: &str) -> Result<(), String> {
        self.conn
            .execute(
                "DELETE FROM credentials WHERE ref_hash = ?1",
                params![hash_ref(ref_name)],
            )
            .map_err(|_| "Failed to delete credential.".to_string())?;
        Ok(())
    }
}

fn credential_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let mut dir = app
        .path()
        .app_data_dir()
        .map_err(|_| "Failed to resolve app data directory.".to_string())?;
    dir.push("credentials");
    Ok(dir)
}

fn load_or_create_master_key(dir: &PathBuf) -> Result<[u8; KEY_LEN], String> {
    let path = dir.join(MASTER_KEY_FILE);
    if path.exists() {
        let encoded = fs::read_to_string(path).map_err(|_| "Failed to read credential key.".to_string())?;
        let decoded = BASE64
            .decode(encoded.trim())
            .map_err(|_| "Stored credential key is invalid.".to_string())?;
        return decoded
            .try_into()
            .map_err(|_| "Stored credential key is invalid.".to_string());
    }

    let mut key = [0_u8; KEY_LEN];
    OsRng.fill_bytes(&mut key);
    write_master_key(&path, &BASE64.encode(key))?;
    Ok(key)
}

fn write_master_key(path: &PathBuf, encoded_key: &str) -> Result<(), String> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        let mut options = fs::OpenOptions::new();
        options.write(true).create_new(true).mode(0o600);
        std::io::Write::write_all(
            &mut options
                .open(path)
                .map_err(|_| "Failed to create credential key.".to_string())?,
            encoded_key.as_bytes(),
        )
        .map_err(|_| "Failed to create credential key.".to_string())?;
    }

    #[cfg(not(unix))]
    {
        fs::write(path, encoded_key).map_err(|_| "Failed to create credential key.".to_string())?;
    }

    Ok(())
}

fn hash_ref(ref_name: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(ref_name.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn validate_ref(ref_name: &str) -> Result<(), String> {
    if ref_name.is_empty() || ref_name.len() > 256 {
        return Err("Invalid credential reference.".into());
    }

    if ref_name
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'-' | b'_'))
    {
        Ok(())
    } else {
        Err("Invalid credential reference.".into())
    }
}

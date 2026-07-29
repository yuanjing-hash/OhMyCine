use aes_gcm::aead::{Aead, KeyInit, OsRng};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::{engine::general_purpose::STANDARD as BASE64, Engine as _};
use rand::RngCore;
use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::PathBuf;
use tauri::AppHandle;

use crate::storage::{self, StorageMode};

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
        let layout = storage::initialize(app)?;
        let key = load_or_create_master_key(&layout.data_dir, layout.mode)?;
        let db_path = layout.data_dir.join(DATABASE_FILE);
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open credential database.".to_string())?;
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
                params![
                    hash_ref(ref_name),
                    BASE64.encode(nonce_bytes),
                    BASE64.encode(ciphertext)
                ],
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

fn load_or_create_master_key(
    dir: &PathBuf,
    storage_mode: StorageMode,
) -> Result<[u8; KEY_LEN], String> {
    let path = dir.join(MASTER_KEY_FILE);
    if path.exists() {
        let encoded =
            fs::read_to_string(path).map_err(|_| "Failed to read credential key.".to_string())?;
        let stored = encoded.trim();
        let key = decode_stored_master_key(stored)?;
        if should_upgrade_master_key(stored, storage_mode) {
            write_master_key(
                &dir.join(MASTER_KEY_FILE),
                &encode_master_key(&key, storage_mode)?,
            )?;
        }
        return Ok(key);
    }

    let mut key = [0_u8; KEY_LEN];
    OsRng.fill_bytes(&mut key);
    write_master_key(&path, &encode_master_key(&key, storage_mode)?)?;
    Ok(key)
}

fn should_upgrade_master_key(stored: &str, storage_mode: StorageMode) -> bool {
    match storage_mode {
        StorageMode::Portable => !stored.starts_with("portable:"),
        StorageMode::Standard if cfg!(windows) => !stored.starts_with("dpapi:"),
        StorageMode::Standard => !stored.starts_with("portable:"),
    }
}

fn decode_stored_master_key(value: &str) -> Result<[u8; KEY_LEN], String> {
    let decoded = if let Some(payload) = value.strip_prefix("dpapi:") {
        let protected = BASE64
            .decode(payload)
            .map_err(|_| "Stored credential key is invalid.".to_string())?;
        unprotect_with_dpapi(&protected)?
    } else {
        let payload = value.strip_prefix("portable:").unwrap_or(value);
        BASE64
            .decode(payload)
            .map_err(|_| "Stored credential key is invalid.".to_string())?
    };

    decoded
        .try_into()
        .map_err(|_| "Stored credential key is invalid.".to_string())
}

fn encode_master_key(key: &[u8; KEY_LEN], storage_mode: StorageMode) -> Result<String, String> {
    if storage_mode == StorageMode::Portable || !cfg!(windows) {
        return Ok(format!("portable:{}", BASE64.encode(key)));
    }

    Ok(format!("dpapi:{}", BASE64.encode(protect_with_dpapi(key)?)))
}

fn write_master_key(path: &PathBuf, encoded_key: &str) -> Result<(), String> {
    if path.exists() {
        return fs::write(path, encoded_key)
            .map_err(|_| "Failed to update credential key protection.".to_string());
    }

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

#[cfg(windows)]
fn protect_with_dpapi(plaintext: &[u8]) -> Result<Vec<u8>, String> {
    use windows_sys::Win32::Foundation::LocalFree;
    use windows_sys::Win32::Security::Cryptography::{
        CryptProtectData, CRYPTPROTECT_UI_FORBIDDEN, CRYPT_INTEGER_BLOB,
    };

    let input = CRYPT_INTEGER_BLOB {
        cbData: plaintext.len() as u32,
        pbData: plaintext.as_ptr() as *mut u8,
    };
    let mut output = CRYPT_INTEGER_BLOB {
        cbData: 0,
        pbData: std::ptr::null_mut(),
    };
    let success = unsafe {
        CryptProtectData(
            &input,
            std::ptr::null(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            CRYPTPROTECT_UI_FORBIDDEN,
            &mut output,
        )
    };
    if success == 0 || output.pbData.is_null() {
        return Err("Failed to protect credential key with Windows DPAPI.".to_string());
    }

    let protected = unsafe {
        let bytes = std::slice::from_raw_parts(output.pbData, output.cbData as usize).to_vec();
        LocalFree(output.pbData as _);
        bytes
    };
    Ok(protected)
}

#[cfg(not(windows))]
fn protect_with_dpapi(_plaintext: &[u8]) -> Result<Vec<u8>, String> {
    Err("Windows DPAPI is unavailable on this platform.".to_string())
}

#[cfg(windows)]
fn unprotect_with_dpapi(protected: &[u8]) -> Result<Vec<u8>, String> {
    use windows_sys::Win32::Foundation::LocalFree;
    use windows_sys::Win32::Security::Cryptography::{
        CryptUnprotectData, CRYPTPROTECT_UI_FORBIDDEN, CRYPT_INTEGER_BLOB,
    };

    let input = CRYPT_INTEGER_BLOB {
        cbData: protected.len() as u32,
        pbData: protected.as_ptr() as *mut u8,
    };
    let mut output = CRYPT_INTEGER_BLOB {
        cbData: 0,
        pbData: std::ptr::null_mut(),
    };
    let success = unsafe {
        CryptUnprotectData(
            &input,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            CRYPTPROTECT_UI_FORBIDDEN,
            &mut output,
        )
    };
    if success == 0 || output.pbData.is_null() {
        return Err("Failed to unlock credential key with Windows DPAPI.".to_string());
    }

    let plaintext = unsafe {
        let bytes = std::slice::from_raw_parts(output.pbData, output.cbData as usize).to_vec();
        LocalFree(output.pbData as _);
        bytes
    };
    Ok(plaintext)
}

#[cfg(not(windows))]
fn unprotect_with_dpapi(_protected: &[u8]) -> Result<Vec<u8>, String> {
    Err("This credential key is protected by Windows DPAPI.".to_string())
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

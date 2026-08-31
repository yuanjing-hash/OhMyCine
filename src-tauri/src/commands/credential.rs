use aes_gcm::aead::{Aead, KeyInit, OsRng};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::{engine::general_purpose::STANDARD as BASE64, Engine as _};
use rand::RngCore;
use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::fs;
use std::path::Path;
use tauri::AppHandle;

use crate::storage::{self, StorageMode};

const MASTER_KEY_FILE: &str = "master.key";
const DATABASE_FILE: &str = "credentials.sqlite";
const KEY_LEN: usize = 32;
const NONCE_LEN: usize = 12;
#[cfg(target_os = "android")]
const ANDROID_KEYSTORE_MARKER: &str = "android-keystore:v1";
#[cfg(any(target_os = "macos", target_os = "ios"))]
const APPLE_KEYCHAIN_MARKER: &str = "apple-keychain:v1";
#[cfg(target_os = "linux")]
const LINUX_SECRET_SERVICE_MARKER: &str = "linux-secret-service:v1";
const LOCAL_FILE_KEY_PREFIX: &str = "local-file:";
const PORTABLE_FILE_KEY_PREFIX: &str = "portable:";
#[cfg(any(target_os = "macos", target_os = "ios", target_os = "linux"))]
const KEYRING_SERVICE: &str = "com.ohmycine.player";
#[cfg(any(target_os = "macos", target_os = "ios", target_os = "linux"))]
const KEYRING_USER: &str = "credential-master-key-v1";

#[tauri::command]
pub async fn credential_set(app: AppHandle, ref_name: String, token: String) -> Result<(), String> {
    validate_ref(&ref_name)?;
    if token.is_empty() {
        return Err("Credential token is empty.".into());
    }

    let storage = CredentialStorage::open(&app).await?;
    storage.set(&ref_name, token.as_bytes())
}

#[tauri::command]
pub async fn credential_get(app: AppHandle, ref_name: String) -> Result<Option<String>, String> {
    validate_ref(&ref_name)?;

    let storage = CredentialStorage::open(&app).await?;
    storage
        .get(&ref_name)?
        .map(String::from_utf8)
        .transpose()
        .map_err(|_| "Stored credential is invalid.".to_string())
}

#[tauri::command]
pub async fn credential_delete(app: AppHandle, ref_name: String) -> Result<(), String> {
    validate_ref(&ref_name)?;

    let storage = CredentialStorage::open(&app).await?;
    storage.delete(&ref_name)?;
    Ok(())
}

pub(crate) async fn read_credential_value(
    app: &AppHandle,
    ref_name: &str,
) -> Result<Option<String>, String> {
    validate_ref(ref_name)?;
    CredentialStorage::open(app)
        .await?
        .get(ref_name)?
        .map(String::from_utf8)
        .transpose()
        .map_err(|_| "Stored credential is invalid.".to_string())
}

struct CredentialStorage {
    key: [u8; KEY_LEN],
    conn: Connection,
}

impl CredentialStorage {
    async fn open(app: &AppHandle) -> Result<Self, String> {
        let layout = storage::initialize(app)?;
        let key = load_or_create_master_key(app, &layout.data_dir, layout.mode).await?;
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

async fn load_or_create_master_key(
    _app: &AppHandle,
    dir: &Path,
    storage_mode: StorageMode,
) -> Result<[u8; KEY_LEN], String> {
    let path = dir.join(MASTER_KEY_FILE);
    let database_exists = dir.join(DATABASE_FILE).is_file();

    if storage_mode == StorageMode::Portable {
        return load_or_create_portable_key(&path, database_exists);
    }

    #[cfg(target_os = "android")]
    return load_or_create_android_key(_app, &path, database_exists).await;

    #[cfg(windows)]
    return load_or_create_windows_key(&path, database_exists);

    #[cfg(any(target_os = "macos", target_os = "ios"))]
    return load_or_create_apple_key(&path, database_exists);

    #[cfg(target_os = "linux")]
    return load_or_create_linux_key(&path, database_exists);

    #[allow(unreachable_code)]
    load_or_create_local_file_key(&path, database_exists)
}

fn load_or_create_portable_key(
    path: &Path,
    database_exists: bool,
) -> Result<[u8; KEY_LEN], String> {
    if path.is_file() {
        let key = decode_file_master_key(&read_master_key_marker(path)?)?;
        write_master_key(
            path,
            &format!("{PORTABLE_FILE_KEY_PREFIX}{}", BASE64.encode(key)),
        )?;
        return Ok(key);
    }

    create_file_master_key(path, PORTABLE_FILE_KEY_PREFIX, database_exists)
}

#[cfg(windows)]
fn load_or_create_windows_key(path: &Path, database_exists: bool) -> Result<[u8; KEY_LEN], String> {
    if path.is_file() {
        let stored = read_master_key_marker(path)?;
        let key = decode_file_master_key(&stored)?;
        if !stored.starts_with("dpapi:") {
            write_master_key(
                path,
                &format!("dpapi:{}", BASE64.encode(protect_with_dpapi(&key)?)),
            )?;
        }
        return Ok(key);
    }

    ensure_new_key_allowed(database_exists)?;
    let key = generate_master_key();
    write_master_key(
        path,
        &format!("dpapi:{}", BASE64.encode(protect_with_dpapi(&key)?)),
    )?;
    Ok(key)
}

#[cfg(target_os = "android")]
async fn load_or_create_android_key(
    app: &AppHandle,
    path: &Path,
    database_exists: bool,
) -> Result<[u8; KEY_LEN], String> {
    use crate::commands::credential_android;

    if path.is_file() {
        let stored = read_master_key_marker(path)?;
        if stored == ANDROID_KEYSTORE_MARKER {
            let encoded = credential_android::get_master_key(app)
                .await?
                .ok_or_else(|| "Android Keystore credential key is missing.".to_string())?;
            return decode_system_master_key(&encoded);
        }

        let key = decode_file_master_key(&stored)?;
        credential_android::store_master_key(app, &BASE64.encode(key)).await?;
        write_master_key(path, ANDROID_KEYSTORE_MARKER)?;
        return Ok(key);
    }

    if let Some(encoded) = credential_android::get_master_key(app).await? {
        let key = decode_system_master_key(&encoded)?;
        write_master_key(path, ANDROID_KEYSTORE_MARKER)?;
        return Ok(key);
    }

    ensure_new_key_allowed(database_exists)?;
    let encoded = credential_android::create_master_key(app).await?;
    let key = decode_system_master_key(&encoded)?;
    write_master_key(path, ANDROID_KEYSTORE_MARKER)?;
    Ok(key)
}

#[cfg(any(target_os = "macos", target_os = "ios"))]
fn load_or_create_apple_key(path: &Path, database_exists: bool) -> Result<[u8; KEY_LEN], String> {
    let entry = system_keyring_entry()?;
    if path.is_file() {
        let stored = read_master_key_marker(path)?;
        if stored == APPLE_KEYCHAIN_MARKER {
            return read_required_keyring_key(&entry, "Apple Keychain credential key is missing.");
        }

        let key = decode_file_master_key(&stored)?;
        store_keyring_key(&entry, &key)?;
        write_master_key(path, APPLE_KEYCHAIN_MARKER)?;
        return Ok(key);
    }

    match entry.get_password() {
        Ok(encoded) => {
            let key = decode_system_master_key(&encoded)?;
            write_master_key(path, APPLE_KEYCHAIN_MARKER)?;
            Ok(key)
        }
        Err(keyring::Error::NoEntry) => {
            ensure_new_key_allowed(database_exists)?;
            let key = generate_master_key();
            store_keyring_key(&entry, &key)?;
            write_master_key(path, APPLE_KEYCHAIN_MARKER)?;
            Ok(key)
        }
        Err(_) => Err("Apple Keychain credential storage is unavailable.".to_string()),
    }
}

#[cfg(target_os = "linux")]
fn load_or_create_linux_key(path: &Path, database_exists: bool) -> Result<[u8; KEY_LEN], String> {
    let entry = system_keyring_entry()?;
    if path.is_file() {
        let stored = read_master_key_marker(path)?;
        if stored == LINUX_SECRET_SERVICE_MARKER {
            return read_required_keyring_key(
                &entry,
                "Linux Secret Service credential key is missing or locked.",
            );
        }

        let key = decode_file_master_key(&stored)?;
        if store_keyring_key(&entry, &key).is_ok() {
            write_master_key(path, LINUX_SECRET_SERVICE_MARKER)?;
        } else if !stored.starts_with(LOCAL_FILE_KEY_PREFIX) {
            write_master_key(
                path,
                &format!("{LOCAL_FILE_KEY_PREFIX}{}", BASE64.encode(key)),
            )?;
        }
        return Ok(key);
    }

    match entry.get_password() {
        Ok(encoded) => {
            let key = decode_system_master_key(&encoded)?;
            write_master_key(path, LINUX_SECRET_SERVICE_MARKER)?;
            Ok(key)
        }
        Err(keyring::Error::NoEntry) if database_exists => {
            Err("Linux Secret Service credential key is missing or locked.".to_string())
        }
        Err(_) => {
            ensure_new_key_allowed(database_exists)?;
            let key = generate_master_key();
            if store_keyring_key(&entry, &key).is_ok() {
                write_master_key(path, LINUX_SECRET_SERVICE_MARKER)?;
            } else {
                write_master_key(
                    path,
                    &format!("{LOCAL_FILE_KEY_PREFIX}{}", BASE64.encode(key)),
                )?;
            }
            Ok(key)
        }
    }
}

#[cfg(any(target_os = "macos", target_os = "ios", target_os = "linux"))]
fn system_keyring_entry() -> Result<keyring::Entry, String> {
    keyring::Entry::new(KEYRING_SERVICE, KEYRING_USER)
        .map_err(|_| "System credential storage is unavailable.".to_string())
}

#[cfg(any(target_os = "macos", target_os = "ios", target_os = "linux"))]
fn store_keyring_key(entry: &keyring::Entry, key: &[u8; KEY_LEN]) -> Result<(), String> {
    entry
        .set_password(&BASE64.encode(key))
        .map_err(|_| "System credential storage is unavailable.".to_string())
}

#[cfg(any(target_os = "macos", target_os = "ios", target_os = "linux"))]
fn read_required_keyring_key(
    entry: &keyring::Entry,
    missing_message: &str,
) -> Result<[u8; KEY_LEN], String> {
    let encoded = entry.get_password().map_err(|error| match error {
        keyring::Error::NoEntry => missing_message.to_string(),
        _ => "System credential storage is unavailable.".to_string(),
    })?;
    decode_system_master_key(&encoded)
}

fn load_or_create_local_file_key(
    path: &Path,
    database_exists: bool,
) -> Result<[u8; KEY_LEN], String> {
    if path.is_file() {
        let key = decode_file_master_key(&read_master_key_marker(path)?)?;
        write_master_key(
            path,
            &format!("{LOCAL_FILE_KEY_PREFIX}{}", BASE64.encode(key)),
        )?;
        return Ok(key);
    }

    create_file_master_key(path, LOCAL_FILE_KEY_PREFIX, database_exists)
}

fn create_file_master_key(
    path: &Path,
    prefix: &str,
    database_exists: bool,
) -> Result<[u8; KEY_LEN], String> {
    ensure_new_key_allowed(database_exists)?;
    let key = generate_master_key();
    write_master_key(path, &format!("{prefix}{}", BASE64.encode(key)))?;
    Ok(key)
}

fn ensure_new_key_allowed(database_exists: bool) -> Result<(), String> {
    if database_exists {
        Err("Credential master key is missing; existing credentials were preserved.".to_string())
    } else {
        Ok(())
    }
}

fn read_master_key_marker(path: &Path) -> Result<String, String> {
    fs::read_to_string(path)
        .map(|value| value.trim().to_string())
        .map_err(|_| "Failed to read credential key.".to_string())
}

fn generate_master_key() -> [u8; KEY_LEN] {
    let mut key = [0_u8; KEY_LEN];
    OsRng.fill_bytes(&mut key);
    key
}

#[cfg(not(windows))]
fn decode_system_master_key(value: &str) -> Result<[u8; KEY_LEN], String> {
    BASE64
        .decode(value.trim())
        .map_err(|_| "Stored credential key is invalid.".to_string())?
        .try_into()
        .map_err(|_| "Stored credential key is invalid.".to_string())
}

fn decode_file_master_key(value: &str) -> Result<[u8; KEY_LEN], String> {
    let decoded = if let Some(payload) = value.strip_prefix("dpapi:") {
        let protected = BASE64
            .decode(payload)
            .map_err(|_| "Stored credential key is invalid.".to_string())?;
        unprotect_with_dpapi(&protected)?
    } else {
        let payload = value
            .strip_prefix(PORTABLE_FILE_KEY_PREFIX)
            .or_else(|| value.strip_prefix(LOCAL_FILE_KEY_PREFIX))
            .unwrap_or(value);
        BASE64
            .decode(payload)
            .map_err(|_| "Stored credential key is invalid.".to_string())?
    };

    decoded
        .try_into()
        .map_err(|_| "Stored credential key is invalid.".to_string())
}

fn write_master_key(path: &Path, encoded_key: &str) -> Result<(), String> {
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

#[cfg(test)]
mod tests {
    use super::{
        decode_file_master_key, load_or_create_portable_key, BASE64, KEY_LEN,
        LOCAL_FILE_KEY_PREFIX, PORTABLE_FILE_KEY_PREFIX,
    };
    use base64::Engine as _;
    use std::fs;

    #[test]
    fn portable_master_key_is_stored_as_a_restricted_file_key() {
        let root = std::env::temp_dir().join(format!(
            "ohmycine-portable-master-key-{}",
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create test root");
        let path = root.join("master.key");

        let key = load_or_create_portable_key(&path, false).expect("create portable key");
        let stored = fs::read_to_string(&path).expect("read portable key");

        assert!(stored.starts_with(PORTABLE_FILE_KEY_PREFIX));
        assert_eq!(
            decode_file_master_key(&stored).expect("decode portable key"),
            key
        );
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn legacy_plain_base64_key_migrates_without_rotation() {
        let root =
            std::env::temp_dir().join(format!("ohmycine-legacy-master-key-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create test root");
        let path = root.join("master.key");
        let key = [7_u8; KEY_LEN];
        fs::write(&path, BASE64.encode(key)).expect("write legacy key");

        let migrated = load_or_create_portable_key(&path, true).expect("migrate legacy key");
        let stored = fs::read_to_string(&path).expect("read migrated key");

        assert_eq!(migrated, key);
        assert!(stored.starts_with(PORTABLE_FILE_KEY_PREFIX));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn missing_master_key_never_rotates_an_existing_database() {
        let root = std::env::temp_dir().join(format!(
            "ohmycine-missing-master-key-{}",
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create test root");

        let error = load_or_create_portable_key(&root.join("master.key"), true)
            .expect_err("existing database must block key rotation");
        assert!(error.contains("preserved"));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn local_file_fallback_marker_remains_decodable() {
        let key = [11_u8; KEY_LEN];
        let stored = format!("{LOCAL_FILE_KEY_PREFIX}{}", BASE64.encode(key));
        assert_eq!(
            decode_file_master_key(&stored).expect("decode local key"),
            key
        );
    }
}

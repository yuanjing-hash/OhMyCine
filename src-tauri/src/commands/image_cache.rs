use base64::{engine::general_purpose::STANDARD as BASE64_STANDARD, Engine};
use reqwest::{
    header::{ACCEPT, AUTHORIZATION, CONTENT_TYPE},
    redirect::Policy,
    Url,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::{Path, PathBuf},
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tauri::AppHandle;

use crate::storage;

const MAX_IMAGE_BYTES: usize = 8 * 1024 * 1024;
const MAX_CACHE_KEY_BYTES: usize = 256;
const DEFAULT_CACHE_LIMIT_BYTES: u64 = 500 * 1024 * 1024;
const MIN_CACHE_LIMIT_BYTES: u64 = 100 * 1024 * 1024;
const MAX_CACHE_LIMIT_BYTES: u64 = 4 * 1024 * 1024 * 1024;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CacheImageRequest {
    cache_key: String,
    url: String,
    server_base_url: Option<String>,
    server_access_token: Option<String>,
    #[serde(default = "default_cache_limit_bytes")]
    max_bytes: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CachedImageReadRequest {
    cache_key: String,
    url: String,
    server_base_url: Option<String>,
    server_access_token: Option<String>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct CachedImageMeta {
    source_hash: String,
    mime_type: String,
    #[serde(default)]
    byte_size: u64,
    #[serde(default)]
    last_accessed: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ImageCacheStats {
    total_bytes: u64,
    file_count: usize,
}

struct ImageCacheEntry {
    image_path: PathBuf,
    meta_path: PathBuf,
    byte_size: u64,
    last_accessed: u64,
}

#[tauri::command]
pub fn player_get_cached_image(
    app: AppHandle,
    request: CachedImageReadRequest,
) -> Result<Option<String>, String> {
    validate_cache_key(&request.cache_key)?;
    let url = parse_image_url(&request.url)?;
    let source_hash = validated_source_hash(
        &url,
        request.server_base_url.as_deref(),
        request.server_access_token.as_deref(),
    )?;
    Ok(read_cached_image_with_meta(&app, &request.cache_key)?
        .and_then(|(meta, data_url)| (meta.source_hash == source_hash).then_some(data_url)))
}

#[tauri::command]
pub async fn player_cache_image(
    app: AppHandle,
    request: CacheImageRequest,
) -> Result<String, String> {
    validate_cache_key(&request.cache_key)?;
    let max_bytes = normalize_cache_limit(request.max_bytes);
    let url = parse_image_url(&request.url)?;
    let protected_artwork = is_protected_server_artwork_url(&url);
    let source_hash = validated_source_hash(
        &url,
        request.server_base_url.as_deref(),
        request.server_access_token.as_deref(),
    )?;
    let server_token = request.server_access_token.as_deref();

    if let Some((meta, data_url)) = read_cached_image_with_meta(&app, &request.cache_key)? {
        if meta.source_hash == source_hash {
            return Ok(data_url);
        }
    }

    let client = if protected_artwork {
        protected_image_client()?
    } else {
        image_client()?
    };
    let mut builder = client.get(url).header(
        ACCEPT,
        "image/avif,image/webp,image/png,image/jpeg,image/gif",
    );
    if let Some(token) = server_token {
        builder = builder.header(AUTHORIZATION, format!("Bearer {token}"));
    }
    let mut response = builder
        .send()
        .await
        .map_err(|_| "图片暂时无法缓存。".to_string())?;
    if !response.status().is_success() {
        return Err("图片暂时无法缓存。".to_string());
    }
    let declared_mime = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .map(str::trim)
        .map(str::to_ascii_lowercase);
    if response.content_length().unwrap_or(0) > MAX_IMAGE_BYTES as u64 {
        return Err("图片文件过大，未写入缓存。".to_string());
    }

    let mut bytes = Vec::with_capacity(response.content_length().unwrap_or(0) as usize);
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "图片暂时无法缓存。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_IMAGE_BYTES {
            return Err("图片文件过大，未写入缓存。".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }

    let mime_type =
        detect_image_mime(&bytes).ok_or_else(|| "下载内容不是受支持的图片。".to_string())?;
    if protected_artwork && declared_mime.as_deref() != Some(mime_type) {
        return Err("Server artwork format is invalid.".to_string());
    }
    write_cached_image(
        &app,
        &request.cache_key,
        &bytes,
        &CachedImageMeta {
            source_hash,
            mime_type: mime_type.to_string(),
            byte_size: bytes.len() as u64,
            last_accessed: unix_timestamp(),
        },
    )?;
    trim_image_cache(&cache_directory(&app)?, max_bytes)?;
    Ok(to_data_url(mime_type, &bytes))
}

#[tauri::command]
pub fn player_image_cache_stats(app: AppHandle) -> Result<ImageCacheStats, String> {
    image_cache_stats(&cache_directory(&app)?)
}

#[tauri::command]
pub fn player_trim_image_cache(app: AppHandle, max_bytes: u64) -> Result<ImageCacheStats, String> {
    let directory = cache_directory(&app)?;
    trim_image_cache(&directory, normalize_cache_limit(max_bytes))?;
    image_cache_stats(&directory)
}

fn image_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(8))
        .timeout(Duration::from_secs(20))
        .user_agent("OhMyCine-Player/0.1")
        .redirect(Policy::custom(|attempt| {
            if attempt.previous().len() >= 3 {
                return attempt.stop();
            }
            let Some(previous) = attempt.previous().last() else {
                return attempt.follow();
            };
            if same_origin(previous, attempt.url()) {
                attempt.follow()
            } else {
                attempt.stop()
            }
        }))
        .build()
        .map_err(|_| "无法初始化图片缓存。".to_string())
}

fn protected_image_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(8))
        .timeout(Duration::from_secs(20))
        .user_agent("OhMyCine-Player/0.1")
        .redirect(Policy::none())
        .build()
        .map_err(|_| "无法初始化图片缓存。".to_string())
}

fn is_protected_server_artwork_url(url: &Url) -> bool {
    if url.query().is_some() || url.fragment().is_some() {
        return false;
    }
    let parts: Vec<_> = url.path().split('/').collect();
    match parts.as_slice() {
        ["", "api", "v1", "player", "artwork", token] => valid_artwork_token(token),
        ["", "api", "v1", "player", "discovery", "images", provider, token] => {
            matches!(*provider, "tmdb" | "douban") && valid_artwork_token(token)
        }
        _ => false,
    }
}

fn valid_artwork_token(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 4096
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
}

fn validate_server_artwork_credentials(
    url: &Url,
    base_url: &str,
    token: &str,
) -> Result<(), String> {
    let base = Url::parse(base_url).map_err(|_| "Server artwork origin is invalid.".to_string())?;
    if !matches!(base.scheme(), "http" | "https")
        || base.host_str().is_none()
        || !base.username().is_empty()
        || base.password().is_some()
        || base.query().is_some()
        || base.fragment().is_some()
        || base.path() != "/"
        || !same_origin(&base, url)
        || !url.username().is_empty()
        || url.password().is_some()
        || !is_protected_server_artwork_url(url)
        || !token.starts_with("omc_player_")
        || token.len() > 256
        || token.chars().any(char::is_control)
    {
        return Err("Server artwork credentials are invalid.".to_string());
    }
    Ok(())
}

fn validated_source_hash(
    url: &Url,
    server_base_url: Option<&str>,
    server_access_token: Option<&str>,
) -> Result<String, String> {
    if is_protected_server_artwork_url(url) {
        let base_url = server_base_url.ok_or("Server artwork credentials are required.")?;
        let token = server_access_token.ok_or("Server artwork credentials are required.")?;
        validate_server_artwork_credentials(url, base_url, token)?;
        Ok(hash_text(&format!(
            "server-auth-v1:{}:{}",
            url.as_str(),
            hash_text(token)
        )))
    } else {
        if server_base_url.is_some() || server_access_token.is_some() {
            return Err("Server artwork credentials are not allowed for this URL.".to_string());
        }
        Ok(hash_text(url.as_str()))
    }
}

fn same_origin(left: &Url, right: &Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
}

fn parse_image_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value.trim()).map_err(|_| "图片地址无效。".to_string())?;
    if !matches!(url.scheme(), "http" | "https") {
        return Err("图片地址无效。".to_string());
    }
    Ok(url)
}

fn validate_cache_key(value: &str) -> Result<(), String> {
    let key = value.trim();
    if key.is_empty() || key.len() > MAX_CACHE_KEY_BYTES {
        return Err("图片缓存标识无效。".to_string());
    }
    Ok(())
}

fn default_cache_limit_bytes() -> u64 {
    DEFAULT_CACHE_LIMIT_BYTES
}

fn normalize_cache_limit(value: u64) -> u64 {
    value.clamp(MIN_CACHE_LIMIT_BYTES, MAX_CACHE_LIMIT_BYTES)
}

fn cache_directory(app: &AppHandle) -> Result<PathBuf, String> {
    let layout = storage::initialize(app)?;
    let directory = layout.cache_dir.join("images");
    fs::create_dir_all(&directory).map_err(|_| "无法创建图片缓存目录。".to_string())?;
    Ok(directory)
}

fn cache_paths(app: &AppHandle, cache_key: &str) -> Result<(PathBuf, PathBuf), String> {
    let directory = cache_directory(app)?;
    let file_name = storage::scoped_cache_key("artwork", cache_key.trim());
    Ok((
        directory.join(format!("{file_name}.bin")),
        directory.join(format!("{file_name}.json")),
    ))
}

fn read_cached_image_with_meta(
    app: &AppHandle,
    cache_key: &str,
) -> Result<Option<(CachedImageMeta, String)>, String> {
    let (image_path, meta_path) = cache_paths(app, cache_key)?;
    if !image_path.is_file() || !meta_path.is_file() {
        return Ok(None);
    }
    let bytes = fs::read(&image_path).map_err(|_| "无法读取图片缓存。".to_string())?;
    if bytes.is_empty() || bytes.len() > MAX_IMAGE_BYTES {
        return Ok(None);
    }
    let mime_type = match detect_image_mime(&bytes) {
        Some(value) => value,
        None => return Ok(None),
    };
    let meta = fs::read_to_string(&meta_path)
        .ok()
        .and_then(|value| serde_json::from_str::<CachedImageMeta>(&value).ok());
    let Some(mut meta) = meta else {
        return Ok(None);
    };
    meta.mime_type = mime_type.to_string();
    meta.byte_size = bytes.len() as u64;
    meta.last_accessed = unix_timestamp();
    if let Ok(meta_json) = serde_json::to_vec(&meta) {
        let _ = fs::write(&meta_path, meta_json);
    }
    Ok(Some((meta, to_data_url(mime_type, &bytes))))
}

fn write_cached_image(
    app: &AppHandle,
    cache_key: &str,
    bytes: &[u8],
    meta: &CachedImageMeta,
) -> Result<(), String> {
    let (image_path, meta_path) = cache_paths(app, cache_key)?;
    fs::write(image_path, bytes).map_err(|_| "无法写入图片缓存。".to_string())?;
    let meta_json = serde_json::to_vec(meta).map_err(|_| "无法写入图片缓存。".to_string())?;
    fs::write(meta_path, meta_json).map_err(|_| "无法写入图片缓存。".to_string())
}

fn to_data_url(mime_type: &str, bytes: &[u8]) -> String {
    format!("data:{mime_type};base64,{}", BASE64_STANDARD.encode(bytes))
}

fn hash_text(value: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(value.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn image_cache_stats(directory: &Path) -> Result<ImageCacheStats, String> {
    let entries = collect_image_cache_entries(directory)?;
    Ok(ImageCacheStats {
        total_bytes: entries.iter().map(|entry| entry.byte_size).sum(),
        file_count: entries.len(),
    })
}

fn trim_image_cache(directory: &Path, max_bytes: u64) -> Result<(), String> {
    let mut entries = collect_image_cache_entries(directory)?;
    let mut total_bytes = entries.iter().map(|entry| entry.byte_size).sum::<u64>();
    if total_bytes <= max_bytes {
        return Ok(());
    }

    entries.sort_by_key(|entry| entry.last_accessed);
    for entry in entries {
        if total_bytes <= max_bytes {
            break;
        }
        let _ = fs::remove_file(&entry.image_path);
        let _ = fs::remove_file(&entry.meta_path);
        total_bytes = total_bytes.saturating_sub(entry.byte_size);
    }
    Ok(())
}

fn collect_image_cache_entries(directory: &Path) -> Result<Vec<ImageCacheEntry>, String> {
    if !directory.is_dir() {
        return Ok(Vec::new());
    }
    let mut entries = Vec::new();
    for item in fs::read_dir(directory).map_err(|_| "无法读取图片缓存目录。".to_string())?
    {
        let Ok(item) = item else { continue };
        let image_path = item.path();
        if image_path.extension().and_then(|value| value.to_str()) != Some("bin") {
            continue;
        }
        let Ok(file_metadata) = item.metadata() else {
            continue;
        };
        if !file_metadata.is_file() {
            continue;
        }
        let meta_path = image_path.with_extension("json");
        let meta = fs::read_to_string(&meta_path)
            .ok()
            .and_then(|value| serde_json::from_str::<CachedImageMeta>(&value).ok());
        let last_accessed = meta
            .as_ref()
            .map(|value| value.last_accessed)
            .filter(|value| *value > 0)
            .unwrap_or_else(|| modified_timestamp(&file_metadata));
        entries.push(ImageCacheEntry {
            image_path,
            meta_path,
            byte_size: file_metadata.len(),
            last_accessed,
        });
    }
    Ok(entries)
}

fn modified_timestamp(metadata: &fs::Metadata) -> u64 {
    metadata
        .modified()
        .ok()
        .and_then(|value| value.duration_since(UNIX_EPOCH).ok())
        .map(|value| value.as_secs())
        .unwrap_or(0)
}

fn unix_timestamp() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|value| value.as_secs())
        .unwrap_or(0)
}

pub(crate) fn detect_image_mime(bytes: &[u8]) -> Option<&'static str> {
    if bytes.starts_with(&[0xFF, 0xD8, 0xFF]) {
        return Some("image/jpeg");
    }
    if bytes.starts_with(b"\x89PNG\r\n\x1a\n") {
        return Some("image/png");
    }
    if bytes.len() >= 12 && bytes.starts_with(b"RIFF") && &bytes[8..12] == b"WEBP" {
        return Some("image/webp");
    }
    if bytes.starts_with(b"GIF87a") || bytes.starts_with(b"GIF89a") {
        return Some("image/gif");
    }
    if bytes.len() >= 12 && &bytes[4..8] == b"ftyp" {
        if matches!(&bytes[8..12], b"avif" | b"avis") {
            return Some("image/avif");
        }
        if bytes.len() >= 24 {
            let box_size = u32::from_be_bytes(bytes[..4].try_into().ok()?) as usize;
            if box_size >= 24 && box_size <= bytes.len() {
                for brand in bytes[16..box_size].as_chunks::<4>().0 {
                    if matches!(brand, b"avif" | b"avis") {
                        return Some("image/avif");
                    }
                }
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::{
        detect_image_mime, hash_text, image_cache_stats, is_protected_server_artwork_url,
        trim_image_cache, validate_server_artwork_credentials, CachedImageMeta,
    };
    use reqwest::Url;
    use std::{fs, time::UNIX_EPOCH};

    #[test]
    fn server_artwork_bearer_is_restricted_to_same_origin_image_routes() {
        let token = format!("omc_player_{}", "a".repeat(43));
        for path in [
            "/api/v1/player/artwork/opaque_123",
            "/api/v1/player/discovery/images/tmdb/opaque-123",
        ] {
            let url = Url::parse(&format!("https://server.example{path}")).expect("valid URL");
            assert!(is_protected_server_artwork_url(&url));
            assert!(
                validate_server_artwork_credentials(&url, "https://server.example", &token).is_ok()
            );
            assert!(
                validate_server_artwork_credentials(&url, "https://other.example", &token).is_err()
            );
            assert!(validate_server_artwork_credentials(
                &url,
                "https://server.example",
                "wrong-token"
            )
            .is_err());
        }
        for path in [
            "/api/v1/player/artwork/opaque?token=secret",
            "/api/v1/player/online-assets/asset",
            "/api/v1/player/artwork/opaque/other",
            "/api/v1/player/artwork/%2fsecret",
        ] {
            let url = Url::parse(&format!("https://server.example{path}")).expect("valid URL");
            assert!(!is_protected_server_artwork_url(&url));
        }
    }

    #[test]
    fn detects_supported_image_signatures() {
        assert_eq!(
            detect_image_mime(&[0xFF, 0xD8, 0xFF, 0x00]),
            Some("image/jpeg")
        );
        assert_eq!(
            detect_image_mime(b"\x89PNG\r\n\x1a\nrest"),
            Some("image/png")
        );
        assert_eq!(detect_image_mime(b"RIFF0000WEBPrest"), Some("image/webp"));
        assert_eq!(detect_image_mime(b"GIF89arest"), Some("image/gif"));
        assert_eq!(detect_image_mime(b"0000ftypavifrest"), Some("image/avif"));
        assert_eq!(
            detect_image_mime(b"\0\0\0\x18ftypmif1\0\0\0\0avifmiaf"),
            Some("image/avif")
        );
        assert_eq!(detect_image_mime(b"<html>"), None);
    }

    #[test]
    fn hashes_cache_sources_without_persisting_urls() {
        let first = hash_text("https://example.test/poster.jpg?token=secret");
        let second = hash_text("https://example.test/poster.jpg?token=changed");
        assert_eq!(first.len(), 64);
        assert_ne!(first, second);
        assert!(!first.contains("secret"));
    }

    #[test]
    fn trims_oldest_images_to_configured_limit() {
        let directory = std::env::temp_dir().join(format!(
            "ohmycine-image-cache-test-{}-{}",
            UNIX_EPOCH.elapsed().expect("system clock").as_nanos(),
            std::process::id()
        ));
        fs::create_dir_all(&directory).expect("create cache test directory");
        for (name, accessed) in [("old", 1_u64), ("new", 2_u64)] {
            fs::write(directory.join(format!("{name}.bin")), vec![0_u8; 8]).expect("write image");
            fs::write(
                directory.join(format!("{name}.json")),
                serde_json::to_vec(&CachedImageMeta {
                    source_hash: name.to_string(),
                    mime_type: "image/jpeg".to_string(),
                    byte_size: 8,
                    last_accessed: accessed,
                })
                .expect("serialize metadata"),
            )
            .expect("write metadata");
        }

        trim_image_cache(&directory, 8).expect("trim cache");
        let stats = image_cache_stats(&directory).expect("read stats");
        assert_eq!(stats.total_bytes, 8);
        assert_eq!(stats.file_count, 1);
        assert!(!directory.join("old.bin").exists());
        assert!(directory.join("new.bin").exists());
        let _ = fs::remove_dir_all(directory);
    }
}

use base64::{engine::general_purpose::STANDARD as BASE64_STANDARD, Engine};
use reqwest::{redirect::Policy, Url};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{fs, path::PathBuf, time::Duration};
use tauri::AppHandle;

use crate::storage;

const MAX_IMAGE_BYTES: usize = 8 * 1024 * 1024;
const MAX_CACHE_KEY_BYTES: usize = 256;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CacheImageRequest {
    cache_key: String,
    url: String,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct CachedImageMeta {
    source_hash: String,
    mime_type: String,
}

#[tauri::command]
pub fn player_get_cached_image(
    app: AppHandle,
    cache_key: String,
) -> Result<Option<String>, String> {
    validate_cache_key(&cache_key)?;
    read_cached_image(&app, &cache_key)
}

#[tauri::command]
pub async fn player_cache_image(
    app: AppHandle,
    request: CacheImageRequest,
) -> Result<String, String> {
    validate_cache_key(&request.cache_key)?;
    let url = parse_image_url(&request.url)?;
    let source_hash = hash_text(url.as_str());

    if let Some((meta, data_url)) = read_cached_image_with_meta(&app, &request.cache_key)? {
        if meta.source_hash == source_hash {
            return Ok(data_url);
        }
    }

    let client = image_client()?;
    let mut response = client
        .get(url)
        .header(reqwest::header::ACCEPT, "image/avif,image/webp,image/png,image/jpeg,image/gif")
        .send()
        .await
        .map_err(|_| "图片暂时无法缓存。".to_string())?;
    if !response.status().is_success() {
        return Err("图片暂时无法缓存。".to_string());
    }
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

    let mime_type = detect_image_mime(&bytes)
        .ok_or_else(|| "下载内容不是受支持的图片。".to_string())?;
    write_cached_image(
        &app,
        &request.cache_key,
        &bytes,
        &CachedImageMeta {
            source_hash,
            mime_type: mime_type.to_string(),
        },
    )?;
    Ok(to_data_url(mime_type, &bytes))
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

fn cache_paths(app: &AppHandle, cache_key: &str) -> Result<(PathBuf, PathBuf), String> {
    let layout = storage::initialize(app)?;
    let directory = layout.cache_dir.join("images");
    fs::create_dir_all(&directory).map_err(|_| "无法创建图片缓存目录。".to_string())?;
    let file_name = storage::scoped_cache_key("artwork", cache_key.trim());
    Ok((directory.join(format!("{file_name}.bin")), directory.join(format!("{file_name}.json"))))
}

fn read_cached_image(app: &AppHandle, cache_key: &str) -> Result<Option<String>, String> {
    Ok(read_cached_image_with_meta(app, cache_key)?.map(|(_, data_url)| data_url))
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

fn detect_image_mime(bytes: &[u8]) -> Option<&'static str> {
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
    if bytes.len() >= 12 && &bytes[4..8] == b"ftyp" && matches!(&bytes[8..12], b"avif" | b"avis") {
        return Some("image/avif");
    }
    None
}

#[cfg(test)]
mod tests {
    use super::{detect_image_mime, hash_text};

    #[test]
    fn detects_supported_image_signatures() {
        assert_eq!(detect_image_mime(&[0xFF, 0xD8, 0xFF, 0x00]), Some("image/jpeg"));
        assert_eq!(detect_image_mime(b"\x89PNG\r\n\x1a\nrest"), Some("image/png"));
        assert_eq!(detect_image_mime(b"RIFF0000WEBPrest"), Some("image/webp"));
        assert_eq!(detect_image_mime(b"GIF89arest"), Some("image/gif"));
        assert_eq!(detect_image_mime(b"0000ftypavifrest"), Some("image/avif"));
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
}

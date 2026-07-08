use serde::Serialize;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::UNIX_EPOCH;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LocalFileEntry {
    name: String,
    path: String,
    is_dir: bool,
    size: Option<u64>,
    modified_ms: Option<u128>,
}

#[tauri::command]
pub fn local_file_list(
    root_path: String,
    path: Option<String>,
) -> Result<Vec<LocalFileEntry>, String> {
    let root = canonicalize_root(&root_path)?;
    let target = resolve_target_path(&root, path.as_deref())?;
    ensure_within_root(&root, &target)?;
    ensure_directory(&target, "本地文件目录不可用。")?;

    let mut entries = Vec::new();
    let read_dir = fs::read_dir(&target).map_err(|_| "本地文件目录读取失败。".to_string())?;
    for entry_result in read_dir {
        let entry = match entry_result {
            Ok(entry) => entry,
            Err(_) => continue,
        };
        let entry_path = entry.path();
        let canonical = match fs::canonicalize(&entry_path) {
            Ok(path) => path,
            Err(_) => continue,
        };
        if !is_within_root(&canonical, &root) {
            continue;
        }
        let metadata = match fs::metadata(&canonical) {
            Ok(metadata) => metadata,
            Err(_) => continue,
        };
        entries.push(entry_from_metadata(&root, &canonical, &metadata));
    }

    entries.sort_by(|left, right| {
        right
            .is_dir
            .cmp(&left.is_dir)
            .then_with(|| left.name.to_lowercase().cmp(&right.name.to_lowercase()))
    });
    Ok(entries)
}

#[tauri::command]
pub fn local_file_metadata(root_path: String, path: String) -> Result<LocalFileEntry, String> {
    let root = canonicalize_root(&root_path)?;
    let target = resolve_target_path(&root, Some(&path))?;
    ensure_within_root(&root, &target)?;
    let metadata = fs::metadata(&target).map_err(|_| "本地文件条目不可用。".to_string())?;
    Ok(entry_from_metadata(&root, &target, &metadata))
}

#[tauri::command]
pub fn local_file_stream_path(root_path: String, path: String) -> Result<String, String> {
    let root = canonicalize_root(&root_path)?;
    let target = resolve_target_path(&root, Some(&path))?;
    ensure_within_root(&root, &target)?;
    let metadata = fs::metadata(&target).map_err(|_| "本地文件条目不可用。".to_string())?;
    if metadata.is_dir() {
        return Err("本地文件夹不能直接播放。".to_string());
    }
    Ok(path_to_string(&target))
}

fn canonicalize_root(root_path: &str) -> Result<PathBuf, String> {
    validate_path_text(root_path, "本地文件根目录无效。")?;
    let root = Path::new(root_path);
    if !root.is_absolute() {
        return Err("本地文件根目录必须是绝对路径。".to_string());
    }
    let canonical = fs::canonicalize(root).map_err(|_| "本地文件根目录不可用。".to_string())?;
    ensure_directory(&canonical, "本地文件根目录必须是文件夹。")?;
    Ok(canonical)
}

fn resolve_target_path(root: &Path, path: Option<&str>) -> Result<PathBuf, String> {
    let raw_target = path.and_then(non_empty_trimmed);
    let candidate = match raw_target {
        Some(value) => {
            validate_path_text(value, "本地文件路径无效。")?;
            root.join(provider_path_to_relative_path(value)?)
        }
        None => root.to_path_buf(),
    };

    fs::canonicalize(candidate).map_err(|_| "本地文件路径不可用。".to_string())
}

fn provider_path_to_relative_path(value: &str) -> Result<PathBuf, String> {
    let normalized = value.trim().replace('\\', "/");
    if normalized == "/" {
        return Ok(PathBuf::new());
    }

    let mut relative = PathBuf::new();
    for segment in normalized.split('/').filter(|segment| !segment.is_empty()) {
        validate_path_text(segment, "本地文件路径无效。")?;
        relative.push(segment);
    }
    Ok(relative)
}

fn ensure_directory(path: &Path, message: &str) -> Result<(), String> {
    let metadata = fs::metadata(path).map_err(|_| message.to_string())?;
    if !metadata.is_dir() {
        return Err(message.to_string());
    }
    Ok(())
}

fn ensure_within_root(root: &Path, target: &Path) -> Result<(), String> {
    if is_within_root(target, root) {
        Ok(())
    } else {
        Err("本地文件路径不在已选择的根目录内。".to_string())
    }
}

fn is_within_root(target: &Path, root: &Path) -> bool {
    target == root || target.starts_with(root)
}

fn entry_from_metadata(
    root: &Path,
    canonical_path: &Path,
    metadata: &fs::Metadata,
) -> LocalFileEntry {
    LocalFileEntry {
        name: display_name(canonical_path),
        path: provider_path_from_canonical(root, canonical_path),
        is_dir: metadata.is_dir(),
        size: metadata.is_file().then(|| metadata.len()),
        modified_ms: metadata
            .modified()
            .ok()
            .and_then(|modified| modified.duration_since(UNIX_EPOCH).ok())
            .map(|duration| duration.as_millis()),
    }
}

fn display_name(canonical_path: &Path) -> String {
    canonical_path
        .file_name()
        .map(|name| name.to_string_lossy().to_string())
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| path_to_string(canonical_path))
}

fn provider_path_from_canonical(root: &Path, canonical_path: &Path) -> String {
    if canonical_path == root {
        return "/".to_string();
    }

    let relative = canonical_path.strip_prefix(root).unwrap_or(canonical_path);
    let segments: Vec<String> = relative
        .components()
        .map(|component| component.as_os_str().to_string_lossy().replace('\\', "/"))
        .filter(|segment| !segment.is_empty())
        .collect();

    if segments.is_empty() {
        "/".to_string()
    } else {
        format!("/{}", segments.join("/"))
    }
}

fn path_to_string(path: &Path) -> String {
    path.to_string_lossy().to_string()
}

fn non_empty_trimmed(value: &str) -> Option<&str> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed)
    }
}

fn validate_path_text(value: &str, message: &str) -> Result<(), String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || trimmed.as_bytes().contains(&0) || is_url_like_path(trimmed) {
        return Err(message.to_string());
    }

    for segment in trimmed
        .split(['/', '\\'])
        .filter(|segment| !segment.is_empty())
    {
        let mut current = segment.to_string();
        for _ in 0..2 {
            if current == "." || current == ".." {
                return Err(message.to_string());
            }
            let decoded = percent_decode_once(&current);
            if decoded == current {
                break;
            }
            current = decoded;
        }
        if current == "." || current == ".." {
            return Err(message.to_string());
        }
    }

    Ok(())
}

fn is_url_like_path(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    lower.starts_with("http:")
        || lower.starts_with("https:")
        || lower.starts_with("webdav:")
        || lower.starts_with("ftp:")
        || lower.starts_with("sftp:")
        || lower.starts_with("file:")
        || lower.starts_with("blob:")
}

fn percent_decode_once(value: &str) -> String {
    let bytes = value.as_bytes();
    let mut output = Vec::with_capacity(bytes.len());
    let mut index = 0;
    let mut changed = false;

    while index < bytes.len() {
        if bytes[index] == b'%' && index + 2 < bytes.len() {
            if let (Some(high), Some(low)) =
                (hex_value(bytes[index + 1]), hex_value(bytes[index + 2]))
            {
                output.push(high * 16 + low);
                index += 3;
                changed = true;
                continue;
            }
        }
        output.push(bytes[index]);
        index += 1;
    }

    if changed {
        String::from_utf8_lossy(&output).to_string()
    } else {
        value.to_string()
    }
}

fn hex_value(value: u8) -> Option<u8> {
    match value {
        b'0'..=b'9' => Some(value - b'0'),
        b'a'..=b'f' => Some(value - b'a' + 10),
        b'A'..=b'F' => Some(value - b'A' + 10),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::{self, File};
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn rejects_traversal_and_url_like_paths() {
        let root = temp_root("rejects_traversal");
        fs::create_dir_all(root.join("media")).unwrap();

        let canonical_root = fs::canonicalize(&root).unwrap();
        assert!(resolve_target_path(&canonical_root, Some("../escape")).is_err());
        assert!(resolve_target_path(&canonical_root, Some("%2e%2e/escape")).is_err());
        assert!(
            resolve_target_path(&canonical_root, Some("https://example.test/movie.mkv")).is_err()
        );

        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn rejects_paths_outside_root() {
        let root = temp_root("rejects_outside_root");
        let outside = temp_root("rejects_outside_target");
        fs::create_dir_all(&root).unwrap();
        fs::create_dir_all(&outside).unwrap();
        let outside_file = outside.join("movie.mkv");
        File::create(&outside_file).unwrap();

        let canonical_root = fs::canonicalize(&root).unwrap();
        let outside_like_provider_path = outside_file.to_string_lossy().replace('\\', "/");
        assert!(resolve_target_path(&canonical_root, Some(&outside_like_provider_path)).is_err());

        fs::remove_dir_all(root).unwrap();
        fs::remove_dir_all(outside).unwrap();
    }

    #[test]
    fn rejects_directory_stream_path() {
        let root = temp_root("rejects_directory_stream");
        fs::create_dir_all(root.join("folder")).unwrap();

        let result = local_file_stream_path(
            root.to_string_lossy().to_string(),
            root.join("folder").to_string_lossy().to_string(),
        );

        assert!(result.is_err());
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn rejects_symlink_escape() {
        use std::os::unix::fs::symlink;

        let root = temp_root("rejects_symlink_root");
        let outside = temp_root("rejects_symlink_outside");
        fs::create_dir_all(&root).unwrap();
        fs::create_dir_all(&outside).unwrap();
        let outside_file = outside.join("outside.mkv");
        File::create(&outside_file).unwrap();
        let link = root.join("outside-link.mkv");
        symlink(&outside_file, &link).unwrap();

        let canonical_root = fs::canonicalize(&root).unwrap();
        let target = resolve_target_path(&canonical_root, Some("/outside-link.mkv")).unwrap();
        assert!(ensure_within_root(&fs::canonicalize(&root).unwrap(), &target).is_err());

        fs::remove_dir_all(root).unwrap();
        fs::remove_dir_all(outside).unwrap();
    }

    fn temp_root(name: &str) -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        std::env::temp_dir().join(format!("ohmycine-{name}-{nonce}"))
    }
}

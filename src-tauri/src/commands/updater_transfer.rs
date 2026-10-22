//! Updater-owned streaming/checkpoint code. The caller validates release identity
//! and verifies the complete package before it is ever offered for installation.
use fs2::FileExt;
use reqwest::header::{ACCEPT_ENCODING, CONTENT_ENCODING, CONTENT_RANGE, ETAG, IF_RANGE, RANGE};
use serde::{Deserialize, Serialize};
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};
use tokio::io::{AsyncSeekExt, AsyncWriteExt};

const MAX_CHECKPOINT_BYTES: u64 = 64 * 1024;
const CHECKPOINT_INTERVAL: u64 = 1024 * 1024;
pub(super) const PACKAGE_FILE: &str = "package.part";

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Checkpoint {
    pub schema: u8,
    pub generation: u64,
    pub candidate: serde_json::Value,
    pub committed: u64,
    pub total: Option<u64>,
    pub etag: Option<String>,
}

impl Checkpoint {
    pub fn new(candidate: serde_json::Value) -> Self {
        Self {
            schema: 1,
            generation: 0,
            candidate,
            committed: 0,
            total: None,
            etag: None,
        }
    }
}

pub(super) struct TransferProgress {
    pub downloaded: u64,
    pub total: Option<u64>,
}

pub(super) struct TransferError {
    pub message: String,
    pub retryable: bool,
}

impl TransferError {
    fn network() -> Self {
        Self {
            message: "更新下载中断，已保留下载进度，可继续下载。".into(),
            retryable: true,
        }
    }
    fn invalid(message: &str) -> Self {
        Self {
            message: message.into(),
            retryable: false,
        }
    }
}

pub(super) fn owned_path(root: &Path, name: &str) -> Result<PathBuf, String> {
    let path = root.join(name);
    if let Ok(metadata) = fs::symlink_metadata(&path) {
        if metadata.file_type().is_symlink() || is_reparse(&metadata) || !metadata.is_file() {
            return Err("更新缓存包含不受支持的文件链接。".into());
        }
    }
    Ok(path)
}

#[cfg(windows)]
fn is_reparse(metadata: &fs::Metadata) -> bool {
    use std::os::windows::fs::MetadataExt;
    metadata.file_attributes() & 0x400 != 0
}
#[cfg(not(windows))]
fn is_reparse(_: &fs::Metadata) -> bool {
    false
}

pub(super) fn prepare_directory(root: &Path) -> Result<(), String> {
    if let Ok(metadata) = fs::symlink_metadata(root) {
        if metadata.file_type().is_symlink() || is_reparse(&metadata) || !metadata.is_dir() {
            return Err("更新目录不是受控的本地目录。".into());
        }
    }
    fs::create_dir_all(root).map_err(|_| "无法创建更新目录。".to_string())
}

/// Retained for the whole transfer/install, including across asynchronous reads.
pub(super) fn lock_directory(root: &Path) -> Result<File, String> {
    let path = owned_path(root, "download.lock")?;
    let file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(path)
        .map_err(|_| "无法打开更新下载锁。".to_string())?;
    file.try_lock_exclusive()
        .map_err(|_| "另一个 Player 正在使用此更新下载。".to_string())?;
    Ok(file)
}

pub(super) fn load(root: &Path) -> Result<Option<Checkpoint>, String> {
    let mut latest: Option<Checkpoint> = None;
    for name in ["checkpoint-a.json", "checkpoint-b.json"] {
        let path = owned_path(root, name)?;
        let Ok(metadata) = fs::metadata(&path) else {
            continue;
        };
        if metadata.len() > MAX_CHECKPOINT_BYTES {
            continue;
        }
        let Ok(bytes) = fs::read(path) else { continue };
        let Ok(checkpoint) = serde_json::from_slice::<Checkpoint>(&bytes) else {
            continue;
        };
        if checkpoint.schema == 1
            && latest
                .as_ref()
                .is_none_or(|old| old.generation < checkpoint.generation)
        {
            latest = Some(checkpoint);
        }
    }
    Ok(latest)
}

/// Alternating slots preserve the last synced record even on Windows, where
/// rename cannot overwrite an existing file. Only the inactive slot is replaced.
pub(super) fn save(root: &Path, checkpoint: &mut Checkpoint) -> Result<(), String> {
    checkpoint.generation = checkpoint
        .generation
        .checked_add(1)
        .ok_or("更新检查点序号无效。")?;
    let name = if checkpoint.generation.is_multiple_of(2) {
        "checkpoint-a.json"
    } else {
        "checkpoint-b.json"
    };
    let target = owned_path(root, name)?;
    let temporary = owned_path(root, "checkpoint.tmp")?;
    let bytes = serde_json::to_vec(checkpoint).map_err(|_| "无法编码更新检查点。".to_string())?;
    if bytes.len() > MAX_CHECKPOINT_BYTES as usize {
        return Err("更新检查点过大。".into());
    }
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .open(&temporary)
        .map_err(|_| "无法写入更新检查点。".to_string())?;
    file.write_all(&bytes)
        .and_then(|_| file.sync_all())
        .map_err(|_| "无法保存更新检查点。".to_string())?;
    drop(file);
    if target.exists() {
        fs::remove_file(&target).map_err(|_| "无法替换更新检查点。".to_string())?;
    }
    fs::rename(temporary, target).map_err(|_| "无法提交更新检查点。".to_string())?;
    #[cfg(unix)]
    File::open(root)
        .and_then(|directory| directory.sync_all())
        .map_err(|_| "无法同步更新目录。".to_string())?;
    Ok(())
}

pub(super) fn reconcile(root: &Path, checkpoint: &mut Checkpoint, max: u64) -> Result<u64, String> {
    if checkpoint.committed > max
        || checkpoint
            .total
            .is_some_and(|total| total > max || checkpoint.committed > total)
        || checkpoint
            .etag
            .as_ref()
            .is_some_and(|tag| !strong_etag(tag))
    {
        return Err("更新检查点内容无效。".into());
    }
    let path = owned_path(root, PACKAGE_FILE)?;
    let actual = fs::metadata(&path)
        .map(|metadata| metadata.len())
        .unwrap_or(0);
    let length = actual.min(checkpoint.committed);
    if actual != length {
        OpenOptions::new()
            .write(true)
            .open(&path)
            .and_then(|file| file.set_len(length))
            .map_err(|_| "无法恢复更新下载文件。".to_string())?;
    }
    checkpoint.committed = length;
    Ok(length)
}

pub(super) fn reset(root: &Path, checkpoint: &mut Checkpoint) -> Result<(), String> {
    let path = owned_path(root, PACKAGE_FILE)?;
    let file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .open(path)
        .map_err(|_| "无法重置损坏的更新下载。".to_string())?;
    file.sync_all()
        .map_err(|_| "无法同步更新下载。".to_string())?;
    checkpoint.committed = 0;
    checkpoint.total = None;
    checkpoint.etag = None;
    save(root, checkpoint)
}

fn strong_etag(value: &str) -> bool {
    value.len() >= 2
        && value.len() <= 1024
        && value.starts_with('"')
        && value.ends_with('"')
        && value.bytes().all(|byte| byte >= 0x20 && byte != 0x7f)
}

fn content_range(value: &str) -> Option<(u64, u64, u64)> {
    let (range, total) = value.strip_prefix("bytes ")?.split_once('/')?;
    let (start, end) = range.split_once('-')?;
    let (start, end, total) = (start.parse().ok()?, end.parse().ok()?, total.parse().ok()?);
    (start <= end && end < total).then_some((start, end, total))
}

async fn commit(
    root: &Path,
    file: &mut tokio::fs::File,
    checkpoint: &mut Checkpoint,
    bytes: u64,
) -> Result<(), TransferError> {
    file.flush()
        .await
        .map_err(|_| TransferError::invalid("更新文件写入失败。"))?;
    file.sync_data()
        .await
        .map_err(|_| TransferError::invalid("更新文件同步失败。"))?;
    checkpoint.committed = bytes;
    save(root, checkpoint).map_err(|message| TransferError {
        message,
        retryable: false,
    })
}

pub(super) async fn download<F, Fut>(
    root: &Path,
    checkpoint: &mut Checkpoint,
    client: &reqwest::Client,
    url: &reqwest::Url,
    max: u64,
    mut progress: F,
) -> Result<(), TransferError>
where
    F: FnMut(TransferProgress) -> Fut,
    Fut: std::future::Future<Output = Result<(), String>>,
{
    reconcile(root, checkpoint, max).map_err(TransferError::invalid_owned)?;
    if checkpoint.total == Some(checkpoint.committed) && checkpoint.committed > 0 {
        return Ok(());
    }
    if checkpoint.committed > 0 && checkpoint.etag.is_none() {
        reset(root, checkpoint).map_err(TransferError::invalid_owned)?;
    }
    // One controlled restart handles an ignored Range, replaced entity or 416.
    for restart in 0..2 {
        let offset = checkpoint.committed;
        let mut request = client.get(url.clone()).header(ACCEPT_ENCODING, "identity");
        if offset > 0 {
            request = request.header(RANGE, format!("bytes={offset}-"));
            if let Some(etag) = &checkpoint.etag {
                request = request.header(IF_RANGE, etag);
            }
        }
        let mut response = tokio::time::timeout(Duration::from_secs(30), request.send())
            .await
            .map_err(|_| TransferError::network())?
            .map_err(|_| TransferError::network())?;
        let status = response.status();
        if status == reqwest::StatusCode::RANGE_NOT_SATISFIABLE {
            let total = response
                .headers()
                .get(CONTENT_RANGE)
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.strip_prefix("bytes */"))
                .and_then(|value| value.parse::<u64>().ok());
            if total == Some(offset) && offset > 0 {
                checkpoint.total = total;
                save(root, checkpoint).map_err(TransferError::invalid_owned)?;
                return Ok(());
            }
            reset(root, checkpoint).map_err(TransferError::invalid_owned)?;
            if restart == 0 {
                continue;
            }
            return Err(TransferError::invalid(
                "更新服务器的文件范围无效，请继续下载以重新获取。",
            ));
        }
        if !matches!(
            status,
            reqwest::StatusCode::OK | reqwest::StatusCode::PARTIAL_CONTENT
        ) {
            return Err(TransferError {
                message: format!("更新下载返回 HTTP {}，进度已保留。", status.as_u16()),
                retryable: status.is_server_error() || matches!(status.as_u16(), 403 | 408 | 429),
            });
        }
        if response
            .headers()
            .get(CONTENT_ENCODING)
            .is_some_and(|value| value != "identity")
        {
            return Err(TransferError::invalid("更新服务器返回了不支持的内容编码。"));
        }
        let etag = response
            .headers()
            .get(ETAG)
            .and_then(|value| value.to_str().ok())
            .filter(|value| strong_etag(value))
            .map(str::to_owned);
        let (start, total, expected_body) = if status == reqwest::StatusCode::PARTIAL_CONTENT {
            let (start, end, total) = response
                .headers()
                .get(CONTENT_RANGE)
                .and_then(|value| value.to_str().ok())
                .and_then(content_range)
                .ok_or_else(|| TransferError::invalid("更新服务器返回了无效的 Content-Range。"))?;
            if start != offset
                || total > max
                || response
                    .content_length()
                    .is_some_and(|length| length != end - start + 1)
            {
                return Err(TransferError::invalid(
                    "更新服务器返回的下载范围不匹配，已保留原有进度。",
                ));
            }
            if offset > 0
                && (checkpoint.total != Some(total) || checkpoint.etag != etag || etag.is_none())
            {
                reset(root, checkpoint).map_err(TransferError::invalid_owned)?;
                if restart == 0 {
                    continue;
                }
                return Err(TransferError::invalid(
                    "更新文件已变化，请继续下载以重新获取。",
                ));
            }
            (start, Some(total), Some(end - start + 1))
        } else {
            // A 200 response to Range MUST replace the owned prefix, never append.
            if offset > 0 {
                reset(root, checkpoint).map_err(TransferError::invalid_owned)?;
            }
            (0, response.content_length(), response.content_length())
        };
        if total.is_some_and(|length| length == 0 || length > max) {
            return Err(TransferError::invalid("更新包大小超出允许范围。"));
        }
        checkpoint.total = total;
        checkpoint.etag = etag;
        save(root, checkpoint).map_err(TransferError::invalid_owned)?;
        let path = owned_path(root, PACKAGE_FILE).map_err(TransferError::invalid_owned)?;
        let mut file = tokio::fs::OpenOptions::new()
            .create(true)
            .truncate(start == 0)
            .write(true)
            .open(path)
            .await
            .map_err(|_| TransferError::invalid("无法打开更新下载文件。"))?;
        file.seek(std::io::SeekFrom::Start(start))
            .await
            .map_err(|_| TransferError::invalid("无法恢复更新下载位置。"))?;
        let mut downloaded = start;
        let mut last_emit = Instant::now() - Duration::from_secs(1);
        let mut last_commit = Instant::now();
        loop {
            let chunk = match tokio::time::timeout(Duration::from_secs(30), response.chunk()).await
            {
                Ok(Ok(chunk)) => chunk,
                _ => {
                    commit(root, &mut file, checkpoint, downloaded).await?;
                    return Err(TransferError::network());
                }
            };
            let Some(chunk) = chunk else { break };
            let next = downloaded.saturating_add(chunk.len() as u64);
            if next > max || expected_body.is_some_and(|expected| next - start > expected) {
                commit(root, &mut file, checkpoint, downloaded).await?;
                return Err(TransferError::invalid(
                    "更新服务器传输了超出声明范围的数据。",
                ));
            }
            file.write_all(&chunk)
                .await
                .map_err(|_| TransferError::invalid("更新文件写入失败，已有进度已保留。"))?;
            downloaded = next;
            if downloaded - checkpoint.committed >= CHECKPOINT_INTERVAL
                || last_commit.elapsed() >= Duration::from_secs(1)
            {
                commit(root, &mut file, checkpoint, downloaded).await?;
                last_commit = Instant::now();
            }
            if last_emit.elapsed() >= Duration::from_millis(250) {
                if let Err(message) = progress(TransferProgress { downloaded, total }).await {
                    commit(root, &mut file, checkpoint, downloaded).await?;
                    return Err(TransferError::invalid(&message));
                }
                last_emit = Instant::now();
            }
        }
        commit(root, &mut file, checkpoint, downloaded).await?;
        if expected_body.is_some_and(|length| downloaded - start != length)
            || total.is_some_and(|length| downloaded != length)
        {
            return Err(TransferError::network());
        }
        if downloaded == 0 {
            return Err(TransferError::invalid("更新包为空。"));
        }
        checkpoint.total = Some(downloaded);
        save(root, checkpoint).map_err(TransferError::invalid_owned)?;
        progress(TransferProgress {
            downloaded,
            total: Some(downloaded),
        })
        .await
        .map_err(TransferError::invalid_owned)?;
        return Ok(());
    }
    Err(TransferError::invalid("无法恢复更新下载。"))
}

impl TransferError {
    fn invalid_owned(message: String) -> Self {
        Self {
            message,
            retryable: false,
        }
    }
}

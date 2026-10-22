use super::updater_transfer::{self as transfer, Checkpoint};
#[cfg(not(target_os = "android"))]
use crate::storage::{self, StorageMode};
use reqwest::header::{ACCEPT, USER_AGENT};
use reqwest::{StatusCode, Url};
#[cfg(target_os = "android")]
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::Duration;
#[cfg(target_os = "android")]
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    Wry,
};
use tauri::{AppHandle, Emitter, Manager, State};
#[cfg(not(target_os = "android"))]
use tauri_plugin_updater::{Update, UpdaterExt};

const GITHUB_RELEASES_API: &str =
    "https://api.github.com/repos/yuanjing-hash/OhMyCine/releases?per_page=30";
const GITHUB_RELEASE_ASSET_PREFIX: &str = "/yuanjing-hash/OhMyCine/releases/download/";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const MAX_RELEASE_RESPONSE_BYTES: usize = 1024 * 1024;
const MAX_PACKAGE_BYTES: u64 = 512 * 1024 * 1024;

#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum UpdateChannel {
    Beta,
    Stable,
}
#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
#[allow(dead_code)]
pub enum UpdatePlatform {
    Desktop,
    Android,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct UpdateCheckResult {
    available: bool,
    current_version: String,
    version: Option<String>,
    date: Option<String>,
    body: Option<String>,
    channel: UpdateChannel,
    platform: UpdatePlatform,
}
#[derive(Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum UpdateStatus {
    Idle,
    Available,
    Downloading,
    Paused,
    Verifying,
    Ready,
    Installing,
    Error,
}
#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateDownloadState {
    update: Option<UpdateCheckResult>,
    status: UpdateStatus,
    downloaded_bytes: u64,
    content_length: Option<u64>,
    error: Option<String>,
}
impl Default for UpdateDownloadState {
    fn default() -> Self {
        Self {
            update: None,
            status: UpdateStatus::Idle,
            downloaded_bytes: 0,
            content_length: None,
            error: None,
        }
    }
}

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(tag = "type", rename_all = "camelCase", deny_unknown_fields)]
enum Integrity {
    Desktop { signature: String, target: String },
    Android { sha256: String },
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Candidate {
    display: UpdateCheckResult,
    tag: String,
    asset: String,
    url: String,
    integrity: Integrity,
}
impl Candidate {
    fn same_package(&self, other: &Self) -> bool {
        self.tag == other.tag
            && self.asset == other.asset
            && self.url == other.url
            && self.integrity == other.integrity
    }

    fn validate(&self, current: &str) -> Result<Url, String> {
        let version = release_version(&self.tag)?;
        if self.tag != format!("v{version}")
            || self.display.version.as_deref() != Some(&version)
            || !self.display.available
            || !is_newer_version(&version, current)
            || self
                .display
                .body
                .as_ref()
                .is_some_and(|body| body.len() > 48 * 1024)
            || self
                .display
                .date
                .as_ref()
                .is_some_and(|date| date.len() > 128)
        {
            return Err("已保存更新的版本信息无效或已安装。".into());
        }
        match &self.integrity {
            Integrity::Desktop { signature, target } => {
                if cfg!(target_os = "android")
                    || self.display.platform != UpdatePlatform::Desktop
                    || self.asset != format!("OhMyCine-Player-{}-windows-x64-setup.exe", self.tag)
                    || target != "windows"
                    || signature.is_empty()
                    || signature.len() > 8192
                {
                    return Err("已保存的桌面更新信息无效。".into());
                }
            }
            Integrity::Android { sha256 } => {
                if !cfg!(target_os = "android")
                    || self.display.platform != UpdatePlatform::Android
                    || self.asset != format!("OhMyCine-Player-{}-android-arm64.apk", self.tag)
                    || sha256.len() != 64
                    || !sha256.bytes().all(|byte| byte.is_ascii_hexdigit())
                {
                    return Err("已保存的 Android 更新信息无效。".into());
                }
            }
        }
        validate_release_asset_url(&self.url, &self.tag, &self.asset)
    }
    fn limit(&self) -> u64 {
        if matches!(self.integrity, Integrity::Android { .. }) {
            300 * 1024 * 1024
        } else {
            MAX_PACKAGE_BYTES
        }
    }
}

#[derive(Default)]
struct Runtime {
    root: Option<PathBuf>,
    candidate: Option<Candidate>,
    snapshot: UpdateDownloadState,
    started: bool,
    active: bool,
    checking: bool,
    #[cfg(not(target_os = "android"))]
    desktop: Option<Update>,
}
#[derive(Default)]
pub struct PendingUpdate {
    inner: Mutex<Runtime>,
    initialize: tokio::sync::Mutex<()>,
}
fn lock(pending: &PendingUpdate) -> Result<std::sync::MutexGuard<'_, Runtime>, String> {
    pending
        .inner
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())
}
fn publish(app: &AppHandle, runtime: &Runtime) {
    // Keep mutation and emission in the same lock so late queries cannot
    // reorder a progress event behind an older snapshot.
    let _ = app.emit("updater:state", &runtime.snapshot);
}

async fn initialize(app: &AppHandle, pending: &PendingUpdate) -> Result<(), String> {
    let _initializing = pending.initialize.lock().await;
    if lock(pending)?.root.is_some() {
        return Ok(());
    }
    #[cfg(not(target_os = "android"))]
    let root = storage::initialize(app)?.data_dir.join("updates");
    #[cfg(target_os = "android")]
    let root = {
        let response: AndroidDirectory = android(app)?
            .run("directory", serde_json::json!({}))
            .await?;
        PathBuf::from(response.path)
    };
    transfer::prepare_directory(&root)?;
    let _file_lock = transfer::lock_directory(&root)?;
    let recovered = transfer::load(&root)?.and_then(|mut checkpoint| {
        let candidate: Candidate = serde_json::from_value(checkpoint.candidate.clone()).ok()?;
        candidate
            .validate(&app.package_info().version.to_string())
            .ok()?;
        let downloaded = transfer::reconcile(&root, &mut checkpoint, candidate.limit()).ok()?;
        Some((candidate, downloaded, checkpoint.total))
    });
    let mut runtime = lock(pending)?;
    runtime.root = Some(root);
    if let Some((candidate, downloaded, total)) = recovered {
        runtime.snapshot = UpdateDownloadState {
            update: Some(candidate.display.clone()),
            status: UpdateStatus::Paused,
            downloaded_bytes: downloaded,
            content_length: total,
            error: None,
        };
        runtime.candidate = Some(candidate);
        runtime.started = true;
    }
    publish(app, &runtime);
    Ok(())
}

#[tauri::command]
pub async fn player_update_state(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
) -> Result<UpdateDownloadState, String> {
    initialize(&app, &pending).await?;
    Ok(lock(&pending)?.snapshot.clone())
}
#[tauri::command]
pub async fn player_check_for_updates(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
    channel: UpdateChannel,
) -> Result<UpdateCheckResult, String> {
    initialize(&app, &pending).await?;
    {
        let mut runtime = lock(&pending)?;
        if runtime.active
            || runtime.checking
            || matches!(
                runtime.snapshot.status,
                UpdateStatus::Ready | UpdateStatus::Installing
            )
        {
            return runtime
                .snapshot
                .update
                .clone()
                .ok_or_else(|| "正在检查更新，请稍候。".to_string());
        }
        runtime.checking = true;
    }
    let checked = discover(&app, channel).await;
    let mut runtime = lock(&pending)?;
    runtime.checking = false;
    let discovered = checked?;
    if runtime.active
        || matches!(
            runtime.snapshot.status,
            UpdateStatus::Ready | UpdateStatus::Installing
        )
    {
        return runtime
            .snapshot
            .update
            .clone()
            .ok_or_else(|| "更新状态暂不可用。".to_string());
    }
    match discovered {
        Some(discovered) => {
            let display = discovered.candidate.display.clone();
            let same_package = runtime
                .candidate
                .as_ref()
                .is_some_and(|old| runtime.started && old.same_package(&discovered.candidate));
            runtime.candidate = Some(discovered.candidate);
            #[cfg(not(target_os = "android"))]
            {
                runtime.desktop = Some(discovered.desktop);
            }
            if same_package {
                runtime.snapshot.update = Some(display.clone());
            } else {
                // Merely discovering a replacement never deletes the saved prefix.
                // Its files are replaced only after the user starts that package.
                runtime.started = false;
                runtime.snapshot = UpdateDownloadState {
                    update: Some(display.clone()),
                    status: UpdateStatus::Available,
                    downloaded_bytes: 0,
                    content_length: None,
                    error: None,
                };
            }
            publish(&app, &runtime);
            Ok(display)
        }
        None => {
            if runtime.started {
                // Keep the explicitly authorized interrupted task if this check
                // found no usable replacement (including another channel).
                publish(&app, &runtime);
                return runtime
                    .snapshot
                    .update
                    .clone()
                    .ok_or_else(|| "更新状态暂不可用。".to_string());
            }
            runtime.candidate = None;
            #[cfg(not(target_os = "android"))]
            {
                runtime.desktop = None;
            }
            runtime.snapshot = UpdateDownloadState::default();
            publish(&app, &runtime);
            Ok(no_update_result(
                app.package_info().version.to_string(),
                channel,
            ))
        }
    }
}

struct Discovered {
    candidate: Candidate,
    #[cfg(not(target_os = "android"))]
    desktop: Update,
}
async fn discover(app: &AppHandle, channel: UpdateChannel) -> Result<Option<Discovered>, String> {
    let Some(release) = fetch_latest_release(channel).await? else {
        return Ok(None);
    };
    let version = release_version(&release.tag_name)?;
    let current = app.package_info().version.to_string();
    if !is_newer_version(&version, &current) {
        return Ok(None);
    }
    #[cfg(not(target_os = "android"))]
    {
        let endpoint = release
            .assets
            .iter()
            .find(|asset| asset.name == "latest.json")
            .ok_or_else(|| "最新发布缺少签名更新清单。".to_string())?;
        validate_release_asset_url(
            &endpoint.browser_download_url,
            &release.tag_name,
            "latest.json",
        )?;
        let update = desktop_update(app, &release.tag_name).await?;
        if update.version != version {
            return Err("签名更新清单与发布版本不一致。".into());
        }
        let candidate = Candidate {
            display: UpdateCheckResult {
                available: true,
                current_version: current,
                version: Some(version),
                date: update.date.map(|date| date.to_string()),
                body: update
                    .body
                    .as_ref()
                    .map(|body| body.chars().take(16 * 1024).collect()),
                channel,
                platform: UpdatePlatform::Desktop,
            },
            tag: release.tag_name.clone(),
            asset: format!("OhMyCine-Player-{}-windows-x64-setup.exe", release.tag_name),
            url: update.download_url.to_string(),
            integrity: Integrity::Desktop {
                signature: update.signature.clone(),
                target: update.target.clone(),
            },
        };
        candidate.validate(&app.package_info().version.to_string())?;
        Ok(Some(Discovered {
            candidate,
            desktop: update,
        }))
    }
    #[cfg(target_os = "android")]
    {
        let apk = format!("OhMyCine-Player-{}-android-arm64.apk", release.tag_name);
        let checksum = format!("OhMyCine-Player-{}-android-arm64.sha256", release.tag_name);
        let asset = release
            .assets
            .iter()
            .find(|asset| asset.name == apk)
            .ok_or("最新发布缺少 Android ARM64 安装包。")?;
        let checksum_asset = release
            .assets
            .iter()
            .find(|asset| asset.name == checksum)
            .ok_or("最新发布缺少 Android 安装包校验文件。")?;
        let url = validate_release_asset_url(&asset.browser_download_url, &release.tag_name, &apk)?;
        let checksum_url = validate_release_asset_url(
            &checksum_asset.browser_download_url,
            &release.tag_name,
            &checksum,
        )?;
        let sha256 = download_checksum(&release_download_client()?, &checksum_url, &apk).await?;
        let candidate = Candidate {
            display: UpdateCheckResult {
                available: true,
                current_version: current,
                version: Some(version),
                date: release.published_at,
                body: release
                    .body
                    .map(|body| body.chars().take(16 * 1024).collect()),
                channel,
                platform: UpdatePlatform::Android,
            },
            tag: release.tag_name,
            asset: apk,
            url: url.to_string(),
            integrity: Integrity::Android { sha256 },
        };
        candidate.validate(&app.package_info().version.to_string())?;
        Ok(Some(Discovered { candidate }))
    }
}
#[cfg(not(target_os = "android"))]
async fn desktop_update(app: &AppHandle, tag: &str) -> Result<Update, String> {
    let endpoint = Url::parse(&format!(
        "https://github.com{GITHUB_RELEASE_ASSET_PREFIX}{tag}/latest.json"
    ))
    .map_err(|_| "无法配置更新清单地址。".to_string())?;
    let mut builder = app
        .updater_builder()
        .endpoints(vec![endpoint])
        .map_err(|_| "无法配置更新清单地址。".to_string())?
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS));
    if storage::initialize(app)?.mode == StorageMode::Portable {
        let directory = std::env::current_exe()
            .ok()
            .and_then(|path| path.parent().map(ToOwned::to_owned))
            .ok_or_else(|| "无法确定便携版安装目录。".to_string())?;
        builder = builder.installer_arg(format!("/D={}", directory.to_string_lossy()));
    }
    builder
        .build()
        .map_err(|_| "无法初始化签名更新器。".to_string())?
        .check()
        .await
        .map_err(|_| "无法读取签名更新清单，请稍后重试。".to_string())?
        .ok_or_else(|| "此更新版本已安装或不再可用。".to_string())
}

#[tauri::command]
pub async fn player_download_update(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
) -> Result<UpdateDownloadState, String> {
    initialize(&app, &pending).await?;
    let (root, candidate, snapshot) = {
        let mut runtime = lock(&pending)?;
        if runtime.active
            || matches!(
                runtime.snapshot.status,
                UpdateStatus::Ready | UpdateStatus::Installing
            )
        {
            return Ok(runtime.snapshot.clone());
        }
        let candidate = runtime
            .candidate
            .clone()
            .ok_or_else(|| "没有等待下载的更新，请先检测更新。".to_string())?;
        candidate.validate(&app.package_info().version.to_string())?;
        let root = runtime.root.clone().ok_or("更新目录尚未初始化。")?;
        runtime.active = true;
        runtime.started = true;
        runtime.snapshot.status = UpdateStatus::Downloading;
        runtime.snapshot.error = None;
        publish(&app, &runtime);
        (root, candidate, runtime.snapshot.clone())
    };
    // Native ownership is independent from command/Channel/WebView lifetime.
    tauri::async_runtime::spawn(async move {
        download_worker(app, root, candidate).await;
    });
    Ok(snapshot)
}
async fn download_worker(app: AppHandle, root: PathBuf, candidate: Candidate) {
    let pending = app.state::<PendingUpdate>();
    let file_lock = match transfer::lock_directory(&root) {
        Ok(file) => file,
        Err(error) => {
            finish_worker(&app, &pending, UpdateStatus::Paused, Some(error), None);
            return;
        }
    };
    let mut checkpoint = match prepare_checkpoint(&root, &candidate) {
        Ok(checkpoint) => checkpoint,
        Err(error) => {
            drop(file_lock);
            finish_worker(&app, &pending, UpdateStatus::Paused, Some(error), None);
            return;
        }
    };
    if let Err(error) =
        start_foreground(&app, &candidate, checkpoint.committed, checkpoint.total).await
    {
        finish_foreground(&app, false).await;
        drop(file_lock);
        finish_worker(
            &app,
            &pending,
            UpdateStatus::Paused,
            Some(error),
            Some(&checkpoint),
        );
        return;
    }
    let mut integrity_failure = false;
    let result = async {
        let client = release_download_client()?;
        let url = candidate.validate(&app.package_info().version.to_string())?;
        for attempt in 0..3 {
            let result = transfer::download(
                &root,
                &mut checkpoint,
                &client,
                &url,
                candidate.limit(),
                |progress| {
                    let app = app.clone();
                    async move {
                        {
                            let pending = app.state::<PendingUpdate>();
                            let mut runtime = lock(&pending)?;
                            runtime.snapshot.downloaded_bytes = progress.downloaded;
                            runtime.snapshot.content_length = progress.total;
                            publish(&app, &runtime);
                        }
                        update_foreground(&app, progress.downloaded, progress.total).await
                    }
                },
            )
            .await;
            match result {
                Ok(()) => break,
                Err(error) if error.retryable && attempt < 2 => {
                    update_foreground(&app, checkpoint.committed, checkpoint.total).await?;
                    tokio::time::sleep(Duration::from_secs(1 << attempt)).await;
                }
                Err(error) => return Err(error.message),
            }
        }
        {
            let mut runtime = lock(&pending)?;
            runtime.snapshot.status = UpdateStatus::Verifying;
            runtime.snapshot.downloaded_bytes = checkpoint.committed;
            runtime.snapshot.content_length = checkpoint.total;
            publish(&app, &runtime);
        }
        let path = transfer::owned_path(&root, transfer::PACKAGE_FILE)?;
        let integrity = candidate.integrity.clone();
        let app_for_verify = app.clone();
        let verified = tauri::async_runtime::spawn_blocking(move || {
            verify_package(&app_for_verify, &path, &integrity)
        })
        .await
        .map_err(|_| "更新包验证被中断。".to_string())?;
        if let Err(error) = verified {
            integrity_failure = true;
            transfer::reset(&root, &mut checkpoint)?;
            return Err(error);
        }
        Ok::<(), String>(())
    }
    .await;
    let status = if result.is_ok() {
        UpdateStatus::Ready
    } else if integrity_failure {
        UpdateStatus::Error
    } else {
        UpdateStatus::Paused
    };
    finish_foreground(&app, status == UpdateStatus::Ready).await;
    drop(file_lock);
    finish_worker(&app, &pending, status, result.err(), Some(&checkpoint));
}
fn prepare_checkpoint(root: &Path, candidate: &Candidate) -> Result<Checkpoint, String> {
    let value = serde_json::to_value(candidate).map_err(|_| "无法保存更新信息。".to_string())?;
    let mut checkpoint = match transfer::load(root)? {
        Some(mut checkpoint)
            if serde_json::from_value::<Candidate>(checkpoint.candidate.clone())
                .is_ok_and(|saved| saved.same_package(candidate)) =>
        {
            checkpoint.candidate = value;
            checkpoint
        }
        previous => {
            let mut checkpoint = Checkpoint::new(value);
            if let Some(old) = previous {
                checkpoint.generation = old.generation;
            }
            transfer::reset(root, &mut checkpoint)?;
            checkpoint
        }
    };
    transfer::reconcile(root, &mut checkpoint, candidate.limit())?;
    transfer::save(root, &mut checkpoint)?;
    Ok(checkpoint)
}
fn finish_worker(
    app: &AppHandle,
    pending: &PendingUpdate,
    status: UpdateStatus,
    error: Option<String>,
    checkpoint: Option<&Checkpoint>,
) {
    if let Ok(mut runtime) = lock(pending) {
        runtime.active = false;
        runtime.snapshot.status = status;
        runtime.snapshot.error = error;
        if let Some(checkpoint) = checkpoint {
            runtime.snapshot.downloaded_bytes = checkpoint.committed;
            runtime.snapshot.content_length = checkpoint.total;
        }
        publish(app, &runtime);
    }
}

struct InstallFailure {
    message: String,
    invalid_package: bool,
}
impl InstallFailure {
    fn package(message: String) -> Self {
        Self {
            message,
            invalid_package: true,
        }
    }
}
impl From<String> for InstallFailure {
    fn from(message: String) -> Self {
        Self {
            message,
            invalid_package: false,
        }
    }
}
impl From<&str> for InstallFailure {
    fn from(message: &str) -> Self {
        message.to_string().into()
    }
}

#[tauri::command]
pub async fn player_install_update(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
) -> Result<(), String> {
    initialize(&app, &pending).await?;
    let (root, candidate) = {
        let mut runtime = lock(&pending)?;
        if runtime.active || runtime.snapshot.status != UpdateStatus::Ready {
            return Err("更新包尚未下载并验证完成。".into());
        }
        let candidate = runtime.candidate.clone().ok_or("没有可安装的更新。")?;
        let root = runtime.root.clone().ok_or("更新目录尚未初始化。")?;
        runtime.active = true;
        runtime.snapshot.status = UpdateStatus::Installing;
        runtime.snapshot.error = None;
        publish(&app, &runtime);
        (root, candidate)
    };
    let result = install_ready(&app, &pending, &root, &candidate).await;
    let invalid_package = result
        .as_ref()
        .err()
        .is_some_and(|error| error.invalid_package);
    if invalid_package {
        let mut runtime = lock(&pending)?;
        runtime.snapshot.downloaded_bytes = 0;
        runtime.snapshot.content_length = None;
    }
    // Android consent or installation can be cancelled. Retain the package.
    finish_worker(
        &app,
        &pending,
        if invalid_package {
            UpdateStatus::Error
        } else {
            UpdateStatus::Ready
        },
        result.as_ref().err().map(|error| error.message.clone()),
        None,
    );
    result.map_err(|error| error.message)
}
async fn install_ready(
    app: &AppHandle,
    pending: &PendingUpdate,
    root: &Path,
    candidate: &Candidate,
) -> Result<(), InstallFailure> {
    let _file_lock = transfer::lock_directory(root)?;
    candidate.validate(&app.package_info().version.to_string())?;
    let path = transfer::owned_path(root, transfer::PACKAGE_FILE)?;
    let result = install_verified(app, pending, root, candidate, path).await;
    if result
        .as_ref()
        .err()
        .is_some_and(|error| error.invalid_package)
    {
        // The installer/permission path retains a good package; a corrupt one
        // must return to Download rather than an endless Ready/install loop.
        let mut checkpoint =
            prepare_checkpoint(root, candidate).map_err(InstallFailure::package)?;
        transfer::reset(root, &mut checkpoint).map_err(InstallFailure::package)?;
    }
    result
}
async fn install_verified(
    app: &AppHandle,
    pending: &PendingUpdate,
    root: &Path,
    candidate: &Candidate,
    path: PathBuf,
) -> Result<(), InstallFailure> {
    #[cfg(not(target_os = "android"))]
    {
        let _ = root;
        let saved = lock(pending)?.desktop.clone();
        let update = match saved {
            Some(update) => update,
            None => desktop_update(app, &candidate.tag).await?,
        };
        let Integrity::Desktop { signature, target } = &candidate.integrity else {
            return Err("更新包平台无效。".into());
        };
        if update.version != candidate.display.version.as_deref().unwrap_or_default()
            || update.download_url.as_str() != candidate.url
            || update.signature != *signature
            || update.target != *target
        {
            return Err(InstallFailure::package(
                "更新清单已变化，请重新检测并下载此更新。".into(),
            ));
        }
        let app_for_install = app.clone();
        let signature = signature.clone();
        tauri::async_runtime::spawn_blocking(move || {
            let bytes = read_bounded(&path, MAX_PACKAGE_BYTES).map_err(InstallFailure::package)?;
            let (key, signature) =
                desktop_signature(&app_for_install, &signature).map_err(InstallFailure::package)?;
            // install() itself DOES NOT verify. Verify these exact bytes.
            key.verify(&bytes, &signature, true).map_err(|_| {
                InstallFailure::package("更新包签名验证失败，请继续下载以重新获取。".into())
            })?;
            update
                .install(bytes)
                .map_err(|_| InstallFailure::from("更新包安装失败，可再次点击安装。"))
        })
        .await
        .map_err(|_| "更新安装被中断。".to_string())??;
        #[cfg(not(windows))]
        app.restart();
        Ok(())
    }
    #[cfg(target_os = "android")]
    {
        let _ = pending;
        let destination = transfer::owned_path(root, "package.apk")?;
        let app_for_verify = app.clone();
        let integrity = candidate.integrity.clone();
        let install_path = destination.clone();
        tauri::async_runtime::spawn_blocking(move || {
            verify_package(&app_for_verify, &path, &integrity).map_err(InstallFailure::package)?;
            std::fs::copy(path, &destination)
                .map_err(|_| "无法准备 Android 安装包。".to_string())?;
            verify_package(&app_for_verify, &destination, &integrity)
                .map_err(InstallFailure::package)
        })
        .await
        .map_err(|_| "Android 更新包验证被中断。".to_string())??;
        let installed: AndroidInstallResult = android(app)?
            .run(
                "install",
                serde_json::json!({ "path": install_path, "version": candidate.display.version }),
            )
            .await?;
        match installed.status.as_str() {
            "launched" => Ok(()),
            "permissionRequired" => {
                Err("请允许 OhMyCine 安装未知应用，然后返回并再次点击安装。".into())
            }
            "invalidPackage" => Err(InstallFailure::package(
                "Android 更新安装包的应用、版本或签名不匹配，请重新检测并下载。".into(),
            )),
            _ => Err("Android 系统安装器未能启动，请重试安装。".into()),
        }
    }
}

fn verify_package(app: &AppHandle, path: &Path, integrity: &Integrity) -> Result<(), String> {
    use std::io::Read;
    let mut file = std::fs::File::open(path).map_err(|_| "更新包不存在。".to_string())?;
    let length = file
        .metadata()
        .map_err(|_| "无法读取更新包。".to_string())?
        .len();
    if length == 0 || length > MAX_PACKAGE_BYTES {
        return Err("更新包大小无效。".into());
    }
    let mut buffer = [0u8; 64 * 1024];
    match integrity {
        #[cfg(not(target_os = "android"))]
        Integrity::Desktop { signature, .. } => {
            let (key, signature) = desktop_signature(app, signature)?;
            let mut verifier = key
                .verify_stream(&signature)
                .map_err(|_| "更新包签名格式不受支持。".to_string())?;
            loop {
                let size = file
                    .read(&mut buffer)
                    .map_err(|_| "更新包读取失败。".to_string())?;
                if size == 0 {
                    break;
                }
                verifier.update(&buffer[..size]);
            }
            verifier
                .finalize()
                .map_err(|_| "更新包签名验证失败，请继续下载以重新获取。".to_string())
        }
        Integrity::Android { sha256 } => {
            use sha2::{Digest, Sha256};
            let _ = app;
            let mut hash = Sha256::new();
            loop {
                let size = file
                    .read(&mut buffer)
                    .map_err(|_| "更新包读取失败。".to_string())?;
                if size == 0 {
                    break;
                }
                hash.update(&buffer[..size]);
            }
            if format!("{:x}", hash.finalize()) == *sha256 {
                Ok(())
            } else {
                Err("更新包 SHA-256 校验失败，请继续下载以重新获取。".into())
            }
        }
        #[cfg(target_os = "android")]
        _ => Err("更新包平台无效。".into()),
    }
}
#[cfg(not(target_os = "android"))]
fn desktop_signature(
    app: &AppHandle,
    signature: &str,
) -> Result<(minisign_verify::PublicKey, minisign_verify::Signature), String> {
    use base64::Engine;
    let pubkey = app
        .config()
        .plugins
        .0
        .get("updater")
        .and_then(|value| value.get("pubkey"))
        .and_then(|value| value.as_str())
        .ok_or_else(|| "更新签名公钥未配置。".to_string())?;
    let decode = |value: &str| {
        base64::engine::general_purpose::STANDARD
            .decode(value)
            .ok()
            .and_then(|bytes| String::from_utf8(bytes).ok())
            .ok_or_else(|| "更新签名编码无效。".to_string())
    };
    let key = minisign_verify::PublicKey::decode(&decode(pubkey)?)
        .map_err(|_| "更新签名公钥无效。".to_string())?;
    let signature = minisign_verify::Signature::decode(&decode(signature)?)
        .map_err(|_| "更新签名格式无效。".to_string())?;
    Ok((key, signature))
}
#[cfg(not(target_os = "android"))]
fn read_bounded(path: &Path, limit: u64) -> Result<Vec<u8>, String> {
    use std::io::Read;
    let mut bytes = Vec::new();
    std::fs::File::open(path)
        .map_err(|_| "更新包不存在。".to_string())?
        .take(limit + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| "更新包读取失败。".to_string())?;
    if bytes.is_empty() || bytes.len() as u64 > limit {
        return Err("更新包大小无效。".into());
    }
    Ok(bytes)
}

fn release_download_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(20))
        .user_agent("OhMyCine-Player-Updater")
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 4 || !is_allowed_download_url(attempt.url()) {
                attempt.stop()
            } else {
                attempt.follow()
            }
        }))
        .build()
        .map_err(|_| "无法初始化更新下载器。".to_string())
}
fn is_allowed_download_url(url: &Url) -> bool {
    url.scheme() == "https"
        && url.username().is_empty()
        && url.password().is_none()
        && url.port_or_known_default() == Some(443)
        && matches!(
            url.host_str(),
            Some("github.com")
                | Some("release-assets.githubusercontent.com")
                | Some("objects.githubusercontent.com")
        )
}
#[derive(Deserialize)]
struct GithubRelease {
    draft: bool,
    prerelease: bool,
    tag_name: String,
    #[cfg(target_os = "android")]
    body: Option<String>,
    #[cfg(target_os = "android")]
    published_at: Option<String>,
    assets: Vec<GithubReleaseAsset>,
}
#[derive(Deserialize)]
struct GithubReleaseAsset {
    name: String,
    browser_download_url: String,
}
async fn fetch_latest_release(channel: UpdateChannel) -> Result<Option<GithubRelease>, String> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化 GitHub 更新客户端。".to_string())?;
    let mut response = client
        .get(GITHUB_RELEASES_API)
        .header(USER_AGENT, "OhMyCine-Player-Updater")
        .header(ACCEPT, "application/vnd.github+json")
        .send()
        .await
        .map_err(|_| "无法连接 GitHub Releases，请稍后重试。".to_string())?;
    let status = response.status();
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "无法读取 GitHub Releases 响应。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_RELEASE_RESPONSE_BYTES {
            return Err("GitHub Releases 响应过大。".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    if !status.is_success() {
        return Err(github_status_message(status));
    }
    let releases: Vec<GithubRelease> = serde_json::from_slice(&bytes)
        .map_err(|_| "GitHub Releases 返回了无法解析的数据。".to_string())?;
    let release = releases
        .into_iter()
        .find(|release| release_matches_channel(release, channel));
    if let Some(release) = &release {
        release_version(&release.tag_name)?;
    }
    Ok(release)
}
#[cfg(target_os = "android")]
async fn download_checksum(
    client: &reqwest::Client,
    url: &Url,
    asset: &str,
) -> Result<String, String> {
    let mut response =
        tokio::time::timeout(Duration::from_secs(20), client.get(url.clone()).send())
            .await
            .map_err(|_| "更新校验文件下载超时。".to_string())?
            .map_err(|_| "无法下载更新校验文件。".to_string())?;
    if !response.status().is_success() || !is_allowed_download_url(response.url()) {
        return Err("更新校验文件下载失败。".into());
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = tokio::time::timeout(Duration::from_secs(20), response.chunk())
        .await
        .map_err(|_| "更新校验文件读取超时。".to_string())?
        .map_err(|_| "无法读取更新校验文件。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > 8192 {
            return Err("更新校验文件过大。".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    let text = std::str::from_utf8(&bytes).map_err(|_| "更新校验文件格式无效。".to_string())?;
    let mut parts = text.split_whitespace();
    let checksum = parts.next().unwrap_or_default().to_ascii_lowercase();
    let filename = parts.next().unwrap_or_default().trim_start_matches('*');
    if filename != asset
        || checksum.len() != 64
        || !checksum.bytes().all(|byte| byte.is_ascii_hexdigit())
        || parts.next().is_some()
    {
        return Err("更新校验文件与安装包不匹配。".into());
    }
    Ok(checksum)
}
fn no_update_result(current_version: String, channel: UpdateChannel) -> UpdateCheckResult {
    UpdateCheckResult {
        available: false,
        current_version,
        version: None,
        date: None,
        body: None,
        channel,
        platform: if cfg!(target_os = "android") {
            UpdatePlatform::Android
        } else {
            UpdatePlatform::Desktop
        },
    }
}
fn validate_release_asset_url(
    value: &str,
    tag_name: &str,
    asset_name: &str,
) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "更新资源地址无效。".to_string())?;
    let expected_path = format!("{GITHUB_RELEASE_ASSET_PREFIX}{tag_name}/{asset_name}");
    if !is_allowed_download_url(&url)
        || url.host_str() != Some("github.com")
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != expected_path
    {
        return Err("更新资源不属于受信任的 OhMyCine GitHub Release。".into());
    }
    Ok(url)
}
fn release_version(value: &str) -> Result<String, String> {
    let version = value.trim().strip_prefix('v').unwrap_or(value.trim());
    let parts = version.split('.').collect::<Vec<_>>();
    if parts.len() == 3
        && parts.iter().all(|part| {
            !part.is_empty() && part.len() <= 8 && part.bytes().all(|byte| byte.is_ascii_digit())
        })
    {
        Ok(version.to_string())
    } else {
        Err("GitHub Release 版本标签无效。".into())
    }
}
fn is_newer_version(candidate: &str, current: &str) -> bool {
    version_tuple(candidate) > version_tuple(current)
}
fn version_tuple(value: &str) -> (u64, u64, u64) {
    let mut parts = value.trim().trim_start_matches('v').split('.');
    (
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
    )
}
fn release_matches_channel(release: &GithubRelease, channel: UpdateChannel) -> bool {
    !release.draft && (channel == UpdateChannel::Beta || !release.prerelease)
}
fn github_status_message(status: StatusCode) -> String {
    match status {
        StatusCode::FORBIDDEN | StatusCode::TOO_MANY_REQUESTS => {
            "GitHub 更新检查频率受限，请稍后重试。".into()
        }
        _ => format!("GitHub Releases 返回 HTTP {}。", status.as_u16()),
    }
}

#[cfg(target_os = "android")]
#[derive(Clone)]
pub struct AndroidUpdaterState {
    handle: PluginHandle<Wry>,
}
#[cfg(target_os = "android")]
impl AndroidUpdaterState {
    async fn run<T: DeserializeOwned>(
        &self,
        command: &str,
        payload: impl Serialize,
    ) -> Result<T, String> {
        self.handle
            .run_mobile_plugin_async(command, payload)
            .await
            .map_err(|_| "Android 更新操作暂不可用，请返回应用后继续。".to_string())
    }
}
#[cfg(target_os = "android")]
fn android(app: &AppHandle) -> Result<AndroidUpdaterState, String> {
    app.try_state::<AndroidUpdaterState>()
        .map(|state| state.inner().clone())
        .ok_or_else(|| "Android 更新服务尚未初始化。".into())
}
#[cfg(target_os = "android")]
pub fn init_android() -> TauriPlugin<Wry> {
    Builder::new("updater-android")
        .setup(|app, api| {
            let handle =
                api.register_android_plugin("com.ohmycine.player.updater", "UpdaterPlugin")?;
            app.manage(AndroidUpdaterState { handle });
            Ok(())
        })
        .build()
}
#[cfg(target_os = "android")]
#[derive(Deserialize)]
struct AndroidDirectory {
    path: String,
}
#[cfg(target_os = "android")]
#[derive(Deserialize)]
struct AndroidInstallResult {
    status: String,
}
#[cfg(target_os = "android")]
#[derive(Deserialize)]
struct ForegroundLease {
    active: bool,
}
async fn start_foreground(
    app: &AppHandle,
    candidate: &Candidate,
    bytes: u64,
    total: Option<u64>,
) -> Result<(), String> {
    #[cfg(target_os = "android")]
    {
        let lease: ForegroundLease = android(app)?.run("startDownload", serde_json::json!({ "version": candidate.display.version, "downloaded": bytes, "total": total })).await?;
        if !lease.active {
            return Err("后台下载服务未能启动，请返回应用后继续。".into());
        }
    }
    #[cfg(not(target_os = "android"))]
    let _ = (app, candidate, bytes, total);
    Ok(())
}
async fn update_foreground(app: &AppHandle, bytes: u64, total: Option<u64>) -> Result<(), String> {
    #[cfg(target_os = "android")]
    {
        let lease: ForegroundLease = android(app)?
            .run(
                "downloadProgress",
                serde_json::json!({ "downloaded": bytes, "total": total }),
            )
            .await?;
        if !lease.active {
            return Err("系统已暂停后台更新，下载进度已保存，返回应用后可继续。".into());
        }
    }
    #[cfg(not(target_os = "android"))]
    let _ = (app, bytes, total);
    Ok(())
}
async fn finish_foreground(app: &AppHandle, ready: bool) {
    #[cfg(target_os = "android")]
    if let Ok(android) = android(app) {
        let _ = android
            .run::<serde_json::Value>("finishDownload", serde_json::json!({ "ready": ready }))
            .await;
    }
    #[cfg(not(target_os = "android"))]
    let _ = (app, ready);
}

#[cfg(test)]
fn is_safe_release_tag(value: &str) -> bool {
    release_version(value).is_ok()
}

#[cfg(test)]
mod tests {
    use super::{
        is_newer_version, is_safe_release_tag, release_matches_channel, validate_release_asset_url,
        GithubRelease, UpdateChannel,
    };

    #[test]
    fn validates_release_tags_and_versions() {
        assert!(is_safe_release_tag("v0.0.6"));
        assert!(is_safe_release_tag("1.2.3"));
        assert!(!is_safe_release_tag("v1.2.3-beta"));
        assert!(!is_safe_release_tag("release/1.2.3"));
        assert!(is_newer_version("0.1.10", "0.1.9"));
        assert!(!is_newer_version("0.1.9", "0.1.9"));
    }

    #[test]
    fn only_accepts_official_release_assets() {
        assert!(validate_release_asset_url(
            "https://github.com/yuanjing-hash/OhMyCine/releases/download/v0.0.6/latest.json",
            "v0.0.6",
            "latest.json",
        )
        .is_ok());
        assert!(validate_release_asset_url(
            "https://github.com/attacker/OhMyCine/releases/download/v0.0.6/latest.json",
            "v0.0.6",
            "latest.json",
        )
        .is_err());
        assert!(validate_release_asset_url(
            "https://github.com/yuanjing-hash/OhMyCine/releases/download/v0.0.5/latest.json",
            "v0.0.6",
            "latest.json",
        )
        .is_err());
    }

    #[test]
    fn filters_beta_and_stable_channels() {
        let beta = GithubRelease {
            draft: false,
            prerelease: true,
            tag_name: "v0.0.6".to_string(),
            #[cfg(target_os = "android")]
            body: None,
            #[cfg(target_os = "android")]
            published_at: None,
            assets: Vec::new(),
        };
        let stable = GithubRelease {
            draft: false,
            prerelease: false,
            tag_name: "v1.0.0".to_string(),
            #[cfg(target_os = "android")]
            body: None,
            #[cfg(target_os = "android")]
            published_at: None,
            assets: Vec::new(),
        };
        assert!(release_matches_channel(&beta, UpdateChannel::Beta));
        assert!(release_matches_channel(&stable, UpdateChannel::Beta));
        assert!(!release_matches_channel(&beta, UpdateChannel::Stable));
        assert!(release_matches_channel(&stable, UpdateChannel::Stable));
    }
}

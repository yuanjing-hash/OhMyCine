#[cfg(not(target_os = "android"))]
use crate::storage::{self, StorageMode};
use reqwest::header::{ACCEPT, USER_AGENT};
use reqwest::{StatusCode, Url};
use serde::{Deserialize, Serialize};
use std::sync::Mutex;
use std::time::Duration;
use tauri::{ipc::Channel, AppHandle, State};
use tauri_plugin_updater::Update;
#[cfg(not(target_os = "android"))]
use tauri_plugin_updater::UpdaterExt;

#[cfg(target_os = "android")]
use serde::de::DeserializeOwned;
#[cfg(target_os = "android")]
use sha2::{Digest, Sha256};
#[cfg(target_os = "android")]
use std::{fs, path::PathBuf};
#[cfg(target_os = "android")]
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    Manager, Wry,
};

const GITHUB_RELEASES_API: &str =
    "https://api.github.com/repos/yuanjing-hash/OhMyCine/releases?per_page=30";
const GITHUB_RELEASE_ASSET_PREFIX: &str = "/yuanjing-hash/OhMyCine/releases/download/";
#[cfg(not(target_os = "android"))]
const UPDATE_MANIFEST_ASSET: &str = "latest.json";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const MAX_RELEASE_RESPONSE_BYTES: usize = 1024 * 1024;
#[cfg(target_os = "android")]
const MAX_ANDROID_APK_BYTES: usize = 300 * 1024 * 1024;
#[cfg(target_os = "android")]
const MAX_CHECKSUM_BYTES: usize = 8 * 1024;
#[cfg(target_os = "android")]
const ANDROID_PLUGIN_IDENTIFIER: &str = "com.ohmycine.player.updater";
#[cfg(target_os = "android")]
const ANDROID_PLUGIN_CLASS: &str = "UpdaterPlugin";

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum UpdateChannel {
    Beta,
    Stable,
}

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
#[allow(dead_code)]
pub enum UpdatePlatform {
    Desktop,
    Android,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateCheckResult {
    available: bool,
    current_version: String,
    version: Option<String>,
    date: Option<String>,
    body: Option<String>,
    channel: UpdateChannel,
    platform: UpdatePlatform,
}

#[derive(Clone, Serialize)]
#[serde(tag = "event", content = "data")]
pub enum UpdateProgressEvent {
    Started { content_length: Option<u64> },
    Progress { chunk_length: usize },
    Finished,
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

#[cfg(target_os = "android")]
#[derive(Clone)]
struct AndroidPendingUpdate {
    apk_name: String,
    apk_url: Url,
    checksum_url: Url,
}

pub struct PendingUpdate {
    desktop: Mutex<Option<Update>>,
    #[cfg(target_os = "android")]
    android: Mutex<Option<AndroidPendingUpdate>>,
}

impl Default for PendingUpdate {
    fn default() -> Self {
        Self {
            desktop: Mutex::new(None),
            #[cfg(target_os = "android")]
            android: Mutex::new(None),
        }
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
            .map_err(|error| format!("Android 系统安装器启动失败：{error}"))
    }
}

#[cfg(target_os = "android")]
pub fn init_android() -> TauriPlugin<Wry> {
    Builder::new("updater-android")
        .setup(|app, api| {
            let handle =
                api.register_android_plugin(ANDROID_PLUGIN_IDENTIFIER, ANDROID_PLUGIN_CLASS)?;
            app.manage(AndroidUpdaterState { handle });
            Ok(())
        })
        .build()
}

#[tauri::command]
pub async fn player_check_for_updates(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
    channel: UpdateChannel,
) -> Result<UpdateCheckResult, String> {
    clear_pending_update(&pending)?;

    #[cfg(target_os = "android")]
    {
        return check_android_update(&app, &pending, channel).await;
    }

    #[cfg(not(target_os = "android"))]
    {
        check_desktop_update(&app, &pending, channel).await
    }
}

#[cfg(not(target_os = "android"))]
async fn check_desktop_update(
    app: &AppHandle,
    pending: &PendingUpdate,
    channel: UpdateChannel,
) -> Result<UpdateCheckResult, String> {
    let current_version = app.package_info().version.to_string();
    let Some(endpoint) = resolve_update_manifest(channel).await? else {
        return Ok(no_update_result(
            current_version,
            channel,
            UpdatePlatform::Desktop,
        ));
    };

    let mut builder = app
        .updater_builder()
        .endpoints(vec![endpoint])
        .map_err(|_| "无法配置更新清单地址。".to_string())?
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS));

    let layout = storage::initialize(app)?;
    if layout.mode == StorageMode::Portable {
        let executable_dir = std::env::current_exe()
            .ok()
            .and_then(|path| path.parent().map(ToOwned::to_owned))
            .ok_or_else(|| "无法确定便携版安装目录。".to_string())?;
        builder = builder.installer_arg(format!("/D={}", executable_dir.to_string_lossy()));
    }

    let updater = builder
        .build()
        .map_err(|_| "无法初始化签名更新器。".to_string())?;
    let Some(update) = updater
        .check()
        .await
        .map_err(|_| "更新清单校验失败，请稍后重试。".to_string())?
    else {
        return Ok(no_update_result(
            current_version,
            channel,
            UpdatePlatform::Desktop,
        ));
    };

    let result = UpdateCheckResult {
        available: true,
        current_version: update.current_version.clone(),
        version: Some(update.version.clone()),
        date: update.date.map(|date| date.to_string()),
        body: update.body.clone(),
        channel,
        platform: UpdatePlatform::Desktop,
    };
    *pending
        .desktop
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())? = Some(update);
    Ok(result)
}

#[cfg(target_os = "android")]
async fn check_android_update(
    app: &AppHandle,
    pending: &PendingUpdate,
    channel: UpdateChannel,
) -> Result<UpdateCheckResult, String> {
    let current_version = app.package_info().version.to_string();
    let Some(release) = fetch_latest_release(channel).await? else {
        return Ok(no_update_result(
            current_version,
            channel,
            UpdatePlatform::Android,
        ));
    };
    let version = release_version(&release.tag_name)?;
    if !is_newer_version(&version, &current_version) {
        return Ok(no_update_result(
            current_version,
            channel,
            UpdatePlatform::Android,
        ));
    }

    let apk_name = format!("OhMyCine-Player-{}-android-arm64.apk", release.tag_name);
    let checksum_name = format!("OhMyCine-Player-{}-android-arm64.sha256", release.tag_name);
    let apk = release
        .assets
        .iter()
        .find(|asset| asset.name == apk_name)
        .ok_or_else(|| "最新发布缺少 Android ARM64 安装包。".to_string())?;
    let checksum = release
        .assets
        .iter()
        .find(|asset| asset.name == checksum_name)
        .ok_or_else(|| "最新发布缺少 Android 安装包校验文件。".to_string())?;
    let pending_update = AndroidPendingUpdate {
        apk_name,
        apk_url: validate_release_asset_url(
            &apk.browser_download_url,
            &release.tag_name,
            &apk.name,
        )?,
        checksum_url: validate_release_asset_url(
            &checksum.browser_download_url,
            &release.tag_name,
            &checksum.name,
        )?,
    };
    *pending
        .android
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())? = Some(pending_update);

    Ok(UpdateCheckResult {
        available: true,
        current_version,
        version: Some(version),
        date: release.published_at,
        body: release.body,
        channel,
        platform: UpdatePlatform::Android,
    })
}

fn no_update_result(
    current_version: String,
    channel: UpdateChannel,
    platform: UpdatePlatform,
) -> UpdateCheckResult {
    UpdateCheckResult {
        available: false,
        current_version,
        version: None,
        date: None,
        body: None,
        channel,
        platform,
    }
}

#[tauri::command]
pub async fn player_install_update(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
    on_event: Channel<UpdateProgressEvent>,
) -> Result<(), String> {
    #[cfg(target_os = "android")]
    {
        return install_android_update(&app, &pending, &on_event).await;
    }

    #[cfg(not(target_os = "android"))]
    {
        install_desktop_update(app, &pending, &on_event).await
    }
}

#[cfg(not(target_os = "android"))]
async fn install_desktop_update(
    app: AppHandle,
    pending: &PendingUpdate,
    on_event: &Channel<UpdateProgressEvent>,
) -> Result<(), String> {
    let update = pending
        .desktop
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())?
        .take()
        .ok_or_else(|| "没有等待安装的更新，请重新检测。".to_string())?;

    let mut started = false;
    update
        .download_and_install(
            |chunk_length, content_length| {
                if !started {
                    started = true;
                    let _ = on_event.send(UpdateProgressEvent::Started { content_length });
                }
                let _ = on_event.send(UpdateProgressEvent::Progress { chunk_length });
            },
            || {
                let _ = on_event.send(UpdateProgressEvent::Finished);
            },
        )
        .await
        .map_err(|_| "更新包下载、签名验证或安装失败。".to_string())?;

    #[cfg(not(windows))]
    app.restart();
    #[cfg(windows)]
    {
        let _ = app;
        Ok(())
    }
}

#[cfg(target_os = "android")]
async fn install_android_update(
    app: &AppHandle,
    pending: &PendingUpdate,
    on_event: &Channel<UpdateProgressEvent>,
) -> Result<(), String> {
    let update = pending
        .android
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())?
        .clone()
        .ok_or_else(|| "没有等待安装的 Android 更新，请重新检测。".to_string())?;
    let apk_path = download_verified_android_apk(app, &update, on_event).await?;
    let state = app
        .try_state::<AndroidUpdaterState>()
        .ok_or_else(|| "Android 系统安装器尚未初始化。".to_string())?
        .inner()
        .clone();
    state
        .run::<serde_json::Value>(
            "install",
            AndroidInstallPayload {
                path: apk_path.to_string_lossy().into_owned(),
            },
        )
        .await?;
    let _ = on_event.send(UpdateProgressEvent::Finished);
    Ok(())
}

#[cfg(target_os = "android")]
#[derive(Serialize)]
struct AndroidInstallPayload {
    path: String,
}

#[cfg(target_os = "android")]
async fn download_verified_android_apk(
    app: &AppHandle,
    update: &AndroidPendingUpdate,
    on_event: &Channel<UpdateProgressEvent>,
) -> Result<PathBuf, String> {
    let client = release_download_client()?;
    let expected_checksum = download_checksum(&client, &update.checksum_url).await?;
    let response = client
        .get(update.apk_url.clone())
        .header(USER_AGENT, "OhMyCine-Player-Android-Updater")
        .send()
        .await
        .map_err(|error| {
            if error.is_timeout() {
                "Android 更新包下载超时。".to_string()
            } else {
                "无法下载 Android 更新包。".to_string()
            }
        })?;
    validate_download_response(&response, MAX_ANDROID_APK_BYTES, "Android 更新包")?;
    let content_length = response.content_length();
    let _ = on_event.send(UpdateProgressEvent::Started { content_length });

    let update_dir = app
        .path()
        .app_cache_dir()
        .map_err(|_| "无法确定 Android 更新缓存目录。".to_string())?
        .join("updates");
    fs::create_dir_all(&update_dir).map_err(|_| "无法创建 Android 更新缓存目录。".to_string())?;
    let target = update_dir.join(&update.apk_name);
    let temporary = update_dir.join(format!("{}.download", update.apk_name));
    let _ = fs::remove_file(&temporary);

    let mut response = response;
    let mut bytes = Vec::new();
    let mut hasher = Sha256::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "Android 更新包下载中断。".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_ANDROID_APK_BYTES {
            return Err("Android 更新包超过允许大小。".to_string());
        }
        hasher.update(&chunk);
        bytes.extend_from_slice(&chunk);
        let _ = on_event.send(UpdateProgressEvent::Progress {
            chunk_length: chunk.len(),
        });
    }
    let actual_checksum = format!("{:x}", hasher.finalize());
    if actual_checksum != expected_checksum {
        return Err("Android 更新包 SHA-256 校验失败。".to_string());
    }
    fs::write(&temporary, bytes).map_err(|_| "Android 更新包写入失败。".to_string())?;
    let _ = fs::remove_file(&target);
    fs::rename(&temporary, &target).map_err(|_| "Android 更新包落盘失败。".to_string())?;
    Ok(target)
}

#[cfg(target_os = "android")]
async fn download_checksum(client: &reqwest::Client, url: &Url) -> Result<String, String> {
    let response = client
        .get(url.clone())
        .header(USER_AGENT, "OhMyCine-Player-Android-Updater")
        .send()
        .await
        .map_err(|_| "无法下载 Android 更新包校验文件。".to_string())?;
    validate_download_response(&response, MAX_CHECKSUM_BYTES, "Android 校验文件")?;
    let bytes = response
        .bytes()
        .await
        .map_err(|_| "无法读取 Android 更新包校验文件。".to_string())?;
    if bytes.len() > MAX_CHECKSUM_BYTES {
        return Err("Android 更新包校验文件过大。".to_string());
    }
    let text =
        std::str::from_utf8(&bytes).map_err(|_| "Android 更新包校验文件格式无效。".to_string())?;
    let checksum = text
        .split_whitespace()
        .next()
        .unwrap_or_default()
        .to_ascii_lowercase();
    if checksum.len() != 64 || !checksum.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("Android 更新包校验值无效。".to_string());
    }
    Ok(checksum)
}

#[cfg(target_os = "android")]
fn validate_download_response(
    response: &reqwest::Response,
    max_bytes: usize,
    label: &str,
) -> Result<(), String> {
    if !response.status().is_success() {
        return Err(format!(
            "{label}下载返回 HTTP {}。",
            response.status().as_u16()
        ));
    }
    if response
        .content_length()
        .is_some_and(|length| length > max_bytes as u64)
    {
        return Err(format!("{label}超过允许大小。"));
    }
    if !is_allowed_download_url(response.url()) {
        return Err(format!("{label}被重定向到不受信任的地址。"));
    }
    Ok(())
}

#[cfg(target_os = "android")]
fn release_download_client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .timeout(Duration::from_secs(120))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 4 || !is_allowed_download_url(attempt.url()) {
                attempt.stop()
            } else {
                attempt.follow()
            }
        }))
        .build()
        .map_err(|_| "无法初始化 Android 更新下载器。".to_string())
}

#[cfg(target_os = "android")]
fn is_allowed_download_url(url: &Url) -> bool {
    url.scheme() == "https"
        && url.username().is_empty()
        && url.password().is_none()
        && matches!(
            url.host_str(),
            Some("github.com")
                | Some("release-assets.githubusercontent.com")
                | Some("objects.githubusercontent.com")
        )
}

#[cfg(not(target_os = "android"))]
async fn resolve_update_manifest(channel: UpdateChannel) -> Result<Option<Url>, String> {
    let Some(release) = fetch_latest_release(channel).await? else {
        return Ok(None);
    };
    let asset = release
        .assets
        .iter()
        .find(|asset| asset.name == UPDATE_MANIFEST_ASSET)
        .ok_or_else(|| "最新发布缺少签名更新清单。".to_string())?;
    validate_release_asset_url(
        &asset.browser_download_url,
        &release.tag_name,
        UPDATE_MANIFEST_ASSET,
    )
    .map(Some)
}

async fn fetch_latest_release(channel: UpdateChannel) -> Result<Option<GithubRelease>, String> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| "无法初始化 GitHub 更新客户端。".to_string())?;
    let response = client
        .get(GITHUB_RELEASES_API)
        .header(USER_AGENT, "OhMyCine-Player-Updater")
        .header(ACCEPT, "application/vnd.github+json")
        .send()
        .await
        .map_err(|error| {
            if error.is_timeout() {
                "GitHub 更新检查超时。".to_string()
            } else {
                "无法连接 GitHub Releases。".to_string()
            }
        })?;
    let status = response.status();
    if response
        .content_length()
        .is_some_and(|length| length > MAX_RELEASE_RESPONSE_BYTES as u64)
    {
        return Err("GitHub Releases 响应过大。".to_string());
    }
    let bytes = response
        .bytes()
        .await
        .map_err(|_| "无法读取 GitHub Releases 响应。".to_string())?;
    if bytes.len() > MAX_RELEASE_RESPONSE_BYTES {
        return Err("GitHub Releases 响应过大。".to_string());
    }
    if !status.is_success() {
        return Err(github_status_message(status));
    }

    let releases: Vec<GithubRelease> = serde_json::from_slice(&bytes)
        .map_err(|_| "GitHub Releases 返回了无法解析的数据。".to_string())?;
    let release = releases
        .into_iter()
        .find(|release| release_matches_channel(release, channel));
    if let Some(release) = release.as_ref() {
        if !is_safe_release_tag(&release.tag_name) {
            return Err("GitHub Release 版本标签无效。".to_string());
        }
    }
    Ok(release)
}

fn validate_release_asset_url(
    value: &str,
    tag_name: &str,
    asset_name: &str,
) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "更新资源地址无效。".to_string())?;
    let expected_path = format!("{GITHUB_RELEASE_ASSET_PREFIX}{tag_name}/{asset_name}");
    let valid = url.scheme() == "https"
        && url.host_str() == Some("github.com")
        && url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
        && url.path() == expected_path;
    if !valid {
        return Err("更新资源不属于受信任的 OhMyCine GitHub Release。".to_string());
    }
    Ok(url)
}

fn is_safe_release_tag(value: &str) -> bool {
    release_version(value).is_ok()
}

fn release_version(value: &str) -> Result<String, String> {
    let version = value.trim().strip_prefix('v').unwrap_or(value.trim());
    let parts = version.split('.').collect::<Vec<_>>();
    if parts.len() == 3
        && parts
            .iter()
            .all(|part| !part.is_empty() && part.bytes().all(|byte| byte.is_ascii_digit()))
    {
        Ok(version.to_string())
    } else {
        Err("GitHub Release 版本标签无效。".to_string())
    }
}

#[cfg(any(target_os = "android", test))]
fn is_newer_version(candidate: &str, current: &str) -> bool {
    version_tuple(candidate) > version_tuple(current)
}

#[cfg(any(target_os = "android", test))]
fn version_tuple(value: &str) -> (u64, u64, u64) {
    let mut parts = value.trim().trim_start_matches('v').split('.');
    (
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
        parts.next().and_then(|part| part.parse().ok()).unwrap_or(0),
    )
}

fn release_matches_channel(release: &GithubRelease, channel: UpdateChannel) -> bool {
    match channel {
        UpdateChannel::Beta => !release.draft,
        UpdateChannel::Stable => !release.draft && !release.prerelease,
    }
}

fn clear_pending_update(pending: &PendingUpdate) -> Result<(), String> {
    *pending
        .desktop
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())? = None;
    #[cfg(target_os = "android")]
    {
        *pending
            .android
            .lock()
            .map_err(|_| "更新状态暂不可用。".to_string())? = None;
    }
    Ok(())
}

fn github_status_message(status: StatusCode) -> String {
    match status {
        StatusCode::FORBIDDEN | StatusCode::TOO_MANY_REQUESTS => {
            "GitHub 更新检查频率受限，请稍后重试。".to_string()
        }
        _ => format!("GitHub Releases 返回 HTTP {}。", status.as_u16()),
    }
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

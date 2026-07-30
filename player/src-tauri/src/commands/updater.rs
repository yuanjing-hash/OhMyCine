use crate::storage::{self, StorageMode};
use reqwest::header::{ACCEPT, USER_AGENT};
use reqwest::{StatusCode, Url};
use serde::{Deserialize, Serialize};
use std::sync::Mutex;
use std::time::Duration;
use tauri::{ipc::Channel, AppHandle, State};
use tauri_plugin_updater::{Update, UpdaterExt};

const GITHUB_RELEASES_API: &str =
    "https://api.github.com/repos/yuanjing-hash/OhMyCine/releases?per_page=30";
const GITHUB_RELEASE_ASSET_PREFIX: &str = "/yuanjing-hash/OhMyCine/releases/download/";
const UPDATE_MANIFEST_ASSET: &str = "latest.json";
const HTTP_TIMEOUT_SECONDS: u64 = 20;
const MAX_RELEASE_RESPONSE_BYTES: usize = 1024 * 1024;

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum UpdateChannel {
    Beta,
    Stable,
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
    assets: Vec<GithubReleaseAsset>,
}

#[derive(Deserialize)]
struct GithubReleaseAsset {
    name: String,
    browser_download_url: String,
}

#[derive(Default)]
pub struct PendingUpdate(Mutex<Option<Update>>);

#[tauri::command]
pub async fn player_check_for_updates(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
    channel: UpdateChannel,
) -> Result<UpdateCheckResult, String> {
    clear_pending_update(&pending)?;
    let current_version = app.package_info().version.to_string();
    let Some(endpoint) = resolve_update_manifest(channel).await? else {
        return Ok(UpdateCheckResult {
            available: false,
            current_version,
            version: None,
            date: None,
            body: None,
            channel,
        });
    };

    let mut builder = app
        .updater_builder()
        .endpoints(vec![endpoint])
        .map_err(|_| "无法配置更新清单地址。".to_string())?
        .timeout(Duration::from_secs(HTTP_TIMEOUT_SECONDS));

    let layout = storage::initialize(&app)?;
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
        return Ok(UpdateCheckResult {
            available: false,
            current_version,
            version: None,
            date: None,
            body: None,
            channel,
        });
    };

    let result = UpdateCheckResult {
        available: true,
        current_version: update.current_version.clone(),
        version: Some(update.version.clone()),
        date: update.date.map(|date| date.to_string()),
        body: update.body.clone(),
        channel,
    };
    *pending
        .0
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())? = Some(update);
    Ok(result)
}

#[tauri::command]
pub async fn player_install_update(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
    on_event: Channel<UpdateProgressEvent>,
) -> Result<(), String> {
    let update = pending
        .0
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

async fn resolve_update_manifest(channel: UpdateChannel) -> Result<Option<Url>, String> {
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
    let Some(release) = release else {
        return Ok(None);
    };
    if !is_safe_release_tag(&release.tag_name) {
        return Err("GitHub Release 版本标签无效。".to_string());
    }

    let asset = release
        .assets
        .into_iter()
        .find(|asset| asset.name == UPDATE_MANIFEST_ASSET)
        .ok_or_else(|| "最新发布缺少签名更新清单。".to_string())?;
    validate_manifest_asset_url(&asset.browser_download_url, &release.tag_name).map(Some)
}

fn validate_manifest_asset_url(value: &str, tag_name: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "更新清单地址无效。".to_string())?;
    let expected_path = format!("{GITHUB_RELEASE_ASSET_PREFIX}{tag_name}/{UPDATE_MANIFEST_ASSET}");
    let valid = url.scheme() == "https"
        && url.host_str() == Some("github.com")
        && url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
        && url.path() == expected_path;
    if !valid {
        return Err("更新清单不属于受信任的 OhMyCine GitHub Release。".to_string());
    }
    Ok(url)
}

fn is_safe_release_tag(value: &str) -> bool {
    let version = value.trim().strip_prefix('v').unwrap_or(value.trim());
    let parts = version.split('.').collect::<Vec<_>>();
    parts.len() == 3
        && parts
            .iter()
            .all(|part| !part.is_empty() && part.bytes().all(|byte| byte.is_ascii_digit()))
}

fn release_matches_channel(release: &GithubRelease, channel: UpdateChannel) -> bool {
    match channel {
        UpdateChannel::Beta => !release.draft,
        UpdateChannel::Stable => !release.draft && !release.prerelease,
    }
}

fn clear_pending_update(pending: &PendingUpdate) -> Result<(), String> {
    *pending
        .0
        .lock()
        .map_err(|_| "更新状态暂不可用。".to_string())? = None;
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
        is_safe_release_tag, release_matches_channel, validate_manifest_asset_url, GithubRelease,
        UpdateChannel,
    };

    #[test]
    fn validates_release_tags() {
        assert!(is_safe_release_tag("v0.0.6"));
        assert!(is_safe_release_tag("1.2.3"));
        assert!(!is_safe_release_tag("v1.2.3-beta"));
        assert!(!is_safe_release_tag("release/1.2.3"));
    }

    #[test]
    fn only_accepts_manifest_from_official_release_assets() {
        assert!(validate_manifest_asset_url(
            "https://github.com/yuanjing-hash/OhMyCine/releases/download/v0.0.6/latest.json",
            "v0.0.6"
        )
        .is_ok());
        assert!(validate_manifest_asset_url(
            "https://github.com/attacker/OhMyCine/releases/download/v0.0.6/latest.json",
            "v0.0.6"
        )
        .is_err());
        assert!(validate_manifest_asset_url(
            "https://example.test/yuanjing-hash/OhMyCine/releases/download/v0.0.6/latest.json",
            "v0.0.6"
        )
        .is_err());
        assert!(validate_manifest_asset_url(
            "https://github.com/yuanjing-hash/OhMyCine/releases/download/v0.0.5/latest.json",
            "v0.0.6"
        )
        .is_err());
    }

    #[test]
    fn filters_beta_and_stable_channels() {
        let beta = GithubRelease {
            draft: false,
            prerelease: true,
            tag_name: "v0.0.6".to_string(),
            assets: Vec::new(),
        };
        let stable = GithubRelease {
            draft: false,
            prerelease: false,
            tag_name: "v1.0.0".to_string(),
            assets: Vec::new(),
        };
        assert!(release_matches_channel(&beta, UpdateChannel::Beta));
        assert!(release_matches_channel(&stable, UpdateChannel::Beta));
        assert!(!release_matches_channel(&beta, UpdateChannel::Stable));
        assert!(release_matches_channel(&stable, UpdateChannel::Stable));
    }
}

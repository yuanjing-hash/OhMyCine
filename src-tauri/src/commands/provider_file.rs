use crate::commands::{credential, settings};
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use reqwest::Url;
use serde::Deserialize;
use serde_json::Value;
use std::collections::HashMap;
use tauri::AppHandle;

const DATASOURCES_SETTING: &str = "ohmycine-datasources";

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct PersistedDataSource {
    id: String,
    #[serde(rename = "type")]
    source_type: String,
    url: String,
    #[serde(default)]
    extra: HashMap<String, Value>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct EmbyCredentialEnvelope {
    version: u8,
    provider: String,
    access_token: String,
}

pub(crate) struct ResolvedProviderDownload {
    pub(crate) url: Url,
    pub(crate) headers: HeaderMap,
}

pub(crate) async fn resolve_source_download(
    app: &AppHandle,
    source_id: &str,
    source_type: &str,
    item_id: &str,
    media_source_id: Option<&str>,
) -> Result<ResolvedProviderDownload, String> {
    if !matches!(source_type, "emby" | "jellyfin") {
        return Err("This data source does not expose a native download resolver.".to_string());
    }
    validate_stable_id(source_id, "Invalid data source identity.")?;
    validate_stable_id(item_id, "Invalid media identity.")?;
    let config = resolve_datasource(app, source_id, source_type)?;
    let credential_ref = config
        .extra
        .get("credentialRef")
        .and_then(Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| "The media server credentials are unavailable.".to_string())?;
    let raw = credential::read_credential_value(app, credential_ref)
        .await?
        .ok_or_else(|| "Emby/Jellyfin credentials are missing.".to_string())?;
    let envelope: EmbyCredentialEnvelope = serde_json::from_str(&raw)
        .map_err(|_| "Stored Emby/Jellyfin credentials are invalid.".to_string())?;
    if envelope.version != 1
        || envelope.provider != source_type
        || envelope.access_token.trim().is_empty()
    {
        return Err("Stored Emby/Jellyfin credentials are invalid.".to_string());
    }

    let base = Url::parse(config.url.trim())
        .map_err(|_| "The media server address is invalid.".to_string())?;
    if !matches!(base.scheme(), "http" | "https")
        || base.host_str().is_none()
        || !base.username().is_empty()
        || base.password().is_some()
        || base.query().is_some()
        || base.fragment().is_some()
    {
        return Err("The media server address is invalid.".to_string());
    }
    let mut url = base
        .join(&format!(
            "Videos/{}/stream?Static=true",
            percent_encode_segment(item_id)
        ))
        .map_err(|_| "The media server address is invalid.".to_string())?;
    if let Some(id) = media_source_id {
        validate_stable_id(id, "Invalid media source identity.")?;
        url.query_pairs_mut().append_pair("MediaSourceId", id);
    }
    let mut headers = HeaderMap::new();
    headers.insert(
        HeaderName::from_static("x-emby-token"),
        HeaderValue::from_str(&envelope.access_token)
            .map_err(|_| "Stored Emby/Jellyfin credentials are invalid.".to_string())?,
    );
    Ok(ResolvedProviderDownload { url, headers })
}

fn resolve_datasource(
    app: &AppHandle,
    source_id: &str,
    expected_type: &str,
) -> Result<PersistedDataSource, String> {
    let raw = settings::read_player_setting(app, DATASOURCES_SETTING)?
        .ok_or_else(|| "Data source configuration is unavailable.".to_string())?;
    let configs: Vec<PersistedDataSource> = serde_json::from_str(&raw)
        .map_err(|_| "Data source configuration is invalid.".to_string())?;
    let config = configs
        .into_iter()
        .find(|config| config.id == source_id)
        .ok_or_else(|| "The data source no longer exists.".to_string())?;
    if config.source_type != expected_type {
        return Err("The data source type changed; reopen the media action and retry.".to_string());
    }
    Ok(config)
}

fn validate_stable_id(value: &str, message: &str) -> Result<(), String> {
    if value.trim().is_empty() || value.len() > 4096 || value.chars().any(char::is_control) {
        return Err(message.to_string());
    }
    Ok(())
}

fn percent_encode_segment(segment: &str) -> String {
    segment
        .as_bytes()
        .iter()
        .map(|byte| match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (*byte as char).to_string()
            }
            value => format!("%{value:02X}"),
        })
        .collect()
}

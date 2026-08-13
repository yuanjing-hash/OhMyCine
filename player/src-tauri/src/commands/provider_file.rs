use crate::commands::{clouddrive2, credential, pan123, quark, settings};
use reqwest::header::{HeaderMap, HeaderName, HeaderValue, AUTHORIZATION};
use reqwest::{Client, Method, Url};
use serde::Deserialize;
use serde_json::Value;
use std::collections::HashMap;
use std::time::Duration;
use tauri::{AppHandle, Manager};

const DATASOURCES_SETTING: &str = "ohmycine-datasources";

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProviderSourceFileRequest {
    source_id: String,
    source_type: String,
    item_id: String,
}

#[derive(Debug, Deserialize)]
struct PersistedDataSource {
    id: String,
    #[serde(rename = "type")]
    source_type: String,
    url: String,
    #[serde(default)]
    extra: HashMap<String, Value>,
}

#[derive(Debug, Deserialize)]
struct WebDavCredentialEnvelope {
    version: u8,
    provider: String,
    username: String,
    password: String,
}

#[derive(Debug, Deserialize)]
struct AlistCredentialEnvelope {
    version: u8,
    provider: String,
    token: String,
}

#[derive(Debug, Deserialize)]
struct QuarkCredentialEnvelope {
    version: u8,
    provider: String,
    cookie: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Pan123CredentialEnvelope {
    version: u8,
    provider: String,
    access_token: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct CloudDrive2CredentialEnvelope {
    version: u8,
    provider: String,
    api_token: String,
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
    validate_stable_id(source_id, "Invalid data source identity.")?;
    let config = resolve_datasource(app, source_id, source_type)?;
    match source_type {
        "clouddrive2" => {
            let root = extra_string(&config, "rootPath")?;
            let path = normalized_provider_path(item_id)?;
            validate_item_path(&path, &root)?;
            let envelope: CloudDrive2CredentialEnvelope =
                credential_envelope(app, &config, "CloudDrive2").await?;
            if envelope.version != 2
                || envelope.provider != "clouddrive2"
                || envelope.api_token.trim().is_empty()
            {
                return Err("Stored CloudDrive2 credentials are invalid.".to_string());
            }
            let state = app.state::<clouddrive2::CloudDrive2GrpcState>();
            let stream = clouddrive2::resolve_download_stream(
                &state,
                &config.url,
                &envelope.api_token,
                &path,
            )
            .await?;
            resolved_download(&stream.url, stream.headers)
        }
        "webdav" => {
            let root = extra_string(&config, "rootPath")?;
            let path = normalized_provider_path(item_id)?;
            validate_item_path(&path, &root)?;
            let envelope: WebDavCredentialEnvelope =
                credential_envelope(app, &config, "WebDAV").await?;
            if envelope.version != 1
                || envelope.provider != "webdav"
                || envelope.username.trim().is_empty()
                || envelope.password.is_empty()
            {
                return Err("Stored WebDAV credentials are invalid.".to_string());
            }
            let url = build_provider_url(&config.url, &path)?;
            let basic = base64::Engine::encode(
                &base64::engine::general_purpose::STANDARD,
                format!("{}:{}", envelope.username, envelope.password),
            );
            let mut headers = HeaderMap::new();
            headers.insert(
                AUTHORIZATION,
                HeaderValue::from_str(&format!("Basic {basic}"))
                    .map_err(|_| "Stored WebDAV credentials are invalid.".to_string())?,
            );
            Ok(ResolvedProviderDownload { url, headers })
        }
        "123" => {
            let root = extra_string(&config, "rootPath")?;
            let path = normalized_provider_path(item_id)?;
            validate_item_path(&path, &root)?;
            let envelope: Pan123CredentialEnvelope =
                credential_envelope(app, &config, "123 Pan").await?;
            if envelope.version != 1
                || envelope.provider != "123"
                || envelope.access_token.trim().is_empty()
            {
                return Err("Stored 123 Pan credentials are invalid.".to_string());
            }
            let stream = pan123::resolve_download_stream(
                envelope.access_token,
                envelope.username,
                envelope.password,
                root,
                path,
            )
            .await?;
            resolved_download(&stream.url, stream.headers)
        }
        "quark" => {
            let root = extra_string(&config, "rootPath")?;
            let path = normalized_provider_path(item_id)?;
            validate_item_path(&path, &root)?;
            let envelope: QuarkCredentialEnvelope =
                credential_envelope(app, &config, "Quark").await?;
            if envelope.version != 1
                || envelope.provider != "quark"
                || envelope.cookie.trim().is_empty()
            {
                return Err("Stored Quark credentials are invalid.".to_string());
            }
            let stream = quark::resolve_download_stream(envelope.cookie, path).await?;
            resolved_download(&stream.url, stream.headers)
        }
        "emby" | "jellyfin" => {
            validate_stable_id(item_id, "Invalid media identity.")?;
            let envelope: EmbyCredentialEnvelope =
                credential_envelope(app, &config, "Emby/Jellyfin").await?;
            if envelope.version != 1
                || envelope.provider != source_type
                || envelope.access_token.trim().is_empty()
            {
                return Err("Stored Emby/Jellyfin credentials are invalid.".to_string());
            }
            let mut url = validated_base_url(&config.url)?
                .join(&format!(
                    "Videos/{}/stream?Static=true",
                    percent_encode_segment(item_id)
                ))
                .map_err(|_| "The media server address is invalid.".to_string())?;
            if let Some(media_source_id) = media_source_id {
                validate_stable_id(media_source_id, "Invalid media source identity.")?;
                url.query_pairs_mut()
                    .append_pair("MediaSourceId", media_source_id);
            }
            let mut headers = HeaderMap::new();
            headers.insert(
                HeaderName::from_static("x-emby-token"),
                HeaderValue::from_str(&envelope.access_token)
                    .map_err(|_| "Stored Emby/Jellyfin credentials are invalid.".to_string())?,
            );
            Ok(ResolvedProviderDownload { url, headers })
        }
        _ => Err("This data source does not expose a secure native download resolver.".to_string()),
    }
}

#[tauri::command]
pub async fn provider_source_file_delete(
    app: AppHandle,
    request: ProviderSourceFileRequest,
) -> Result<(), String> {
    validate_stable_id(&request.source_id, "Invalid data source identity.")?;
    let config = resolve_datasource(&app, &request.source_id, &request.source_type)?;
    let root = extra_string(&config, "rootPath")?;
    let path = normalized_provider_path(&request.item_id)?;
    validate_deletable_path(&path, &root)?;
    match request.source_type.as_str() {
        "alist" => delete_alist(&app, &config, &path).await,
        "webdav" => delete_webdav(&app, &config, &path).await,
        "123" => {
            let envelope: Pan123CredentialEnvelope = credential_envelope(&app, &config, "123 Pan").await?;
            if envelope.version != 1 || envelope.provider != "123" || envelope.access_token.trim().is_empty() {
                return Err("Stored 123 Pan credentials are invalid.".to_string());
            }
            pan123::delete_source_path(envelope.access_token, envelope.username, envelope.password, root, path).await
        }
        "quark" => {
            let envelope: QuarkCredentialEnvelope = credential_envelope(&app, &config, "Quark").await?;
            if envelope.version != 1 || envelope.provider != "quark" || envelope.cookie.trim().is_empty() {
                return Err("Stored Quark credentials are invalid.".to_string());
            }
            quark::delete_source_path(envelope.cookie, root, path).await
        }
        "clouddrive2" => Err("CloudDrive2 source deletion is unavailable because the installed gRPC contract does not expose a verified delete method.".to_string()),
        _ => Err("This data source does not expose a verified native source-file delete operation.".to_string()),
    }
}

async fn delete_alist(
    app: &AppHandle,
    config: &PersistedDataSource,
    path: &str,
) -> Result<(), String> {
    let envelope: AlistCredentialEnvelope =
        credential_envelope(app, config, "OpenList/Alist").await?;
    if envelope.version != 1 || envelope.provider != "alist" || envelope.token.trim().is_empty() {
        return Err("Stored OpenList/Alist credentials are invalid.".to_string());
    }
    let (directory, name) = path
        .rsplit_once('/')
        .ok_or_else(|| "Invalid OpenList/Alist source path.".to_string())?;
    if name.is_empty() {
        return Err("Invalid OpenList/Alist source path.".to_string());
    }
    let endpoint = validated_base_url(&config.url)?
        .join("api/fs/remove")
        .map_err(|_| "The OpenList/Alist address is invalid.".to_string())?;
    let response = controlled_client()?
        .post(endpoint)
        .header(AUTHORIZATION, &envelope.token)
        .json(&serde_json::json!({ "dir": if directory.is_empty() { "/" } else { directory }, "names": [name] }))
        .send()
        .await
        .map_err(|_| "OpenList/Alist source deletion could not reach the configured server.".to_string())?;
    if matches!(response.status().as_u16(), 401 | 403) {
        return Err(
            "OpenList/Alist credentials do not have source deletion permission.".to_string(),
        );
    }
    let value: Value = response
        .json()
        .await
        .map_err(|_| "OpenList/Alist returned an invalid deletion response.".to_string())?;
    if value.get("code").and_then(Value::as_i64) == Some(200) {
        Ok(())
    } else {
        Err("OpenList/Alist rejected the source deletion request.".to_string())
    }
}

async fn delete_webdav(
    app: &AppHandle,
    config: &PersistedDataSource,
    path: &str,
) -> Result<(), String> {
    let envelope: WebDavCredentialEnvelope = credential_envelope(app, config, "WebDAV").await?;
    if envelope.version != 1
        || envelope.provider != "webdav"
        || envelope.username.trim().is_empty()
        || envelope.password.is_empty()
    {
        return Err("Stored WebDAV credentials are invalid.".to_string());
    }
    let url = build_provider_url(&config.url, path)?;
    let response = controlled_client()?
        .request(Method::DELETE, url)
        .basic_auth(envelope.username, Some(envelope.password))
        .send()
        .await
        .map_err(|_| "WebDAV source deletion could not reach the configured server.".to_string())?;
    if response.status().is_success() || response.status().as_u16() == 204 {
        return Ok(());
    }
    if matches!(response.status().as_u16(), 401 | 403) {
        return Err(
            "WebDAV credentials do not have permission to delete this source item.".to_string(),
        );
    }
    if response.status().as_u16() == 404 {
        return Err("The WebDAV source item no longer exists.".to_string());
    }
    Err(format!(
        "WebDAV source deletion failed (HTTP {}).",
        response.status().as_u16()
    ))
}

async fn credential_envelope<T: for<'de> Deserialize<'de>>(
    app: &AppHandle,
    config: &PersistedDataSource,
    label: &str,
) -> Result<T, String> {
    let credential_ref = extra_string(config, "credentialRef")?;
    let raw = credential::read_credential_value(app, &credential_ref)
        .await?
        .ok_or_else(|| format!("{label} credentials are missing."))?;
    serde_json::from_str(&raw).map_err(|_| format!("Stored {label} credentials are invalid."))
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

fn extra_string(config: &PersistedDataSource, key: &str) -> Result<String, String> {
    config
        .extra
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
        .ok_or_else(|| "The data source configuration is incomplete.".to_string())
}

fn validate_stable_id(value: &str, message: &str) -> Result<(), String> {
    if value.trim().is_empty() || value.len() > 4096 || value.chars().any(char::is_control) {
        return Err(message.to_string());
    }
    Ok(())
}

fn normalized_provider_path(value: &str) -> Result<String, String> {
    let value = value.trim().replace('\\', "/");
    if value.is_empty()
        || value.len() > 4096
        || value.contains('?')
        || value.contains('#')
        || value.contains("://")
        || value.chars().any(char::is_control)
    {
        return Err("Invalid provider media identity.".to_string());
    }
    let mut segments = Vec::new();
    for segment in value.split('/').filter(|segment| !segment.is_empty()) {
        let lower = segment.to_ascii_lowercase();
        if matches!(segment, "." | "..")
            || matches!(
                lower.as_str(),
                "%2e" | ".%2e" | "%2e." | "%2e%2e" | "%252e" | "%252e%252e"
            )
        {
            return Err("Invalid provider media identity.".to_string());
        }
        segments.push(segment);
    }
    Ok(format!("/{}", segments.join("/")))
}

fn validate_deletable_path(path: &str, root: &str) -> Result<(), String> {
    let root = normalized_provider_path(root)?;
    if path == "/" || path == root || !(root == "/" || path.starts_with(&format!("{root}/"))) {
        return Err(
            "Source deletion is restricted to an item below the configured root.".to_string(),
        );
    }
    Ok(())
}

fn validate_item_path(path: &str, root: &str) -> Result<(), String> {
    let root = normalized_provider_path(root)?;
    if !(root == "/" || path == root || path.starts_with(&format!("{root}/"))) {
        return Err("The media item is outside the configured provider root.".to_string());
    }
    Ok(())
}

fn build_provider_url(base: &str, path: &str) -> Result<Url, String> {
    let mut url = validated_base_url(base)?;
    let suffix = path
        .split('/')
        .filter(|segment| !segment.is_empty())
        .map(percent_encode_segment)
        .collect::<Vec<_>>()
        .join("/");
    url.set_path(&format!("{}/{}", url.path().trim_end_matches('/'), suffix));
    Ok(url)
}

fn validated_base_url(base: &str) -> Result<Url, String> {
    let url =
        Url::parse(base.trim()).map_err(|_| "The provider address is invalid.".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("The provider address is invalid.".to_string());
    }
    Ok(url)
}

fn resolved_download(
    url: &str,
    values: HashMap<String, String>,
) -> Result<ResolvedProviderDownload, String> {
    let url = Url::parse(url.trim())
        .map_err(|_| "The provider returned an invalid download address.".to_string())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("The provider returned an invalid download address.".to_string());
    }
    let mut headers = HeaderMap::new();
    for (name, value) in values {
        if !matches!(
            name.to_ascii_lowercase().as_str(),
            "authorization" | "cookie" | "referer" | "user-agent" | "x-urlp"
        ) {
            continue;
        }
        let name = HeaderName::from_bytes(name.as_bytes())
            .map_err(|_| "The provider returned invalid download headers.".to_string())?;
        let value = HeaderValue::from_str(&value)
            .map_err(|_| "The provider returned invalid download headers.".to_string())?;
        headers.insert(name, value);
    }
    Ok(ResolvedProviderDownload { url, headers })
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

fn controlled_client() -> Result<Client, String> {
    Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(15))
        .timeout(Duration::from_secs(60))
        .build()
        .map_err(|_| "Failed to initialize the provider file client.".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_root_and_traversal_deletes() {
        assert!(validate_deletable_path("/Media/movie.mkv", "/Media").is_ok());
        assert!(validate_deletable_path("/Media", "/Media").is_err());
        assert!(normalized_provider_path("/Media/../secret").is_err());
        assert!(normalized_provider_path("/Media/%2e%2e/secret").is_err());
    }

    #[test]
    fn webdav_url_encodes_each_provider_segment() {
        let url = build_provider_url("https://dav.example.test/root", "/电影/A B.mkv").unwrap();
        assert_eq!(
            url.as_str(),
            "https://dav.example.test/root/%E7%94%B5%E5%BD%B1/A%20B.mkv"
        );
    }
}

use base64::{engine::general_purpose::STANDARD, Engine as _};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    AppHandle, Manager, Wry,
};

const PLUGIN_IDENTIFIER: &str = "com.ohmycine.player.downloads";
const PLUGIN_CLASS: &str = "DownloadPlugin";

#[derive(Clone)]
pub struct AndroidDownloadState {
    handle: PluginHandle<Wry>,
}

impl AndroidDownloadState {
    async fn run<T: DeserializeOwned>(
        &self,
        command: &str,
        payload: impl Serialize,
    ) -> Result<T, String> {
        self.handle
            .run_mobile_plugin_async(command, payload)
            .await
            .map_err(|error| format!("Android 下载存储不可用：{error}"))
    }
}

pub fn init_android() -> TauriPlugin<Wry> {
    Builder::new("downloads-android")
        .setup(|app, api| {
            let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, PLUGIN_CLASS)?;
            app.manage(AndroidDownloadState { handle });
            Ok(())
        })
        .build()
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PickedDirectory {
    pub cancelled: bool,
    pub uri: Option<String>,
    pub name: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PreparedDocument {
    pub partial_uri: String,
    pub destination_name: String,
    pub existing_bytes: u64,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AvailableDocumentName {
    pub destination_name: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReadChunk {
    pub data: String,
    pub bytes_read: usize,
    pub total_bytes: Option<u64>,
    pub entity_hash: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct DirectoryPayload<'a> {
    uri: &'a str,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PreparePayload<'a> {
    directory_uri: &'a str,
    destination_name: &'a str,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct WritePayload<'a> {
    document_uri: &'a str,
    data: &'a str,
    truncate: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ReadPayload<'a> {
    root_uri: &'a str,
    path: &'a str,
    offset: u64,
    length: usize,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct FinalizePayload<'a> {
    partial_uri: &'a str,
    destination_name: &'a str,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ProgressPayload<'a> {
    task_id: &'a str,
    title: &'a str,
    downloaded: u64,
    total: Option<u64>,
    state: &'a str,
}

fn state(app: &AppHandle) -> Result<AndroidDownloadState, String> {
    app.try_state::<AndroidDownloadState>()
        .map(|value| value.inner().clone())
        .ok_or_else(|| "Android 下载组件尚未初始化。".to_string())
}

pub async fn pick_directory(app: &AppHandle) -> Result<PickedDirectory, String> {
    state(app)?.run("pickDirectory", ()).await
}

pub async fn validate_directory(app: &AppHandle, uri: &str) -> Result<String, String> {
    #[derive(Deserialize)]
    struct Response {
        name: String,
    }
    state(app)?
        .run::<Response>("validateDirectory", DirectoryPayload { uri })
        .await
        .map(|response| response.name)
}

pub async fn prepare_document(
    app: &AppHandle,
    directory_uri: &str,
    destination_name: &str,
) -> Result<PreparedDocument, String> {
    state(app)?
        .run(
            "prepareDocument",
            PreparePayload {
                directory_uri,
                destination_name,
            },
        )
        .await
}

pub async fn available_name(
    app: &AppHandle,
    directory_uri: &str,
    destination_name: &str,
) -> Result<String, String> {
    state(app)?
        .run::<AvailableDocumentName>(
            "availableName",
            PreparePayload {
                directory_uri,
                destination_name,
            },
        )
        .await
        .map(|response| response.destination_name)
}

pub async fn write_chunk(
    app: &AppHandle,
    document_uri: &str,
    bytes: &[u8],
    truncate: bool,
) -> Result<(), String> {
    let data = STANDARD.encode(bytes);
    state(app)?
        .run::<serde_json::Value>(
            "writeChunk",
            WritePayload {
                document_uri,
                data: &data,
                truncate,
            },
        )
        .await
        .map(|_| ())
}

pub async fn read_local_chunk(
    app: &AppHandle,
    root_uri: &str,
    path: &str,
    offset: u64,
    length: usize,
) -> Result<(Vec<u8>, Option<u64>, String), String> {
    let result: ReadChunk = state(app)?
        .run(
            "readLocalChunk",
            ReadPayload {
                root_uri,
                path,
                offset,
                length,
            },
        )
        .await?;
    let bytes = STANDARD
        .decode(result.data)
        .map_err(|_| "Android 本地媒体返回了无效数据。".to_string())?;
    if bytes.len() != result.bytes_read || bytes.len() > length {
        return Err("Android 本地媒体返回了无效数据。".to_string());
    }
    if result.entity_hash.len() != 64
        || !result
            .entity_hash
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit())
    {
        return Err("Android 本地媒体返回了无效实体指纹。".to_string());
    }
    Ok((bytes, result.total_bytes, result.entity_hash))
}

pub async fn finalize_document(
    app: &AppHandle,
    partial_uri: &str,
    destination_name: &str,
) -> Result<(), String> {
    state(app)?
        .run::<serde_json::Value>(
            "finalizeDocument",
            FinalizePayload {
                partial_uri,
                destination_name,
            },
        )
        .await
        .map(|_| ())
}

pub async fn notify(
    app: &AppHandle,
    task_id: &str,
    title: &str,
    downloaded: u64,
    total: Option<u64>,
    status: &str,
) {
    let command = if matches!(status, "completed" | "failed" | "cancelled") {
        "finishForeground"
    } else {
        "updateForeground"
    };
    if let Ok(state) = state(app) {
        let _ = state
            .run::<serde_json::Value>(
                command,
                ProgressPayload {
                    task_id,
                    title,
                    downloaded,
                    total,
                    state: status,
                },
            )
            .await;
    }
}

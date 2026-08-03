use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    AppHandle, Manager, State, Wry,
};

const PLUGIN_IDENTIFIER: &str = "com.ohmycine.player.localmedia";
const PLUGIN_CLASS: &str = "LocalMediaPlugin";

#[derive(Clone)]
pub struct AndroidLocalMediaState {
    handle: PluginHandle<Wry>,
}

impl AndroidLocalMediaState {
    async fn run<T: DeserializeOwned>(
        &self,
        command: &str,
        payload: impl Serialize,
    ) -> Result<T, String> {
        self.handle
            .run_mobile_plugin_async(command, payload)
            .await
            .map_err(|error| format!("Android 本地媒体命令执行失败：{error}"))
    }
}

pub fn init_android() -> TauriPlugin<Wry> {
    Builder::new("local-media-android")
        .setup(|app, api| {
            let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, PLUGIN_CLASS)?;
            app.manage(AndroidLocalMediaState { handle });
            Ok(())
        })
        .build()
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LocalFileEntry {
    name: String,
    path: String,
    is_dir: bool,
    size: Option<u64>,
    modified_ms: Option<u64>,
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AndroidPickedLocalMedia {
    cancelled: bool,
    uri: Option<String>,
    name: Option<String>,
    size: Option<u64>,
    modified_ms: Option<u64>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct LocalEntryPayload {
    root_path: String,
    path: Option<String>,
}

#[tauri::command]
pub async fn local_file_pick_video(
    state: State<'_, AndroidLocalMediaState>,
) -> Result<AndroidPickedLocalMedia, String> {
    state.run("pickVideo", ()).await
}

#[tauri::command]
pub async fn local_file_pick_directory(
    state: State<'_, AndroidLocalMediaState>,
) -> Result<AndroidPickedLocalMedia, String> {
    state.run("pickDirectory", ()).await
}

#[tauri::command]
pub async fn local_file_list(
    root_path: String,
    path: Option<String>,
    state: State<'_, AndroidLocalMediaState>,
) -> Result<Vec<LocalFileEntry>, String> {
    state
        .run("list", LocalEntryPayload { root_path, path })
        .await
}

#[tauri::command]
pub async fn local_file_metadata(
    root_path: String,
    path: String,
    state: State<'_, AndroidLocalMediaState>,
) -> Result<LocalFileEntry, String> {
    state
        .run(
            "metadata",
            LocalEntryPayload {
                root_path,
                path: Some(path),
            },
        )
        .await
}

#[tauri::command]
pub async fn local_file_stream_path(
    root_path: String,
    path: String,
    state: State<'_, AndroidLocalMediaState>,
) -> Result<String, String> {
    state
        .run(
            "streamPath",
            LocalEntryPayload {
                root_path,
                path: Some(path),
            },
        )
        .await
}

#[tauri::command]
pub async fn local_file_watch_start(
    _app: AppHandle,
    _source_id: String,
    _root_path: String,
) -> Result<(), String> {
    Err("Android 文档树不支持文件系统实时监听，将继续使用增量扫描。".to_string())
}

#[tauri::command]
pub async fn local_file_watch_stop(_source_id: String) -> Result<(), String> {
    Ok(())
}

#[derive(Default)]
pub struct LocalFileWatcherState;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn serializes_android_document_tree_payload_with_desktop_command_shape() {
        let value = serde_json::to_value(LocalEntryPayload {
            root_path: "content://provider/tree/root".to_string(),
            path: Some("/Movies/Example.mkv".to_string()),
        })
        .unwrap();
        assert_eq!(value["rootPath"], "content://provider/tree/root");
        assert_eq!(value["path"], "/Movies/Example.mkv");
    }
}

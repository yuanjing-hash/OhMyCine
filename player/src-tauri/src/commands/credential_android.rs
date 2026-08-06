use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    AppHandle, Manager, Wry,
};

const PLUGIN_IDENTIFIER: &str = "com.ohmycine.player.credentials";
const PLUGIN_CLASS: &str = "CredentialPlugin";

#[derive(Clone)]
pub struct AndroidCredentialState {
    handle: PluginHandle<Wry>,
}

impl AndroidCredentialState {
    async fn run<T: DeserializeOwned>(
        &self,
        command: &str,
        payload: impl Serialize,
    ) -> Result<T, String> {
        self.handle
            .run_mobile_plugin_async(command, payload)
            .await
            .map_err(|_| "Android secure credential storage is unavailable.".to_string())
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AndroidMasterKeyResponse {
    key: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct AndroidMasterKeyPayload<'a> {
    key: &'a str,
}

pub fn init_android() -> TauriPlugin<Wry> {
    Builder::new("credentials-android")
        .setup(|app, api| {
            let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, PLUGIN_CLASS)?;
            app.manage(AndroidCredentialState { handle });
            Ok(())
        })
        .build()
}

pub async fn get_master_key(app: &AppHandle) -> Result<Option<String>, String> {
    let state = app
        .try_state::<AndroidCredentialState>()
        .ok_or_else(|| "Android secure credential storage is unavailable.".to_string())?;
    state
        .run::<AndroidMasterKeyResponse>("getMasterKey", ())
        .await
        .map(|response| response.key)
}

pub async fn create_master_key(app: &AppHandle) -> Result<String, String> {
    let state = app
        .try_state::<AndroidCredentialState>()
        .ok_or_else(|| "Android secure credential storage is unavailable.".to_string())?;
    state
        .run::<AndroidMasterKeyResponse>("createMasterKey", ())
        .await?
        .key
        .ok_or_else(|| "Android secure credential storage returned an invalid key.".to_string())
}

pub async fn store_master_key(app: &AppHandle, key: &str) -> Result<(), String> {
    let state = app
        .try_state::<AndroidCredentialState>()
        .ok_or_else(|| "Android secure credential storage is unavailable.".to_string())?;
    state
        .run::<serde_json::Value>("storeMasterKey", AndroidMasterKeyPayload { key })
        .await
        .map(|_| ())
}

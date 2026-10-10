//! Private, ephemeral Server transport plans. Never serialize these structs into tasks/events.
use super::*;
use aes_gcm::aes::{
    cipher::{BlockDecrypt, KeyInit},
    Aes128,
};
use std::io::Read;

const MAX_UNITS: usize = 8192;
const MAX_PLAN_BYTES: usize = 8 * 1024 * 1024;
const MAX_ENCRYPTED_UNIT_BYTES: u64 = 64 * 1024 * 1024;
const MAX_UNIT_BYTES: u64 = 16 * 1024 * 1024 * 1024;
const INVALID_PLAN: &str = "The online offline-download plan is invalid.";

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct NativePlan {
    version: u8,
    library_id: String,
    work_id: String,
    segment_id: String,
    version_id: String,
    variant_id: String,
    suggested_file_name: String,
    format: String,
    expires_at: u64,
    tracks: Vec<Track>,
    #[serde(default)]
    sidecars: Vec<Sidecar>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Track {
    id: String,
    kind: String,
    units: Vec<Unit>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Unit {
    id: String,
    url: String,
    #[serde(default)]
    headers: HashMap<String, String>,
    expected_bytes: Option<u64>,
    byte_range: Option<ByteRange>,
    encryption: Option<Encryption>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ByteRange {
    offset: u64,
    length: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Encryption {
    method: String,
    key_base64: String,
    iv_hex: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Sidecar {
    id: String,
    kind: String,
    url: String,
    #[serde(default)]
    headers: HashMap<String, String>,
    format: Option<String>,
    language: Option<String>,
}

pub(super) struct ServerAccess {
    base: Url,
    token: String,
    lease_until: Option<u64>,
}

impl ServerAccess {
    async fn load(app: &AppHandle, task: &DownloadTask) -> Result<Self, String> {
        let config = resolve_datasource(app, &task.source_id, "server")?;
        let base = validate_http_url(&config.url)?;
        if base.query().is_some() || base.fragment().is_some() || base.path() != "/" {
            return Err("The OhMyCine Server address is invalid.".to_string());
        }
        let credential_ref = extra_string(&config, "credentialRef")?;
        let raw = credential::read_credential_value(app, &credential_ref)
            .await?
            .ok_or_else(|| "OhMyCine Server device credentials are missing.".to_string())?;
        let envelope: ServerCredentialEnvelope = serde_json::from_str(&raw)
            .map_err(|_| "Stored OhMyCine Server credentials are invalid.".to_string())?;
        if envelope.version != 1
            || envelope.provider != "server"
            || !envelope.access_token.starts_with("omc_player_")
        {
            return Err("Stored OhMyCine Server credentials are invalid.".to_string());
        }
        Ok(Self {
            base,
            token: envelope.access_token,
            lease_until: None,
        })
    }

    fn transport(
        &self,
        library: &str,
        value: &str,
        supplied: &HashMap<String, String>,
    ) -> Result<ResolvedRemote, String> {
        if value.len() > 16_384 {
            return Err(INVALID_PLAN.to_string());
        }
        let url = self
            .base
            .join(value)
            .map_err(|_| INVALID_PLAN.to_string())?;
        let gateway = origin_key(&url) == origin_key(&self.base)
            && url.path().starts_with(&format!(
                "/api/v1/player/online-libraries/{}/offline-assets/",
                encode_component(library)
            ))
            && url.query().is_none()
            && url.fragment().is_none();
        if !gateway && (url.scheme() != "https" || url.host_str().is_none()) {
            #[cfg(not(test))]
            return Err(INVALID_PLAN.to_string());
            #[cfg(test)]
            if url.scheme() != "http" || !matches!(url.host_str(), Some("127.0.0.1" | "localhost"))
            {
                return Err(INVALID_PLAN.to_string());
            }
        }
        if !url.username().is_empty() || url.password().is_some() || url.fragment().is_some() {
            return Err(INVALID_PLAN.to_string());
        }
        // An upstream URL cannot impersonate another private Server route.
        if origin_key(&url) == origin_key(&self.base) && !gateway {
            return Err(INVALID_PLAN.to_string());
        }
        let mut headers = HeaderMap::new();
        if supplied.len() > 3 {
            return Err(INVALID_PLAN.to_string());
        }
        for (name, value) in supplied {
            let name =
                HeaderName::from_bytes(name.as_bytes()).map_err(|_| INVALID_PLAN.to_string())?;
            if !matches!(name.as_str(), "referer" | "user-agent" | "accept") || value.len() > 4096 {
                return Err(INVALID_PLAN.to_string());
            }
            if name.as_str() == "referer" {
                let referer = Url::parse(value).map_err(|_| INVALID_PLAN.to_string())?;
                if referer.scheme() != "https"
                    || referer.host_str().is_none()
                    || !referer.username().is_empty()
                    || referer.password().is_some()
                    || referer.query().is_some()
                    || referer.fragment().is_some()
                {
                    return Err(INVALID_PLAN.to_string());
                }
            }
            headers.insert(
                name,
                HeaderValue::from_str(value).map_err(|_| INVALID_PLAN.to_string())?,
            );
        }
        if gateway {
            headers.insert(
                AUTHORIZATION,
                HeaderValue::from_str(&format!("Bearer {}", self.token))
                    .map_err(|_| INVALID_PLAN.to_string())?,
            );
        }
        Ok(ResolvedRemote {
            url,
            headers,
            online: true,
            lease_until: self.lease_until,
        })
    }
}

pub(super) async fn resolve(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<(NativePlan, ServerAccess), String> {
    let identity = task
        .online_identity
        .as_ref()
        .ok_or_else(|| INVALID_PLAN.to_string())?;
    let variant = task
        .variant_id
        .as_deref()
        .ok_or_else(|| INVALID_PLAN.to_string())?;
    let mut access = ServerAccess::load(app, task).await?;
    let url = access
        .base
        .join(&format!(
            "/api/v1/player/online-libraries/{}/items/{}/offline-plan",
            encode_component(&identity.library_id),
            encode_component(&identity.work_id)
        ))
        .map_err(|_| INVALID_PLAN.to_string())?;
    let client = Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(15))
        .read_timeout(Duration::from_secs(60))
        .timeout(Duration::from_secs(60))
        .build()
        .map_err(|_| "Failed to initialize the online plan client.".to_string())?;
    let response = client.post(url).bearer_auth(&access.token).json(&serde_json::json!({
        "segmentId": identity.segment_id, "versionId": identity.version_id, "variantId": variant,
    })).send().await.map_err(|_| "The online download resolver could not be reached.".to_string())?;
    if !response.status().is_success() {
        return Err(match response.status().as_u16() {
            401 | 403 => "Online download authorization is missing or expired.",
            404 | 501 => "This Server or plugin does not support local downloads.",
            _ => "The online download resolver rejected this selection.",
        }
        .to_string());
    }
    let mut body = Vec::new();
    let mut stream = response.bytes_stream();
    while let Some(next) = stream.next().await {
        let bytes =
            next.map_err(|_| "The online download resolver response was interrupted.".to_string())?;
        if body.len().saturating_add(bytes.len()) > MAX_PLAN_BYTES {
            return Err(INVALID_PLAN.to_string());
        }
        body.extend_from_slice(&bytes);
    }
    #[derive(Deserialize)]
    struct Envelope {
        code: Value,
        data: NativePlan,
    }
    let envelope: Envelope = serde_json::from_slice(&body).map_err(|_| INVALID_PLAN.to_string())?;
    if envelope.code != Value::from(0) && envelope.code != Value::from("success") {
        return Err(INVALID_PLAN.to_string());
    }
    envelope.data.validate(task, &access)?;
    access.lease_until = Some(envelope.data.expires_at);
    Ok((envelope.data, access))
}

impl NativePlan {
    fn lease_expired(&self) -> bool {
        self.expires_at <= unix_timestamp().max(0) as u64
    }

    fn validate(&self, task: &DownloadTask, access: &ServerAccess) -> Result<(), String> {
        let identity = task
            .online_identity
            .as_ref()
            .ok_or_else(|| INVALID_PLAN.to_string())?;
        if self.version != 1
            || self.library_id != identity.library_id
            || self.work_id != identity.work_id
            || self.segment_id != identity.segment_id
            || self.version_id != identity.version_id
            || task.variant_id.as_deref() != Some(self.variant_id.as_str())
            || self.suggested_file_name.is_empty()
            || self.suggested_file_name.len() > 512
            || !matches!(self.format.as_str(), "progressive" | "hls" | "dash")
            || self.lease_expired()
            || self.expires_at > unix_timestamp().max(0) as u64 + 16 * 60
            || !(1..=2).contains(&self.tracks.len())
            || self.sidecars.len() > 16
        {
            return Err(INVALID_PLAN.to_string());
        }
        let mut ids = HashSet::new();
        let mut unit_ids = HashSet::new();
        let mut units = 0;
        let mut video = 0;
        let mut audio = 0;
        for track in &self.tracks {
            validate_stable_id(&track.id, INVALID_PLAN)?;
            if !ids.insert(&track.id) || track.units.is_empty() {
                return Err(INVALID_PLAN.to_string());
            }
            match track.kind.as_str() {
                "video" => video += 1,
                "audio" => audio += 1,
                _ => return Err(INVALID_PLAN.to_string()),
            }
            for unit in &track.units {
                validate_stable_id(&unit.id, INVALID_PLAN)?;
                units += 1;
                if !unit_ids.insert(&unit.id) || units > MAX_UNITS || unit.expected_bytes == Some(0)
                {
                    return Err(INVALID_PLAN.to_string());
                }
                if let Some(range) = &unit.byte_range {
                    if range.length == 0
                        || range.offset.checked_add(range.length).is_none()
                        || unit
                            .expected_bytes
                            .is_some_and(|bytes| bytes != range.length)
                    {
                        return Err(INVALID_PLAN.to_string());
                    }
                }
                if let Some(encryption) = &unit.encryption {
                    if self.format != "hls"
                        || encryption.method != "aes-128"
                        || BASE64_STANDARD
                            .decode(&encryption.key_base64)
                            .ok()
                            .map(|key| key.len())
                            != Some(16)
                        || decode_iv(&encryption.iv_hex).is_err()
                    {
                        return Err(INVALID_PLAN.to_string());
                    }
                }
                access.transport(&self.library_id, &unit.url, &unit.headers)?;
            }
        }
        if video != 1
            || audio > 1
            || (self.format == "dash" && audio != 1)
            || (self.format == "progressive"
                && (units != 1
                    || audio != 0
                    || self.tracks[0].units[0].byte_range.is_some()
                    || self.tracks[0].units[0].encryption.is_some()))
        {
            return Err(INVALID_PLAN.to_string());
        }
        for sidecar in &self.sidecars {
            validate_stable_id(&sidecar.id, INVALID_PLAN)?;
            if !ids.insert(&sidecar.id)
                || !matches!(sidecar.kind.as_str(), "subtitle" | "danmaku")
                || sidecar.language.as_ref().is_some_and(|v| v.len() > 128)
                || sidecar.format.as_ref().is_some_and(|v| {
                    !matches!(v.as_str(), "vtt" | "srt" | "ass" | "ssa" | "xml" | "json")
                })
            {
                return Err(INVALID_PLAN.to_string());
            }
            access.transport(&self.library_id, &sidecar.url, &sidecar.headers)?;
        }
        Ok(())
    }

    fn topology(&self) -> String {
        // Keys, URLs and headers must never influence a persisted transport record.
        let tracks: Vec<Value> = self.tracks.iter().map(|track| serde_json::json!({
            "id": track.id, "kind": track.kind,
            "units": track.units.iter().map(|unit| serde_json::json!({"id": unit.id, "range": unit.byte_range,
                "bytes": unit.expected_bytes, "encryption": unit.encryption.as_ref().map(|e| (&e.method, &e.iv_hex))})).collect::<Vec<_>>()
        })).collect();
        format!(
            "{:x}",
            Sha256::digest(
                serde_json::to_vec(&(
                    &self.library_id,
                    &self.work_id,
                    &self.segment_id,
                    &self.version_id,
                    &self.variant_id,
                    &self.format,
                    tracks
                ))
                .unwrap_or_default()
            )
        )
    }
}

pub(super) async fn progressive(
    app: &AppHandle,
    task: &DownloadTask,
) -> Result<ResolvedRemote, String> {
    let (plan, access) = resolve(app, task).await?;
    if plan.format != "progressive" {
        return Err(
            "The online download representation changed. Retry the exact selection.".to_string(),
        );
    }
    let unit = &plan.tracks[0].units[0];
    access.transport(&plan.library_id, &unit.url, &unit.headers)
}

pub(super) async fn execute(
    app: &AppHandle,
    task: &DownloadTask,
    control: &Arc<AtomicU8>,
) -> Result<DownloadAttachments, String> {
    let mut last: String;
    let mut retries = RefreshBudget::default();
    loop {
        ensure_running(control)?;
        let (mut plan, mut access) = await_controlled(control, resolve(app, task)).await??;
        match execute_plan(app, task, control, &plan, &access).await {
            Ok(()) => {
                let mut attachments = DownloadAttachments::default();
                if plan.lease_expired() && !plan.sidecars.is_empty() {
                    match await_controlled(control, resolve(app, task)).await? {
                        Ok((fresh_plan, fresh_access)) => {
                            plan = fresh_plan;
                            access = fresh_access;
                        }
                        Err(_) => {
                            attachments
                                .failed_kinds
                                .extend(plan.sidecars.iter().map(|sidecar| sidecar.kind.clone()));
                            return Ok(attachments);
                        }
                    }
                }
                for sidecar in &plan.sidecars {
                    ensure_running(control)?;
                    if plan.lease_expired() {
                        attachments.failed_kinds.push(sidecar.kind.clone());
                        continue;
                    }
                    match fetch_sidecar(
                        &access,
                        &plan.library_id,
                        sidecar,
                        control,
                        plan.expires_at,
                    )
                    .await
                    {
                        Ok(input) => attachments.attachments.push(input),
                        Err(_) => attachments.failed_kinds.push(sidecar.kind.clone()),
                    }
                }
                return Ok(attachments);
            }
            Err(error) => last = error,
        }
        ensure_running(control)?;
        let completed_units = DownloadStorage::open(app)?
            .conn
            .query_row(
                "SELECT COUNT(*) FROM download_online_units WHERE task_id=?1",
                [&task.id],
                |row| row.get::<_, u32>(0),
            )
            .map_err(|_| "Failed to read online progress.".to_string())?;
        if !retries.refresh_after_failure(completed_units) {
            break;
        }
        tokio::time::sleep(Duration::from_millis(
            350 * (1 << retries.consecutive_failures.saturating_sub(1)),
        ))
        .await;
    }
    Err(last)
}

#[derive(Default)]
struct RefreshBudget {
    completed_high_water: u32,
    consecutive_failures: u32,
    refreshes: u32,
}

impl RefreshBudget {
    fn refresh_after_failure(&mut self, completed_units: u32) -> bool {
        // Ordinary expiring leases may occur repeatedly during a long VOD. Forward
        // progress resets only the consecutive failure budget; the total stays bounded.
        self.consecutive_failures = if completed_units > self.completed_high_water {
            self.completed_high_water = completed_units;
            1
        } else {
            self.consecutive_failures + 1
        };
        self.refreshes += 1;
        self.consecutive_failures < 3 && self.refreshes <= 64
    }
}

async fn execute_plan(
    app: &AppHandle,
    task: &DownloadTask,
    control: &Arc<AtomicU8>,
    plan: &NativePlan,
    access: &ServerAccess,
) -> Result<(), String> {
    if plan.format == "progressive" {
        #[cfg(target_os = "android")]
        return execute_android_task(app, task, control).await;
        #[cfg(not(target_os = "android"))]
        {
            let root = validate_destination_directory(&task.destination_directory)?;
            let final_path = safe_destination_path(&root, &task.destination_name)?;
            let partial = partial_path(&final_path)?;
            if final_path.exists() {
                return Err("The destination file already exists.".to_string());
            }
            execute_remote_download(app, task, &partial, control).await?;
            ensure_running(control)?;
            DownloadStorage::open(app)?.set_transfer_state(&task.id, "finalizing", 0)?;
            fs::rename(partial, final_path)
                .map_err(|_| "Failed to finalize the downloaded file atomically.".to_string())?;
            return Ok(());
        }
    }
    let root = work_directory(app, &task.id, true)?;
    let topology = plan.topology();
    initialize_units(app, task, &root, &topology)?;
    let mut ordinal = 0_u32;
    let mut completed = 0_u64;
    let mut track_paths = Vec::new();
    for track in &plan.tracks {
        let track_path = root.join(format!("{}.track", track.kind));
        check_file(&track_path)?;
        let mut track_file = OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(true)
            .open(&track_path)
            .map_err(|_| "Failed to prepare the online track.".to_string())?;
        for unit in &track.units {
            ensure_running(control)?;
            if plan.lease_expired() {
                return Err(
                    "The online media lease expired; resolve the exact selection again."
                        .to_string(),
                );
            }
            let path = root.join(format!("{ordinal:04}.unit"));
            check_file(&path)?;
            let remote = access.transport(&plan.library_id, &unit.url, &unit.headers)?;
            let checkpoint = unit_checkpoint(app, &task.id, ordinal)?;
            let reusable = match checkpoint {
                Some(ref checkpoint)
                    if checkpoint.topology == topology
                        && checkpoint.bytes == partial_len(&path) =>
                {
                    file_hash_controlled(&path, control).await?.as_str() == checkpoint.hash
                        && checkpoint.entity.as_ref().is_some()
                        && await_controlled(control, probe_entity(&remote, unit, plan.expires_at))
                            .await??
                            .as_ref()
                            == checkpoint.entity.as_ref()
                }
                _ => false,
            };
            if !reusable {
                let entity = fetch_unit(
                    Some((app, &task.id, completed)),
                    &remote,
                    unit,
                    &path,
                    control,
                    plan.expires_at,
                )
                .await?;
                if let Some(encryption) = &unit.encryption {
                    decrypt_unit(&path, encryption)?;
                }
                let bytes = partial_len(&path);
                save_unit(
                    app,
                    &task.id,
                    ordinal,
                    &topology,
                    bytes,
                    &file_hash_controlled(&path, control).await?,
                    entity.as_deref(),
                )?;
            }
            let mut input = fs::File::open(&path)
                .map_err(|_| "Failed to read the downloaded unit.".to_string())?;
            let mut buffer = vec![0_u8; 256 * 1024];
            loop {
                ensure_running(control)?;
                let read = input
                    .read(&mut buffer)
                    .map_err(|_| "Failed to read the downloaded unit.".to_string())?;
                if read == 0 {
                    break;
                }
                ensure_space(&root, read as u64)?;
                track_file
                    .write_all(&buffer[..read])
                    .map_err(|_| "Failed to assemble the online track.".to_string())?;
                tokio::task::yield_now().await;
            }
            completed = completed.saturating_add(partial_len(&path));
            persist_progress(app, &task.id, completed, None);
            ordinal += 1;
        }
        track_file
            .sync_all()
            .map_err(|_| "Failed to flush the online track.".to_string())?;
        track_paths.push((track.kind.clone(), track_path));
    }
    ensure_running(control)?;
    DownloadStorage::open(app)?.set_transfer_state(&task.id, "finalizing", 0)?;
    emit_task(app, &DownloadStorage::open(app)?, &task.id);
    let video = track_paths
        .iter()
        .find(|(kind, _)| kind == "video")
        .map(|(_, path)| path.as_path())
        .ok_or_else(|| INVALID_PLAN.to_string())?;
    let audio = track_paths
        .iter()
        .find(|(kind, _)| kind == "audio")
        .map(|(_, path)| path.as_path());
    let mut output = root.join("remux.mp4");
    check_file(&output)?;
    if super::remux::remux(app, video, audio, &output, control)
        .await
        .is_err()
    {
        ensure_running(control)?;
        if output.exists() {
            fs::remove_file(&output)
                .map_err(|_| "Failed to remove the incomplete remux.".to_string())?;
        }
        output = root.join("remux.mkv");
        check_file(&output)?;
        super::remux::remux(app, video, audio, &output, control).await?;
    }
    ensure_running(control)?;
    let completed_task = choose_container_name(
        app,
        task,
        output.extension().and_then(|v| v.to_str()).unwrap_or("mp4"),
    )
    .await?;
    let bytes = partial_len(&output);
    if bytes == 0 {
        return Err("The remux produced an empty media file.".to_string());
    }
    #[cfg(not(target_os = "android"))]
    {
        let destination = validate_destination_directory(&completed_task.destination_directory)?;
        ensure_space(&destination, bytes)?;
        let final_path = safe_destination_path(&destination, &completed_task.destination_name)?;
        let partial = partial_path(&final_path)?;
        check_file(&partial)?;
        if final_path.exists() {
            return Err("The destination file already exists.".to_string());
        }
        copy_controlled(app, &output, &partial, control).await?;
        ensure_running(control)?;
        fs::rename(partial, final_path)
            .map_err(|_| "Failed to finalize the downloaded file atomically.".to_string())?;
    }
    #[cfg(target_os = "android")]
    {
        download_android::validate_directory(app, &completed_task.destination_directory).await?;
        let document = download_android::prepare_document(
            app,
            &completed_task.destination_directory,
            &completed_task.destination_name,
        )
        .await?;
        if document.destination_name != completed_task.destination_name {
            return Err("The Android destination name changed.".to_string());
        }
        let mut input =
            fs::File::open(&output).map_err(|_| "Failed to open the remuxed media.".to_string())?;
        let mut buffer = vec![0; 256 * 1024];
        let mut truncate = true;
        loop {
            ensure_running(control)?;
            let read = input
                .read(&mut buffer)
                .map_err(|_| "Failed to read the remuxed media.".to_string())?;
            if read == 0 {
                break;
            }
            download_android::write_chunk(app, &document.partial_uri, &buffer[..read], truncate)
                .await?;
            truncate = false;
        }
        ensure_running(control)?;
        download_android::finalize_document(
            app,
            &document.partial_uri,
            &completed_task.destination_name,
        )
        .await?;
    }
    persist_progress(app, &task.id, bytes, Some(bytes));
    if let Err(error) = cleanup(app, &task.id) {
        log::warn!("Completed online download cache cleanup failed: {error}");
    }
    Ok(())
}

async fn fetch_sidecar(
    access: &ServerAccess,
    library: &str,
    sidecar: &Sidecar,
    control: &AtomicU8,
    lease_until: u64,
) -> Result<OfflineAttachmentInput, String> {
    let remote = access.transport(library, &sidecar.url, &sidecar.headers)?;
    ensure_lease(lease_until)?;
    let response = await_controlled(control, request_transport(&remote, None)).await??;
    let maximum = max_attachment_bytes(&sidecar.kind)?;
    if !response.status().is_success()
        || response
            .content_length()
            .is_some_and(|bytes| bytes > maximum as u64)
    {
        return Err("The online attachment is unavailable.".to_string());
    }
    let mut bytes = Vec::new();
    let mut stream = response.bytes_stream();
    while let Some(chunk) = await_controlled(control, stream.next()).await? {
        let chunk = chunk.map_err(|_| "The online attachment was interrupted.".to_string())?;
        if bytes.len().saturating_add(chunk.len()) > maximum {
            return Err("The online attachment exceeds its bounds.".to_string());
        }
        bytes.extend_from_slice(&chunk);
    }
    if sidecar.kind == "danmaku" {
        let value: Value = serde_json::from_slice(&bytes)
            .map_err(|_| "The online danmaku is invalid.".to_string())?;
        if let Some(comments) = value.get("comments").and_then(Value::as_array) {
            bytes = serde_json::to_vec(comments)
                .map_err(|_| "The online danmaku is invalid.".to_string())?;
        }
    }
    validate_attachment_bytes(&sidecar.kind, sidecar.format.as_deref(), &bytes)?;
    Ok(OfflineAttachmentInput {
        kind: sidecar.kind.clone(),
        data_url: Some(format!(
            "data:{};base64,{}",
            if sidecar.kind == "danmaku" {
                "application/json"
            } else {
                "text/plain"
            },
            BASE64_STANDARD.encode(bytes)
        )),
        remote_url: None,
        headers: HashMap::new(),
        extension: sidecar.format.clone(),
    })
}

async fn choose_container_name(
    app: &AppHandle,
    task: &DownloadTask,
    extension: &str,
) -> Result<DownloadTask, String> {
    let stem = Path::new(&task.destination_name)
        .file_stem()
        .and_then(|v| v.to_str())
        .unwrap_or("media");
    let name = format!("{stem}.{extension}");
    if name == task.destination_name {
        return Ok(task.clone());
    }
    #[cfg(not(target_os = "android"))]
    let name = available_destination_name(
        &validate_destination_directory(&task.destination_directory)?,
        &name,
    )?;
    #[cfg(target_os = "android")]
    let name = download_android::available_name(app, &task.destination_directory, &name).await?;
    let stored = DownloadStorage::open(app)?;
    stored
        .conn
        .execute(
            "UPDATE download_tasks SET destination_name=?2 WHERE id=?1 AND status='finalizing'",
            params![task.id, name],
        )
        .map_err(|_| "Failed to save the offline container name.".to_string())?;
    stored
        .get(&task.id)?
        .ok_or_else(|| "Download stopped by user request.".to_string())
}

#[cfg(not(target_os = "android"))]
async fn copy_controlled(
    app: &AppHandle,
    input: &Path,
    output: &Path,
    control: &AtomicU8,
) -> Result<(), String> {
    let mut input =
        fs::File::open(input).map_err(|_| "Failed to open the remuxed media.".to_string())?;
    let mut output = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(output)
        .map_err(|_| "Failed to write the offline media.".to_string())?;
    let mut buffer = vec![0; 1024 * 1024];
    loop {
        ensure_running(control)?;
        let bytes = input
            .read(&mut buffer)
            .map_err(|_| "Failed to read the remuxed media.".to_string())?;
        if bytes == 0 {
            break;
        }
        output
            .write_all(&buffer[..bytes])
            .map_err(|_| "Failed to write the offline media.".to_string())?;
        let _ = app;
        tokio::task::yield_now().await;
    }
    output
        .sync_all()
        .map_err(|_| "Failed to flush the offline media.".to_string())
}

async fn probe_entity(
    remote: &ResolvedRemote,
    unit: &Unit,
    lease_until: u64,
) -> Result<Option<String>, String> {
    ensure_lease(lease_until)?;
    let start = unit
        .byte_range
        .as_ref()
        .map(|range| range.offset)
        .unwrap_or(0);
    let response = request_media_range(remote, Some((start, Some(start)))).await?;
    if response.status() != StatusCode::PARTIAL_CONTENT
        || parse_content_range(response.headers())
            .is_none_or(|range| range.start != start || range.end != start)
    {
        return Ok(None);
    }
    Ok(response_entity_hash(&response))
}

async fn fetch_unit(
    progress: Option<(&AppHandle, &str, u64)>,
    remote: &ResolvedRemote,
    unit: &Unit,
    path: &Path,
    control: &AtomicU8,
    lease_until: u64,
) -> Result<Option<String>, String> {
    ensure_running(control)?;
    ensure_lease(lease_until)?;
    check_file(path)?;
    let range = unit
        .byte_range
        .as_ref()
        .map(|range| (range.offset, Some(range.offset + range.length - 1)));
    let response = await_controlled(control, request_media_range(remote, range)).await??;
    if let Some(range) = &unit.byte_range {
        let coverage = parse_content_range(response.headers())
            .ok_or_else(|| "The media server did not honor the unit range.".to_string())?;
        if response.status() != StatusCode::PARTIAL_CONTENT
            || coverage.start != range.offset
            || coverage.end != range.offset + range.length - 1
        {
            return Err("The media server did not honor the unit range.".to_string());
        }
    } else if response.status() != StatusCode::OK {
        return Err("The online unit could not be downloaded.".to_string());
    }
    validate_media_content_type(&response)?;
    let entity = response_entity_hash(&response);
    let expected = unit
        .byte_range
        .as_ref()
        .map(|range| range.length)
        .or(unit.expected_bytes)
        .or(response.content_length());
    if let Some(bytes) = expected {
        ensure_space(
            path.parent().ok_or_else(|| INVALID_PLAN.to_string())?,
            bytes,
        )?;
    }
    let part = path.with_extension("part");
    check_file(&part)?;
    let mut file = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(&part)
        .map_err(|_| "Failed to create the online unit.".to_string())?;
    let mut bytes = 0_u64;
    let mut emit_at = Instant::now();
    let mut stream = response.bytes_stream();
    while let Some(next) = await_controlled(control, stream.next()).await? {
        ensure_running(control)?;
        let chunk = next.map_err(|_| "The online unit transfer was interrupted.".to_string())?;
        if let Some((app, _, _)) = progress {
            acquire_global_budget_async(app, chunk.len() as u64, control).await?;
        }
        bytes = bytes.saturating_add(chunk.len() as u64);
        if expected.is_some_and(|expected| bytes > expected)
            || bytes > MAX_UNIT_BYTES
            || (unit.encryption.is_some() && bytes > MAX_ENCRYPTED_UNIT_BYTES)
        {
            return Err("The online unit exceeds its declared bounds.".to_string());
        }
        if expected.is_none() {
            ensure_space(
                path.parent().ok_or_else(|| INVALID_PLAN.to_string())?,
                chunk.len() as u64,
            )?;
        }
        file.write_all(&chunk)
            .map_err(|_| "Failed to write the online unit.".to_string())?;
        if emit_at.elapsed() >= Duration::from_millis(250) {
            if let Some((app, task, completed)) = progress {
                persist_progress(app, task, completed + bytes, None);
            }
            emit_at = Instant::now();
        }
    }
    if bytes == 0 || expected.is_some_and(|expected| bytes != expected) {
        return Err("The online unit transfer ended before all bytes were received.".to_string());
    }
    file.sync_all()
        .map_err(|_| "Failed to flush the online unit.".to_string())?;
    drop(file);
    ensure_running(control)?;
    fs::rename(&part, path).map_err(|_| "Failed to finalize the online unit.".to_string())?;
    Ok(entity)
}

fn decode_iv(value: &str) -> Result<[u8; 16], String> {
    if value.len() != 32 || !value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(INVALID_PLAN.to_string());
    }
    let mut bytes = [0_u8; 16];
    for (index, byte) in bytes.iter_mut().enumerate() {
        *byte = u8::from_str_radix(&value[index * 2..index * 2 + 2], 16)
            .map_err(|_| INVALID_PLAN.to_string())?;
    }
    Ok(bytes)
}

fn decrypt_unit(path: &Path, encryption: &Encryption) -> Result<(), String> {
    let mut key = BASE64_STANDARD
        .decode(&encryption.key_base64)
        .map_err(|_| INVALID_PLAN.to_string())?;
    let mut iv = decode_iv(&encryption.iv_hex)?;
    let cipher = Aes128::new_from_slice(&key).map_err(|_| INVALID_PLAN.to_string())?;
    key.fill(0);
    let mut bytes = fs::read(path).map_err(|_| "Failed to read the encrypted unit.".to_string())?;
    if bytes.is_empty() || bytes.len() % 16 != 0 || bytes.len() as u64 > MAX_ENCRYPTED_UNIT_BYTES {
        return Err("The encrypted unit is invalid.".to_string());
    }
    for block in bytes.chunks_exact_mut(16) {
        let encrypted: [u8; 16] = block.try_into().map_err(|_| INVALID_PLAN.to_string())?;
        cipher.decrypt_block(block.into());
        for (index, byte) in block.iter_mut().enumerate() {
            *byte ^= iv[index];
        }
        iv = encrypted;
    }
    let padding = *bytes.last().unwrap_or(&0) as usize;
    if !(1..=16).contains(&padding)
        || bytes[bytes.len() - padding..]
            .iter()
            .any(|byte| *byte as usize != padding)
    {
        return Err("The encrypted unit has invalid AES padding.".to_string());
    }
    bytes.truncate(bytes.len() - padding);
    let mut output = OpenOptions::new()
        .write(true)
        .truncate(true)
        .open(path)
        .map_err(|_| "Failed to write the decrypted unit.".to_string())?;
    output
        .write_all(&bytes)
        .map_err(|_| "Failed to write the decrypted unit.".to_string())?;
    output
        .sync_all()
        .map_err(|_| "Failed to flush the decrypted unit.".to_string())
}

fn ensure_running(control: &AtomicU8) -> Result<(), String> {
    if control.load(Ordering::Acquire) != CONTROL_RUNNING {
        return Err("Download stopped by user request.".to_string());
    }
    Ok(())
}

fn ensure_lease(lease_until: u64) -> Result<(), String> {
    if lease_until <= unix_timestamp().max(0) as u64 {
        return Err(
            "The online media lease expired; resolve the exact selection again.".to_string(),
        );
    }
    Ok(())
}

async fn await_controlled<F: std::future::Future>(
    control: &AtomicU8,
    future: F,
) -> Result<F::Output, String> {
    ensure_running(control)?;
    tokio::select! {
        result = future => Ok(result),
        _ = async {
            while control.load(Ordering::Acquire) == CONTROL_RUNNING {
                tokio::time::sleep(Duration::from_millis(100)).await;
            }
        } => Err("Download stopped by user request.".to_string()),
    }
}

async fn file_hash_controlled(path: &Path, control: &AtomicU8) -> Result<String, String> {
    let mut input =
        fs::File::open(path).map_err(|_| "Failed to verify the online unit.".to_string())?;
    let mut digest = Sha256::new();
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        ensure_running(control)?;
        let read = input
            .read(&mut buffer)
            .map_err(|_| "Failed to verify the online unit.".to_string())?;
        if read == 0 {
            break;
        }
        digest.update(&buffer[..read]);
        tokio::task::yield_now().await;
    }
    Ok(format!("{:x}", digest.finalize()))
}

fn check_file(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if !metadata.is_file() || metadata_is_link_or_reparse(&metadata) => {
            Err("The online work file is unsafe.".to_string())
        }
        Ok(_) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err("Failed to inspect the online work file.".to_string()),
    }
}

fn work_directory(app: &AppHandle, task: &str, create: bool) -> Result<PathBuf, String> {
    validate_task_id(task)?;
    let cache = storage::initialize(app)?.cache_dir;
    let metadata = fs::symlink_metadata(&cache)
        .map_err(|_| "Failed to inspect the online cache.".to_string())?;
    if !metadata.is_dir() || metadata_is_link_or_reparse(&metadata) {
        return Err("The online cache is unsafe.".to_string());
    }
    let parent = cache.join("online-downloads");
    let path = parent.join(task);
    for directory in [&parent, &path] {
        match fs::symlink_metadata(directory) {
            Ok(metadata) if !metadata.is_dir() || metadata_is_link_or_reparse(&metadata) => {
                return Err("The online work directory is unsafe.".to_string())
            }
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound && create => {
                fs::create_dir(directory)
                    .map_err(|_| "Failed to prepare the online work directory.".to_string())?;
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err("Failed to inspect the online work directory.".to_string()),
        }
    }
    Ok(path)
}

pub(super) fn cleanup(app: &AppHandle, task: &str) -> Result<(), String> {
    let path = work_directory(app, task, false)?;
    if !path.exists() {
        return Ok(());
    }
    clear_work(&path)?;
    fs::remove_dir(path).map_err(|_| "Failed to remove the online work directory.".to_string())
}

pub(super) fn recover_orphans(app: &AppHandle) -> Result<(), String> {
    let root = storage::initialize(app)?.cache_dir.join("online-downloads");
    match fs::symlink_metadata(&root) {
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Ok(metadata) if metadata.is_dir() && !metadata_is_link_or_reparse(&metadata) => {}
        _ => return Err("The online cache is unsafe.".to_string()),
    }
    let stored = DownloadStorage::open(app)?;
    for entry in
        fs::read_dir(root).map_err(|_| "Failed to inspect online checkpoints.".to_string())?
    {
        let entry = entry.map_err(|_| "Failed to inspect online checkpoints.".to_string())?;
        let id = entry.file_name().to_string_lossy().to_string();
        validate_task_id(&id)?;
        if stored
            .get(&id)?
            .is_none_or(|task| task.status == "completed")
        {
            // Failed cleanup stays internal and retries at the next startup.
            if let Err(error) = cleanup(app, &id) {
                log::warn!("Online task work cleanup will retry: {error}");
            }
        }
    }
    Ok(())
}

fn clear_work(root: &Path) -> Result<(), String> {
    for entry in
        fs::read_dir(root).map_err(|_| "Failed to inspect online work files.".to_string())?
    {
        let entry = entry.map_err(|_| "Failed to inspect online work files.".to_string())?;
        let name = entry.file_name();
        let name = name
            .to_str()
            .ok_or_else(|| "The online work filename is unsafe.".to_string())?;
        let unit = name.split_once('.').is_some_and(|(id, extension)| {
            id.len() == 4
                && id.bytes().all(|byte| byte.is_ascii_digit())
                && matches!(extension, "unit" | "part")
        });
        if !unit
            && !matches!(
                name,
                "video.track" | "audio.track" | "remux.mp4" | "remux.mkv"
            )
        {
            return Err("The online work filename is unsafe.".to_string());
        }
        check_file(&entry.path())?;
        fs::remove_file(entry.path())
            .map_err(|_| "Failed to remove the online work file.".to_string())?;
    }
    Ok(())
}

fn initialize_units(
    app: &AppHandle,
    task: &DownloadTask,
    root: &Path,
    topology: &str,
) -> Result<(), String> {
    let stored = DownloadStorage::open(app)?;
    let previous: Option<String> = stored
        .conn
        .query_row(
            "SELECT topology FROM download_online_units WHERE task_id=?1 LIMIT 1",
            [&task.id],
            |row| row.get(0),
        )
        .optional()
        .map_err(|_| "Failed to read online checkpoints.".to_string())?;
    if previous
        .as_deref()
        .is_some_and(|previous| previous != topology)
    {
        clear_work(root)?;
        stored
            .conn
            .execute(
                "DELETE FROM download_online_units WHERE task_id=?1",
                [&task.id],
            )
            .map_err(|_| "Failed to reset online checkpoints.".to_string())?;
    }
    Ok(())
}

struct Checkpoint {
    topology: String,
    bytes: u64,
    hash: String,
    entity: Option<String>,
}

fn unit_checkpoint(
    app: &AppHandle,
    task: &str,
    ordinal: u32,
) -> Result<Option<Checkpoint>, String> {
    DownloadStorage::open(app)?.conn.query_row("SELECT topology,bytes,sha256,entity_hash FROM download_online_units WHERE task_id=?1 AND unit_index=?2", params![task, ordinal], |row| Ok(Checkpoint { topology: row.get(0)?, bytes: row.get(1)?, hash: row.get(2)?, entity: row.get(3)? })).optional().map_err(|_| "Failed to read online checkpoints.".to_string())
}

fn save_unit(
    app: &AppHandle,
    task: &str,
    ordinal: u32,
    topology: &str,
    bytes: u64,
    hash: &str,
    entity: Option<&str>,
) -> Result<(), String> {
    DownloadStorage::open(app)?.conn.execute("INSERT OR REPLACE INTO download_online_units(task_id,unit_index,topology,bytes,sha256,entity_hash) VALUES(?1,?2,?3,?4,?5,?6)", params![task, ordinal, topology, bytes, hash, entity]).map(|_| ()).map_err(|_| "Failed to save online checkpoints.".to_string())
}

fn encode_component(value: &str) -> String {
    let mut result = String::new();
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || b"-_.!~*'()".contains(&byte) {
            result.push(byte as char);
        } else {
            result.push_str(&format!("%{byte:02X}"));
        }
    }
    result
}

pub(super) fn identity_matches(item: &str, identity: &DownloadOnlineIdentity) -> bool {
    item == format!(
        "online-version|{}|{}|{}|{}",
        encode_component(&identity.library_id),
        encode_component(&identity.work_id),
        encode_component(&identity.segment_id),
        encode_component(&identity.version_id)
    )
}

pub(super) fn work_matches_version(work: &str, version: &str) -> bool {
    let decode = |value: &str| -> Option<String> {
        let mut bytes = Vec::new();
        let mut input = value.as_bytes().iter().copied();
        while let Some(byte) = input.next() {
            if byte == b'%' {
                let high = (input.next()? as char).to_digit(16)?;
                let low = (input.next()? as char).to_digit(16)?;
                bytes.push((high * 16 + low) as u8);
            } else {
                bytes.push(byte);
            }
        }
        let decoded = String::from_utf8(bytes).ok()?;
        if decoded.is_empty() || encode_component(&decoded) != value {
            return None;
        }
        Some(decoded)
    };
    let work: Vec<_> = work.split('|').collect();
    let version: Vec<_> = version.split('|').collect();
    work.len() == 3
        && work[0] == "online-work"
        && version.len() == 5
        && version[0] == "online-version"
        && decode(work[1]).is_some()
        && decode(work[2]).is_some()
        && decode(work[1]) == decode(version[1])
        && decode(work[2]) == decode(version[2])
        && decode(version[3]).is_some()
        && decode(version[4]).is_some()
}

pub(super) fn initialize_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch("CREATE TABLE IF NOT EXISTS download_online_units (
        task_id TEXT NOT NULL,unit_index INTEGER NOT NULL,topology TEXT NOT NULL,bytes INTEGER NOT NULL,
        sha256 TEXT NOT NULL,entity_hash TEXT,PRIMARY KEY(task_id,unit_index),
        FOREIGN KEY(task_id) REFERENCES download_tasks(id) ON DELETE CASCADE
    );").map_err(|_| "Failed to prepare online checkpoints.".to_string())
}

pub(super) async fn request_transport(
    remote: &ResolvedRemote,
    range: Option<(u64, Option<u64>)>,
) -> Result<Response, String> {
    let mut url = remote.url.clone();
    let mut headers = remote.headers.clone();
    let owning_origin = origin_key(&url);
    let gateway = headers.contains_key(AUTHORIZATION);
    for _ in 0..=MAX_REDIRECTS {
        if let Some(lease_until) = remote.lease_until {
            ensure_lease(lease_until)?;
        }
        let private_gateway = gateway && origin_key(&url) == owning_origin;
        let mut builder = Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(Duration::from_secs(15))
            .read_timeout(Duration::from_secs(30))
            .timeout(Duration::from_secs(30 * 60));
        if !private_gateway {
            #[cfg(not(test))]
            if url.scheme() != "https" {
                return Err("The online CDN address is unsafe.".to_string());
            }
            let host = url.host_str().ok_or_else(|| INVALID_PLAN.to_string())?;
            let addresses: Vec<_> = tokio::net::lookup_host((
                host,
                url.port_or_known_default()
                    .ok_or_else(|| INVALID_PLAN.to_string())?,
            ))
            .await
            .map_err(|_| "The online CDN could not be resolved.".to_string())?
            .collect();
            if addresses.is_empty() || addresses.iter().any(|address| !public_ip(address.ip())) {
                #[cfg(not(test))]
                return Err("The online CDN resolved to a private or reserved address.".to_string());
                #[cfg(test)]
                if addresses.is_empty()
                    || addresses.iter().any(|address| !address.ip().is_loopback())
                {
                    return Err(
                        "The online CDN resolved to a private or reserved address.".to_string()
                    );
                }
            }
            builder = builder.resolve_to_addrs(host, &addresses);
        }
        let client = builder
            .build()
            .map_err(|_| "Failed to initialize the online CDN client.".to_string())?;
        let mut request = client.get(url.clone()).headers(headers.clone());
        if let Some((start, end)) = range {
            request = request.header(
                RANGE,
                end.map(|end| format!("bytes={start}-{end}"))
                    .unwrap_or_else(|| format!("bytes={start}-")),
            );
        }
        if let Some(lease_until) = remote.lease_until {
            ensure_lease(lease_until)?;
        }
        let response = request
            .send()
            .await
            .map_err(|_| "The online CDN could not be reached.".to_string())?;
        if !response.status().is_redirection() {
            return Ok(response);
        }
        let next = url
            .join(
                response
                    .headers()
                    .get(LOCATION)
                    .and_then(|value| value.to_str().ok())
                    .ok_or_else(|| "The online CDN redirect is invalid.".to_string())?,
            )
            .map_err(|_| "The online CDN redirect is invalid.".to_string())?;
        validate_redirect(&url, &next)?;
        if origin_key(&next) != owning_origin {
            headers.clear();
        }
        url = next;
    }
    Err("The online CDN returned too many redirects.".to_string())
}

fn public_ip(address: std::net::IpAddr) -> bool {
    use std::net::IpAddr;
    match address {
        IpAddr::V4(ip) => {
            let octets = ip.octets();
            !ip.is_private()
                && !ip.is_loopback()
                && !ip.is_link_local()
                && !ip.is_multicast()
                && !ip.is_broadcast()
                && !ip.is_documentation()
                && octets[0] != 0
                && octets[0] < 240
                && !(octets[0] == 100 && (64..=127).contains(&octets[1]))
                && !(octets[0] == 198 && (18..=19).contains(&octets[1]))
                && !(octets[0] == 192 && octets[1] == 0 && octets[2] == 0)
        }
        IpAddr::V6(ip) => {
            let segments = ip.segments();
            (segments[0] & 0xe000) == 0x2000
                && segments[0] != 0x2002
                && !(segments[0] == 0x2001 && (segments[1] == 0x0db8 || segments[1] < 0x0200))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn cancellation_interrupts_an_unresponsive_network_future() {
        let control = AtomicU8::new(CONTROL_RUNNING);
        let stopped = async {
            tokio::time::sleep(Duration::from_millis(20)).await;
            control.store(CONTROL_CANCEL, Ordering::Release);
        };
        let (result, _) = tokio::join!(
            await_controlled(&control, std::future::pending::<()>()),
            stopped
        );
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn hashing_verifies_real_bytes_and_observes_cancellation_between_chunks() {
        let path = std::env::temp_dir().join(format!("ohmycine-unit-hash-{}", random_id()));
        let bytes = vec![0xa5_u8; 4 * 1024 * 1024];
        fs::write(&path, &bytes).unwrap();
        let control = AtomicU8::new(CONTROL_RUNNING);
        assert_eq!(
            file_hash_controlled(&path, &control).await.unwrap(),
            format!("{:x}", Sha256::digest(&bytes))
        );
        let hashing = file_hash_controlled(&path, &control);
        let stopping = async {
            tokio::task::yield_now().await;
            control.store(CONTROL_CANCEL, Ordering::Release);
        };
        let (result, _) = tokio::join!(hashing, stopping);
        assert!(result.is_err());
        fs::remove_file(path).unwrap();
    }

    #[test]
    fn lease_refresh_counts_consecutive_failures_and_bounds_progressing_attempts() {
        let mut budget = RefreshBudget::default();
        for completed in 1..=24 {
            assert!(budget.refresh_after_failure(completed));
        }
        assert!(budget.refresh_after_failure(24));
        assert!(!budget.refresh_after_failure(24));
        let mut bounded = RefreshBudget::default();
        for completed in 1..=64 {
            assert!(bounded.refresh_after_failure(completed));
        }
        assert!(!bounded.refresh_after_failure(65));
    }
    use aes_gcm::aes::cipher::BlockEncrypt;
    fn temporary() -> PathBuf {
        let path = std::env::temp_dir().join(format!("ohmycine-online-test-{}", random_id()));
        fs::create_dir(&path).unwrap();
        path
    }
    #[test]
    fn aes128_real_bytes_and_invalid_padding() {
        let path = temporary().join("0000.unit");
        let key = [7_u8; 16];
        let cipher = Aes128::new_from_slice(&key).unwrap();
        let clear = b"offline video unit fixture";
        let mut bytes = clear.to_vec();
        let padding = 16 - bytes.len() % 16;
        bytes.extend(std::iter::repeat_n(padding as u8, padding));
        let mut iv = [3_u8; 16];
        for block in bytes.chunks_exact_mut(16) {
            for (index, byte) in block.iter_mut().enumerate() {
                *byte ^= iv[index];
            }
            cipher.encrypt_block(block.into());
            iv.copy_from_slice(block);
        }
        fs::write(&path, &bytes).unwrap();
        let encryption = Encryption {
            method: "aes-128".to_string(),
            key_base64: BASE64_STANDARD.encode(key),
            iv_hex: "03".repeat(16),
        };
        decrypt_unit(&path, &encryption).unwrap();
        assert_eq!(fs::read(&path).unwrap(), clear);
        assert!(decode_iv(&"一".repeat(10)).is_err());
        bytes.truncate(bytes.len() - 1);
        fs::write(&path, bytes).unwrap();
        assert!(decrypt_unit(&path, &encryption).is_err());
        fs::remove_file(&path).unwrap();
        fs::remove_dir(path.parent().unwrap()).unwrap();
    }
    #[test]
    fn transport_credentials_and_reserved_ip_validation() {
        let access = ServerAccess {
            base: Url::parse("https://server.example").unwrap(),
            token: "omc_player_secret".to_string(),
            lease_until: None,
        };
        let remote = access
            .transport(
                "library",
                "/api/v1/player/online-libraries/library/offline-assets/asset",
                &HashMap::new(),
            )
            .unwrap();
        assert!(remote.headers.contains_key(AUTHORIZATION));
        let remote = access
            .transport(
                "library",
                "https://cdn.example/video?signed=ephemeral",
                &HashMap::new(),
            )
            .unwrap();
        assert!(!remote.headers.contains_key(AUTHORIZATION));
        assert!(access
            .transport(
                "library",
                "/api/v1/player/media-entries/1/stream",
                &HashMap::new()
            )
            .is_err());
        assert!(access
            .transport(
                "library",
                "https://cdn.example/video",
                &HashMap::from([("Cookie".to_string(), "secret".to_string())])
            )
            .is_err());
        assert!(access
            .transport(
                "library",
                "https://secret@cdn.example/video",
                &HashMap::new()
            )
            .is_err());
        assert!(access
            .transport(
                "library",
                "https://cdn.example/video",
                &HashMap::from([(
                    "Referer".to_string(),
                    "https://site.example/?token=secret".to_string()
                )])
            )
            .is_err());
        for ip in [
            "127.0.0.1",
            "192.168.1.1",
            "10.0.0.1",
            "100.64.0.1",
            "198.18.1.1",
            "::1",
            "fc00::1",
            "2001:db8::1",
            "::ffff:127.0.0.1",
        ] {
            assert!(!public_ip(ip.parse().unwrap()), "{ip}");
        }
        assert!(public_ip("8.8.8.8".parse().unwrap()));
        assert!(public_ip("2606:4700:4700::1111".parse().unwrap()));
    }
    #[test]
    fn topology_and_database_never_persist_transport_secrets() {
        let data = serde_json::json!({"version":1,"libraryId":"library","workId":"work","segmentId":"segment","versionId":"version","variantId":"hd","suggestedFileName":"video.mp4","format":"hls","expiresAt":unix_timestamp()+300,"tracks":[{"id":"v","kind":"video","units":[{"id":"v:1","url":"https://cdn.example/old-token","encryption":{"method":"aes-128","keyBase64":BASE64_STANDARD.encode([1_u8;16]),"ivHex":"00".repeat(16)}}]}]});
        let mut missing_expiry = data.clone();
        missing_expiry.as_object_mut().unwrap().remove("expiresAt");
        assert!(serde_json::from_value::<NativePlan>(missing_expiry).is_err());
        let mut plan: NativePlan = serde_json::from_value(data).unwrap();
        let old = plan.topology();
        plan.tracks[0].units[0].url = "https://cdn.example/new-token".to_string();
        plan.tracks[0].units[0]
            .encryption
            .as_mut()
            .unwrap()
            .key_base64 = BASE64_STANDARD.encode([2_u8; 16]);
        assert_eq!(old, plan.topology());
        plan.tracks[0].units[0].id = "v:2".to_string();
        assert_ne!(old, plan.topology());
        let conn = Connection::open_in_memory().unwrap();
        initialize_download_schema(&conn).unwrap();
        initialize_schema(&conn).unwrap();
        let columns: Vec<String> = conn
            .prepare("PRAGMA table_info(download_online_units)")
            .unwrap()
            .query_map([], |row| row.get(1))
            .unwrap()
            .collect::<Result<_, _>>()
            .unwrap();
        for forbidden in ["url", "headers", "key", "cookie", "token"] {
            assert!(!columns.iter().any(|column| column.contains(forbidden)));
        }
    }
    #[tokio::test]
    async fn http_fixture_downloads_exact_byte_range_and_rejects_ignored_range() {
        use axum::{
            body::Body,
            http::{Request, Response as HttpResponse},
            routing::get,
            Router,
        };
        let media: Vec<u8> = (0..=255).cycle().take(8192).collect();
        let fixture = media.clone();
        let app = Router::new()
            .route(
                "/media",
                get(move |request: Request<Body>| {
                    let bytes = fixture.clone();
                    async move {
                        let range = request
                            .headers()
                            .get(RANGE)
                            .and_then(|value| value.to_str().ok())
                            .and_then(|value| value.strip_prefix("bytes="))
                            .and_then(|value| value.split_once('-'))
                            .and_then(|(start, end)| {
                                Some((start.parse::<usize>().ok()?, end.parse::<usize>().ok()?))
                            });
                        match range {
                            Some((start, end)) => HttpResponse::builder()
                                .status(206)
                                .header(
                                    CONTENT_RANGE,
                                    format!("bytes {start}-{end}/{}", bytes.len()),
                                )
                                .header(ETAG, "\"fixture\"")
                                .header(CONTENT_TYPE, "application/octet-stream")
                                .body(Body::from(bytes[start..=end].to_vec()))
                                .unwrap(),
                            None => HttpResponse::builder()
                                .header(ETAG, "\"fixture\"")
                                .header(CONTENT_TYPE, "application/octet-stream")
                                .body(Body::from(bytes))
                                .unwrap(),
                        }
                    }
                }),
            )
            .route(
                "/ignored",
                get(|| async {
                    HttpResponse::builder()
                        .header(CONTENT_TYPE, "application/octet-stream")
                        .body(Body::from(vec![0_u8; 8192]))
                        .unwrap()
                }),
            );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let root = temporary();
        let path = root.join("0000.unit");
        let mut unit = Unit {
            id: "v:0".to_string(),
            url: format!("http://{address}/media"),
            headers: HashMap::new(),
            expected_bytes: Some(1024),
            byte_range: Some(ByteRange {
                offset: 512,
                length: 1024,
            }),
            encryption: None,
        };
        let remote = ResolvedRemote {
            url: Url::parse(&unit.url).unwrap(),
            headers: HeaderMap::new(),
            online: true,
            lease_until: None,
        };
        let control = AtomicU8::new(CONTROL_RUNNING);
        let current_lease = unix_timestamp() as u64 + 300;
        let entity = fetch_unit(None, &remote, &unit, &path, &control, current_lease)
            .await
            .unwrap();
        assert!(entity.is_some());
        assert_eq!(fs::read(&path).unwrap(), media[512..1536]);
        assert_eq!(
            probe_entity(&remote, &unit, current_lease).await.unwrap(),
            entity
        );
        // The old URL still serves HTTP200, but an expired plan cannot open another request.
        assert_eq!(
            request_transport(&remote, None).await.unwrap().status(),
            StatusCode::OK
        );
        let expired_remote = ResolvedRemote {
            url: remote.url.clone(),
            headers: remote.headers.clone(),
            online: true,
            lease_until: Some(unix_timestamp().max(0) as u64),
        };
        assert!(request_transport(&expired_remote, None).await.is_err());
        assert!(fetch_unit(
            None,
            &remote,
            &unit,
            &path,
            &control,
            unix_timestamp().max(0) as u64
        )
        .await
        .is_err());
        assert!(probe_entity(&remote, &unit, unix_timestamp().max(0) as u64)
            .await
            .is_err());
        assert_eq!(fs::read(&path).unwrap(), media[512..1536]);
        unit.url = format!("http://{address}/ignored");
        let ignored = ResolvedRemote {
            url: Url::parse(&unit.url).unwrap(),
            headers: HeaderMap::new(),
            online: true,
            lease_until: None,
        };
        assert!(
            fetch_unit(None, &ignored, &unit, &path, &control, current_lease)
                .await
                .is_err()
        );
        control.store(CONTROL_PAUSE, Ordering::Release);
        assert!(
            fetch_unit(None, &remote, &unit, &path, &control, current_lease)
                .await
                .is_err()
        );
        server.abort();
        clear_work(&root).unwrap();
        fs::remove_dir(root).unwrap();
    }
}

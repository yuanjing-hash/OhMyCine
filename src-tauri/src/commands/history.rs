use rusqlite::{params, params_from_iter, types::Value, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use tauri::AppHandle;

use crate::storage;

const DATABASE_FILE: &str = "playback_history.sqlite";
const MIN_RESUME_POSITION_SECONDS: f64 = 30.0;
const COMPLETED_REMAINING_SECONDS: f64 = 90.0;
const COMPLETED_PROGRESS_RATIO: f64 = 0.92;
const DEFAULT_CONTINUE_LIMIT: u32 = 20;
const MAX_CONTINUE_LIMIT: u32 = 100;
const DEFAULT_HISTORY_PAGE_SIZE: u32 = 24;
const MAX_HISTORY_PAGE_SIZE: u32 = 100;
const MAX_HISTORY_PAGE: u32 = 100_000;
const MAX_ID_LENGTH: usize = 512;
const MAX_IDENTITY_LENGTH: usize = 2048;
const MAX_TEXT_LENGTH: usize = 2048;
const NOW_MILLIS_SQL: &str = "(CAST(strftime('%s','now') AS INTEGER) * 1000 + CAST(substr(strftime('%f','now'),4,3) AS INTEGER))";
const VISIBLE_SCOPE_SQL: &str = "((owner_scope IS NULL AND NOT EXISTS (
    SELECT 1 FROM playback_history_source_scopes known
    WHERE known.source_id=playback_history.source_id)) OR EXISTS (
    SELECT 1 FROM playback_history_source_scopes current
    WHERE current.source_id=playback_history.source_id AND current.scope=playback_history.owner_scope AND current.active=1))";

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackProgressIdentity {
    source_id: String,
    media_identity: String,
    owner_scope: Option<String>,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackProgressUpsert {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    title_logo_url: Option<String>,
    display_subtitle: Option<String>,
    episode_still_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    completed: Option<bool>,
    owner_scope: Option<String>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistoryEntry {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    title_logo_url: Option<String>,
    display_subtitle: Option<String>,
    episode_still_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    progress: Option<f64>,
    updated_at: i64,
    completed: bool,
    deleted: bool,
    progress_source: String,
    owner_scope: Option<String>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistoryPage {
    list: Vec<PlaybackHistoryEntry>,
    total: u64,
    page: u32,
    page_size: u32,
    has_more: bool,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistoryMerge {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    title_logo_url: Option<String>,
    display_subtitle: Option<String>,
    episode_still_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    completed: bool,
    deleted: bool,
    updated_at: i64,
    owner_scope: Option<String>,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistoryAcknowledgment {
    source_id: String,
    media_identity: String,
    updated_at: i64,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistorySyncApplyRequest {
    source_id: String,
    scope: String,
    mapping_key: String,
    generation: u64,
    cursor: u64,
    acknowledgments: Vec<PlaybackHistoryAcknowledgment>,
    entries: Vec<PlaybackHistoryMerge>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaybackHistorySyncState {
    cursor: u64,
    generation: u64,
}

pub fn initialize_playback_history_session(app: &AppHandle) -> Result<(), String> {
    PlaybackHistoryStorage::open(app)?
        .conn
        .execute("UPDATE playback_history_source_scopes SET active=0", [])
        .map_err(|_| "Failed to initialize playback history account validation.".to_string())?;
    Ok(())
}

#[tauri::command]
pub fn player_invalidate_playback_history_sources(
    app: AppHandle,
    source_ids: Vec<String>,
) -> Result<(), String> {
    let source_ids = normalize_excluded_source_ids(Some(source_ids))?;
    let mut storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .transaction()
        .map_err(|_| "Failed to begin history account invalidation.".to_string())?;
    for source_id in source_ids {
        tx.execute(
            "UPDATE playback_history_source_scopes SET active=0 WHERE source_id=?1",
            params![source_id],
        )
        .map_err(|_| "Failed to invalidate playback history account.".to_string())?;
    }
    tx.commit()
        .map_err(|_| "Failed to persist playback history account invalidation.".to_string())
}

#[tauri::command]
pub fn player_restore_playback_history_scope(
    app: AppHandle,
    source_id: String,
    validation_key: String,
) -> Result<Option<String>, String> {
    let source_id = normalize_id(source_id, "Invalid playback source.")?;
    validate_sync_scope(&validation_key)?;
    let mut storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .transaction()
        .map_err(|_| "Failed to begin history account restore.".to_string())?;
    let scope: Option<String> = tx.query_row(
        "SELECT scope FROM playback_history_source_scopes WHERE source_id=?1 AND validation_key=?2",
        params![source_id, validation_key], |row| row.get(0),
    ).optional().map_err(|_| "Failed to restore playback history account.".to_string())?;
    // A newly written offline record has an explicit credential-context owner until bootstrap
    // validates its account. Unbound pre-existing rows are never assigned an account here.
    let scope = scope.unwrap_or_else(|| validation_key.clone());
    tx.execute(
        "INSERT INTO playback_history_source_scopes (source_id, scope, active, validation_key) VALUES (?1,?2,1,?3)
         ON CONFLICT(source_id) DO UPDATE SET scope=excluded.scope, active=1, validation_key=excluded.validation_key",
        params![source_id, scope, validation_key],
    ).map_err(|_| "Failed to reactivate playback history account.".to_string())?;
    tx.commit()
        .map_err(|_| "Failed to persist history account restore.".to_string())?;
    Ok(Some(scope))
}

#[tauri::command]
pub fn player_get_playback_history_generation(app: AppHandle) -> Result<u64, String> {
    history_generation(&PlaybackHistoryStorage::open(&app)?.conn)
}

#[tauri::command]
pub fn player_get_playback_history_sync_state(
    app: AppHandle,
    source_id: String,
    scope: String,
    mapping_key: String,
    validation_key: String,
) -> Result<PlaybackHistorySyncState, String> {
    let source_id = normalize_id(source_id, "Invalid playback source.")?;
    validate_sync_scope(&scope)?;
    validate_sync_scope(&mapping_key)?;
    validate_sync_scope(&validation_key)?;
    let mut storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .transaction()
        .map_err(|_| "Failed to begin history sync state.".to_string())?;
    let previous_scope: Option<String> = tx
        .query_row(
            "SELECT scope FROM playback_history_source_scopes WHERE source_id=?1",
            params![source_id],
            |row| row.get(0),
        )
        .optional()
        .map_err(|_| "Failed to read previous playback history account.".to_string())?;
    let account_changed = previous_scope.as_deref() != Some(&scope);
    if previous_scope.as_deref() == Some(&validation_key) && scope != validation_key {
        tx.execute(
            "UPDATE playback_history SET owner_scope=?1 WHERE source_id=?2 AND owner_scope=?3 AND progress_source='local'",
            params![scope, source_id, validation_key],
        ).map_err(|_| "Failed to bind newly saved offline playback history.".to_string())?;
    }
    tx.execute(
        "INSERT INTO playback_history_source_scopes (source_id, scope, active, validation_key) VALUES (?1, ?2, 1, ?3)
         ON CONFLICT(source_id) DO UPDATE SET scope=excluded.scope, active=1, validation_key=excluded.validation_key",
        params![source_id, scope, validation_key],
    ).map_err(|_| "Failed to bind playback history account.".to_string())?;
    tx.execute(
        "INSERT INTO playback_history_sync_state (scope, mapping_key, cursor) VALUES (?1, ?2, 0)
         ON CONFLICT(scope) DO UPDATE SET mapping_key=excluded.mapping_key,
             cursor=CASE WHEN ?3=0 AND playback_history_sync_state.mapping_key=excluded.mapping_key
                 THEN playback_history_sync_state.cursor ELSE 0 END",
        params![scope, mapping_key, if account_changed { 1 } else { 0 }],
    )
    .map_err(|_| "Failed to initialize history sync checkpoint.".to_string())?;
    let state = PlaybackHistorySyncState {
        cursor: tx
            .query_row(
                "SELECT cursor FROM playback_history_sync_state WHERE scope=?1",
                params![scope],
                |row| row.get(0),
            )
            .map_err(|_| "Failed to read history sync checkpoint.".to_string())?,
        generation: history_generation(&tx)?,
    };
    tx.commit()
        .map_err(|_| "Failed to persist history sync state.".to_string())?;
    Ok(state)
}

#[tauri::command]
pub fn player_clear_local_playback_history(app: AppHandle) -> Result<u64, String> {
    let mut storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .transaction()
        .map_err(|_| "Failed to begin local history reset.".to_string())?;
    tx.execute_batch(
        "DELETE FROM playback_history;
         DELETE FROM playback_history_sync_ack;
         DELETE FROM playback_history_sync_state;
         UPDATE playback_history_meta SET generation=generation+1 WHERE id=1;",
    )
    .map_err(|_| "Failed to clear local playback history.".to_string())?;
    let generation = history_generation(&tx)?;
    tx.commit()
        .map_err(|_| "Failed to persist local history reset.".to_string())?;
    Ok(generation)
}

#[tauri::command]
pub fn player_upsert_playback_progress(
    app: AppHandle,
    progress: PlaybackProgressUpsert,
    generation: u64,
) -> Result<PlaybackHistoryEntry, String> {
    let mut normalized = NormalizedProgress::from_payload(progress)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .unchecked_transaction()
        .map_err(|_| "Failed to begin history save.".to_string())?;
    ensure_history_generation(&tx, generation)?;
    normalized.owner_scope = resolve_owner_scope(
        &tx,
        &normalized.source_id,
        normalized.owner_scope.as_deref(),
    )?;
    storage.upsert(&normalized)?;
    let saved = storage
        .get(&PlaybackProgressIdentity {
            source_id: normalized.source_id,
            media_identity: normalized.media_identity,
            owner_scope: normalized.owner_scope,
        })?
        .ok_or_else(|| "Failed to read saved playback progress.".to_string())?;
    tx.commit()
        .map_err(|_| "Failed to persist history save.".to_string())?;
    Ok(saved)
}

#[tauri::command]
pub fn player_get_playback_progress(
    app: AppHandle,
    identity: PlaybackProgressIdentity,
) -> Result<Option<PlaybackHistoryEntry>, String> {
    let mut identity = normalize_identity(identity)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    identity.owner_scope = resolve_owner_scope(
        &storage.conn,
        &identity.source_id,
        identity.owner_scope.as_deref(),
    )?;
    storage.get(&identity)
}

#[tauri::command]
pub fn player_list_continue_watching(
    app: AppHandle,
    limit: Option<u32>,
    excluded_source_ids: Option<Vec<String>>,
) -> Result<Vec<PlaybackHistoryEntry>, String> {
    let excluded_source_ids = normalize_excluded_source_ids(excluded_source_ids)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.list_continue_watching(
        limit
            .unwrap_or(DEFAULT_CONTINUE_LIMIT)
            .min(MAX_CONTINUE_LIMIT),
        &excluded_source_ids,
    )
}

#[tauri::command]
pub fn player_list_playback_history(
    app: AppHandle,
    page: Option<u32>,
    page_size: Option<u32>,
    excluded_source_ids: Option<Vec<String>>,
) -> Result<PlaybackHistoryPage, String> {
    let excluded_source_ids = normalize_excluded_source_ids(excluded_source_ids)?;
    let page = page.unwrap_or(1).clamp(1, MAX_HISTORY_PAGE);
    let page_size = page_size
        .unwrap_or(DEFAULT_HISTORY_PAGE_SIZE)
        .clamp(1, MAX_HISTORY_PAGE_SIZE);
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.list_history(page, page_size, &excluded_source_ids)
}

#[tauri::command]
pub fn player_list_playback_history_sync(
    app: AppHandle,
    page: Option<u32>,
    page_size: Option<u32>,
    scope: Option<String>,
) -> Result<PlaybackHistoryPage, String> {
    let page = page.unwrap_or(1).clamp(1, MAX_HISTORY_PAGE);
    let page_size = page_size
        .unwrap_or(DEFAULT_HISTORY_PAGE_SIZE)
        .clamp(1, MAX_HISTORY_PAGE_SIZE);
    if let Some(scope) = scope.as_deref() {
        validate_sync_scope(scope)?;
    }
    PlaybackHistoryStorage::open(&app)?.list_pending_history(page, page_size, scope.as_deref())
}

#[tauri::command]
pub fn player_list_playback_history_tombstones(
    app: AppHandle,
    page: Option<u32>,
    page_size: Option<u32>,
) -> Result<PlaybackHistoryPage, String> {
    let page = page.unwrap_or(1).clamp(1, MAX_HISTORY_PAGE);
    let page_size = page_size
        .unwrap_or(DEFAULT_HISTORY_PAGE_SIZE)
        .clamp(1, MAX_HISTORY_PAGE_SIZE);
    PlaybackHistoryStorage::open(&app)?.list_tombstones(page, page_size)
}

#[tauri::command]
pub fn player_set_playback_completed(
    app: AppHandle,
    identity: PlaybackProgressIdentity,
    completed: bool,
    generation: u64,
) -> Result<bool, String> {
    let mut identity = normalize_identity(identity)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .unchecked_transaction()
        .map_err(|_| "Failed to begin history update.".to_string())?;
    ensure_history_generation(&tx, generation)?;
    identity.owner_scope =
        resolve_owner_scope(&tx, &identity.source_id, identity.owner_scope.as_deref())?;
    let changed = storage.set_completed(&identity, completed)?;
    tx.commit()
        .map_err(|_| "Failed to persist history update.".to_string())?;
    Ok(changed)
}

#[tauri::command]
pub fn player_remove_continue_watching(
    app: AppHandle,
    progress: PlaybackProgressUpsert,
    generation: u64,
) -> Result<PlaybackHistoryEntry, String> {
    let mut progress = NormalizedProgress::from_payload(progress)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .unchecked_transaction()
        .map_err(|_| "Failed to begin history removal.".to_string())?;
    ensure_history_generation(&tx, generation)?;
    progress.owner_scope =
        resolve_owner_scope(&tx, &progress.source_id, progress.owner_scope.as_deref())?;
    storage.tombstone(&progress)?;
    let saved = storage
        .get_any(&PlaybackProgressIdentity {
            source_id: progress.source_id,
            media_identity: progress.media_identity,
            owner_scope: progress.owner_scope,
        })?
        .ok_or_else(|| "Failed to read playback history tombstone.".to_string())?;
    tx.commit()
        .map_err(|_| "Failed to persist history removal.".to_string())?;
    Ok(saved)
}

#[tauri::command]
pub fn player_get_playback_completion_batch(
    app: AppHandle,
    identities: Vec<PlaybackProgressIdentity>,
) -> Result<Vec<PlaybackHistoryEntry>, String> {
    let storage = PlaybackHistoryStorage::open(&app)?;
    identities
        .into_iter()
        .take(500)
        .map(normalize_identity)
        .collect::<Result<Vec<_>, _>>()?
        .into_iter()
        .filter_map(|identity| storage.get(&identity).transpose())
        .collect()
}

#[tauri::command]
pub fn player_delete_playback_history_for_source(
    app: AppHandle,
    source_id: String,
) -> Result<u64, String> {
    let source_id = normalize_id(source_id, "Invalid playback source.")?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    storage.delete_for_source(&source_id)
}

#[tauri::command]
pub fn player_apply_playback_history_sync(
    app: AppHandle,
    request: PlaybackHistorySyncApplyRequest,
) -> Result<u64, String> {
    let PlaybackHistorySyncApplyRequest {
        source_id,
        scope,
        mapping_key,
        generation,
        cursor,
        acknowledgments,
        entries,
    } = request;
    if entries.len() > 500 || acknowledgments.len() > 500 {
        return Err("Too many playback history entries.".to_string());
    }
    validate_sync_scope(&scope)?;
    validate_sync_scope(&mapping_key)?;
    let storage = PlaybackHistoryStorage::open(&app)?;
    let tx = storage
        .conn
        .unchecked_transaction()
        .map_err(|_| "Failed to begin playback history merge.".to_string())?;
    ensure_history_generation(&tx, generation)?;
    let previous: Option<u64> = tx.query_row(
        "SELECT state.cursor FROM playback_history_sync_state state
         JOIN playback_history_source_scopes current ON current.scope=state.scope
         WHERE state.scope=?1 AND current.source_id=?2 AND current.active=1 AND state.mapping_key=?3",
        params![scope, source_id, mapping_key], |row| row.get(0),
    ).optional().map_err(|_| "Failed to validate playback history account.".to_string())?;
    if previous.is_none() || previous.is_some_and(|previous| cursor < previous) {
        return Err("Playback history account or checkpoint changed.".to_string());
    }
    let mut changed = 0_u64;
    for entry in entries {
        if entry.updated_at <= 0 {
            return Err("Invalid playback history timestamp.".to_string());
        }
        let updated_at = entry.updated_at;
        let deleted = entry.deleted;
        let progress = NormalizedProgress::from_payload(PlaybackProgressUpsert {
            source_id: entry.source_id,
            library_id: entry.library_id,
            item_id: entry.item_id,
            media_identity: entry.media_identity,
            title: entry.title,
            stream_identity: entry.stream_identity,
            media_type: entry.media_type,
            poster_url: entry.poster_url,
            backdrop_url: entry.backdrop_url,
            title_logo_url: entry.title_logo_url,
            display_subtitle: entry.display_subtitle,
            episode_still_url: entry.episode_still_url,
            position: entry.position,
            duration: entry.duration,
            completed: Some(entry.completed),
            owner_scope: entry.owner_scope,
        })?;
        if resolve_owner_scope(&tx, &progress.source_id, progress.owner_scope.as_deref())?
            != progress.owner_scope
        {
            return Err("Playback history import has an unconfirmed account.".to_string());
        }
        if progress.source_id == source_id && progress.owner_scope.as_deref() != Some(&scope) {
            return Err("Playback history entry belongs to a different account.".to_string());
        }
        let key = identity_key(&progress.source_id, &progress.media_identity);
        if deleted {
            changed += storage.conn.execute(
                "INSERT INTO playback_history (
                    identity_key, source_id, library_id, item_id, media_identity, title,
                    stream_identity, media_type, poster_url, backdrop_url, title_logo_url,
                    display_subtitle, episode_still_url, position, duration, completed, deleted,
                    progress_source, created_at, updated_at, owner_scope
                 ) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,0,NULL,0,1,'server',?14,?14,?15)
                 ON CONFLICT(identity_key) DO UPDATE SET
                    library_id=excluded.library_id, item_id=excluded.item_id, title=excluded.title,
                    media_type=excluded.media_type, poster_url=excluded.poster_url,
                    backdrop_url=excluded.backdrop_url, title_logo_url=excluded.title_logo_url,
                    display_subtitle=excluded.display_subtitle, episode_still_url=excluded.episode_still_url,
                    position=0, duration=NULL, completed=0, deleted=1,
                    progress_source='server', updated_at=excluded.updated_at, owner_scope=excluded.owner_scope
                 WHERE playback_history.updated_at <= excluded.updated_at
                    OR playback_history.owner_scope IS NOT excluded.owner_scope",
                params![key, progress.source_id, progress.library_id, progress.item_id,
                    progress.media_identity, progress.title, progress.stream_identity, progress.media_type,
                    progress.poster_url, progress.backdrop_url, progress.title_logo_url,
                    progress.display_subtitle, progress.episode_still_url, updated_at, progress.owner_scope],
            ).map_err(|_| "Failed to merge playback history deletion.".to_string())? as u64;
            continue;
        }
        changed += storage
            .conn
            .execute(
                "INSERT INTO playback_history (
                identity_key, source_id, library_id, item_id, media_identity, title,
                stream_identity, media_type, poster_url, backdrop_url, title_logo_url,
                display_subtitle, episode_still_url, position, duration, completed, deleted,
                progress_source, created_at, updated_at, owner_scope
             ) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15,?16,0,'server',?17,?17,?18)
             ON CONFLICT(identity_key) DO UPDATE SET
                library_id=excluded.library_id, item_id=excluded.item_id, title=excluded.title,
                stream_identity=excluded.stream_identity, media_type=excluded.media_type,
                poster_url=excluded.poster_url, backdrop_url=excluded.backdrop_url,
                title_logo_url=excluded.title_logo_url, position=excluded.position,
                display_subtitle=excluded.display_subtitle,
                episode_still_url=excluded.episode_still_url,
                duration=excluded.duration, completed=excluded.completed, deleted=0,
                progress_source='server', updated_at=excluded.updated_at, owner_scope=excluded.owner_scope
             WHERE playback_history.updated_at <= excluded.updated_at
                OR playback_history.owner_scope IS NOT excluded.owner_scope",
                params![
                    key,
                    progress.source_id,
                    progress.library_id,
                    progress.item_id,
                    progress.media_identity,
                    progress.title,
                    progress.stream_identity,
                    progress.media_type,
                    progress.poster_url,
                    progress.backdrop_url,
                    progress.title_logo_url,
                    progress.display_subtitle,
                    progress.episode_still_url,
                    progress.position,
                    progress.duration,
                    if progress.completed { 1 } else { 0 },
                    updated_at,
                    progress.owner_scope,
                ],
            )
            .map_err(|_| "Failed to merge playback history.".to_string())?
            as u64;
    }
    for acknowledgment in acknowledgments {
        let source_id = normalize_id(acknowledgment.source_id, "Invalid playback source.")?;
        let media_identity = normalize_media_identity(acknowledgment.media_identity)?;
        if acknowledgment.updated_at <= 0 {
            return Err("Invalid history acknowledgment timestamp.".to_string());
        }
        tx.execute(
            "INSERT INTO playback_history_sync_ack (scope, identity_key, updated_at) VALUES (?1,?2,?3)
             ON CONFLICT(scope, identity_key) DO UPDATE SET updated_at=MAX(updated_at,excluded.updated_at)",
            params![scope, identity_key(&source_id, &media_identity), acknowledgment.updated_at],
        ).map_err(|_| "Failed to acknowledge playback history upload.".to_string())?;
    }
    tx.execute(
        "UPDATE playback_history_sync_state SET cursor=?1 WHERE scope=?2",
        params![cursor, scope],
    )
    .map_err(|_| "Failed to advance playback history checkpoint.".to_string())?;
    tx.commit()
        .map_err(|_| "Failed to persist playback history sync.".to_string())?;
    Ok(changed)
}

struct PlaybackHistoryStorage {
    conn: Connection,
}

struct NormalizedProgress {
    source_id: String,
    library_id: Option<String>,
    item_id: Option<String>,
    media_identity: String,
    title: String,
    stream_identity: Option<String>,
    media_type: Option<String>,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    title_logo_url: Option<String>,
    display_subtitle: Option<String>,
    episode_still_url: Option<String>,
    position: f64,
    duration: Option<f64>,
    completed: bool,
    owner_scope: Option<String>,
}

impl PlaybackHistoryStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let db_path = storage::data_file(app, DATABASE_FILE)?;
        let conn = Connection::open(db_path)
            .map_err(|_| "Failed to open playback history database.".to_string())?;
        conn.busy_timeout(std::time::Duration::from_secs(5))
            .map_err(|_| "Failed to configure playback history storage.".to_string())?;
        initialize_schema(&conn)?;

        Ok(Self { conn })
    }

    fn delete_for_source(&self, source_id: &str) -> Result<u64, String> {
        let deleted = self
            .conn
            .execute(
                "DELETE FROM playback_history WHERE source_id = ?1",
                params![source_id],
            )
            .map_err(|_| "Failed to delete playback history for source.".to_string())?;
        Ok(deleted as u64)
    }

    fn set_completed(
        &self,
        identity: &PlaybackProgressIdentity,
        completed: bool,
    ) -> Result<bool, String> {
        let position_expression = if completed {
            "CASE WHEN duration IS NOT NULL AND duration > 0 THEN duration ELSE position END"
        } else {
            "0"
        };
        let changed = self
            .conn
            .execute(
                &format!(
                    "UPDATE playback_history
                     SET completed = ?1, position = {position_expression}, progress_source='local',
                         owner_scope=COALESCE(?3,owner_scope),
                         updated_at = CASE WHEN updated_at >= {NOW_MILLIS_SQL}
                             THEN updated_at + 1 ELSE {NOW_MILLIS_SQL} END
                     WHERE identity_key = ?2 AND deleted = 0 AND {VISIBLE_SCOPE_SQL}"
                ),
                params![
                    if completed { 1 } else { 0 },
                    identity_key(&identity.source_id, &identity.media_identity),
                    identity.owner_scope,
                ],
            )
            .map_err(|_| "Failed to update playback completion state.".to_string())?;
        Ok(changed > 0)
    }

    fn tombstone(&self, progress: &NormalizedProgress) -> Result<(), String> {
        self.conn.execute(
            &format!("INSERT INTO playback_history (
                identity_key, source_id, library_id, item_id, media_identity, title,
                stream_identity, media_type, poster_url, backdrop_url, title_logo_url,
                display_subtitle, episode_still_url, position, duration, completed, deleted,
                progress_source, created_at, updated_at, owner_scope
            ) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,0,NULL,0,1,'local',{NOW_MILLIS_SQL},{NOW_MILLIS_SQL},?14)
            ON CONFLICT(identity_key) DO UPDATE SET
                library_id=COALESCE(playback_history.library_id, excluded.library_id),
                item_id=COALESCE(playback_history.item_id, excluded.item_id),
                title=CASE WHEN playback_history.title<>'' THEN playback_history.title ELSE excluded.title END,
                media_type=COALESCE(playback_history.media_type, excluded.media_type),
                poster_url=COALESCE(playback_history.poster_url, excluded.poster_url),
                backdrop_url=COALESCE(playback_history.backdrop_url, excluded.backdrop_url),
                title_logo_url=COALESCE(playback_history.title_logo_url, excluded.title_logo_url),
                display_subtitle=COALESCE(playback_history.display_subtitle, excluded.display_subtitle),
                episode_still_url=COALESCE(playback_history.episode_still_url, excluded.episode_still_url),
                position=0, duration=NULL, completed=0, deleted=1, progress_source='local', owner_scope=excluded.owner_scope,
                updated_at=CASE WHEN playback_history.owner_scope IS excluded.owner_scope AND playback_history.updated_at >= {NOW_MILLIS_SQL}
                    THEN playback_history.updated_at + 1 ELSE {NOW_MILLIS_SQL} END"),
            params![identity_key(&progress.source_id, &progress.media_identity),
                progress.source_id, progress.library_id, progress.item_id, progress.media_identity,
                progress.title, progress.stream_identity, progress.media_type, progress.poster_url,
                progress.backdrop_url, progress.title_logo_url, progress.display_subtitle,
                progress.episode_still_url, progress.owner_scope],
        ).map_err(|_| "Failed to persist playback history deletion.".to_string())?;
        Ok(())
    }

    fn upsert(&self, progress: &NormalizedProgress) -> Result<(), String> {
        self.conn
            .execute(
                &format!("INSERT INTO playback_history (
                    identity_key, source_id, library_id, item_id, media_identity, title,
                    stream_identity, media_type, poster_url, backdrop_url, title_logo_url,
                    display_subtitle, episode_still_url, position, duration, completed, deleted,
                    progress_source, created_at, updated_at, owner_scope
                )
                VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, 0, 'local', {NOW_MILLIS_SQL}, {NOW_MILLIS_SQL}, ?17)
                ON CONFLICT(identity_key) DO UPDATE SET
                    source_id = excluded.source_id,
                    library_id = excluded.library_id,
                    item_id = excluded.item_id,
                    media_identity = excluded.media_identity,
                    title = excluded.title,
                    stream_identity = excluded.stream_identity,
                    media_type = excluded.media_type,
                    poster_url = excluded.poster_url,
                    backdrop_url = excluded.backdrop_url,
                    title_logo_url = excluded.title_logo_url,
                    display_subtitle = excluded.display_subtitle,
                    episode_still_url = excluded.episode_still_url,
                    position = excluded.position,
                    duration = excluded.duration,
                    completed = excluded.completed,
                    deleted = 0,
                    progress_source = 'local',
                    owner_scope = excluded.owner_scope,
                    updated_at = CASE WHEN playback_history.owner_scope IS excluded.owner_scope AND playback_history.updated_at >= {NOW_MILLIS_SQL}
                        THEN playback_history.updated_at + 1 ELSE {NOW_MILLIS_SQL} END"),
                params![
                    identity_key(&progress.source_id, &progress.media_identity),
                    progress.source_id,
                    progress.library_id,
                    progress.item_id,
                    progress.media_identity,
                    progress.title,
                    progress.stream_identity,
                    progress.media_type,
                    progress.poster_url,
                    progress.backdrop_url,
                    progress.title_logo_url,
                    progress.display_subtitle,
                    progress.episode_still_url,
                    progress.position,
                    progress.duration,
                    if progress.completed { 1 } else { 0 },
                    progress.owner_scope,
                ],
            )
            .map_err(|_| "Failed to save playback progress.".to_string())?;
        Ok(())
    }

    fn get(
        &self,
        identity: &PlaybackProgressIdentity,
    ) -> Result<Option<PlaybackHistoryEntry>, String> {
        self.conn
            .query_row(
                &format!("SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                    media_type, poster_url, backdrop_url, title_logo_url, position, duration, updated_at,
                    completed, progress_source, display_subtitle, episode_still_url, deleted, owner_scope
                 FROM playback_history
                 WHERE identity_key = ?1 AND deleted = 0 AND {VISIBLE_SCOPE_SQL} AND (?2 IS NULL OR owner_scope=?2)"),
                params![identity_key(&identity.source_id, &identity.media_identity), identity.owner_scope],
                map_history_entry,
            )
            .optional()
            .map_err(|_| "Failed to read playback progress.".to_string())
    }

    fn get_any(
        &self,
        identity: &PlaybackProgressIdentity,
    ) -> Result<Option<PlaybackHistoryEntry>, String> {
        self.conn.query_row(
            &format!("SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                media_type, poster_url, backdrop_url, title_logo_url, position, duration, updated_at,
                completed, progress_source, display_subtitle, episode_still_url, deleted, owner_scope
             FROM playback_history WHERE identity_key = ?1 AND {VISIBLE_SCOPE_SQL} AND (?2 IS NULL OR owner_scope=?2)"),
            params![identity_key(&identity.source_id, &identity.media_identity), identity.owner_scope], map_history_entry,
        ).optional().map_err(|_| "Failed to read playback history entry.".to_string())
    }

    fn list_continue_watching(
        &self,
        limit: u32,
        excluded_source_ids: &[String],
    ) -> Result<Vec<PlaybackHistoryEntry>, String> {
        let mut values = vec![Value::Real(MIN_RESUME_POSITION_SECONDS)];
        let mut filter = " WHERE deleted = 0 AND completed = 0
            AND position >= ?1"
            .to_string();
        filter.push_str(&format!(" AND {VISIBLE_SCOPE_SQL}"));
        append_source_exclusions(&mut filter, &mut values, excluded_source_ids);
        values.push(Value::Integer(i64::from(limit)));
        let mut statement = self
            .conn
            .prepare(&format!(
                "SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                    media_type, poster_url, backdrop_url, title_logo_url, position, duration, updated_at,
                    completed, progress_source, display_subtitle, episode_still_url, deleted, owner_scope
                 FROM playback_history{filter}
                 ORDER BY updated_at DESC, identity_key ASC
                 LIMIT ?{}",
                values.len()
            ))
            .map_err(|_| "Failed to read continue watching entries.".to_string())?;

        let entries = statement
            .query_map(params_from_iter(values.iter()), map_history_entry)
            .map_err(|_| "Failed to read continue watching entries.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read continue watching entries.".to_string())?;

        Ok(entries)
    }

    fn list_history(
        &self,
        page: u32,
        page_size: u32,
        excluded_source_ids: &[String],
    ) -> Result<PlaybackHistoryPage, String> {
        self.list_history_filter(page, page_size, Some(false), excluded_source_ids, None)
    }

    #[cfg(test)]
    fn list_history_sync(&self, page: u32, page_size: u32) -> Result<PlaybackHistoryPage, String> {
        self.list_history_filter(page, page_size, None, &[], None)
    }

    fn list_pending_history(
        &self,
        page: u32,
        page_size: u32,
        scope: Option<&str>,
    ) -> Result<PlaybackHistoryPage, String> {
        self.list_history_filter(page, page_size, None, &[], scope)
    }

    fn list_tombstones(&self, page: u32, page_size: u32) -> Result<PlaybackHistoryPage, String> {
        self.list_history_filter(page, page_size, Some(true), &[], None)
    }

    fn list_history_filter(
        &self,
        page: u32,
        page_size: u32,
        deleted: Option<bool>,
        excluded_source_ids: &[String],
        pending_scope: Option<&str>,
    ) -> Result<PlaybackHistoryPage, String> {
        let mut filter = match deleted {
            Some(true) => " WHERE deleted = 1",
            Some(false) => " WHERE deleted = 0",
            None => "",
        }
        .to_string();
        let mut values = Vec::new();
        filter.push_str(if filter.is_empty() {
            " WHERE "
        } else {
            " AND "
        });
        filter.push_str(VISIBLE_SCOPE_SQL);
        if let Some(scope) = pending_scope {
            values.push(Value::Text(scope.to_string()));
            filter.push_str(" AND progress_source='local' AND NOT EXISTS (
                SELECT 1 FROM playback_history_sync_ack ack WHERE ack.scope=?1
                AND ack.identity_key=playback_history.identity_key AND ack.updated_at=playback_history.updated_at)");
        }
        append_source_exclusions(&mut filter, &mut values, excluded_source_ids);
        let total = self
            .conn
            .query_row(
                &format!("SELECT COUNT(*) FROM playback_history{filter}"),
                params_from_iter(values.iter()),
                |row| row.get::<_, u64>(0),
            )
            .map_err(|_| "Failed to count playback history entries.".to_string())?;
        let offset = u64::from(page.saturating_sub(1)) * u64::from(page_size);
        let limit_parameter = values.len() + 1;
        let offset_parameter = values.len() + 2;
        values.push(Value::Integer(i64::from(page_size)));
        values.push(Value::Integer(offset as i64));
        let mut statement = self.conn.prepare(&format!(
            "SELECT source_id, library_id, item_id, media_identity, title, stream_identity,
                media_type, poster_url, backdrop_url, title_logo_url, position, duration, updated_at,
                completed, progress_source, display_subtitle, episode_still_url, deleted, owner_scope
             FROM playback_history{filter}
             ORDER BY updated_at DESC, identity_key ASC LIMIT ?{limit_parameter} OFFSET ?{offset_parameter}"
        )).map_err(|_| "Failed to read playback history entries.".to_string())?;
        let list = statement
            .query_map(params_from_iter(values.iter()), map_history_entry)
            .map_err(|_| "Failed to read playback history entries.".to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "Failed to read playback history entries.".to_string())?;
        Ok(PlaybackHistoryPage {
            list,
            total,
            page,
            page_size,
            has_more: offset.saturating_add(u64::from(page_size)) < total,
        })
    }
}

fn append_source_exclusions(
    filter: &mut String,
    values: &mut Vec<Value>,
    excluded_source_ids: &[String],
) {
    if excluded_source_ids.is_empty() {
        return;
    }
    filter.push_str(if filter.is_empty() {
        " WHERE "
    } else {
        " AND "
    });
    filter.push_str("source_id NOT IN (");
    for (index, source_id) in excluded_source_ids.iter().enumerate() {
        if index > 0 {
            filter.push(',');
        }
        values.push(Value::Text(source_id.clone()));
        filter.push_str(&format!("?{}", values.len()));
    }
    filter.push(')');
}

fn normalize_excluded_source_ids(source_ids: Option<Vec<String>>) -> Result<Vec<String>, String> {
    let mut source_ids = source_ids
        .unwrap_or_default()
        .into_iter()
        .map(|source_id| normalize_id(source_id, "Invalid playback source."))
        .collect::<Result<Vec<_>, _>>()?;
    source_ids.sort();
    source_ids.dedup();
    Ok(source_ids)
}

fn initialize_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch(
        "CREATE TABLE IF NOT EXISTS playback_history (
            identity_key TEXT PRIMARY KEY NOT NULL,
            source_id TEXT NOT NULL,
            library_id TEXT,
            item_id TEXT,
            media_identity TEXT NOT NULL,
            title TEXT NOT NULL,
            stream_identity TEXT,
            media_type TEXT,
            poster_url TEXT,
            backdrop_url TEXT,
            title_logo_url TEXT,
            display_subtitle TEXT,
            episode_still_url TEXT,
            position REAL NOT NULL,
            duration REAL,
            completed INTEGER NOT NULL DEFAULT 0,
            deleted INTEGER NOT NULL DEFAULT 0,
            progress_source TEXT NOT NULL DEFAULT 'local',
            owner_scope TEXT,
            created_at INTEGER NOT NULL DEFAULT (unixepoch()),
            updated_at INTEGER NOT NULL DEFAULT (unixepoch())
        );
        CREATE INDEX IF NOT EXISTS idx_playback_history_continue
            ON playback_history (completed, updated_at DESC);
        CREATE INDEX IF NOT EXISTS idx_playback_history_source
            ON playback_history (source_id);
        CREATE TABLE IF NOT EXISTS playback_history_meta (id INTEGER PRIMARY KEY, generation INTEGER NOT NULL);
        INSERT OR IGNORE INTO playback_history_meta (id,generation) VALUES (1,0);
        CREATE TABLE IF NOT EXISTS playback_history_source_scopes (
            source_id TEXT PRIMARY KEY, scope TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0, validation_key TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS playback_history_sync_state (
            scope TEXT PRIMARY KEY, mapping_key TEXT NOT NULL, cursor INTEGER NOT NULL DEFAULT 0);
        CREATE TABLE IF NOT EXISTS playback_history_sync_ack (
            scope TEXT NOT NULL, identity_key TEXT NOT NULL, updated_at INTEGER NOT NULL,
            PRIMARY KEY (scope,identity_key));",
    )
    .map_err(|_| "Failed to initialize playback history database.".to_string())?;
    let _ = conn.execute(
        "ALTER TABLE playback_history ADD COLUMN title_logo_url TEXT",
        params![],
    );
    let _ = conn.execute(
        "ALTER TABLE playback_history ADD COLUMN display_subtitle TEXT",
        params![],
    );
    let _ = conn.execute(
        "ALTER TABLE playback_history ADD COLUMN episode_still_url TEXT",
        params![],
    );
    let _ = conn.execute(
        "ALTER TABLE playback_history ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0",
        params![],
    );
    let _ = conn.execute(
        "ALTER TABLE playback_history ADD COLUMN owner_scope TEXT",
        params![],
    );
    let _ = conn.execute(
        "ALTER TABLE playback_history_source_scopes ADD COLUMN active INTEGER NOT NULL DEFAULT 0",
        params![],
    );
    let _ = conn.execute("ALTER TABLE playback_history_source_scopes ADD COLUMN validation_key TEXT NOT NULL DEFAULT ''", params![]);
    conn.execute_batch("CREATE INDEX IF NOT EXISTS idx_playback_history_deleted_updated ON playback_history (deleted, updated_at DESC, identity_key ASC)")
        .map_err(|_| "Failed to index playback history deletions.".to_string())?;
    Ok(())
}

fn validate_sync_scope(scope: &str) -> Result<(), String> {
    if scope.len() == 64
        && scope
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        Ok(())
    } else {
        Err("Invalid playback history account scope.".to_string())
    }
}

fn history_generation(conn: &Connection) -> Result<u64, String> {
    conn.query_row(
        "SELECT generation FROM playback_history_meta WHERE id=1",
        [],
        |row| row.get(0),
    )
    .map_err(|_| "Failed to read playback history generation.".to_string())
}

fn ensure_history_generation(conn: &Connection, expected: u64) -> Result<(), String> {
    if history_generation(conn)? == expected {
        Ok(())
    } else {
        Err("Local playback history was reset.".to_string())
    }
}

fn resolve_owner_scope(
    conn: &Connection,
    source_id: &str,
    scope: Option<&str>,
) -> Result<Option<String>, String> {
    let Some(scope) = scope else {
        return Ok(None);
    };
    validate_sync_scope(scope)?;
    let current: Option<(String, String)> = conn.query_row(
        "SELECT scope, validation_key FROM playback_history_source_scopes WHERE source_id=?1 AND active=1",
        params![source_id], |row| Ok((row.get(0)?, row.get(1)?)),
    ).optional().map_err(|_| "Failed to validate playback history account.".to_string())?;
    match current {
        Some((current_scope, context_key)) if current_scope == scope || context_key == scope => {
            Ok(Some(current_scope))
        }
        _ => Err("Playback history account changed.".to_string()),
    }
}

impl NormalizedProgress {
    fn from_payload(payload: PlaybackProgressUpsert) -> Result<Self, String> {
        let identity = normalize_identity(PlaybackProgressIdentity {
            source_id: payload.source_id,
            media_identity: payload.media_identity,
            owner_scope: payload.owner_scope.clone(),
        })?;

        let position = sanitize_position(payload.position)?;
        let duration = payload.duration.map(sanitize_duration).transpose()?;
        let completed = payload
            .completed
            .unwrap_or_else(|| is_completed(position, duration));
        let owner_scope = payload
            .owner_scope
            .map(|scope| {
                validate_sync_scope(&scope)?;
                Ok::<String, String>(scope)
            })
            .transpose()?;
        let title = sanitize_text(payload.title, "Untitled", MAX_TEXT_LENGTH);

        Ok(Self {
            source_id: identity.source_id,
            library_id: payload
                .library_id
                .and_then(|value| sanitize_optional_id(value, "Invalid playback library."))
                .transpose()?,
            item_id: payload
                .item_id
                .and_then(|value| sanitize_optional_id(value, "Invalid playback item."))
                .transpose()?,
            media_identity: identity.media_identity,
            title,
            stream_identity: payload
                .stream_identity
                .and_then(|value| sanitize_optional_display_text(value, true)),
            media_type: payload
                .media_type
                .and_then(|value| sanitize_optional_display_text(value, false)),
            poster_url: payload.poster_url.and_then(sanitize_optional_url),
            backdrop_url: payload.backdrop_url.and_then(sanitize_optional_url),
            title_logo_url: payload.title_logo_url.and_then(sanitize_optional_url),
            display_subtitle: payload
                .display_subtitle
                .and_then(|value| sanitize_optional_display_text(value, false)),
            episode_still_url: payload.episode_still_url.and_then(sanitize_optional_url),
            position,
            duration,
            completed,
            owner_scope,
        })
    }
}

fn map_history_entry(row: &rusqlite::Row<'_>) -> rusqlite::Result<PlaybackHistoryEntry> {
    let duration = row.get::<_, Option<f64>>(11)?;
    let position = row.get::<_, f64>(10)?;
    Ok(PlaybackHistoryEntry {
        source_id: row.get(0)?,
        library_id: row.get(1)?,
        item_id: row.get(2)?,
        media_identity: row.get(3)?,
        title: row.get(4)?,
        stream_identity: row.get(5)?,
        media_type: row.get(6)?,
        poster_url: row.get(7)?,
        backdrop_url: row.get(8)?,
        title_logo_url: row.get(9)?,
        display_subtitle: row.get(15)?,
        episode_still_url: row.get(16)?,
        position,
        duration,
        progress: progress_ratio(position, duration),
        updated_at: row.get(12)?,
        completed: row.get::<_, i64>(13)? != 0,
        deleted: row.get::<_, i64>(17)? != 0,
        progress_source: row.get(14)?,
        owner_scope: row.get(18)?,
    })
}

fn identity_key(source_id: &str, media_identity: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(source_id.as_bytes());
    hasher.update([0]);
    hasher.update(media_identity.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn normalize_identity(
    identity: PlaybackProgressIdentity,
) -> Result<PlaybackProgressIdentity, String> {
    Ok(PlaybackProgressIdentity {
        source_id: normalize_id(identity.source_id, "Invalid playback source.")?,
        media_identity: normalize_media_identity(identity.media_identity)?,
        owner_scope: identity.owner_scope,
    })
}

fn normalize_id(value: String, message: &str) -> Result<String, String> {
    let trimmed = value.trim();
    validate_id(trimmed, message)?;
    Ok(trimmed.to_string())
}

fn normalize_media_identity(value: String) -> Result<String, String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) {
        return Err("Invalid playback identity.".to_string());
    }

    let normalized = if looks_sensitive_url(trimmed) {
        redact_sensitive_url(trimmed)
    } else {
        trimmed.to_string()
    };

    if normalized.len() > MAX_IDENTITY_LENGTH {
        Err("Invalid playback identity.".to_string())
    } else {
        Ok(normalized)
    }
}

fn validate_id(value: &str, message: &str) -> Result<(), String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || trimmed.len() > MAX_ID_LENGTH || contains_control_character(trimmed) {
        Err(message.to_string())
    } else {
        Ok(())
    }
}

fn sanitize_optional_id(value: String, message: &str) -> Option<Result<String, String>> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return None;
    }
    Some(validate_id(trimmed, message).map(|_| trimmed.to_string()))
}

fn sanitize_position(value: f64) -> Result<f64, String> {
    if value.is_finite() && value >= 0.0 {
        Ok(value)
    } else {
        Err("Invalid playback position.".to_string())
    }
}

fn sanitize_duration(value: f64) -> Result<f64, String> {
    if value.is_finite() && value >= 0.0 {
        Ok(value)
    } else {
        Err("Invalid playback duration.".to_string())
    }
}

fn sanitize_text(value: String, fallback: &str, max_len: usize) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return fallback.to_string();
    }

    trimmed.chars().take(max_len).collect()
}

fn sanitize_optional_display_text(value: String, allow_local_path: bool) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) {
        return None;
    }

    let value = sanitize_text(trimmed.to_string(), "", MAX_TEXT_LENGTH);
    if looks_sensitive_url(&value) {
        return Some(redact_sensitive_url(&value));
    }

    if !allow_local_path && (value.contains('/') || value.contains('\\')) {
        return None;
    }

    Some(value)
}

fn sanitize_optional_url(value: String) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty() || contains_control_character(trimmed) || looks_sensitive_url(trimmed) {
        return None;
    }

    if trimmed.starts_with("http://") || trimmed.starts_with("https://") {
        Some(sanitize_text(trimmed.to_string(), "", MAX_TEXT_LENGTH))
    } else {
        None
    }
}

fn contains_control_character(value: &str) -> bool {
    value.chars().any(|ch| ch.is_control())
}

fn is_completed(position: f64, duration: Option<f64>) -> bool {
    let Some(duration) = duration else {
        return false;
    };
    if duration <= MIN_RESUME_POSITION_SECONDS {
        return false;
    }

    position >= duration * COMPLETED_PROGRESS_RATIO
        || duration - position <= COMPLETED_REMAINING_SECONDS
}

fn progress_ratio(position: f64, duration: Option<f64>) -> Option<f64> {
    let duration = duration?;
    if duration <= 0.0 {
        return None;
    }
    Some((position / duration).clamp(0.0, 1.0))
}

fn looks_sensitive_url(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    (lower.starts_with("http://") || lower.starts_with("https://"))
        && [
            "api_key=",
            "apikey=",
            "access_key=",
            "access_token=",
            "token=",
            "x-emby-token=",
            "expires=",
            "x-amz-expires=",
            "signature=",
            "sig=",
            "sign=",
            "password=",
            "passwd=",
            "pwd=",
            "security-token=",
            "x-amz-signature=",
            "x-amz-credential=",
            "x-amz-security-token=",
            "ossaccesskeyid=",
            "awsaccesskeyid=",
        ]
        .iter()
        .any(|needle| lower.contains(needle))
}

fn redact_sensitive_url(value: &str) -> String {
    let mut redacted = value.to_string();
    for key in [
        "api_key",
        "apikey",
        "access_key",
        "access_token",
        "token",
        "x-emby-token",
        "expires",
        "x-amz-expires",
        "signature",
        "sig",
        "sign",
        "password",
        "passwd",
        "pwd",
        "security-token",
        "x-amz-signature",
        "x-amz-credential",
        "x-amz-security-token",
        "ossaccesskeyid",
        "awsaccesskeyid",
    ] {
        redacted = redact_query_value(&redacted, key);
    }
    redacted
}

fn redact_query_value(value: &str, key: &str) -> String {
    let lower = value.to_ascii_lowercase();
    let needle = format!("{}=", key.to_ascii_lowercase());
    let mut output = String::with_capacity(value.len());
    let mut index = 0;

    while let Some(relative_start) = lower[index..].find(&needle) {
        let start = index + relative_start;
        let value_start = start + needle.len();
        let value_end = value[value_start..]
            .find('&')
            .map(|relative_end| value_start + relative_end)
            .unwrap_or(value.len());
        output.push_str(&value[index..value_start]);
        output.push_str("[redacted]");
        index = value_end;
    }

    output.push_str(&value[index..]);
    output
}

#[cfg(test)]
mod tests {
    use super::{
        initialize_schema, NormalizedProgress, PlaybackHistoryStorage, PlaybackProgressIdentity,
    };
    use rusqlite::Connection;

    #[test]
    fn deleting_source_history_does_not_touch_other_sources() {
        let conn = Connection::open_in_memory().expect("open playback history test database");
        initialize_schema(&conn).expect("initialize playback history schema");
        let storage = PlaybackHistoryStorage { conn };

        storage
            .upsert(&progress("source-a", "movie-1"))
            .expect("insert source a history");
        storage
            .upsert(&progress("source-b", "movie-2"))
            .expect("insert source b history");

        assert_eq!(
            storage
                .delete_for_source("source-a")
                .expect("delete source a history"),
            1
        );
        assert!(storage
            .get(&identity("source-a", "movie-1"))
            .expect("query source a")
            .is_none());
        assert!(storage
            .get(&identity("source-b", "movie-2"))
            .expect("query source b")
            .is_some());
    }

    #[test]
    fn old_history_schema_adds_tombstones_before_creating_the_index() {
        let conn = Connection::open_in_memory().expect("open legacy history database");
        conn.execute_batch(
            "CREATE TABLE playback_history (
            identity_key TEXT PRIMARY KEY NOT NULL, source_id TEXT NOT NULL,
            library_id TEXT, item_id TEXT, media_identity TEXT NOT NULL,
            title TEXT NOT NULL, stream_identity TEXT, media_type TEXT,
            poster_url TEXT, backdrop_url TEXT, position REAL NOT NULL,
            duration REAL, completed INTEGER NOT NULL DEFAULT 0,
            progress_source TEXT NOT NULL DEFAULT 'local',
            created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
        )",
        )
        .expect("create previous history schema");
        initialize_schema(&conn).expect("migrate previous history schema");
        initialize_schema(&conn).expect("repeat history schema migration");
        let storage = PlaybackHistoryStorage { conn };
        storage
            .tombstone(&progress("source-a", "old-movie"))
            .expect("write tombstone after upgrade");
        assert_eq!(
            storage
                .list_tombstones(1, 10)
                .expect("indexed tombstones")
                .total,
            1
        );
    }

    #[test]
    fn deletion_tombstone_hides_completed_and_missing_history_until_new_playback() {
        let conn = Connection::open_in_memory().expect("open playback history test database");
        initialize_schema(&conn).expect("initialize playback history schema");
        let storage = PlaybackHistoryStorage { conn };
        let identity = identity("source-a", "movie-1");
        let movie_progress = progress("source-a", "movie-1");
        storage.upsert(&movie_progress).expect("insert history");
        assert!(storage
            .set_completed(&identity, true)
            .expect("mark completed"));
        storage
            .tombstone(&movie_progress)
            .expect("delete completed history");
        assert!(storage.get(&identity).expect("visible query").is_none());
        let deleted = storage
            .get_any(&identity)
            .expect("raw query")
            .expect("tombstone");
        assert!(deleted.deleted);
        assert_eq!(
            storage
                .list_history(1, 10, &[])
                .expect("visible page")
                .total,
            0
        );
        assert_eq!(
            storage.list_history_sync(1, 10).expect("sync page").total,
            1
        );
        assert_eq!(
            storage
                .list_tombstones(1, 10)
                .expect("tombstone page")
                .total,
            1
        );
        storage
            .tombstone(&progress("source-a", "movie-2"))
            .expect("delete missing row");
        assert_eq!(
            storage
                .list_history_sync(1, 10)
                .expect("two tombstones")
                .total,
            2
        );
        storage.upsert(&movie_progress).expect("new playback");
        let replayed = storage
            .get(&identity)
            .expect("visible query")
            .expect("new playback");
        assert!(!replayed.deleted);
        assert!(replayed.updated_at > deleted.updated_at);
    }

    #[test]
    fn full_history_uses_stable_bounded_pages() {
        let conn = Connection::open_in_memory().expect("open playback history test database");
        initialize_schema(&conn).expect("initialize playback history schema");
        let storage = PlaybackHistoryStorage { conn };
        for index in 0..5 {
            storage
                .upsert(&progress("source-a", &format!("movie-{index}")))
                .expect("insert paged history");
        }

        let first = storage.list_history(1, 2, &[]).expect("first history page");
        let second = storage
            .list_history(2, 2, &[])
            .expect("second history page");
        let third = storage.list_history(3, 2, &[]).expect("third history page");
        assert_eq!(first.total, 5);
        assert!(first.has_more);
        assert!(second.has_more);
        assert!(!third.has_more);
        let mut identities = first
            .list
            .into_iter()
            .chain(second.list)
            .chain(third.list)
            .map(|entry| entry.media_identity)
            .collect::<Vec<_>>();
        identities.sort();
        identities.dedup();
        assert_eq!(identities.len(), 5);
    }

    #[test]
    fn episode_presentation_survives_local_history_round_trip() {
        let conn = Connection::open_in_memory().expect("open playback history test database");
        initialize_schema(&conn).expect("initialize playback history schema");
        let storage = PlaybackHistoryStorage { conn };
        let mut episode = progress("server-home", "server:v1:episode:9:work:1:2");
        episode.media_type = Some("episode".to_string());
        episode.title = "示例剧".to_string();
        episode.display_subtitle = Some("S01E02 · 改稻为桑".to_string());
        episode.episode_still_url = Some("https://image.example.test/episode.jpg".to_string());

        storage.upsert(&episode).expect("insert episode history");
        let saved = storage
            .get(&identity("server-home", "server:v1:episode:9:work:1:2"))
            .expect("read episode history")
            .expect("episode history");

        assert_eq!(saved.title, "示例剧");
        assert_eq!(saved.display_subtitle.as_deref(), Some("S01E02 · 改稻为桑"));
        assert_eq!(
            saved.episode_still_url.as_deref(),
            Some("https://image.example.test/episode.jpg")
        );
    }

    #[test]
    fn online_history_start_progress_and_resume_keep_exact_identity_and_edition() {
        let conn = Connection::open_in_memory().expect("open online history database");
        initialize_schema(&conn).expect("initialize online history schema");
        let storage = PlaybackHistoryStorage { conn };
        let token = "online-version|library%2Fone|work%2Fone|extra%2F32|version%2Fextra";
        let mut started = progress("server-home", token);
        started.library_id = Some("online-library|library%2Fone".to_string());
        started.media_type = Some("episode".to_string());
        started.title = "官方综艺".to_string();
        started.display_subtitle = Some("彩蛋 · 2026-10-10 幕后故事".to_string());
        started.position = 0.0;
        storage
            .upsert(&started)
            .expect("save confirmed native start");
        let saved = storage
            .get(&identity("server-home", token))
            .unwrap()
            .unwrap();
        assert_eq!(saved.position, 0.0);
        assert!(!saved.completed);
        assert_eq!(storage.list_history(1, 10, &[]).unwrap().total, 1);

        started.position = 180.0;
        storage.upsert(&started).expect("save real online progress");
        let resumed = storage
            .get(&identity("server-home", token))
            .unwrap()
            .unwrap();
        assert_eq!(resumed.position, 180.0);
        assert_eq!(resumed.item_id.as_deref(), Some(token));
        assert_eq!(resumed.library_id, started.library_id);
        assert_eq!(resumed.display_subtitle, started.display_subtitle);
        assert_eq!(resumed.media_identity, token);
        assert_eq!(storage.list_history(1, 10, &[]).unwrap().total, 1);

        let mut other = progress("server-home", token);
        other.title = started.title.clone();
        other.media_identity = token.replace("extra%2F32", "main%2F31");
        other.item_id = Some(other.media_identity.clone());
        storage
            .upsert(&other)
            .expect("save same-title main edition");
        other.media_identity = token.replace("library%2Fone", "library%2Ftwo");
        other.item_id = Some(other.media_identity.clone());
        other.library_id = Some("online-library|library%2Ftwo".to_string());
        storage
            .upsert(&other)
            .expect("save same-title other library");
        other.source_id = "another-server".to_string();
        storage
            .upsert(&other)
            .expect("save same-title other server");
        assert_eq!(storage.list_history(1, 10, &[]).unwrap().total, 4);
        assert!(storage
            .get(&identity("missing-server", token))
            .unwrap()
            .is_none());
        assert_eq!(
            storage
                .get(&identity("server-home", token))
                .unwrap()
                .unwrap()
                .position,
            180.0
        );
    }

    fn identity(source_id: &str, media_identity: &str) -> PlaybackProgressIdentity {
        PlaybackProgressIdentity {
            source_id: source_id.to_string(),
            media_identity: media_identity.to_string(),
            owner_scope: None,
        }
    }

    fn progress(source_id: &str, media_identity: &str) -> NormalizedProgress {
        NormalizedProgress {
            source_id: source_id.to_string(),
            library_id: None,
            item_id: Some(media_identity.to_string()),
            media_identity: media_identity.to_string(),
            title: media_identity.to_string(),
            stream_identity: None,
            media_type: Some("movie".to_string()),
            poster_url: None,
            backdrop_url: None,
            title_logo_url: None,
            display_subtitle: None,
            episode_still_url: None,
            position: 120.0,
            duration: Some(3600.0),
            completed: false,
            owner_scope: None,
        }
    }
}

use rusqlite::{params, Connection};
use tauri::AppHandle;

use crate::storage;

const DATABASE_FILE: &str = "media_collections.sqlite";
const FAVORITES_ID: &str = "local-favorites";

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CollectionMemberInput {
    source_id: String,
    item_id: String,
    title: String,
    media_type: String,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CollectionMember {
    source_id: String,
    item_id: String,
    title: String,
    media_type: String,
    poster_url: Option<String>,
    backdrop_url: Option<String>,
    position: i64,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaCollection {
    id: String,
    name: String,
    kind: String,
    members: Vec<CollectionMember>,
}

#[tauri::command]
pub fn player_list_media_collections(app: AppHandle) -> Result<Vec<MediaCollection>, String> {
    let storage = CollectionStorage::open(&app)?;
    storage.list()
}

#[tauri::command]
pub fn player_create_media_collection(
    app: AppHandle,
    name: String,
    kind: String,
) -> Result<String, String> {
    let storage = CollectionStorage::open(&app)?;
    storage.create(name, kind)
}

#[tauri::command]
pub fn player_delete_media_collection(app: AppHandle, id: String) -> Result<bool, String> {
    let storage = CollectionStorage::open(&app)?;
    storage.delete_collection(&clean_id(id)?)
}

#[tauri::command]
pub fn player_set_local_favorite(
    app: AppHandle,
    member: CollectionMemberInput,
    favorite: bool,
) -> Result<bool, String> {
    let storage = CollectionStorage::open(&app)?;
    let member = normalize_member(member)?;
    if favorite {
        storage.add_member(FAVORITES_ID, &member)
    } else {
        storage.remove_member(FAVORITES_ID, &member.source_id, &member.item_id)
    }
}

#[tauri::command]
pub fn player_add_media_collection_member(
    app: AppHandle,
    collection_id: String,
    member: CollectionMemberInput,
) -> Result<bool, String> {
    let storage = CollectionStorage::open(&app)?;
    storage.add_member(&clean_id(collection_id)?, &normalize_member(member)?)
}

#[tauri::command]
pub fn player_remove_media_collection_member(
    app: AppHandle,
    collection_id: String,
    source_id: String,
    item_id: String,
) -> Result<bool, String> {
    let storage = CollectionStorage::open(&app)?;
    storage.remove_member(
        &clean_id(collection_id)?,
        &clean_id(source_id)?,
        &clean_id(item_id)?,
    )
}

struct CollectionStorage {
    conn: Connection,
}

impl CollectionStorage {
    fn open(app: &AppHandle) -> Result<Self, String> {
        let conn = Connection::open(storage::data_file(app, DATABASE_FILE)?)
            .map_err(|_| "Failed to open media collection database.".to_string())?;
        initialize_schema(&conn)?;
        Ok(Self { conn })
    }

    fn create(&self, name: String, kind: String) -> Result<String, String> {
        let name = clean_text(name)?;
        if kind != "playlist" && kind != "collection" {
            return Err("Invalid collection kind.".into());
        }
        let id = format!("local-{}-{}", kind, unique_suffix());
        self.conn.execute("INSERT INTO media_collections (id,name,kind,created_at) VALUES (?1,?2,?3,unixepoch())", params![id,name,kind])
            .map_err(|_| "Failed to create media collection.".to_string())?;
        Ok(id)
    }

    fn delete_collection(&self, id: &str) -> Result<bool, String> {
        if id == FAVORITES_ID {
            return Ok(false);
        }
        Ok(self
            .conn
            .execute("DELETE FROM media_collections WHERE id=?1", params![id])
            .map_err(|_| "Failed to delete media collection.".to_string())?
            > 0)
    }

    fn add_member(&self, id: &str, member: &CollectionMemberInput) -> Result<bool, String> {
        let kind: String = self
            .conn
            .query_row(
                "SELECT kind FROM media_collections WHERE id=?1",
                params![id],
                |r| r.get(0),
            )
            .map_err(|_| "Media collection not found.".to_string())?;
        let position: i64 = if kind == "playlist" {
            self.conn.query_row("SELECT COALESCE(MAX(position),-1)+1 FROM media_collection_members WHERE collection_id=?1", params![id], |r| r.get(0)).unwrap_or(0)
        } else {
            0
        };
        Ok(self.conn.execute("INSERT OR IGNORE INTO media_collection_members (collection_id,source_id,item_id,title,media_type,poster_url,backdrop_url,position,added_at) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,unixepoch())", params![id,member.source_id,member.item_id,member.title,member.media_type,member.poster_url,member.backdrop_url,position]).map_err(|_| "Failed to add collection member.".to_string())? > 0)
    }

    fn remove_member(&self, id: &str, source: &str, item: &str) -> Result<bool, String> {
        Ok(self.conn.execute("DELETE FROM media_collection_members WHERE collection_id=?1 AND source_id=?2 AND item_id=?3", params![id,source,item]).map_err(|_| "Failed to remove collection member.".to_string())? > 0)
    }

    fn list(&self) -> Result<Vec<MediaCollection>, String> {
        let mut stmt = self.conn.prepare("SELECT id,name,kind FROM media_collections ORDER BY CASE kind WHEN 'favorite' THEN 0 WHEN 'playlist' THEN 1 ELSE 2 END, created_at").map_err(|_| "Failed to list collections.".to_string())?;
        let rows = stmt
            .query_map([], |r| {
                Ok((
                    r.get::<_, String>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                ))
            })
            .map_err(|_| "Failed to list collections.".to_string())?;
        let mut output = Vec::new();
        for row in rows {
            let (id, name, kind) = row.map_err(|_| "Failed to list collections.".to_string())?;
            let mut members_stmt = self.conn.prepare("SELECT source_id,item_id,title,media_type,poster_url,backdrop_url,position FROM media_collection_members WHERE collection_id=?1 ORDER BY position,added_at").map_err(|_| "Failed to list collection members.".to_string())?;
            let members = members_stmt
                .query_map(params![id], |r| {
                    Ok(CollectionMember {
                        source_id: r.get(0)?,
                        item_id: r.get(1)?,
                        title: r.get(2)?,
                        media_type: r.get(3)?,
                        poster_url: r.get(4)?,
                        backdrop_url: r.get(5)?,
                        position: r.get(6)?,
                    })
                })
                .map_err(|_| "Failed to list collection members.".to_string())?
                .collect::<Result<Vec<_>, _>>()
                .map_err(|_| "Failed to list collection members.".to_string())?;
            output.push(MediaCollection {
                id,
                name,
                kind,
                members,
            });
        }
        Ok(output)
    }
}

fn initialize_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch("PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS media_collections (id TEXT PRIMARY KEY,name TEXT NOT NULL,kind TEXT NOT NULL,created_at INTEGER NOT NULL); CREATE TABLE IF NOT EXISTS media_collection_members (collection_id TEXT NOT NULL,source_id TEXT NOT NULL,item_id TEXT NOT NULL,title TEXT NOT NULL,media_type TEXT NOT NULL,poster_url TEXT,backdrop_url TEXT,position INTEGER NOT NULL DEFAULT 0,added_at INTEGER NOT NULL,PRIMARY KEY(collection_id,source_id,item_id),FOREIGN KEY(collection_id) REFERENCES media_collections(id) ON DELETE CASCADE); INSERT OR IGNORE INTO media_collections(id,name,kind,created_at) VALUES ('local-favorites','收藏','favorite',0);").map_err(|_| "Failed to initialize media collection database.".to_string())
}

fn normalize_member(mut member: CollectionMemberInput) -> Result<CollectionMemberInput, String> {
    member.source_id = clean_id(member.source_id)?;
    member.item_id = clean_id(member.item_id)?;
    member.title = clean_text(member.title)?;
    member.media_type = clean_id(member.media_type)?;
    member.poster_url = safe_url(member.poster_url);
    member.backdrop_url = safe_url(member.backdrop_url);
    Ok(member)
}
fn clean_id(value: String) -> Result<String, String> {
    let v = value.trim();
    if v.is_empty() || v.len() > 512 {
        Err("Invalid stable media identity.".into())
    } else {
        Ok(v.into())
    }
}
fn clean_text(value: String) -> Result<String, String> {
    let v = value.trim();
    if v.is_empty() || v.len() > 512 || v.chars().any(char::is_control) {
        Err("Invalid display text.".into())
    } else {
        Ok(v.into())
    }
}
fn safe_url(value: Option<String>) -> Option<String> {
    value.filter(|v| {
        (v.starts_with("https://") || v.starts_with("http://"))
            && !v.contains('?')
            && !v.contains('#')
            && !v.chars().any(char::is_control)
    })
}
fn unique_suffix() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    format!(
        "{}",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn favorites_are_unique_and_playlists_are_ordered() {
        let conn = Connection::open_in_memory().unwrap();
        initialize_schema(&conn).unwrap();
        let s = CollectionStorage { conn };
        let m = |id: &str| CollectionMemberInput {
            source_id: "s".into(),
            item_id: id.into(),
            title: id.into(),
            media_type: "movie".into(),
            poster_url: None,
            backdrop_url: None,
        };
        assert!(s.add_member(FAVORITES_ID, &m("1")).unwrap());
        assert!(!s.add_member(FAVORITES_ID, &m("1")).unwrap());
        let id = s.create("Queue".into(), "playlist".into()).unwrap();
        s.add_member(&id, &m("1")).unwrap();
        s.add_member(&id, &m("2")).unwrap();
        let all = s.list().unwrap();
        let p = all.iter().find(|c| c.id == id).unwrap();
        assert_eq!(
            p.members.iter().map(|m| m.position).collect::<Vec<_>>(),
            vec![0, 1]
        );
    }
}

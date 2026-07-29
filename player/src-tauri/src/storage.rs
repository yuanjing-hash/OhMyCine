use serde::Serialize;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::OnceLock;
use tauri::{AppHandle, Manager};

const PORTABLE_FLAG: &str = "portable.flag";
const DATA_DIR: &str = "data";
const CACHE_DIR: &str = "cache";
const LOG_DIR: &str = "logs";

const MIGRATION_FILES: &[(&str, &str)] = &[
    ("credentials/credentials.sqlite", "credentials.sqlite"),
    ("credentials/master.key", "master.key"),
    ("history/playback_history.sqlite", "playback_history.sqlite"),
    (
        "preferences/player_preferences.sqlite",
        "player_preferences.sqlite",
    ),
    ("scraper/raw_scan_cache.sqlite", "raw_scan_cache.sqlite"),
];

static STORAGE_LAYOUT: OnceLock<StorageLayout> = OnceLock::new();

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum StorageMode {
    Standard,
    Portable,
}

#[derive(Clone, Debug)]
pub struct StorageLayout {
    pub mode: StorageMode,
    pub base_dir: PathBuf,
    pub data_dir: PathBuf,
    pub cache_dir: PathBuf,
    pub log_dir: PathBuf,
    pub portable_marker_path: PathBuf,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StorageInfo {
    mode: StorageMode,
    base_dir: String,
    data_dir: String,
    cache_dir: String,
    log_dir: String,
    portable_marker_path: String,
    credential_protection: String,
}

pub fn initialize(app: &AppHandle) -> Result<StorageLayout, String> {
    if let Some(layout) = STORAGE_LAYOUT.get() {
        return Ok(layout.clone());
    }

    let layout = resolve(app)?;
    prepare_layout(&layout)?;
    migrate_legacy_storage(app, &layout)?;
    let _ = STORAGE_LAYOUT.set(layout);
    STORAGE_LAYOUT
        .get()
        .cloned()
        .ok_or_else(|| "Failed to initialize Player storage layout.".to_string())
}

pub fn resolve(app: &AppHandle) -> Result<StorageLayout, String> {
    let executable = std::env::current_exe()
        .map_err(|_| "Failed to resolve Player executable path.".to_string())?;
    let standard_base = app
        .path()
        .app_local_data_dir()
        .map_err(|_| "Failed to resolve Player local data directory.".to_string())?;
    Ok(resolve_from(
        &standard_base,
        &executable,
        std::env::args_os().any(|arg| arg == "--portable"),
    ))
}

pub fn data_file(app: &AppHandle, file_name: &str) -> Result<PathBuf, String> {
    let layout = initialize(app)?;
    Ok(layout.data_dir.join(file_name))
}

pub fn storage_info(app: &AppHandle) -> Result<StorageInfo, String> {
    let layout = initialize(app)?;
    Ok(StorageInfo {
        mode: layout.mode,
        base_dir: display_path(&layout.base_dir),
        data_dir: display_path(&layout.data_dir),
        cache_dir: display_path(&layout.cache_dir),
        log_dir: display_path(&layout.log_dir),
        portable_marker_path: display_path(&layout.portable_marker_path),
        credential_protection: credential_protection_label(layout.mode).to_string(),
    })
}

fn resolve_from(standard_base: &Path, executable: &Path, portable_arg: bool) -> StorageLayout {
    let executable_dir = executable.parent().unwrap_or_else(|| Path::new("."));
    let portable_marker_path = executable_dir.join(PORTABLE_FLAG);
    let mode = if portable_arg || portable_marker_path.is_file() {
        StorageMode::Portable
    } else {
        StorageMode::Standard
    };
    let base_dir = match mode {
        StorageMode::Standard => standard_base.to_path_buf(),
        StorageMode::Portable => executable_dir.to_path_buf(),
    };

    StorageLayout {
        mode,
        data_dir: base_dir.join(DATA_DIR),
        cache_dir: base_dir.join(CACHE_DIR),
        log_dir: base_dir.join(LOG_DIR),
        base_dir,
        portable_marker_path,
    }
}

fn prepare_layout(layout: &StorageLayout) -> Result<(), String> {
    for dir in [&layout.data_dir, &layout.cache_dir, &layout.log_dir] {
        fs::create_dir_all(dir)
            .map_err(|_| "Failed to prepare Player storage directory.".to_string())?;
    }
    Ok(())
}

fn migrate_legacy_storage(app: &AppHandle, layout: &StorageLayout) -> Result<(), String> {
    let legacy_roaming = app
        .path()
        .app_data_dir()
        .map_err(|_| "Failed to resolve legacy Player data directory.".to_string())?;
    let standard_data = app
        .path()
        .app_local_data_dir()
        .map_err(|_| "Failed to resolve Player local data directory.".to_string())?
        .join(DATA_DIR);

    if layout.mode == StorageMode::Portable && standard_data != layout.data_dir {
        for (_, target_name) in MIGRATION_FILES {
            copy_if_missing(
                &standard_data.join(target_name),
                &layout.data_dir.join(target_name),
            )?;
        }
        copy_if_missing(
            &standard_data.join("settings.sqlite"),
            &layout.data_dir.join("settings.sqlite"),
        )?;
    }

    for (legacy_relative, target_name) in MIGRATION_FILES {
        let source = legacy_roaming.join(legacy_relative);
        let target = layout.data_dir.join(target_name);
        match layout.mode {
            StorageMode::Standard => migrate_if_missing(&source, &target)?,
            StorageMode::Portable => copy_if_missing(&source, &target)?,
        }
    }
    if layout.mode == StorageMode::Standard {
        remove_empty_legacy_dirs(&legacy_roaming);
    }
    Ok(())
}

fn migrate_if_missing(source: &Path, target: &Path) -> Result<(), String> {
    if !source.is_file() {
        return Ok(());
    }
    if target.is_file() {
        if files_equal(source, target) {
            let _ = fs::remove_file(source);
        }
        return Ok(());
    }

    copy_if_missing(source, target)?;
    if !target.is_file() {
        return Err("Failed to verify migrated Player data.".to_string());
    }
    if fs::remove_file(source).is_err() {
        log::warn!("migrated Player data but could not remove the legacy source file");
    }
    Ok(())
}

fn files_equal(left: &Path, right: &Path) -> bool {
    let Ok(left_metadata) = fs::metadata(left) else {
        return false;
    };
    let Ok(right_metadata) = fs::metadata(right) else {
        return false;
    };
    if left_metadata.len() != right_metadata.len() {
        return false;
    }

    match (fs::read(left), fs::read(right)) {
        (Ok(left_bytes), Ok(right_bytes)) => left_bytes == right_bytes,
        _ => false,
    }
}

fn copy_if_missing(source: &Path, target: &Path) -> Result<(), String> {
    if target.exists() || !source.is_file() {
        return Ok(());
    }

    let parent = target
        .parent()
        .ok_or_else(|| "Invalid Player storage migration target.".to_string())?;
    fs::create_dir_all(parent)
        .map_err(|_| "Failed to prepare Player storage migration target.".to_string())?;
    fs::copy(source, target).map_err(|_| "Failed to migrate existing Player data.".to_string())?;
    Ok(())
}

fn remove_empty_legacy_dirs(legacy_root: &Path) {
    for dir in ["credentials", "history", "preferences", "scraper"] {
        let _ = fs::remove_dir(legacy_root.join(dir));
    }
    let _ = fs::remove_dir(legacy_root);
}

fn credential_protection_label(mode: StorageMode) -> &'static str {
    match mode {
        StorageMode::Portable => "portableFileKey",
        StorageMode::Standard if cfg!(windows) => "windowsDpapi",
        StorageMode::Standard => "localFileKey",
    }
}

fn display_path(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

#[cfg(test)]
mod tests {
    use super::{copy_if_missing, migrate_if_missing, resolve_from, StorageMode};
    use std::fs;
    use std::path::{Path, PathBuf};

    #[test]
    fn standard_layout_uses_local_app_data() {
        let standard = PathBuf::from("C:/Users/Test/AppData/Local/com.ohmycine.player");
        let executable = PathBuf::from("C:/Program Files/OhMyCine/ohmycine-player.exe");
        let layout = resolve_from(&standard, &executable, false);

        assert_eq!(layout.mode, StorageMode::Standard);
        assert_eq!(layout.data_dir, standard.join("data"));
        assert_eq!(layout.cache_dir, standard.join("cache"));
        assert_eq!(layout.log_dir, standard.join("logs"));
    }

    #[test]
    fn portable_argument_uses_executable_directory() {
        let standard = PathBuf::from("C:/Users/Test/AppData/Local/com.ohmycine.player");
        let executable = PathBuf::from("D:/Apps/OhMyCine/ohmycine-player.exe");
        let layout = resolve_from(&standard, &executable, true);

        assert_eq!(layout.mode, StorageMode::Portable);
        assert_eq!(layout.data_dir, PathBuf::from("D:/Apps/OhMyCine/data"));
    }

    #[test]
    fn portable_flag_enables_portable_layout() {
        let root =
            std::env::temp_dir().join(format!("ohmycine-storage-layout-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create portable layout root");
        fs::write(root.join("portable.flag"), "portable\n").expect("write portable flag");
        let executable = root.join("ohmycine-player.exe");
        let layout = resolve_from(Path::new("unused"), &executable, false);

        assert_eq!(layout.mode, StorageMode::Portable);
        assert_eq!(layout.base_dir, root);
        let _ = fs::remove_dir_all(&layout.base_dir);
    }

    #[test]
    fn migration_copy_never_overwrites_new_data() {
        let root =
            std::env::temp_dir().join(format!("ohmycine-storage-migration-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create migration root");
        let source = root.join("legacy.sqlite");
        let target = root.join("data/settings.sqlite");
        fs::write(&source, "legacy").expect("write legacy data");

        copy_if_missing(&source, &target).expect("copy legacy data");
        assert_eq!(
            fs::read_to_string(&target).expect("read migrated data"),
            "legacy"
        );

        fs::write(&target, "new").expect("write new data");
        copy_if_missing(&source, &target).expect("skip existing new data");
        assert_eq!(
            fs::read_to_string(&target).expect("read preserved new data"),
            "new"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn legacy_migration_removes_source_after_verified_copy() {
        let root =
            std::env::temp_dir().join(format!("ohmycine-storage-move-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create migration root");
        let source = root.join("legacy/master.key");
        let target = root.join("data/master.key");
        fs::create_dir_all(source.parent().expect("legacy parent")).expect("create legacy parent");
        fs::write(&source, "legacy-key").expect("write legacy key");

        migrate_if_missing(&source, &target).expect("migrate legacy key");
        assert!(!source.exists());
        assert_eq!(
            fs::read_to_string(&target).expect("read migrated key"),
            "legacy-key"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn standard_migration_only_removes_matching_legacy_duplicates() {
        let root =
            std::env::temp_dir().join(format!("ohmycine-storage-duplicate-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).expect("create duplicate root");
        let source = root.join("legacy.sqlite");
        let target = root.join("data.sqlite");

        fs::write(&source, "same").expect("write duplicate source");
        fs::write(&target, "same").expect("write duplicate target");
        migrate_if_missing(&source, &target).expect("clean matching duplicate");
        assert!(!source.exists());

        fs::write(&source, "older").expect("write different source");
        fs::write(&target, "newer").expect("write different target");
        migrate_if_missing(&source, &target).expect("preserve different source");
        assert!(source.exists());
        let _ = fs::remove_dir_all(&root);
    }
}

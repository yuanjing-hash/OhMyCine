package services

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	mediaLibraryPersistenceStageConfiguration = "configuration_revalidate"
	mediaLibraryPersistenceStageLoadEntries   = "load_existing_entries"
	mediaLibraryPersistenceStageSourceAssets  = "persist_source_assets"
	mediaLibraryPersistenceStageRecognition   = "persist_recognition"
	mediaLibraryPersistenceStageEntries       = "persist_entries"
	mediaLibraryPersistenceStagePrune         = "prune_stale_entries"
	mediaLibraryPersistenceStageGeneration    = "advance_library_generation"
	mediaLibraryPersistenceStageScanRun       = "persist_scan_run"
	mediaLibraryPersistenceStageChange        = "record_media_change"

	mediaLibraryDatabaseErrorConfigurationChanged = "configuration_changed"
	mediaLibraryDatabaseErrorForeignKey           = "foreign_key"
	mediaLibraryDatabaseErrorUnique               = "unique"
	mediaLibraryDatabaseErrorConstraint           = "constraint"
	mediaLibraryDatabaseErrorBusy                 = "busy"
	mediaLibraryDatabaseErrorUnknown              = "unknown"
)

var errMediaLibraryConfigurationChanged = errors.New("media library configuration changed during recognition")

// mediaLibraryPersistenceError keeps the original cause available to internal
// callers while ensuring an accidental Error() log cannot expose SQL, paths,
// media names or provider identities.
type mediaLibraryPersistenceError struct {
	stage string
	cause error
}

func (e *mediaLibraryPersistenceError) Error() string {
	return "media library persistence failed at " + e.stage
}

func (e *mediaLibraryPersistenceError) Unwrap() error { return e.cause }

func wrapMediaLibraryPersistence(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &mediaLibraryPersistenceError{stage: stage, cause: err}
}

func mediaLibraryPersistenceDiagnostics(err error) (string, string) {
	stage := mediaLibraryDatabaseErrorUnknown
	var persistenceErr *mediaLibraryPersistenceError
	if errors.As(err, &persistenceErr) {
		stage = persistenceErr.stage
	}
	if errors.Is(err, errMediaLibraryConfigurationChanged) {
		return stage, mediaLibraryDatabaseErrorConfigurationChanged
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return stage, mediaLibraryDatabaseErrorUnknown
	}
	code := sqliteErr.Code()
	switch code {
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return stage, mediaLibraryDatabaseErrorForeignKey
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return stage, mediaLibraryDatabaseErrorUnique
	}
	switch code & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return stage, mediaLibraryDatabaseErrorBusy
	case sqlite3.SQLITE_CONSTRAINT:
		return stage, mediaLibraryDatabaseErrorConstraint
	default:
		return stage, mediaLibraryDatabaseErrorUnknown
	}
}

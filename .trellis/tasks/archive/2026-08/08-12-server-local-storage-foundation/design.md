# Design: Local Storage Foundation

## Backend Boundaries

- `internal/models`: Storage persistence model and DTO-safe projections.
- `internal/services`: validation, canonicalization, probe, CRUD, reference policy and audit orchestration.
- `internal/handlers`: bind input and call service only.
- `internal/storage`: local filesystem capability/probe abstraction so future cloud drivers do not branch through handlers.

## Path Rules

- Preserve a canonical absolute Windows path in SQLite; comparisons are case-insensitive on Windows.
- Resolve the root itself and reject Reparse Point roots. Recursive media scan safety is deferred to MediaLibrary but must re-check every traversed directory later.
- Never infer writability by writing a file into the real root.
- Error responses use stable codes such as `storage_path_not_absolute`, `storage_path_not_found`, `storage_path_not_directory`, `storage_path_reparse_point`, `storage_unreadable`.

## API Shape

Create/update accepts `name`, `type=local`, `root_path`, `enabled`. Read returns configuration plus latest probe summary. Raw Go/OS errors stay server-side and absolute paths do not enter audit metadata.

## UI

Replace only the Storage portion of the current planned Connections/Storage workspace. Forms use explicit Windows path input for now; native browser directory picking is not assumed in Web UI.


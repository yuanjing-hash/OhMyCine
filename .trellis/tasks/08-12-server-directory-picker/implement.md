# Implementation Plan

1. Add `storages.browse` to the canonical permission catalog, generated frontend constants, system-role seeds and drift/RBAC tests.
2. Define directory root/list DTOs, stable safe errors, opaque navigation/selection token signer and expiry/purpose tests.
3. Implement `PlatformDirectoryAdapter` with Windows logical/mapped drive enumeration and Unix root/mount discovery; use injectable fake adapters for cross-platform tests.
4. Implement bounded one-level directory listing, sort/truncation/cancellation, symlink/Reparse Point disabling and no-path logging/audit policy.
5. Add thin authenticated routes for roots, child browsing and existing-Storage/current-path token resolution; apply middleware plus service authorization, no-store and rate/concurrency limits.
6. Allow Storage create/update to resolve picker tokens and then reuse the existing canonicalization, uniqueness, probe and audit transaction. Retain raw `root_path` API compatibility outside Web UI.
7. Build reusable `DirectoryPickerDialog.vue` with root/breadcrumb/up/current-directory selection, loading/empty/error/truncated and accessible modal behavior.
8. Replace Storage create/edit free-text inputs with read-only selection summaries and picker actions; cover create/update permission combinations and remote-browser semantics.
9. Update Server security/API/Web UI specs and architecture docs with the Server-filesystem picker contract.
10. Run focused Windows/Unix adapter, token tamper/expiry, RBAC, symlink/Reparse Point, bounded listing, audit redaction and Storage round-trip tests.
11. Run full `server/test.ps1`, root/Web UI `go mod verify`, `go build -tags webui ./cmd/server`, `git diff --check`, and a Windows real-process picker smoke without changing the selected directory.

## Rollback Points

- Permission/catalog changes must remain synchronized across JSON, Go constants, role seeds and generated TypeScript.
- Token format is internal and versioned; do not persist picker tokens in the database.
- If native root discovery is unreliable on a platform, fail that adapter with a stable unavailable state rather than falling back to arbitrary free-text browsing.

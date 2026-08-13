# Design: Server Cross-platform Directory Picker

## Architecture

```text
StorageView / DirectoryPickerDialog
  -> GET /api/v1/filesystem/roots
  -> GET /api/v1/filesystem/directories?token=<opaque>
       -> storages.browse middleware
       -> DirectoryBrowserService policy
       -> PlatformDirectoryAdapter
            Windows: logical drives + mapped drives + UNC navigation
            Unix: / + discovered visible mount points
  -> opaque selected token
  -> POST/PATCH /api/v1/storages with selected token
       -> resolve token + revalidate canonical root + read-only probe
```

The browser is a Server filesystem read model, not a browser-native file picker. The Web client never assumes its own operating system or directly joins path fragments.

## Authorization and Token Contract

- Add stable sensitive permission `storages.browse`; owner, administrator, and operator receive it, viewer does not.
- Root and child listing routes use permission middleware and `DirectoryBrowserService` repeats `actor.Can` policy.
- Responses use short-lived opaque HMAC tokens for navigation/selection. Token payload contains normalized server path, platform/adapter version, expiry and purpose; raw payload is never trusted without signature/expiry validation.
- The UI sends only a token to browse children or commit a selected directory. The Server resolves it and repeats Lstat/Reparse Point/readability/canonicalization checks at use time.
- Storage create/update accepts a picker token as the primary contract. The existing raw `root_path` backend field may remain temporarily for API compatibility and tests, but Web UI no longer exposes a free-text editor; both inputs converge on the same validation service.

## Platform Roots

### Windows

- Enumerate logical drives visible to the Server process and classify fixed/removable/remote/CD-ROM/RAM disk; exclude invalid/unready drives from selectable roots while returning a safe unavailable state where useful.
- Mapped network drives naturally appear if visible to the service account.
- UNC navigation begins only from a server-issued token (for example an existing configured UNC Storage or a listed mapped remote drive); the UI cannot submit an arbitrary hostname/share path for probing.
- Junctions, symlinks, mount-point Reparse Points and other Reparse Point children are disabled and not traversed.

### Unix / NAS / Docker

- `/` is a visible root for authorized administrators. Parse the platform mount table through an adapter to provide helpful mount entries, while deduplicating paths and never promising access outside the process/container namespace.
- Linux/NAS/Docker shows only paths actually visible inside the Server process namespace. Bind mounts/volumes appear at their in-container path.
- Symlink children are disabled and never traversed.

## Directory Listing

- Use `os.ReadDir`/bounded platform equivalents for exactly one directory level.
- Return directories only, sorted with platform-aware stable name comparison.
- Cap a response (initial contract: 500 entries) and return `truncated=true`; do not recursively determine whether each child has descendants.
- Per request accepts context cancellation and uses a service-level timeout. Map disappeared/unreadable/unavailable roots to stable safe codes.
- Each item returns `name`, `token`, `selectable`, `enterable`, optional safe `unavailable_reason`, and root kind. Absolute paths may be displayed only in the active authorized picker response; never log/audit them.
- Response headers include `Cache-Control: no-store`.

## UI

- Reusable `DirectoryPickerDialog.vue` renders location breadcrumbs, platform/root chips, current-level folders, back/up navigation, refresh, loading/empty/error/truncated states and “选择当前目录”.
- Storage create/edit uses a read-only selected-path field plus “选择目录/更换目录”. No free-text path input is rendered.
- Opening edit starts from a Server-issued token for the current saved root; stale or missing directories show a recoverable reselect state.
- Keyboard/focus: modal traps focus, Escape closes, arrows/tab remain usable, and folder buttons expose disabled reason to assistive technology.

## Security and Observability

- Directory browsing is high-sensitivity read access. It never reads file content, file names, ACLs, owners or directory sizes.
- No browse action writes audit path metadata. Aggregate audit/rate-limit events record actor, adapter/platform, result code and count only.
- Apply per-user/IP request rate limits and bounded concurrent browse requests to avoid using directory enumeration as a local DoS primitive.
- Storage save remains the authority: token selection cannot bypass canonical absolute path, Reparse Point rejection, uniqueness or read-only probe.

## Compatibility and Rollback

- No database migration is required except permission seed/catalog synchronization.
- Existing stored Storage paths remain valid and can be opened by requesting an authorized token for that exact saved root.
- API clients using `root_path` remain supported during this slice; rollback removes picker endpoints/UI but does not invalidate stored Storage records.

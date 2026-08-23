# Design: Plugin Host Capabilities and Declarative Settings

## Boundaries

The plugin remains an untrusted provider adapter. It may resolve remote site facts, but it never receives storage credentials, local paths, database handles, media-tool execution, or unrestricted UI code.

```text
Plugin operation (site/download/metadata)
  -> validated contract DTO
  -> Server DownloadService / MediaTool
  -> Server TransferService
  -> Storage Driver (local or 115 upload)
  -> Server artifact renderer and library reconciliation
```

The target media library remains a Host/UI choice. A plugin cannot choose or override the destination, transfer mode, conflict policy, naming templates, or credentials.

## Contract Changes

### Provider metadata

Add `media.metadata` as a capability and runtime operation. Its request contains only the host-bound plugin connection and opaque media identities. Its response is a bounded `ProviderMetadataSnapshot` containing:

- provider and content identities;
- title, original title, overview, author, published date and duration;
- media kind and optional season/episode indices;
- bounded genres/tags and people;
- provider-registered poster/backdrop asset refs;
- optional provider extra identifiers from an allowlisted string map.

The Server invokes this operation only through the connection that owns the online library item. It validates that returned work/segment identities match the requested download plan, then persists an immutable snapshot with plugin ID/version/connection ID. No global scraper registry is modified.

### Declarative settings

Manifest v1 gains an optional `settingsPage` object with its own schema version and a bounded component tree. Supported components are Host-owned primitives only: tabs, section, notice, switch, text, number, select and credential-status. Field components bind to keys declared by `configSchema`; credential-status has no access to secret values and emits only Host-owned login actions.

Unknown component types, duplicate IDs/keys, excess nesting/items, unsafe text, invalid options, or bindings missing from `configSchema.properties` fail Manifest validation. The Web UI renders this DTO through a fixed component renderer and submits ordinary config through the existing encrypted plugin-connection service.

## Download and Import Flow

1. UI submits plugin item identities plus the target media library.
2. Server snapshots the target library, Profile, transfer mode, conflict policy, Storage and provider root.
3. Plugin returns a DownloadPlan; Server downloads and merges assets into its managed staging root.
4. Server invokes the same plugin connection's `media.metadata` operation and stores the validated snapshot.
5. Server's provider-neutral artifact renderer creates NFO/poster/fanart beside the managed media output and extends the import manifest.
6. TransferService routes by target Storage:
   - local: existing move/copy/link executor;
   - 115: new general UploadDriver path, using Server-owned connection credentials and provider rate limits.
7. Transfer completion updates the media library dirty generation and uses the existing scan/artifact reconciliation path.

## Storage Upload Contract

Add a general `UploadDriver` interface to `pkg/cloud` for bounded local-file upload with progress callbacks. The 115 adapter wraps the existing 115driver rapid/multipart upload API and repeats provider directory validation. The Transfer worker opens only files already proven to be regular descendants of the task's managed staging root.

The cloud upload worker reuses existing directory creation, conflict discovery, rename/overwrite/skip behavior, durable cloud state, queue heartbeat, and audit/log operations. It never exposes local paths to the plugin or public DTOs.

## NFO and Artwork

Extend the NFO package with a provider-neutral snapshot renderer. TMDB rendering remains unchanged for existing scans. Provider NFO uses a movie/episode-compatible document with provider `uniqueid` entries and no required TMDB ID. Artwork bytes are fetched through Host-owned opaque asset refs with existing domain, redirect, MIME and size limits.

For Bilibili:

- title/overview/author/published date/duration come from Bilibili detail;
- BVID and CID become stable unique IDs;
- poster/fanart use Bilibili image assets;
- ordinary video succeeds without TMDB.

## Compatibility and Migration

- `settingsPage` and `media.metadata` are optional, so existing v1 plugins remain installable.
- Plugins without metadata capability continue through the existing global recognition path.
- Existing connection `ConfigJSON` remains valid; the new form is only a safer presentation and validation layer.
- Existing local and 115-native-offline transfers retain their current branches and persisted state format.

## Security and Rollback

- Installation revalidates the full declarative UI tree.
- Runtime identity is bound by Host; guest-provided plugin/connection IDs cannot select another provider.
- Metadata assets are opaque refs, not arbitrary Server-side paths.
- General upload is available only to the Transfer worker, not as a raw guest Host call.
- A failing upload retains staging data and a retryable task state.
- The implementation can be rolled back without schema destruction because new database fields are additive and existing JSON remains readable.

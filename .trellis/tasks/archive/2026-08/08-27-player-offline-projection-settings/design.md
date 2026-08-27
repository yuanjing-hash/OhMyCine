# Design

## Boundaries

- Vue/Pinia owns user-visible source ordering, persisted display-cache cleanup, download badges, and post-delete invalidation.
- `OfflineDataSource` remains registered inside `DataSourceManager` only so existing internal offline routes and cold-start fallback continue to resolve; it is excluded from `orderedConfigs` and therefore from navigation, home aggregation, and search.
- Rust owns default settings and safe segmented-resume topology changes.

## Display flow

```text
Configured sources -> orderedConfigs (no __offline__) -> Home/Search/Sidebar
Download completion -> offline index -> badge on original sourceId/itemId card
Delete file -> delete offline item -> refresh offline index -> prune legacy offline projection -> badge removed
Playback -> original sourceId/itemId -> resolveCompletedDownload first -> online fallback
```

Persisted cache sanitization rejects both `section.sourceId === __offline__` and individual items whose `sourceId`/`originType` identify the offline projection. The same pruning helper updates live home/snapshot state after deletion.

## Segment reconfiguration

On each remote execution attempt Rust already reads current `DownloadSettings`. When an entity-valid existing segment set has a different topology from `plan_segments(total, current_segments_per_task)`, build the new layout and project only safely reusable bytes:

1. Convert every old segment's completed prefix into byte intervals.
2. Merge adjacent/overlapping completed intervals.
3. For each new segment, retain only the continuous completed prefix beginning at its `range_start`.
4. Replace checkpoint rows atomically and schedule only incomplete new segments.

Bytes outside the retained prefix may remain in the preallocated partial file but are treated as untrusted/incomplete and overwritten. If entity validation or Range support fails, existing fallback resets segmented state and downloads one stream.

## Compatibility

- Existing saved settings are preserved because defaults apply only when the settings key is absent.
- Existing offline packages remain playable through original-source local-first resolution.
- Legacy `__offline__` bookmarks may still resolve internally, but the source is no longer advertised.

## Rollback

- Reverting the Vue changes restores the visible offline source without changing offline storage.
- Reverting segment reprojection leaves existing checkpoints in their prior topology; database schema is unchanged.

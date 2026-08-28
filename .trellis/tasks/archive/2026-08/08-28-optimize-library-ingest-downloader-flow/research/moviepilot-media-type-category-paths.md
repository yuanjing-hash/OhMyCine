# MoviePilot media-type-first classification research

## Source

- Repository: `jxxghp/MoviePilot`
- Inspected commit: `fcdef31ecf8529fa40f1eb3faac2d05ad0ac475b`
- Local reference: `C:/Users/VibeCoder/AppData/Local/Temp/ohmycine-mp-analysis-20260827/MoviePilot`
- This research records behavior only. No GPL implementation is copied.

## Evidence

`app/schemas/category.py` models classification as two separate maps: `movie` and `tv`. A leaf category therefore belongs to one media-type domain rather than one global flat category list.

`app/schemas/system.py` exposes two independent library organization switches:

- `library_type_folder`: create a media-type directory.
- `library_category_folder`: create a media-category directory.

`app/modules/filemanager/transhandler.py:get_dest_dir` applies them in order:

1. Resolve the target media-library root.
2. Append `mediainfo.type.value` when the type folder is enabled.
3. Append `mediainfo.category` when the category folder is enabled.

This yields `library root / media type / media category`, not a flat `library root / media category` projection.

## OhMyCine finding

OhMyCine already has separate movie and TV rule groups, but the default directory templates begin with `{category}`. The shared Transfer planner therefore projects only the leaf category beneath the selected MediaLibrary root. The rule model is type-aware while the physical organization model loses that parent dimension.

## Adopted contract

- Keep OhMyCine's independent Profile, task snapshot, backend and transfer architecture.
- Make the first automatic organization segment a Server-owned invariant: `电影` for movie and `电视剧` for TV.
- Keep the Profile match result as the second segment.
- Normalize future Profile/MediaLibrary templates once; do not mutate already queued task snapshots or existing media files.
- Apply the same normalized plan to local, 115 native, cross-source, plugin, follow and corrective-reorganization flows.

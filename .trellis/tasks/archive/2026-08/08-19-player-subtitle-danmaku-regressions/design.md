# Technical Design

## Data Flow

```text
Emby PlaybackInfo
  -> selected MediaSource (Id + MediaStreams + RequiredHttpHeaders)
  -> transient MediaStreamRequest (video + subtitles)
  -> PlayerView known subtitle inventory
  -> useMpv mpv_add_subtitle IPC
  -> shared Rust bounded subtitle preparation
  -> cache/mpv-subtitles/<opaque hash>.<ext>
  -> desktop libmpv / Android Kotlin libmpv sub-add
```

Sensitive playback URL/Header values stop at the transient request and IPC boundary. They are never copied into `PlaybackMediaContext`, route state, per-media preferences, history, logs, or diagnostics.

## Preference Restoration

- Extract track matching into a typed service so behavior can be directly tested.
- Match stable fingerprint fields before numeric ID fallback.
- Keep restoration event-driven from existing track/video readiness updates.
- Record one pending wake-up when a reactive update arrives while restoration is already in flight; replay it after the current attempt settles.
- Propagate `sub-add` failure to the restore caller so a failed external subtitle is not marked restored.

## Emby Subtitle Resolution

- Extend transient playback request metadata with resolved `mediaSourceId` and subtitle tracks.
- Parse nested `MediaStreams` from the same selected PlaybackInfo media source used for video.
- Keep ordinary `getDetail` subtitles metadata-only for Emby; tokenized playable subtitle requests are produced only by `getStreamRequest`.
- Carry bounded request headers only through transient `SubtitleTrack -> KnownSubtitleTrackInput -> mpv_add_subtitle` state.

## Native Subtitle Preparation

- Move the existing desktop remote/local subtitle preparation into `player_shared.rs`.
- Both desktop and Android commands call the shared helper.
- Validate HTTP(S), headers, redirect count/origin downgrade, content length, extension and final byte count.
- Hash request material into an opaque cache filename; never log request values.

## Danmaku Settings

- Normalize an enabled configuration with no visible modes by enabling scrolling comments.
- Preserve explicitly disabled top/bottom modes and all valid existing configurations.

## Compatibility

- No database migration is required.
- Existing per-media preference rows remain readable.
- Raw/local/OpenList/CloudDrive2/WebDAV subtitle discovery continues through detail metadata when playback requests do not provide exact subtitle tracks.
- No change to Android media 302 proxy or danmaku API routing.

//! Bounded HLS control rewriting. URLs are transient native proxy capabilities.

pub(super) const MAX_MANIFEST_BYTES: usize = 1024 * 1024;
pub(super) const MAX_HLS_REFERENCES: usize = 8192;
pub(super) const MAX_URI_BYTES: usize = 4096;
const MAX_LINES: usize = 20_000;
const MAX_LINE_BYTES: usize = 16 * 1024;

pub(super) fn signature(bytes: &[u8]) -> bool {
    bytes
        .strip_prefix(b"\xef\xbb\xbf")
        .unwrap_or(bytes)
        .starts_with(b"#EXTM3U")
}

pub(super) fn content_type(value: &str) -> bool {
    matches!(
        value
            .split(';')
            .next()
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str(),
        "application/vnd.apple.mpegurl"
            | "application/x-mpegurl"
            | "audio/mpegurl"
            | "audio/x-mpegurl"
    )
}

pub(super) fn rewrite<F>(bytes: &[u8], mut replace: F) -> Result<Vec<u8>, &'static str>
where
    F: FnMut(&str) -> Result<String, &'static str>,
{
    if bytes.len() > MAX_MANIFEST_BYTES || !signature(bytes) {
        return Err("hls-manifest-invalid");
    }
    let text = std::str::from_utf8(bytes).map_err(|_| "hls-manifest-encoding")?;
    let text = text.strip_prefix('\u{feff}').unwrap_or(text);
    let mut result = String::new();
    let mut references = 0;
    for (index, line) in text.lines().enumerate() {
        if index >= MAX_LINES || line.len() > MAX_LINE_BYTES || line.contains('\0') {
            return Err("hls-manifest-limit");
        }
        let trimmed = line.trim();
        if index == 0 && trimmed != "#EXTM3U" {
            return Err("hls-manifest-invalid");
        }
        if !trimmed.is_empty() && !trimmed.starts_with('#') {
            result.push_str(&replace_reference(trimmed, &mut references, &mut replace)?);
        } else if uri_directive(trimmed) {
            // HLS attributes use comma-separated keys and quoted URI values. Scan
            // outside quotes so URI-looking text in a title is never interpreted.
            let mut start = 0;
            let mut cursor = 0;
            let mut quoted = false;
            let raw = line.as_bytes();
            while cursor < raw.len() {
                if raw[cursor] == b'"' {
                    quoted = !quoted;
                }
                if !quoted
                    && raw[cursor..].starts_with(b"URI")
                    && cursor > 0
                    && matches!(raw[cursor - 1], b',' | b':' | b'-' | b' ' | b'\t')
                {
                    if matches!(raw.get(cursor + 3), Some(b' ' | b'\t'))
                        || matches!(raw[cursor - 1], b' ' | b'\t')
                    {
                        return Err("hls-uri-invalid");
                    }
                    if raw.get(cursor + 3) != Some(&b'=') {
                        cursor += 1;
                        continue;
                    }
                    let value_start = cursor + 4;
                    if raw.get(value_start) != Some(&b'"') {
                        return Err("hls-uri-invalid");
                    }
                    let value_start = value_start + 1;
                    let end = raw[value_start..]
                        .iter()
                        .position(|byte| *byte == b'"')
                        .map(|offset| offset + value_start)
                        .ok_or("hls-uri-invalid")?;
                    result.push_str(&line[start..value_start]);
                    result.push_str(&replace_reference(
                        &line[value_start..end],
                        &mut references,
                        &mut replace,
                    )?);
                    start = end;
                    cursor = end;
                }
                cursor += 1;
            }
            if quoted {
                return Err("hls-uri-invalid");
            }
            result.push_str(&line[start..]);
        } else {
            result.push_str(line);
        }
        result.push('\n');
        if result.len() > MAX_MANIFEST_BYTES {
            return Err("hls-manifest-limit");
        }
    }
    Ok(result.into_bytes())
}

fn uri_directive(value: &str) -> bool {
    [
        "#EXT-X-KEY:",
        "#EXT-X-SESSION-KEY:",
        "#EXT-X-MAP:",
        "#EXT-X-MEDIA:",
        "#EXT-X-I-FRAME-STREAM-INF:",
        "#EXT-X-IMAGE-STREAM-INF:",
        "#EXT-X-PART:",
        "#EXT-X-PRELOAD-HINT:",
        "#EXT-X-RENDITION-REPORT:",
        "#EXT-X-SESSION-DATA:",
        "#EXT-X-CONTENT-STEERING:",
    ]
    .iter()
    .any(|prefix| value.starts_with(prefix))
}

fn replace_reference<F>(
    uri: &str,
    count: &mut usize,
    replace: &mut F,
) -> Result<String, &'static str>
where
    F: FnMut(&str) -> Result<String, &'static str>,
{
    *count += 1;
    if *count > MAX_HLS_REFERENCES
        || uri.is_empty()
        || uri.len() > MAX_URI_BYTES
        || uri
            .chars()
            .any(|value| value.is_control() || value.is_whitespace())
        || uri.contains(['\\', '{', '}'])
    {
        return Err("hls-uri-invalid");
    }
    replace(uri)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rewrites_master_audio_key_map_parts_and_media_without_touching_titles() {
        let text = b"#EXTM3U\r\n#EXT-X-MEDIA:TYPE=AUDIO,NAME=\"URI=title\",URI=\"audio.m3u8\"\r\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\r\n#EXT-X-MAP:URI=\"init\",BYTERANGE=\"4@0\"\r\n#EXT-X-PART:DURATION=1,URI=\"part\"\r\n#EXTINF:1,\r\nsegment\r\n";
        let mut seen = vec![];
        let out = rewrite(text, |uri| {
            seen.push(uri.to_string());
            Ok(format!("/media/{}", seen.len()))
        })
        .unwrap();
        assert_eq!(seen, ["audio.m3u8", "key", "init", "part", "segment"]);
        assert!(std::str::from_utf8(&out)
            .unwrap()
            .contains("NAME=\"URI=title\""));
    }

    #[test]
    fn rejects_unbounded_or_malformed_control_and_uri_values() {
        for text in [
            "#EXTM3U\n#EXT-X-KEY:URI=bad\n",
            "#EXTM3U\n#EXT-X-MAP:URI=\"bad\n",
            "#EXTM3U\nfile with space\n",
            "#EXTM3U\n{$unresolved}\n",
            "#EXTM3U\n../bad\\file\n",
            "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128, URI=\"bad\"\n",
            "#EXTM3U\n#EXT-X-KEY:URI =\"bad\"\n",
        ] {
            assert!(rewrite(text.as_bytes(), |_| Ok("/media/test".into())).is_err());
        }
        assert!(rewrite(&vec![b'a'; MAX_MANIFEST_BYTES + 1], |_| Ok(String::new())).is_err());
        assert!(rewrite(b"#EXTM3U\n\xff", |_| Ok("/media/test".into())).is_err());
        assert!(rewrite(b"#EXTM3U-invalid\nsegment\n", |_| Ok("/media/test".into())).is_err());
        assert!(rewrite(
            format!("#EXTM3U\n{}\n", "x".repeat(MAX_URI_BYTES + 1)).as_bytes(),
            |_| Ok("/media/test".into())
        )
        .is_err());
        assert!(rewrite(
            format!("#EXTM3U\n#{}\n", "x".repeat(MAX_LINE_BYTES)).as_bytes(),
            |_| Ok("/media/test".into())
        )
        .is_err());
        assert!(rewrite(
            format!("#EXTM3U\n{}", "#comment\n".repeat(MAX_LINES)).as_bytes(),
            |_| Ok("/media/test".into())
        )
        .is_err());
        assert!(rewrite(
            format!("#EXTM3U\n{}", "segment\n".repeat(MAX_HLS_REFERENCES + 1)).as_bytes(),
            |_| Ok("/media/test".into())
        )
        .is_err());
        assert!(content_type("Application/Vnd.Apple.Mpegurl; charset=utf-8"));
        assert!(signature(b"\xef\xbb\xbf#EXTM3U\n"));
        assert!(std::str::from_utf8(
            &rewrite(b"#EXTM3U\n#EXTINF:6,URI=episode title\nsegment\n", |_| Ok(
                "/media/test".into()
            ))
            .unwrap()
        )
        .unwrap()
        .contains("URI=episode title"));
    }
}

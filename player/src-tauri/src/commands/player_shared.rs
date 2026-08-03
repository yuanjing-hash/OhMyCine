#[derive(Clone, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MpvHttpHeader {
    pub name: String,
    pub value: String,
}

pub fn sanitize_http_headers(headers: Vec<MpvHttpHeader>) -> Result<Vec<MpvHttpHeader>, String> {
    if headers.len() > 16 {
        return Err("播放请求 header 数量过多。".to_string());
    }

    headers
        .into_iter()
        .map(|header| {
            let name = header.name.trim().to_string();
            let value = header.value.trim().to_string();
            if name.is_empty() || value.is_empty() {
                return Err("播放请求 header 格式无效。".to_string());
            }
            if !is_valid_header_name(&name) || value.chars().any(|ch| ch == '\r' || ch == '\n') {
                return Err("播放请求 header 格式无效。".to_string());
            }
            Ok(MpvHttpHeader { name, value })
        })
        .collect()
}

fn is_valid_header_name(value: &str) -> bool {
    value.bytes().all(|byte| {
        byte.is_ascii_alphanumeric()
            || matches!(
                byte,
                b'!' | b'#'
                    | b'$'
                    | b'%'
                    | b'&'
                    | b'\''
                    | b'*'
                    | b'+'
                    | b'-'
                    | b'.'
                    | b'^'
                    | b'_'
                    | b'`'
                    | b'|'
                    | b'~'
            )
    })
}

#[cfg(test)]
mod tests {
    use super::{sanitize_http_headers, MpvHttpHeader};

    #[test]
    fn sanitizes_playback_headers_without_exposing_invalid_lines() {
        let headers = sanitize_http_headers(vec![MpvHttpHeader {
            name: " Authorization ".to_string(),
            value: " Bearer secret ".to_string(),
        }])
        .unwrap();

        assert_eq!(headers[0].name, "Authorization");
        assert_eq!(headers[0].value, "Bearer secret");
        assert!(sanitize_http_headers(vec![MpvHttpHeader {
            name: "Authorization\r\nInjected".to_string(),
            value: "secret".to_string(),
        }])
        .is_err());
    }
}

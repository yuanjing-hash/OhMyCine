//! Bounded, in-memory native evidence. Raw mpv messages must never cross IPC.

use std::collections::VecDeque;

const STARTUP_LINES: usize = 12;
const TAIL_LINES: usize = 24;
const LINE_CHARS: usize = 360;

#[derive(Default)]
pub(super) struct NativeLogBuffer {
    startup: Vec<String>,
    tail: VecDeque<String>,
    first_error: bool,
    startup_complete: bool,
}

impl NativeLogBuffer {
    pub fn record(&mut self, prefix: &str, text: &str, is_error: bool) {
        let Some(line) = diagnostic_line(prefix, text) else {
            return;
        };
        if is_error && !self.first_error {
            // Reserve the first genuine native error even after startup warnings filled up.
            self.first_error = true;
            if !self.startup.contains(&line) {
                if self.startup.len() == STARTUP_LINES {
                    self.startup.pop();
                }
                self.startup.push(line.clone());
            }
        } else if !self.startup_complete
            && self.startup.len() < STARTUP_LINES
            && !self.startup.contains(&line)
        {
            self.startup.push(line.clone());
        }
        if self.tail.back() != Some(&line) {
            if self.tail.len() == TAIL_LINES {
                self.tail.pop_front();
            }
            self.tail.push_back(line);
        }
    }

    pub fn finish_startup(&mut self) {
        self.startup_complete = true;
    }

    pub fn snapshot(&self) -> Vec<String> {
        let mut lines = self.startup.clone();
        for line in &self.tail {
            if !lines.contains(line) {
                lines.push(line.clone());
            }
        }
        lines
    }
}

fn diagnostic_line(prefix: &str, text: &str) -> Option<String> {
    // Bound work as well as output. Truncate only at character boundaries, after redaction.
    let text: String = text.chars().take(4096).collect();
    let normalized = text.to_ascii_lowercase();
    let sensitive = [
        "authorization",
        "cookie",
        "bearer",
        "password",
        "credential",
        "http-header",
        "http header",
        "api_key",
        "api-key",
        "apikey",
        "x-emby-token",
        "token=",
        "token:",
        "signature=",
        "auth=",
        "sig=",
    ]
    .iter()
    .any(|key| normalized.contains(key));
    let clean = if sensitive {
        // Header values can contain spaces or continuation text: redact the entire record.
        "[private request data omitted]".to_string()
    } else {
        redact_locations(&text)
    };
    let clean: String = clean
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
        .chars()
        .filter(|ch| !ch.is_control())
        .take(LINE_CHARS)
        .collect();
    if clean.is_empty() {
        return None;
    }
    let prefix: String = if prefix.starts_with('/')
        || prefix.contains("//")
        || !prefix
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || "._-/".contains(ch))
    {
        "mpv".to_string()
    } else {
        prefix.chars().take(40).collect()
    };
    Some(if prefix.is_empty() {
        clean
    } else {
        format!("{prefix}: {clean}")
    })
}

fn redact_locations(text: &str) -> String {
    let chars: Vec<char> = text.chars().collect();
    let mut clean = String::new();
    let mut index = 0;
    while index < chars.len() {
        let ch = chars[index];
        let boundary = index == 0
            || chars[index - 1].is_whitespace()
            || "\"'([{=<>".contains(chars[index - 1]);
        // Any URI scheme, including file/content and the private loopback capability.
        let scheme_end = if boundary && ch.is_ascii_alphabetic() {
            (index..chars.len()).find(|offset| {
                !chars[*offset].is_ascii_alphanumeric() && !"+.-".contains(chars[*offset])
            })
        } else {
            None
        };
        if scheme_end.is_some_and(|end| chars.get(end..end + 3) == Some(&[':', '/', '/'])) {
            clean.push_str("[media-location]");
            let terminator = index
                .checked_sub(1)
                .and_then(|previous| match chars[previous] {
                    '\'' => Some('\''),
                    '"' => Some('"'),
                    '<' => Some('>'),
                    _ => None,
                });
            if let Some(terminator) = terminator {
                // A quoted file URI may contain literal spaces. Keep any error text after
                // the closing delimiter, but never expose the trailing path components.
                while index < chars.len() && chars[index] != terminator {
                    index += 1;
                }
            } else {
                // Native messages can include unquoted paths with spaces. Their end is
                // ambiguous, so use the same conservative rule as a Windows/UNC path.
                break;
            }
            continue;
        }
        let drive_path = ch.is_ascii_alphabetic()
            && chars.get(index + 1) == Some(&':')
            && chars
                .get(index + 2)
                .is_some_and(|next| "/\\".contains(*next));
        let absolute_path = boundary && (ch == '/' || ch == '\\');
        if drive_path || absolute_path || ch == '\\' {
            // An unquoted Windows/UNC path can contain spaces. Omit the rest instead of
            // exposing its trailing directories or filename after only masking one token.
            clean.push_str("[local-media path omitted]");
            break;
        }
        if ch == '?' && chars[index..].contains(&'=') {
            clean.push_str("[query omitted]");
            break;
        }
        if ch == '0'
            && chars
                .get(index + 1)
                .is_some_and(|next| *next == 'x' || *next == 'X')
        {
            let end = (index + 2..chars.len())
                .find(|offset| !chars[*offset].is_ascii_hexdigit())
                .unwrap_or(chars.len());
            let labeled_hresult = end == index + 10 && has_hresult_label(&clean);
            if end >= index + 8 && !labeled_hresult {
                clean.push_str("[native-address]");
                index = end;
                continue;
            }
        }
        clean.push(ch);
        index += 1;
    }
    clean
}

fn has_hresult_label(prefix: &str) -> bool {
    let lower = prefix.to_ascii_lowercase();
    let label = lower.trim_end();
    let label = label
        .strip_suffix('=')
        .or_else(|| label.strip_suffix(':'))
        .unwrap_or(label)
        .trim_end();
    label.strip_suffix("hresult").is_some_and(|before| {
        before
            .chars()
            .next_back()
            .is_none_or(|ch| !ch.is_ascii_alphanumeric() && ch != '_')
    })
}

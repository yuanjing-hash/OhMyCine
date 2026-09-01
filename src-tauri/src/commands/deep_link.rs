use std::sync::Mutex;

#[derive(Default)]
pub struct DeepLinkState {
    pending: Mutex<Vec<String>>,
}

impl DeepLinkState {
    pub fn from_current_process() -> Self {
        let state = Self::default();
        state.push_arguments(std::env::args());
        state
    }

    pub fn push_arguments<I>(&self, arguments: I) -> Vec<String>
    where
        I: IntoIterator<Item = String>,
    {
        let urls = arguments
            .into_iter()
            .filter_map(|argument| sanitize_deep_link(&argument))
            .collect::<Vec<_>>();
        if !urls.is_empty() {
            if let Ok(mut pending) = self.pending.lock() {
                pending.extend(urls.iter().cloned());
                if pending.len() > 16 {
                    let remove = pending.len() - 16;
                    pending.drain(..remove);
                }
            }
        }
        urls
    }
}

#[tauri::command]
pub fn player_take_pending_deep_links(
    state: tauri::State<'_, DeepLinkState>,
) -> Vec<String> {
    state
        .pending
        .lock()
        .map(|mut pending| pending.drain(..).collect())
        .unwrap_or_default()
}

fn sanitize_deep_link(value: &str) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.len() <= 4096 && trimmed.to_ascii_lowercase().starts_with("ohmycine://") {
        Some(trimmed.to_string())
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_only_bounded_ohmycine_urls() {
        assert_eq!(sanitize_deep_link("ohmycine://open?work=1").as_deref(), Some("ohmycine://open?work=1"));
        assert!(sanitize_deep_link("https://example.com").is_none());
        assert!(sanitize_deep_link(&format!("ohmycine://{}", "a".repeat(5000))).is_none());
    }
}

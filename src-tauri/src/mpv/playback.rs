/// Only a natural EOF is eligible for completed history or automatic continuation.
#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Deserialize, serde::Serialize)]
#[serde(rename_all = "lowercase")]
pub enum MpvEndReason {
    Eof,
    Stop,
    Quit,
    Error,
    Redirect,
}
